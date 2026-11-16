# ==============================================================================
# Bliss Atelier Perfumes - Observability & Synthetic Incident Drill Runner
# Issue #29 [R23]: Telemetry, Alerts, Dashboards & Incident Runbooks
# ==============================================================================

$ErrorActionPreference = "Stop"

Write-Host "=================================================================" -ForegroundColor Cyan
Write-Host "  BLISS ATELIER: OBSERVABILITY & INCIDENT DRILL VERIFICATION    " -ForegroundColor Cyan
Write-Host "=================================================================" -ForegroundColor Cyan

$baseDir = Split-Path -Parent $PSScriptRoot

# ------------------------------------------------------------------------------
# STEP 1: Verify Incident Runbooks
# ------------------------------------------------------------------------------
Write-Host "`n[1/5] Verifying Incident Runbooks in docs/runbooks/..." -ForegroundColor Yellow

$requiredRunbooks = @(
    "docs/runbooks/runbook-payment-reconciliation-failure.md",
    "docs/runbooks/runbook-dlq-poison-messages.md",
    "docs/runbooks/runbook-database-pool-exhaustion.md",
    "docs/runbooks/runbook-cache-and-rate-limit-outage.md",
    "docs/runbooks/runbook-api-error-surge.md"
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
Write-Host "`n[2/5] Verifying Prometheus Alerting Rules & Runbook Linkage..." -ForegroundColor Yellow

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
Write-Host "`n[3/5] Verifying Provisioned Grafana Dashboards..." -ForegroundColor Yellow

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
# STEP 4: Execute Go Synthetic Incident Drill Suite
# ------------------------------------------------------------------------------
Write-Host "`n[4/5] Executing Go Synthetic Incident Drill Suite..." -ForegroundColor Yellow

Push-Location $baseDir
try {
    $drillOutput = go test -v -run "TestSyntheticIncidentDrill" ./internal/telemetry
    if ($LASTEXITCODE -ne 0) {
        throw "Go synthetic incident drill failed with exit code $LASTEXITCODE"
    }
    $drillOutput | ForEach-Object { Write-Host "    $_" -ForegroundColor Gray }
    Write-Host "  ✓ Synthetic incident drill executed successfully and reached designated responder" -ForegroundColor Green
} finally {
    Pop-Location
}

# ------------------------------------------------------------------------------
# STEP 5: Strict PII & Metric Label Cardinality Audit
# ------------------------------------------------------------------------------
Write-Host "`n[5/5] Auditing Telemetry for PII Redaction & Label Cardinality..." -ForegroundColor Yellow

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

Write-Host "`n=================================================================" -ForegroundColor Green
Write-Host "  ALL OBSERVABILITY DRILLS & AUDITS PASSED WITH FULL COMPLIANCE   " -ForegroundColor Green
Write-Host "=================================================================" -ForegroundColor Green
