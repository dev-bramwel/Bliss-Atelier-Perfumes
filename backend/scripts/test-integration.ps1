$ErrorActionPreference = 'Stop'
$containerName = 'bliss-foundation-test-' + [guid]::NewGuid().ToString('N').Substring(0, 10)
$previousTestURL = $env:GO_TEST_DATABASE_URL
try {
    docker run --detach --rm --name $containerName --publish 127.0.0.1::5432 --env POSTGRES_USER=bliss_test --env POSTGRES_PASSWORD=disposable_test_only --env POSTGRES_DB=bliss_foundation_test postgres@sha256:b0f9560a2de083e2cc7382e75f808c7381a32852a7ec49117deedb300e552b24 | Out-Null
    if ($LASTEXITCODE -ne 0) { throw 'Could not start disposable PostgreSQL' }
    $ready = $false
    for ($attempt = 0; $attempt -lt 60; $attempt++) {
        docker exec $containerName pg_isready -U bliss_test -d bliss_foundation_test *> $null
        if ($LASTEXITCODE -eq 0) { $ready = $true; break }
        Start-Sleep -Seconds 1
    }
    if (-not $ready) { throw 'Disposable PostgreSQL readiness timed out' }
    $binding = docker port $containerName 5432/tcp
    $testPort = ($binding -split ':')[-1]
    $env:GO_TEST_DATABASE_URL = "postgresql://bliss_test:disposable_test_only@127.0.0.1:$testPort/bliss_foundation_test?sslmode=disable"
    Push-Location (Join-Path $PSScriptRoot '..')
    try {
        go test -count=1 ./internal/database
        if ($LASTEXITCODE -ne 0) { throw 'Database integration checks failed' }
    } finally { Pop-Location }
} finally {
    $env:GO_TEST_DATABASE_URL = $previousTestURL
    docker rm --force $containerName *> $null
}
