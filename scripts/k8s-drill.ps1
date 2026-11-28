$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $false

$repoRoot = Split-Path -Parent $PSScriptRoot
Push-Location $repoRoot
try {
    Write-Host '=== 1. Generating local K8s secrets and TLS cert ==='
    & (Join-Path $PSScriptRoot 'k8s-local-secrets.ps1')
    if ($LASTEXITCODE -ne 0) { throw 'Secret generation failed' }

    Write-Host '=== 2. Applying Kustomize manifests (infra/k8s/base) ==='
    kubectl delete job migrate -n bliss-local --ignore-not-found | Out-Null
    kubectl apply -k infra/k8s/base
    if ($LASTEXITCODE -ne 0) { throw 'kubectl apply -k failed' }

    Write-Host '=== 3. Waiting for backing services (db, redis, rabbitmq, redpanda) ==='
    kubectl rollout status statefulset/db -n bliss-local --timeout=180s
    if ($LASTEXITCODE -ne 0) { throw 'db StatefulSet failed to become ready' }
    kubectl rollout status deployment/redis -n bliss-local --timeout=120s
    if ($LASTEXITCODE -ne 0) { throw 'redis Deployment failed to become ready' }
    kubectl rollout status statefulset/rabbitmq -n bliss-local --timeout=180s
    if ($LASTEXITCODE -ne 0) { throw 'rabbitmq StatefulSet failed to become ready' }
    kubectl rollout status statefulset/redpanda -n bliss-local --timeout=180s
    if ($LASTEXITCODE -ne 0) { throw 'redpanda StatefulSet failed to become ready' }

    Write-Host '=== 4. Waiting for migration Job ==='
    kubectl wait --for=condition=complete job/migrate -n bliss-local --timeout=120s
    if ($LASTEXITCODE -ne 0) { throw 'migrate Job failed' }

    Write-Host '=== 5. Waiting for application workloads (fake-provider, api, worker, frontend) ==='
    foreach ($dep in @('fake-provider', 'api', 'worker', 'frontend')) {
        kubectl rollout status "deployment/$dep" -n bliss-local --timeout=180s
        if ($LASTEXITCODE -ne 0) { throw "$dep Deployment failed to become ready" }
    }

    Write-Host '=== 6. Rolling restart drill (api & frontend) ==='
    kubectl rollout restart deployment/api deployment/frontend -n bliss-local
    if ($LASTEXITCODE -ne 0) { throw 'Rolling restart command failed' }
    kubectl rollout status deployment/api -n bliss-local --timeout=120s
    if ($LASTEXITCODE -ne 0) { throw 'api rollout status after restart failed' }
    kubectl rollout status deployment/frontend -n bliss-local --timeout=120s
    if ($LASTEXITCODE -ne 0) { throw 'frontend rollout status after restart failed' }

    Write-Host '=== 7. Pod-loss & worker redelivery/deduplication drill ==='
    $workerPodBefore = kubectl get pods -n bliss-local -l app=worker -o jsonpath='{.items[0].metadata.name}'
    if ($LASTEXITCODE -ne 0 -or -not $workerPodBefore) { throw 'Could not resolve worker pod before deletion' }

    $drillEvtId = 'evt_k8s_drill_' + [guid]::NewGuid().ToString('N').Substring(0, 12)
    $drillPayload = '{"orderId":"ord_k8s_drill","customerName":"Synthetic Drill","customerPhone":"254700000000","deliveryLocation":"Mombasa","totalKes":2500,"items":[{"productId":"p1","quantity":1}],"createdAt":"2026-11-28T00:00:00Z"}'

    # 1. Hold an uncommitted PostgreSQL row lock on inbox_message so worker blocks before Ack()
    $lockJob = Start-Job -ScriptBlock {
        param($evtId)
        $sql = @"
BEGIN;
INSERT INTO public.inbox_message (id, consumer_name, status, payload, created_at)
VALUES ('$evtId', 'bliss-notification-worker', 'processing', '{}'::jsonb, now());
SELECT pg_sleep(180) /* $evtId */;
ROLLBACK;
"@
        $sql | kubectl exec -i -n bliss-local db-0 -- psql -U bliss_admin -d bliss_dev -v ON_ERROR_STOP=1 | Out-Null
    } -ArgumentList $drillEvtId

    try {
        $lockActive = $false
        for ($i = 0; $i -lt 20; $i++) {
            $activeCount = kubectl exec -n bliss-local db-0 -- psql -U bliss_admin -d bliss_dev -At -c "SELECT count(*) FROM pg_stat_activity WHERE query LIKE '%$drillEvtId%' AND query NOT LIKE '%pg_stat_activity%' AND state = 'active';"
            if ($LASTEXITCODE -eq 0 -and [int]$activeCount -ge 1) {
                $lockActive = $true
                break
            }
            Start-Sleep -Milliseconds 500
        }
        if (-not $lockActive) { throw "Barrier lock transaction did not become active for $drillEvtId" }

        # 2. Enqueue pending outbox event, wait for outbox publish, and confirm RabbitMQ holds it unacknowledged
        $insertSQL = "INSERT INTO public.outbox_event (id, event_type, aggregate_id, aggregate_version, correlation_id, schema_version, payload, destination, status, retry_count, created_at, updated_at) VALUES ('$drillEvtId', 'order.created.v1', 'ord_k8s_drill', 1, 'corr_k8s_drill', 1, '$drillPayload'::jsonb, 'rabbitmq', 'pending', 0, now(), now());"
        $insertSQL | kubectl exec -i -n bliss-local db-0 -- psql -U bliss_admin -d bliss_dev -v ON_ERROR_STOP=1 | Out-Null
        if ($LASTEXITCODE -ne 0) { throw 'Failed to insert synthetic outbox drill event' }

        $published = $false
        for ($i = 0; $i -lt 30; $i++) {
            $pubState = kubectl exec -n bliss-local db-0 -- psql -U bliss_admin -d bliss_dev -At -c "SELECT status FROM public.outbox_event WHERE id='$drillEvtId';"
            if ($LASTEXITCODE -eq 0 -and $pubState -eq 'published') {
                $published = $true
                break
            }
            Start-Sleep -Milliseconds 500
        }
        if (-not $published) { throw "Outbox did not publish $drillEvtId to RabbitMQ in time" }

        $inFlight = $false
        for ($i = 0; $i -lt 6; $i++) {
            $qLine = kubectl exec -n bliss-local rabbitmq-0 -- rabbitmqctl list_queues -q name messages_unacknowledged | Select-String '^bliss\.notifications\.v1\s'
            if ($LASTEXITCODE -eq 0 -and $qLine) {
                $parts = ($qLine.Line.Trim() -split '\s+')
                if ($parts.Count -ge 2 -and [int]$parts[1] -ge 1) {
                    $inFlight = $true
                    Write-Host "Confirmed unacknowledged RabbitMQ delivery in flight: $($qLine.Line.Trim())"
                    break
                }
            }
            Start-Sleep -Seconds 1
        }
        if (-not $inFlight) { throw "Did not observe unacknowledged RabbitMQ delivery for $drillEvtId before worker termination" }

        # 3. Terminate worker before Ack() and release the PostgreSQL barrier so RabbitMQ requeues to the replacement
        Write-Host "Terminating api and worker ($workerPodBefore) while delivery is unacknowledged..."
        kubectl delete pod -n bliss-local $workerPodBefore --force --grace-period=0 | Out-Null
        if ($LASTEXITCODE -ne 0) { throw 'Failed to force-delete worker pod during in-flight delivery' }
    } finally {
        kubectl exec -n bliss-local db-0 -- psql -U bliss_admin -d bliss_dev -At -c "SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE query LIKE '%$drillEvtId%' AND query NOT LIKE '%pg_stat_activity%' AND pid <> pg_backend_pid();" | Out-Null
        Stop-Job -Job $lockJob -ErrorAction SilentlyContinue | Out-Null
        Remove-Job -Job $lockJob -Force -ErrorAction SilentlyContinue | Out-Null
    }

    kubectl delete pod -n bliss-local -l app=api --wait=true
    if ($LASTEXITCODE -ne 0) { throw 'Failed to delete api pod during pod-loss drill' }
    kubectl rollout status deployment/api -n bliss-local --timeout=120s
    if ($LASTEXITCODE -ne 0) { throw 'api Deployment did not recover after pod loss' }
    kubectl rollout status deployment/worker -n bliss-local --timeout=120s
    if ($LASTEXITCODE -ne 0) { throw 'worker Deployment did not recover after pod loss' }
    $workerPodAfter = kubectl get pods -n bliss-local -l app=worker -o jsonpath='{.items[0].metadata.name}'
    if ($LASTEXITCODE -ne 0 -or -not $workerPodAfter) { throw 'Could not resolve replacement worker pod' }
    if ($workerPodAfter -eq $workerPodBefore) { throw "Worker pod was not replaced ($workerPodAfter)" }
    Write-Host "Replacement worker pod ready: $workerPodAfter"

    # 4. Verify replacement worker processed the broker-requeued delivery and drained unacked count to 0
    $processed = $false
    for ($i = 0; $i -lt 30; $i++) {
        $state = kubectl exec -n bliss-local db-0 -- psql -U bliss_admin -d bliss_dev -At -c "SELECT (SELECT status FROM public.outbox_event WHERE id='$drillEvtId') || '|' || (SELECT coalesce((SELECT status FROM public.inbox_message WHERE id='$drillEvtId' AND consumer_name='bliss-notification-worker'), 'missing'));"
        $qLine = kubectl exec -n bliss-local rabbitmq-0 -- rabbitmqctl list_queues -q name messages_unacknowledged | Select-String '^bliss\.notifications\.v1\s'
        $unacked = 1
        if ($qLine) {
            $parts = ($qLine.Line.Trim() -split '\s+')
            if ($parts.Count -ge 2) { $unacked = [int]$parts[1] }
        }
        $logs = kubectl logs -n bliss-local $workerPodAfter --tail=50
        if ($LASTEXITCODE -eq 0 -and $state -eq 'published|processed' -and $unacked -eq 0 -and ($logs -match 'notification_order_created')) {
            $processed = $true
            break
        }
        Start-Sleep -Seconds 1
    }
    if (-not $processed) { throw "Replacement worker did not process broker-requeued delivery $drillEvtId in time" }

    # 5. Re-trigger RabbitMQ delivery of the exact same event_id to verify duplicate suppression (inbox count=1)
    kubectl exec -n bliss-local db-0 -- psql -U bliss_admin -d bliss_dev -v ON_ERROR_STOP=1 -c "UPDATE public.outbox_event SET status='pending', updated_at=now() WHERE id='$drillEvtId';" | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'Failed to requeue drill event for duplicate redelivery check' }

    $redelivered = $false
    for ($i = 0; $i -lt 20; $i++) {
        $outStatus = kubectl exec -n bliss-local db-0 -- psql -U bliss_admin -d bliss_dev -At -c "SELECT status FROM public.outbox_event WHERE id='$drillEvtId';"
        $logs = kubectl logs -n bliss-local $workerPodAfter --tail=50
        if ($LASTEXITCODE -eq 0 -and $outStatus -eq 'published' -and ($logs -match 'consumer_duplicate_delivery_ignored')) {
            $redelivered = $true
            break
        }
        Start-Sleep -Seconds 1
    }
    if (-not $redelivered) { throw "Duplicate redelivery check did not observe consumer_duplicate_delivery_ignored for $drillEvtId" }

    $inboxCount = kubectl exec -n bliss-local db-0 -- psql -U bliss_admin -d bliss_dev -At -c "SELECT count(*) FROM public.inbox_message WHERE id='$drillEvtId';"
    if ($LASTEXITCODE -ne 0 -or $inboxCount -ne '1') { throw "Expected exactly 1 inbox row for $drillEvtId, got '$inboxCount'" }
    kubectl exec -n bliss-local db-0 -- psql -U bliss_admin -d bliss_dev -v ON_ERROR_STOP=1 -c "DELETE FROM public.inbox_message WHERE id='$drillEvtId'; DELETE FROM public.outbox_event WHERE id='$drillEvtId';" | Out-Null
    Write-Host "Verified in-flight redelivery and duplicate suppression for $drillEvtId (inbox count=1)."
    kubectl logs -n bliss-local $workerPodAfter --tail=20
    if ($LASTEXITCODE -ne 0) { throw 'Failed to read replacement worker logs' }

    Write-Host '=== 8. HPA & Metrics check ==='
    kubectl get hpa -n bliss-local
    $topOut = cmd /c "kubectl top pods -n bliss-local 2>&1"
    Write-Host ($topOut -join "`n")

    Write-Host '=== 9. Cluster inventory summary ==='
    kubectl get pods,svc,ingress,networkpolicy,hpa,pvc -n bliss-local
} finally {
    Pop-Location
}
