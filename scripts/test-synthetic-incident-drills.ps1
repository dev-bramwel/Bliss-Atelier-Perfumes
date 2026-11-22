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
        # Live Containerized Prometheus -> Alertmanager -> Responder Pipeline Drill
        # ----------------------------------------------------------------------
        Write-Host "`n  [4b] Verifying Live Containerized Prometheus -> Alertmanager -> Responder Delivery Pipeline..." -ForegroundColor Yellow

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

        # Capture environment state before modifications to guarantee restoration in finally block
        $prevForwardUrl = $env:GO_ESCALATION_FORWARD_URL
        $prevPlatformUrl = $env:GO_PLATFORM_ESCALATION_URL
        $prevFinanceUrl = $env:GO_FINANCE_ESCALATION_URL

        # Inspect actively running observability services so they can be restored rather than removed
        $runningServices = @(docker compose --profile observability ps --services --filter "status=running" 2>$null)
        $wasResponderRunning = $runningServices -contains "drill-responder"
        $wasAlertmanagerRunning = $runningServices -contains "alertmanager"
        $wasPrometheusRunning = $runningServices -contains "prometheus"

        function Post-DrillJson {
            param([string]$Url, [string]$Json, [string]$Token)
            $tmpFile = [System.IO.Path]::GetTempFileName()
            try {
                [System.IO.File]::WriteAllText($tmpFile, $Json, [System.Text.UTF8Encoding]::new($false))
                if ($Token) {
                    return (curl.exe -s -o /dev/null -w "%{http_code}" -X POST $Url -H "Authorization: Bearer $Token" -H "Content-Type: application/json" --data-binary "@$tmpFile")
                } else {
                    return (curl.exe -s -o /dev/null -w "%{http_code}" -X POST $Url -H "Content-Type: application/json" --data-binary "@$tmpFile")
                }
            } finally {
                if (Test-Path $tmpFile) { Remove-Item $tmpFile -Force }
            }
        }

        function Find-MatchingAlert {
            param([string]$RawJson, [string]$AlertName, [string]$Status, [string]$Receiver)
            if (-not $RawJson -or -not $RawJson.Trim().StartsWith("[")) {
                return $false
            }
            try {
                $notifications = $RawJson | ConvertFrom-Json
                foreach ($n in $notifications) {
                    if ($Receiver -and $n.receiver -ne $Receiver) {
                        continue
                    }
                    if ($n.alerts) {
                        foreach ($a in $n.alerts) {
                            $matchesName = ($a.labels -and $a.labels.alertname -eq $AlertName) -or ($n.commonLabels -and $n.commonLabels.alertname -eq $AlertName)
                            $matchesStatus = (-not $Status) -or ($a.status -eq $Status)
                            if ($matchesName -and $matchesStatus) {
                                return $true
                            }
                        }
                    }
                }
            } catch {
                return $false
            }
            return $false
        }

        try {
            # Isolate drill execution to in-container mock test sink, preventing accidental real pages
            $env:GO_ESCALATION_FORWARD_URL = "http://127.0.0.1:9099/test-sink"
            $env:GO_PLATFORM_ESCALATION_URL = "http://127.0.0.1:9099/test-sink"
            $env:GO_FINANCE_ESCALATION_URL = "http://127.0.0.1:9099/test-sink"

            Write-Host "    Starting drill-responder, alertmanager, and prometheus containers..." -ForegroundColor Gray
            docker compose --profile observability up -d --force-recreate --no-deps drill-responder alertmanager prometheus | Out-Null

            $ready = $false
            for ($i = 0; $i -lt 20; $i++) {
                $liveCode = curl.exe -s -o /dev/null -w "%{http_code}" http://127.0.0.1:9099/health/live
                $amReadyCode = curl.exe -s -o /dev/null -w "%{http_code}" http://127.0.0.1:9093/-/ready
                $promReadyCode = curl.exe -s -o /dev/null -w "%{http_code}" http://127.0.0.1:9090/-/ready
                if ($liveCode -eq "200" -and $amReadyCode -eq "200" -and $promReadyCode -eq "200") {
                    $ready = $true
                    break
                }
                Start-Sleep -Seconds 1
            }
            if (-not $ready) {
                throw "Prometheus, Alertmanager, or Drill-Responder container failed to become ready within timeout"
            }

            # 1. Assert unauthenticated queries are rejected with 401
            $unauthCode = curl.exe -s -o /dev/null -w "%{http_code}" http://127.0.0.1:9099/alerts
            if ($unauthCode -ne "401") {
                throw "Expected 401 Unauthorized without bearer token on GET /alerts, got $unauthCode"
            }
            $unauthSinkCode = curl.exe -s -o /dev/null -w "%{http_code}" http://127.0.0.1:9099/test-sink
            if ($unauthSinkCode -ne "401") {
                throw "Expected 401 Unauthorized without bearer token on GET /test-sink, got $unauthSinkCode"
            }
            Write-Host "    ✓ Unauthorized access to responder and test sink rejected with 401" -ForegroundColor Green

            # 2. Verify Prometheus scrape target health evaluation (ServiceTargetDown)
            Write-Host "    Awaiting live Prometheus scrape rule evaluation (ServiceTargetDown)..." -ForegroundColor Gray
            $promDelivered = $false
            for ($p = 0; $p -lt 45; $p++) {
                Start-Sleep -Seconds 1
                $responderAlerts = curl.exe -s -H "Authorization: Bearer $secretToken" http://127.0.0.1:9099/alerts
                if (Find-MatchingAlert -RawJson $responderAlerts -AlertName "ServiceTargetDown" -Receiver "platform_sre_escalation") {
                    $promDelivered = $true
                    break
                }
            }
            if (-not $promDelivered) {
                throw "Prometheus did not fire ServiceTargetDown to Alertmanager and Responder within 45s. Responder output: $responderAlerts"
            }
            Write-Host "    ✓ Live Prometheus scrape pipeline evaluated target status and triggered ServiceTargetDown" -ForegroundColor Green
            Write-Host "    ✓ Alertmanager routed ServiceTargetDown to responder escalation channel (/escalations/platform-sre)" -ForegroundColor Green

            # 3. Live Payment Exception Rule Delivery through Prometheus Scrape Pipeline
            Write-Host "    Inducing live payment exception metric via responder /drill/induce-metric..." -ForegroundColor Gray
            $inducePaymentBody = '{"metric":"payment_exceptions_total","label":"AMOUNT_MISMATCH","value":1}'
            $inducePaymentCode = Post-DrillJson -Url "http://127.0.0.1:9099/drill/induce-metric" -Json $inducePaymentBody -Token $secretToken
            if ($inducePaymentCode -ne "200") {
                throw "Failed to induce payment_exceptions_total metric, got HTTP $inducePaymentCode"
            }

            Write-Host "    Awaiting Prometheus scrape and rule evaluation for PaymentExceptionDetected -> finance-ops..." -ForegroundColor Gray
            $paymentDelivered = $false
            for ($p = 0; $p -lt 35; $p++) {
                Start-Sleep -Seconds 1
                $responderAlerts = curl.exe -s -H "Authorization: Bearer $secretToken" http://127.0.0.1:9099/alerts
                if (Find-MatchingAlert -RawJson $responderAlerts -AlertName "PaymentExceptionDetected" -Receiver "finance_operations_escalation") {
                    $paymentDelivered = $true
                    break
                }
            }
            if (-not $paymentDelivered) {
                throw "Prometheus did not fire PaymentExceptionDetected to Alertmanager and Responder within 35s. Responder output: $responderAlerts"
            }
            Write-Host "    ✓ Prometheus scraped application metric and fired PaymentExceptionDetected" -ForegroundColor Green
            Write-Host "    ✓ Alertmanager routed PaymentExceptionDetected to /escalations/finance-ops" -ForegroundColor Green

            # 4. Live Queue DLQ Poison Message Rule Delivery & Recovery through Prometheus Scrape Pipeline
            Write-Host "    Inducing live DLQ poison message metric via responder /drill/induce-metric..." -ForegroundColor Gray
            $induceDlqBody = '{"metric":"rabbitmq_dlq_messages_gauge","label":"notifications","value":5}'
            $induceDlqCode = Post-DrillJson -Url "http://127.0.0.1:9099/drill/induce-metric" -Json $induceDlqBody -Token $secretToken
            if ($induceDlqCode -ne "200") {
                throw "Failed to induce rabbitmq_dlq_messages_gauge metric, got HTTP $induceDlqCode"
            }

            Write-Host "    Awaiting Prometheus scrape and rule evaluation for DlqPoisonMessagesDetected -> platform-sre..." -ForegroundColor Gray
            $dlqDelivered = $false
            for ($d = 0; $d -lt 35; $d++) {
                Start-Sleep -Seconds 1
                $responderAlerts = curl.exe -s -H "Authorization: Bearer $secretToken" http://127.0.0.1:9099/alerts
                if (Find-MatchingAlert -RawJson $responderAlerts -AlertName "DlqPoisonMessagesDetected" -Status "firing") {
                    $dlqDelivered = $true
                    break
                }
            }
            if (-not $dlqDelivered) {
                throw "Prometheus did not fire DlqPoisonMessagesDetected to Alertmanager and Responder within 35s. Responder output: $responderAlerts"
            }
            Write-Host "    ✓ Prometheus scraped queue metric and fired firing DlqPoisonMessagesDetected" -ForegroundColor Green

            # Clear DLQ metric and verify resolution delivery
            Write-Host "    Clearing DLQ poison metric to test resolution dispatch through Prometheus scrape..." -ForegroundColor Gray
            $clearDlqBody = '{"metric":"rabbitmq_dlq_messages_gauge","label":"notifications"}'
            $clearDlqCode = Post-DrillJson -Url "http://127.0.0.1:9099/drill/clear-metric" -Json $clearDlqBody -Token $secretToken
            if ($clearDlqCode -ne "200") {
                throw "Failed to clear rabbitmq_dlq_messages_gauge metric, got HTTP $clearDlqCode"
            }

            $dlqResolved = $false
            for ($r = 0; $r -lt 35; $r++) {
                Start-Sleep -Seconds 1
                $responderAlerts = curl.exe -s -H "Authorization: Bearer $secretToken" http://127.0.0.1:9099/alerts
                if (Find-MatchingAlert -RawJson $responderAlerts -AlertName "DlqPoisonMessagesDetected" -Status "resolved") {
                    $dlqResolved = $true
                    break
                }
            }
            if (-not $dlqResolved) {
                throw "Prometheus did not resolve DlqPoisonMessagesDetected to Alertmanager and Responder within 35s. Responder output: $responderAlerts"
            }
            Write-Host "    ✓ Prometheus cleared metric, evaluated recovery, and Alertmanager delivered resolved alert" -ForegroundColor Green

            # 5. Live Unresolved Payment Spikes Rule Delivery through Prometheus Scrape Pipeline
            Write-Host "    Inducing unresolved payments gauge via responder /drill/induce-metric..." -ForegroundColor Gray
            $induceUnresolvedBody = '{"metric":"unresolved_payments_gauge","value":5}'
            $induceUnresolvedCode = Post-DrillJson -Url "http://127.0.0.1:9099/drill/induce-metric" -Json $induceUnresolvedBody -Token $secretToken
            if ($induceUnresolvedCode -ne "200") {
                throw "Failed to induce unresolved_payments_gauge metric, got HTTP $induceUnresolvedCode"
            }

            Write-Host "    Awaiting Prometheus scrape and rule evaluation for UnresolvedPaymentSpike -> finance-ops..." -ForegroundColor Gray
            $unresolvedDelivered = $false
            for ($u = 0; $u -lt 40; $u++) {
                Start-Sleep -Seconds 1
                $responderAlerts = curl.exe -s -H "Authorization: Bearer $secretToken" http://127.0.0.1:9099/alerts
                if (Find-MatchingAlert -RawJson $responderAlerts -AlertName "UnresolvedPaymentSpike" -Receiver "finance_operations_escalation") {
                    $unresolvedDelivered = $true
                    break
                }
            }
            if (-not $unresolvedDelivered) {
                throw "Prometheus did not fire UnresolvedPaymentSpike to Alertmanager and Responder within 40s. Responder output: $responderAlerts"
            }
            Write-Host "    ✓ Prometheus scraped unresolved payments gauge and fired UnresolvedPaymentSpike" -ForegroundColor Green
            Write-Host "    ✓ Alertmanager routed UnresolvedPaymentSpike to /escalations/finance-ops" -ForegroundColor Green

            $clearUnresolvedBody = '{"metric":"unresolved_payments_gauge"}'
            $null = Post-DrillJson -Url "http://127.0.0.1:9099/drill/clear-metric" -Json $clearUnresolvedBody -Token $secretToken

            # 6. Live API 5xx Surge Rule Delivery through Prometheus Scrape Pipeline
            Write-Host "    Inducing HTTP 5xx error rate spike via responder /drill/induce-metric..." -ForegroundColor Gray
            $null = Post-DrillJson -Url "http://127.0.0.1:9099/drill/induce-metric" -Json '{"metric":"http_requests_total","label":"200","value":10}' -Token $secretToken
            $induce5xxCode = Post-DrillJson -Url "http://127.0.0.1:9099/drill/induce-metric" -Json '{"metric":"http_requests_total","label":"500","value":100}' -Token $secretToken
            if ($induce5xxCode -ne "200") {
                throw "Failed to induce http_requests_total metric, got HTTP $induce5xxCode"
            }

            Write-Host "    Awaiting Prometheus scrape and rule evaluation for ApiHigh5xxRate -> platform-sre..." -ForegroundColor Gray
            $api5xxDelivered = $false
            for ($a = 0; $a -lt 40; $a++) {
                Start-Sleep -Seconds 1
                $responderAlerts = curl.exe -s -H "Authorization: Bearer $secretToken" http://127.0.0.1:9099/alerts
                if (Find-MatchingAlert -RawJson $responderAlerts -AlertName "ApiHigh5xxRate" -Receiver "platform_sre_escalation") {
                    $api5xxDelivered = $true
                    break
                }
            }
            if (-not $api5xxDelivered) {
                throw "Prometheus did not fire ApiHigh5xxRate to Alertmanager and Responder within 40s. Responder output: $responderAlerts"
            }
            Write-Host "    ✓ Prometheus scraped HTTP 5xx error surge and fired ApiHigh5xxRate" -ForegroundColor Green
            Write-Host "    ✓ Alertmanager routed ApiHigh5xxRate to /escalations/platform-sre" -ForegroundColor Green

            $null = Post-DrillJson -Url "http://127.0.0.1:9099/drill/clear-metric" -Json '{"metric":"http_requests_total"}' -Token $secretToken

            # 7. Verify isolated mock upstream test sink captured both platform-sre and finance-ops escalations
            Write-Host "    Verifying isolated mock upstream test sink captured forwarded escalations..." -ForegroundColor Gray
            $testSinkAlerts = curl.exe -s -H "Authorization: Bearer $secretToken" http://127.0.0.1:9099/test-sink
            if ($testSinkAlerts -notmatch "platform_sre_escalation" -or $testSinkAlerts -notmatch "finance_operations_escalation") {
                throw "Mock upstream test sink (/test-sink) did not receive both platform and finance escalations. Test sink output: $testSinkAlerts"
            }
            Write-Host "    ✓ Mock upstream test sink verified live synchronous forwarding without real paging" -ForegroundColor Green
        } finally {
            $prevEAP = $ErrorActionPreference
            $ErrorActionPreference = "Continue"

            # Restore the services that were actively running before the drill; stop services that were not running
            $servicesToRestore = @()
            $servicesToStop = @()

            if ($wasResponderRunning) { $servicesToRestore += "drill-responder" } else { $servicesToStop += "drill-responder" }
            if ($wasAlertmanagerRunning) { $servicesToRestore += "alertmanager" } else { $servicesToStop += "alertmanager" }
            if ($wasPrometheusRunning) { $servicesToRestore += "prometheus" } else { $servicesToStop += "prometheus" }

            if ($servicesToStop.Count -gt 0) {
                docker compose --profile observability stop $servicesToStop 2>&1 | Out-Null
            }
            if ($servicesToRestore.Count -gt 0) {
                docker compose --profile observability up -d --no-deps $servicesToRestore 2>&1 | Out-Null
            }
            $ErrorActionPreference = $prevEAP

            $env:GO_ESCALATION_FORWARD_URL = $prevForwardUrl
            $env:GO_PLATFORM_ESCALATION_URL = $prevPlatformUrl
            $env:GO_FINANCE_ESCALATION_URL = $prevFinanceUrl
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
