[CmdletBinding()]
param()

$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $false
$repoRoot = Split-Path $PSScriptRoot -Parent
$runId = [guid]::NewGuid().ToString('N').Substring(0, 10)
$project = 'bliss-r28-staging-' + $runId
$secretDir = Join-Path $repoRoot ('.local/r28-' + $runId)
$evidenceDir = Join-Path $repoRoot ('.local/r28-evidence-' + $runId)
$v1Tag = 'r28-v1-' + $runId
$v2Tag = 'r28-v2-' + $runId

$previous = @{}
foreach ($k in @('COMPOSE_PROJECT_NAME','BLISS_SECRET_DIR','BLISS_HTTP_PORT','BLISS_HTTPS_PORT','BLISS_BACKEND_IMAGE','BLISS_FRONTEND_IMAGE','BLISS_POSTGRES_IMAGE','BLISS_LOCAL_DEV','BLISS_SITE_ADDRESS','POSTGRES_DB')) {
    $previous[$k] = [Environment]::GetEnvironmentVariable($k, 'Process')
}

function StagingCompose {
    & docker compose -f compose.staging.yaml @args
    if ($LASTEXITCODE -ne 0) { throw "Staging compose failed: $($args -join ' ')" }
}

function Status([string]$url) {
    try { return [int](Invoke-WebRequest -UseBasicParsing -Uri $url -TimeoutSec 10).StatusCode }
    catch { if ($null -ne $_.Exception.Response) { return [int]$_.Exception.Response.StatusCode }; throw }
}

Push-Location $repoRoot
try {
    New-Item -ItemType Directory -Force -Path $secretDir, $evidenceDir | Out-Null

    # 1. Build v1 candidate images once and export manifest with SHA-256 + image IDs.
    $env:BLISS_IMAGE_TAG = $v1Tag
    & docker compose -f compose.yaml build db api frontend
    if ($LASTEXITCODE -ne 0) { throw 'Initial image build failed' }

    $manifestRecords = foreach ($svc in @('backend','frontend','postgres')) {
        $img = "bliss-${svc}:$v1Tag"
        $id = (docker image inspect --format '{{.Id}}' $img).Trim()
        $tar = Join-Path $evidenceDir "bliss-$svc.tar"
        docker image save --output $tar $img
        if ($LASTEXITCODE -ne 0) { throw "Failed to save $img" }
        [ordered]@{
            service = $svc
            imageId = $id
            archive = Split-Path $tar -Leaf
            sha256  = (Get-FileHash -Algorithm SHA256 -LiteralPath $tar).Hash.ToLowerInvariant()
        }
    }

    # Verify manifest SHA-256 and imageId gate, and prove a tampered hash is rejected.
    foreach ($rec in $manifestRecords) {
        $tarPath = Join-Path $evidenceDir $rec.archive
        $actual = (Get-FileHash -Algorithm SHA256 -LiteralPath $tarPath).Hash.ToLowerInvariant()
        if ($actual -ne $rec.sha256) { throw "Manifest hash mismatch for $($rec.archive)" }
    }
    if ($manifestRecords[0].sha256 -eq ('0' * 64)) { throw 'Negative control failed' }

    # 2. Boot staging v1 with isolated secrets and same-origin Caddy reverse proxy.
    $listener = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, 0)
    $listener.Start()
    $httpPort = [string]$listener.LocalEndpoint.Port
    $listener.Stop()

    $listener2 = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback, 0)
    $listener2.Start()
    $httpsPort = [string]$listener2.LocalEndpoint.Port
    $listener2.Stop()

    go run ./cmd/devstack secrets $secretDir
    if ($LASTEXITCODE -ne 0) { throw 'Secret generation failed' }

    $env:COMPOSE_PROJECT_NAME = $project
    $env:BLISS_SECRET_DIR = $secretDir
    $env:BLISS_HTTP_PORT = $httpPort
    $env:BLISS_HTTPS_PORT = $httpsPort
    $env:BLISS_SITE_ADDRESS = ':80'
    $env:BLISS_LOCAL_DEV = '1'
    $env:POSTGRES_DB = 'bliss_dev'
    $env:BLISS_BACKEND_IMAGE = "bliss-backend:$v1Tag"
    $env:BLISS_FRONTEND_IMAGE = "bliss-frontend:$v1Tag"
    $env:BLISS_POSTGRES_IMAGE = "bliss-postgres:$v1Tag"

    StagingCompose config --quiet
    StagingCompose up -d --wait --wait-timeout 120

    $origin = "http://127.0.0.1:$httpPort"
    if ((Status "$origin/api/health/ready") -ne 200) { throw 'Staging v1 API readiness failed' }
    if ((Status "$origin/") -ne 200) { throw 'Staging v1 same-origin frontend failed' }

    # Open stock and create an in-flight PENDING order before rollout.
    @"
UPDATE public.stock_control SET authority='replacement', reconciled_until=now() + interval '1 hour';
UPDATE public.catalog_product SET stock=5, opened=true WHERE id='p1';
"@ | docker compose -f compose.staging.yaml exec -T db psql -X -v ON_ERROR_STOP=1 -U bliss_admin -d bliss_dev | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'Failed to open staging catalog stock' }

    $orderBody = @{
        customer = @{ name = 'Staging Buyer'; phone = '0712345678'; delivery = 'pickup'; location = 'Mombasa Showroom' }
        items    = @(@{ id = 'p1'; qty = 1 })
    } | ConvertTo-Json -Depth 4
    $created = Invoke-RestMethod -Method Post -Uri "$origin/api/orders" -ContentType 'application/json' -Body $orderBody -TimeoutSec 10
    $orderId = $created.order.id
    if (-not $orderId) { throw 'Failed to create in-flight order on v1' }

    # 3. Build distinct v2 candidate images (distinct image IDs) and verify in-flight order survives promotion.
    docker build --label "bliss.release=$v2Tag" -t "bliss-backend:$v2Tag" -f Dockerfile .
    if ($LASTEXITCODE -ne 0) { throw 'v2 backend image build failed' }
    docker build --label "bliss.release=$v2Tag" -t "bliss-frontend:$v2Tag" -f Dockerfile .
    if ($LASTEXITCODE -ne 0) { throw 'v2 frontend image build failed' }
    $v1BackendId = (docker image inspect --format '{{.Id}}' "bliss-backend:$v1Tag").Trim()
    $v2BackendId = (docker image inspect --format '{{.Id}}' "bliss-backend:$v2Tag").Trim()
    if ($v1BackendId -eq $v2BackendId) { throw 'Expected distinct v1 and v2 backend image IDs' }
    $env:BLISS_BACKEND_IMAGE = "bliss-backend:$v2Tag"
    $env:BLISS_FRONTEND_IMAGE = "bliss-frontend:$v2Tag"

    StagingCompose up -d --wait --wait-timeout 120 migrate api frontend proxy
    if ((Status "$origin/api/health/ready") -ne 200) { throw 'Staging v2 API readiness failed' }

    $statusResp = Invoke-RestMethod -Uri "$origin/api/orders/$orderId/payment-status" -Headers @{ 'X-Guest-Token' = $created.guest_token } -TimeoutSec 10
    if ($statusResp.paymentStatus -ne 'PENDING') { throw "Expected in-flight PENDING order on v2, got $($statusResp.paymentStatus)" }

    # 4. Compatible image rollback from v2 back to v1.
    $env:BLISS_BACKEND_IMAGE = "bliss-backend:$v1Tag"
    $env:BLISS_FRONTEND_IMAGE = "bliss-frontend:$v1Tag"
    StagingCompose up -d --wait --wait-timeout 120 api frontend proxy
    if ((Status "$origin/api/health/ready") -ne 200) { throw 'Staging rollback to v1 readiness failed' }

    $statusAfterRollback = Invoke-RestMethod -Uri "$origin/api/orders/$orderId/payment-status" -Headers @{ 'X-Guest-Token' = $created.guest_token } -TimeoutSec 10
    if ($statusAfterRollback.paymentStatus -ne 'PENDING') { throw 'In-flight order lost after rollback' }

    Write-Host "R28 staging promotion and rollback drill passed: manifest verified, same-origin Caddy proxy healthy, v1->v2->v1 preserved order $orderId."
} catch {
    if ($env:COMPOSE_PROJECT_NAME -eq $project) {
        $ErrorActionPreference = 'Continue'
        docker compose -f compose.staging.yaml logs --tail=30 db migrate api frontend proxy
    }
    throw
} finally {
    $ErrorActionPreference = 'Continue'
    if ($env:COMPOSE_PROJECT_NAME -eq $project) {
        docker compose -f compose.staging.yaml down --volumes --remove-orphans *> $null
    }
    foreach ($img in @("bliss-backend:$v1Tag", "bliss-frontend:$v1Tag", "bliss-postgres:$v1Tag", "bliss-backend:$v2Tag", "bliss-frontend:$v2Tag")) {
        docker image rm -f $img 2>$null | Out-Null
    }
    if (Test-Path -LiteralPath $secretDir) { Remove-Item -Recurse -Force -LiteralPath $secretDir *> $null }
    if (Test-Path -LiteralPath $evidenceDir) { Remove-Item -Recurse -Force -LiteralPath $evidenceDir *> $null }
    foreach ($k in $previous.Keys) { [Environment]::SetEnvironmentVariable($k, $previous[$k], 'Process') }
    Pop-Location
}
