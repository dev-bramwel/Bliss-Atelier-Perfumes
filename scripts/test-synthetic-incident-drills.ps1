# ==============================================================================
# Bliss Atelier Perfumes - Observability & Synthetic Incident Drill Runner
# Issue #29 [R23]: Telemetry, Alerts, Dashboards & Incident Runbooks
# ==============================================================================

param(
    [switch]$RequireDocker = $false
)

$ErrorActionPreference = "Stop"

Write-Host "=================================================================" -ForegroundColor Cyan
Write-Host "  BLISS ATELIER: OBSERVABILITY & INCIDENT DRILL VERIFICATION    " -ForegroundColor Cyan
Write-Host "=================================================================" -ForegroundColor Cyan

$baseDir = Split-Path -Parent $PSScriptRoot

# ------------------------------------------------------------------------------
# STEP 1: Verify Incident Runbooks
# ------------------------------------------------------------------------------
Write-Host "`n[1/6] Verifying Incident Runbooks in docs/runbooks/..." -ForegroundColor Yellow

$requiredRunbooks = @(
    "docs/runbooks/runbook-payment-reconciliation-failure.md",
    "docs/runbooks/runbook-dlq-poison-messages.md",
    "docs/runbooks/runbook-database-pool-exhaustion.md",
    "docs/runbooks/runbook-cache-and-rate-limit-outage.md",
    "docs/runbooks/runbook-api-error-surge.md",
    "docs/runbooks/runbook-outbox-backlog.md"
)

foreach ($rb in $requiredRunbooks) {
    $fullPath = Join-Path $baseDir $rb
    if (-not (Test-Path $fullPath)) {
        throw "Missing required runbook: $rb"
    }
    $content = Get-Content $fullPath -Raw
    if ($content.Length -lt 200) {
        throw "Runbook $rb appears truncated or empty"
    }
    Write-Host "  ✓ Found runbook: $rb ($($content.Length) bytes)" -ForegroundColor Green
}

# ------------------------------------------------------------------------------
# STEP 2: Verify Prometheus Alerting Rules & Runbook Linkage
# ------------------------------------------------------------------------------
Write-Host "`n[2/6] Verifying Prometheus Alerting Rules & Runbook Linkage..." -ForegroundColor Yellow

$alertsPath = Join-Path $baseDir "infra/observability/prometheus/alerts.yml"
if (-not (Test-Path $alertsPath)) {
    throw "Missing alerts configuration: $alertsPath"
}

$alertsContent = Get-Content $alertsPath -Raw
$alertNames = [regex]::Matches($alertsContent, "alert:\s+([a-zA-Z0-9]+)") | ForEach-Object { $_.Groups[1].Value }
$runbookUrls = [regex]::Matches($alertsContent, 'runbook_url:\s+"([^"]+)"') | ForEach-Object { $_.Groups[1].Value }

Write-Host "  ✓ Configured Alerts ($($alertNames.Count)): $($alertNames -join ', ')" -ForegroundColor Green

foreach ($url in $runbookUrls) {
    $targetPath = Join-Path $baseDir $url
    if (-not (Test-Path $targetPath)) {
        throw "Alert rule references non-existent runbook_url: $url"
    }
}
Write-Host "  ✓ All alerting rules have verified runbook_url destinations" -ForegroundColor Green

# ------------------------------------------------------------------------------
# STEP 3: Verify Provisioned Grafana Dashboards
# ------------------------------------------------------------------------------
Write-Host "`n[3/6] Verifying Provisioned Grafana Dashboards..." -ForegroundColor Yellow

$dashboardFiles = @(
    "infra/observability/grafana/dashboards/bliss-system-overview.json",
    "infra/observability/grafana/dashboards/bliss-financial-operations.json",
    "infra/observability/grafana/dashboards/bliss-messaging-sre.json"
)

foreach ($df in $dashboardFiles) {
    $dfPath = Join-Path $baseDir $df
    if (-not (Test-Path $dfPath)) {
        throw "Missing dashboard definition: $df"
    }
    $jsonContent = Get-Content $dfPath -Raw | ConvertFrom-Json
    if (-not $jsonContent.title -or $jsonContent.panels.Count -eq 0) {
        throw "Invalid dashboard JSON in $df"
    }
    Write-Host "  ✓ Dashboard: '$($jsonContent.title)' ($($jsonContent.panels.Count) panels)" -ForegroundColor Green
}

# ------------------------------------------------------------------------------
# STEP 4: Prometheus & Alertmanager Tooling Rule & Route Verification
# ------------------------------------------------------------------------------
Write-Host "`n[4/6] Verifying Alert Rules and Routing via promtool & amtool..." -ForegroundColor Yellow

$dockerCmd = Get-Command docker -ErrorAction SilentlyContinue
$dockerAvailable = $false
if ($dockerCmd) {
    & docker info --format '{{.ServerVersion}}' 2>$null | Out-Null
    if ($LASTEXITCODE -eq 0) {
        $dockerAvailable = $true
    }
}

$dockerSkipped = $false

if ($dockerAvailable) {
    Push-Location $baseDir
    try {
        # Check rule syntax
        $promCheck = docker run --rm --entrypoint /bin/promtool -v "${baseDir}/infra/observability/prometheus:/etc/prometheus:ro" prom/prometheus:v2.53.0 check rules /etc/prometheus/alerts.yml
        if ($LASTEXITCODE -ne 0) {
            throw "promtool check rules failed"
        }
        Write-Host "  ✓ promtool check rules: $($promCheck.Trim())" -ForegroundColor Green

        # Run unit tests for alerting rules
        $promTest = docker run --rm --entrypoint /bin/promtool -v "${baseDir}/infra/observability/prometheus:/etc/prometheus:ro" prom/prometheus:v2.53.0 test rules /etc/prometheus/alerts_test.yml
        if ($LASTEXITCODE -ne 0) {
            throw "promtool test rules failed"
        }
        Write-Host "  ✓ promtool test rules: $($promTest.Trim())" -ForegroundColor Green

        # Check Alertmanager routing
        $amCheck = docker run --rm --entrypoint /bin/amtool -v "${baseDir}/infra/observability/alertmanager:/etc/alertmanager:ro" prom/alertmanager:v0.27.0 check-config /etc/alertmanager/alertmanager.yml
        if ($LASTEXITCODE -ne 0) {
            throw "amtool check-config failed"
        }
        Write-Host "  ✓ amtool check-config: validated Alertmanager configuration" -ForegroundColor Green

        # Test route resolution
        $amRouteFinance = docker run --rm --entrypoint /bin/amtool -v "${baseDir}/infra/observability/alertmanager:/etc/alertmanager:ro" prom/alertmanager:v0.27.0 config routes test --config.file=/etc/alertmanager/alertmanager.yml alertname=UnresolvedPaymentSpike severity=critical team=finance-ops
        if ($LASTEXITCODE -ne 0 -or $amRouteFinance.Trim() -ne "finance_operations_escalation") {
            throw "amtool config routes test failed: expected finance_operations_escalation, got $amRouteFinance"
        }
        Write-Host "  ✓ amtool route test (finance-ops): alerts route to $($amRouteFinance.Trim())" -ForegroundColor Green

        $amRoutePlatform = docker run --rm --entrypoint /bin/amtool -v "${baseDir}/infra/observability/alertmanager:/etc/alertmanager:ro" prom/alertmanager:v0.27.0 config routes test --config.file=/etc/alertmanager/alertmanager.yml alertname=ApiHigh5xxRate severity=critical team=platform-sre
        if ($LASTEXITCODE -ne 0 -or $amRoutePlatform.Trim() -ne "platform_sre_escalation") {
            throw "amtool config routes test failed: expected platform_sre_escalation, got $amRoutePlatform"
        }
        Write-Host "  ✓ amtool route test (platform-sre): alerts route to $($amRoutePlatform.Trim())" -ForegroundColor Green

        $amRouteDrill = docker run --rm --entrypoint /bin/amtool -v "${baseDir}/infra/observability/alertmanager:/etc/alertmanager:ro" prom/alertmanager:v0.27.0 config routes test --config.file=/etc/alertmanager/alertmanager.yml alertname=SyntheticAlert severity=critical
        if ($LASTEXITCODE -ne 0 -or $amRouteDrill.Trim() -ne "drill_responder_webhook") {
            throw "amtool config routes test failed: expected drill_responder_webhook, got $amRouteDrill"
        }
        Write-Host "  ✓ amtool route test (synthetic drill): alerts route to $($amRouteDrill.Trim())" -ForegroundColor Green

        # ----------------------------------------------------------------------
        # Live Containerized Alertmanager -> Drill-Responder Delivery Drill
        # ----------------------------------------------------------------------
        Write-Host "`n  [4b] Verifying Live Containerized Alertmanager -> Responder Delivery..." -ForegroundColor Yellow

        $secretDir = Join-Path $baseDir ".local/dev"
        $secretPath = Join-Path $secretDir "drill_responder_token"
        if (-not (Test-Path $secretPath)) {
            Write-Host "    Generating secrets via devstack in $secretDir..." -ForegroundColor Gray
            & go run ./cmd/devstack secrets $secretDir
            if ($LASTEXITCODE -ne 0 -or -not (Test-Path $secretPath)) {
                throw "Failed to bootstrap responder secrets in $secretDir via devstack secrets"
            }
        }
        $secretToken = (Get-Content $secretPath -Raw).Trim()

        Write-Host "    Starting drill-responder and alertmanager containers..." -ForegroundColor Gray
        docker compose --profile observability up -d drill-responder alertmanager | Out-Null
        try {
            $ready = $false
            for ($i = 0; $i -lt 15; $i++) {
                $liveCode = curl.exe -s -o /dev/null -w "%{http_code}" http://127.0.0.1:9099/health/live
                $amReadyCode = curl.exe -s -o /dev/null -w "%{http_code}" http://127.0.0.1:9093/-/ready
                if ($liveCode -eq "200" -and $amReadyCode -eq "200") {
                    $ready = $true
                    break
                }
                Start-Sleep -Seconds 1
            }
            if (-not $ready) {
                throw "Alertmanager or Drill-Responder container failed to become ready within timeout"
            }

            # 1. Assert unauthenticated query rejected with 401
            $unauthCode = curl.exe -s -o /dev/null -w "%{http_code}" http://127.0.0.1:9099/alerts
            if ($unauthCode -ne "401") {
                throw "Expected 401 Unauthorized without bearer token on GET /alerts, got $unauthCode"
            }
            Write-Host "    ✓ Unauthorized access to responder rejected with 401" -ForegroundColor Green

            # 2. Post synthetic firing alert to Alertmanager with unique drill run ID
            $drillId = [System.Guid]::NewGuid().ToString("N").Substring(0, 8)
            $alertName = "SyntheticPaymentDrill_$drillId"
            $startsAt = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
            $firingPayload = @"
[
  {
    "labels": {
      "alertname": "$alertName",
      "severity": "critical",
      "service": "bliss-backend"
    },
    "annotations": {
      "summary": "Live container drill: 5 payment attempts pending (run $drillId)",
      "runbook_url": "docs/runbooks/runbook-payment-reconciliation-failure.md"
    },
    "startsAt": "$startsAt"
  }
]
"@
            $tempFiringFile = [System.IO.Path]::GetTempFileName()
            Set-Content -Path $tempFiringFile -Value $firingPayload
            $null = curl.exe -s -X POST http://127.0.0.1:9093/api/v2/alerts -H "Content-Type: application/json" --data-binary "@$tempFiringFile"
            Remove-Item $tempFiringFile

            # Poll responder for firing alert notification (group_wait is 2s)
            $deliveredFiring = $false
            $alertsAfterFiring = "[]"
            for ($j = 0; $j -lt 10; $j++) {
                Start-Sleep -Seconds 1
                $alertsAfterFiring = curl.exe -s -H "Authorization: Bearer $secretToken" http://127.0.0.1:9099/alerts
                if ($alertsAfterFiring -match "firing" -and $alertsAfterFiring -match $alertName) {
                    $deliveredFiring = $true
                    break
                }
            }
            if (-not $deliveredFiring) {
                throw "Drill responder did not receive firing alert notification from Alertmanager: $alertsAfterFiring"
            }
            Write-Host "    ✓ Alertmanager delivered firing alert ($alertName) to responder with Docker secret auth" -ForegroundColor Green

            # 3. Post synthetic resolved alert to Alertmanager
            $endsAt = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
            $resolvedPayload = @"
[
  {
    "labels": {
      "alertname": "$alertName",
      "severity": "critical",
      "service": "bliss-backend"
    },
    "annotations": {
      "summary": "Live container drill: 5 payment attempts pending (run $drillId)",
      "runbook_url": "docs/runbooks/runbook-payment-reconciliation-failure.md"
    },
    "startsAt": "$startsAt",
    "endsAt": "$endsAt"
  }
]
"@
            $tempResolvedFile = [System.IO.Path]::GetTempFileName()
            Set-Content -Path $tempResolvedFile -Value $resolvedPayload
            $null = curl.exe -s -X POST http://127.0.0.1:9093/api/v2/alerts -H "Content-Type: application/json" --data-binary "@$tempResolvedFile"
            Remove-Item $tempResolvedFile

            # Poll responder for resolved alert notification (group_interval is 5s)
            $deliveredResolved = $false
            $alertsAfterResolve = "[]"
            for ($k = 0; $k -lt 12; $k++) {
                Start-Sleep -Seconds 1
                $alertsAfterResolve = curl.exe -s -H "Authorization: Bearer $secretToken" http://127.0.0.1:9099/alerts
                if ($alertsAfterResolve -match "resolved" -and $alertsAfterResolve -match $alertName) {
                    $deliveredResolved = $true
                    break
                }
            }
            if (-not $deliveredResolved) {
                throw "Drill responder did not receive resolved alert notification from Alertmanager: $alertsAfterResolve"
            }
            Write-Host "    ✓ Alertmanager delivered resolved alert ($alertName) to responder with Docker secret auth" -ForegroundColor Green
        } finally {
            docker compose --profile observability stop drill-responder alertmanager | Out-Null
        }

        # ----------------------------------------------------------------------
        # Live Containerized RabbitMQ & Shovel Plugin Verification
        # ----------------------------------------------------------------------
        Write-Host "`n  [4c] Verifying Live Containerized RabbitMQ Broker & Shovel Plugin..." -ForegroundColor Yellow
        Write-Host "    Starting rabbitmq container..." -ForegroundColor Gray
        docker compose --profile observability up -d rabbitmq | Out-Null
        try {
            $rabbitReady = $false
            $prevPref = $ErrorActionPreference
            for ($r = 0; $r -lt 45; $r++) {
                $ErrorActionPreference = 'Continue'
                $diag = docker compose --profile observability exec -T rabbitmq rabbitmq-diagnostics -q check_running 2>&1
                $code = $LASTEXITCODE
                $ErrorActionPreference = $prevPref
                if ($code -eq 0) {
                    $rabbitReady = $true
                    break
                }
                Start-Sleep -Seconds 2
            }
            if (-not $rabbitReady) {
                throw "RabbitMQ container failed to become ready within timeout"
            }
            Write-Host "    ✓ RabbitMQ broker fully booted and healthy on private network" -ForegroundColor Green

            # Verify rabbitmq_shovel plugin is enabled
            $prevPref = $ErrorActionPreference
            $ErrorActionPreference = 'Continue'
            $shovelEnabled = docker compose --profile observability exec -T rabbitmq rabbitmq-plugins is_enabled rabbitmq_shovel 2>&1
            $code = $LASTEXITCODE
            $ErrorActionPreference = $prevPref
            if ($code -ne 0) {
                throw "rabbitmq_shovel plugin is not enabled: $shovelEnabled"
            }
            Write-Host "    ✓ rabbitmq_shovel plugin is enabled out-of-the-box" -ForegroundColor Green

            # Verify dynamic shovel replay parameter can be declared with valid JSON
            $shovelObj = [ordered]@{
                "src-uri" = "amqp://"
                "src-queue" = "bliss.notifications.v1.dead"
                "dest-uri" = "amqp://"
                "dest-queue" = "bliss.notifications.v1"
                "src-delete-after" = "queue-length"
            }
            $shovelJson = $shovelObj | ConvertTo-Json -Compress
            $argJson = if ($PSVersionTable.PSVersion.Major -le 5) { $shovelJson.Replace('"', '\"') } else { $shovelJson }
            $prevPref = $ErrorActionPreference
            $ErrorActionPreference = 'Continue'
            $shovelRes = docker compose --profile observability exec -T rabbitmq rabbitmqctl set_parameter shovel drill-test-shovel $argJson 2>&1
            $code = $LASTEXITCODE
            $ErrorActionPreference = $prevPref
            if ($code -ne 0) {
                throw "Failed to set test shovel parameter: $shovelRes"
            }
            # Clean up the test parameter if still active
            $prevPref = $ErrorActionPreference
            $ErrorActionPreference = 'Continue'
            $null = docker compose --profile observability exec -T rabbitmq rabbitmqctl clear_parameter shovel drill-test-shovel 2>&1
            $ErrorActionPreference = $prevPref
            Write-Host "    ✓ Supported dynamic shovel parameter validated for DLQ replay" -ForegroundColor Green
        } finally {
            docker compose --profile observability stop rabbitmq | Out-Null
        }
    } finally {
        Pop-Location
    }
} else {
    if ($RequireDocker) {
        throw "Docker engine is required for containerized promtool, amtool, and live Alertmanager delivery drills, but Docker was not found in PATH or daemon is unreachable"
    }
    $dockerSkipped = $true
    Write-Host "  ! Docker unavailable or daemon not responding; skipping containerized promtool/amtool checks" -ForegroundColor Yellow
}

# ------------------------------------------------------------------------------
# STEP 5: Execute Go Synthetic Incident Drill Suite & Recovery Lifecycles
# ------------------------------------------------------------------------------
Write-Host "`n[5/6] Executing Go Synthetic Incident Drills & Recovery Lifecycles..." -ForegroundColor Yellow

Push-Location $baseDir
try {
    $drillOutput = go test -v -run "TestSyntheticIncidentDrill|TestRunbookDiagnosisAndRecovery" ./internal/telemetry
    if ($LASTEXITCODE -ne 0) {
        throw "Go synthetic incident drill failed with exit code $LASTEXITCODE"
    }
    $drillOutput | ForEach-Object { Write-Host "    $_" -ForegroundColor Gray }
    Write-Host "  ✓ Incident induction, diagnosis, and recovery lifecycles verified" -ForegroundColor Green
} finally {
    Pop-Location
}

# ------------------------------------------------------------------------------
# STEP 6: Strict PII & Metric Label Cardinality Audit
# ------------------------------------------------------------------------------
Write-Host "`n[6/6] Auditing Telemetry for PII Redaction & Label Cardinality..." -ForegroundColor Yellow

Push-Location $baseDir
try {
    $auditOutput = go test -v -run "TestTelemetryRedactionAndZeroPIIInMetrics" ./internal/telemetry
    if ($LASTEXITCODE -ne 0) {
        throw "PII / Cardinality audit failed with exit code $LASTEXITCODE"
    }
    Write-Host "  ✓ Zero customer phone numbers, addresses, or tokens found in metrics" -ForegroundColor Green
    Write-Host "  ✓ Zero dynamic order IDs found in metric labels" -ForegroundColor Green
} finally {
    Pop-Location
}

if ($dockerSkipped) {
    Write-Host "`n=================================================================" -ForegroundColor Yellow
    Write-Host "  PARTIAL RUN: CODE DRILLS PASSED (CONTAINER CHECKS SKIPPED)      " -ForegroundColor Yellow
    Write-Host "=================================================================" -ForegroundColor Yellow
} else {
    Write-Host "`n=================================================================" -ForegroundColor Green
    Write-Host "  ALL OBSERVABILITY DRILLS & AUDITS PASSED WITH FULL COMPLIANCE   " -ForegroundColor Green
    Write-Host "=================================================================" -ForegroundColor Green
}
