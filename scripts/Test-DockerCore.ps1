[CmdletBinding()]
param([string]$EvidenceDirectory = '')

$ErrorActionPreference = 'Stop'
# The migration-failure drill deliberately expects a nonzero native exit code.
$PSNativeCommandUseErrorActionPreference = $false
$repoRoot = Split-Path $PSScriptRoot -Parent
$suffix = [guid]::NewGuid().ToString('N').Substring(0, 10)
$project = 'bliss-r07-check-' + $suffix
$secretDirectory = Join-Path $repoRoot ('.local/check-' + $suffix)
$evidence = $null
if ($EvidenceDirectory) {
    $evidence = [System.IO.Path]::GetFullPath((Join-Path $repoRoot $EvidenceDirectory))
    New-Item -ItemType Directory -Force -Path $evidence | Out-Null
}
$previous = @{}
foreach ($key in @('COMPOSE_PROJECT_NAME','BLISS_SECRET_DIR','BLISS_HTTP_PORT','BLISS_IMAGE_TAG')) {
    $previous[$key] = [Environment]::GetEnvironmentVariable($key, 'Process')
}
function Compose {
    & docker compose @args
    if ($LASTEXITCODE -ne 0) { throw 'Compose operation failed' }
}
function Sql([string]$query) {
    $query | docker compose exec -T db psql -X -At -v ON_ERROR_STOP=1 -U bliss_admin -d bliss_dev
    if ($LASTEXITCODE -ne 0) { throw 'Disposable SQL check failed' }
}
function Container([string]$service) { return (docker compose ps --all --quiet $service) }
function Status([string]$url) {
    try { return [int](Invoke-WebRequest -UseBasicParsing -Uri $url -TimeoutSec 10).StatusCode }
    catch { if ($null -ne $_.Exception.Response) {return [int]$_.Exception.Response.StatusCode}; throw }
}
Push-Location $repoRoot
try {
    $env:COMPOSE_PROJECT_NAME = $project
    $env:BLISS_SECRET_DIR = $secretDirectory
    $env:BLISS_IMAGE_TAG = $suffix
    $listener = [System.Net.Sockets.TcpListener]::new([System.Net.IPAddress]::Loopback,0)
    $listener.Start()
    $env:BLISS_HTTP_PORT = [string]$listener.LocalEndpoint.Port
    $listener.Stop()
    Push-Location backend
    try {
        go run ./cmd/devstack secrets $secretDirectory
        if ($LASTEXITCODE -ne 0) { throw 'Local credential generation failed' }
    } finally {Pop-Location}
    Compose config --quiet
    Compose build db api frontend
    Compose up -d --wait --wait-timeout 120
    $privateNetwork = (docker network inspect ($project + '_private') | ConvertFrom-Json)[0]
    if (-not $privateNetwork.Internal) {throw 'Private service network allows external egress'}
    $origin = 'http://127.0.0.1:' + $env:BLISS_HTTP_PORT
    $checkout = Invoke-WebRequest -UseBasicParsing ($origin + '/checkout.html')
    if ($checkout.StatusCode -ne 200 -or $checkout.Content -notmatch 'api-base-url" content=""') {throw 'Same-origin checkout was not served'}
    if ($checkout.Content -match 'cdn.tailwindcss.com') {throw 'Checkout still requires runtime Tailwind CDN'}
    foreach ($path in @('/checkout.js','/shared.js','/style.css','/tailwind.css','/api/health/live','/api/health/ready')) {
        if ((Status ($origin + $path)) -ne 200) {throw 'Static asset or same-origin health failed'}
    }
    foreach ($service in @('frontend','api','migrate','db','fake-provider')) {
        $detail = (docker inspect (Container $service) | ConvertFrom-Json)[0]
        if ($detail.Config.User -eq '' -or $detail.Config.User -match '^0(:|$)') {throw 'Root container detected'}
        if ($service -ne 'frontend' -and $null -ne $detail.HostConfig.PortBindings -and @($detail.HostConfig.PortBindings.PSObject.Properties).Count -gt 0) {throw 'Internal service published a host port'}
        if ($service -ne 'db' -and -not $detail.HostConfig.ReadonlyRootfs) {throw 'Application root filesystem is writable'}
        if ($service -eq 'frontend') {
            foreach ($binding in $detail.HostConfig.PortBindings.'8080/tcp') {
                if ($binding.HostIp -ne '127.0.0.1') {throw 'Frontend is exposed beyond loopback'}
            }
        }
    }
    $certificatePath = Join-Path $secretDirectory 'ca.crt'
    docker cp "$(Container api):/etc/ssl/certs/ca-certificates.crt" $certificatePath
    if ($LASTEXITCODE -ne 0 -or (Get-Item -LiteralPath $certificatePath).Length -lt 1000) {throw 'Runtime CA store missing'}
    # Inspect final image layers, not mounted running files, for generated secrets.
    foreach ($image in @("bliss-backend:$suffix", "bliss-frontend:$suffix", "bliss-postgres:$suffix")) {
        $archive = Join-Path $secretDirectory 'image.tar'
        if ($evidence) {
            $archive = Join-Path $evidence (($image.Split(':')[0]) + '.tar')
        }
        docker image save --output $archive $image
        if ($LASTEXITCODE -ne 0) {throw 'Image export failed'}
        Push-Location backend
        try {
            go run ./cmd/devstack audit-image $archive $secretDirectory
            if ($LASTEXITCODE -ne 0) {throw 'Image layer credential scan failed'}
        } finally {Pop-Location}
        if (-not $evidence) {Remove-Item -LiteralPath $archive}
    }
    if ($evidence) {
        $browserReports = Join-Path $evidence 'browser'
        New-Item -ItemType Directory -Force -Path $browserReports | Out-Null
        docker run --rm --network ($project + '_edge') --mount "type=bind,source=$repoRoot/tools/ci,target=/source,readonly" --mount "type=bind,source=$browserReports,target=/reports" mcr.microsoft.com/playwright:v1.64.1-noble@sha256:7852dc5a7d72f5fe458a597d690f6c42fbc8e9e26ddfa95f8e6ef552558d9bbf sh -ec 'cp -R /source /tmp/ci; cd /tmp/ci; npm ci --ignore-scripts; npm run browser'
        if ($LASTEXITCODE -ne 0) {
            $reportFile = Join-Path $browserReports 'browser.json'
            if (Test-Path -LiteralPath $reportFile) {
                Write-Host "Browser smoke report:`n$((Get-Content -Raw -LiteralPath $reportFile))"
            }
            throw 'Real browser smoke failed'
        }
    }
    Compose run --rm --no-deps --entrypoint /app/devstack api provider-check
    Sql @'
INSERT INTO public."Order"(id,"updatedAt","customerName","customerPhone","deliveryLocation","totalKes",items)
VALUES ('r07-persistence',now(),'Synthetic Customer','254700000000','Synthetic pickup',2500,'[]');
'@ | Out-Null
    # down without volumes followed by up proves persistence across recreation.
    Compose down
    Compose up -d --wait --wait-timeout 120
    if ((Sql 'SELECT "totalKes" FROM public."Order" WHERE id=''r07-persistence''') -ne '2500') {throw 'Data did not survive recreation'}
    Sql 'REVOKE SELECT ON public."Order" FROM bliss_runtime' | Out-Null
    if ((Status ($origin + '/api/health/ready')) -ne 503) {throw 'Missing runtime privileges did not disable readiness'}
    Sql 'GRANT SELECT ON public."Order" TO bliss_runtime' | Out-Null
    # Check a running API becomes unready while its process remains live.
    Sql "UPDATE public.bliss_schema_migrations SET checksum='intentional-r07-mismatch' WHERE version=1" | Out-Null
    if ((Status ($origin + '/api/health/ready')) -ne 503 -or (Status ($origin + '/api/health/live')) -ne 200) {throw 'Schema mismatch did not disable readiness independently of liveness'}
    Compose down
    & docker compose up -d --wait --wait-timeout 60
    if ($LASTEXITCODE -eq 0) {throw 'Mismatched migration incorrectly allowed startup'}
    $apiID = Container api
    if ($apiID -and (docker inspect --format '{{.State.Running}}' $apiID) -eq 'true') {throw 'API started after failed migration'}
    $expectedHash = '0bf265f9da0f9aaf07cb4a91e54efc03bad02aa33935806b1ba8d03d96226e49'
    Sql ("UPDATE public.bliss_schema_migrations SET checksum='" + $expectedHash + "' WHERE version=1") | Out-Null
    Compose up -d --wait --wait-timeout 120
    if ((Status ($origin + '/api/health/ready')) -ne 200) {throw 'Recovery failed'}
    $catalog = Invoke-RestMethod -Uri ($origin + '/api/catalog') -TimeoutSec 10
    if ($catalog.products.Count -ne 23) {throw 'Catalog seed count differs'}
    foreach ($product in $catalog.products) {
        if ($product.priceKes -ne 2500 -or $product.available) {throw 'Unopened catalog price/availability differs'}
    }

    if ($evidence) {
        $imageRecords = foreach ($kind in @('backend','frontend','postgres')) {
            $image = "bliss-${kind}:$suffix"
            $detail = (docker image inspect $image | ConvertFrom-Json)[0]
            $archive = Join-Path $evidence ("bliss-$kind.tar")
            [ordered]@{service=$kind; imageId=$detail.Id; archive=(Split-Path $archive -Leaf); sha256=(Get-FileHash -Algorithm SHA256 -LiteralPath $archive).Hash.ToLowerInvariant()}
        }
        $manifest = [ordered]@{sourceCommit=(git rev-parse HEAD); runId=$env:GITHUB_RUN_ID; runAttempt=$env:GITHUB_RUN_ATTEMPT; platform='linux/amd64'; environment='disposable-local'; status='passed'; images=@($imageRecords)}
        [System.IO.File]::WriteAllText((Join-Path $evidence 'build-manifest.json'),($manifest | ConvertTo-Json -Depth 6),[System.Text.UTF8Encoding]::new($false))
        [System.IO.File]::WriteAllText((Join-Path $evidence 'acceptance.json'),' {"checkout":true,"persistence":true,"migrationFailure":true,"privateServices":true,"credentialLayers":true,"providerFixture":true} ',[System.Text.UTF8Encoding]::new($false))
    }
    docker stats --no-stream --format '{{.Name}} {{.CPUPerc}} {{.MemUsage}}' (Container frontend) (Container api) (Container db) (Container fake-provider)
    Write-Host 'R07 Docker core checks passed: checkout, proxy, non-root/private services, CA store, clean layers, persistence and migration failure gating.'
} catch {
    docker compose logs --tail=50 db migrate api frontend
    throw
} finally {
    # Destructive cleanup is confined to this generated disposable project.
    if ($env:COMPOSE_PROJECT_NAME -eq $project -and $project.StartsWith('bliss-r07-check-')) {
        docker compose down --volumes --remove-orphans
    }
    foreach ($key in $previous.Keys) {[Environment]::SetEnvironmentVariable($key,$previous[$key],'Process')}
    Pop-Location
}
