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

    Write-Host '=== 7. Pod-loss & worker redelivery/shutdown drill ==='
    $workerPodBefore = kubectl get pods -n bliss-local -l app=worker -o jsonpath='{.items[0].metadata.name}'
    if ($LASTEXITCODE -ne 0 -or -not $workerPodBefore) { throw 'Could not resolve worker pod before deletion' }
    Write-Host "Deleting api and worker ($workerPodBefore) pods..."
    kubectl delete pod -n bliss-local -l app=api --wait=true
    if ($LASTEXITCODE -ne 0) { throw 'Failed to delete api pod during pod-loss drill' }
    kubectl delete pod -n bliss-local -l app=worker --wait=true
    if ($LASTEXITCODE -ne 0) { throw 'Failed to delete worker pod during pod-loss drill' }
    kubectl rollout status deployment/api -n bliss-local --timeout=120s
    if ($LASTEXITCODE -ne 0) { throw 'api Deployment did not recover after pod loss' }
    kubectl rollout status deployment/worker -n bliss-local --timeout=120s
    if ($LASTEXITCODE -ne 0) { throw 'worker Deployment did not recover after pod loss' }
    $workerPodAfter = kubectl get pods -n bliss-local -l app=worker -o jsonpath='{.items[0].metadata.name}'
    if ($LASTEXITCODE -ne 0 -or -not $workerPodAfter) { throw 'Could not resolve replacement worker pod' }
    if ($workerPodAfter -eq $workerPodBefore) { throw "Worker pod was not replaced ($workerPodAfter)" }
    Write-Host "Replacement worker pod ready: $workerPodAfter"
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
