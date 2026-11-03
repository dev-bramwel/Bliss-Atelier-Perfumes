$ErrorActionPreference = 'Stop'
$containerName = 'bliss-foundation-test-' + [guid]::NewGuid().ToString('N').Substring(0, 10)
$previousTestURL = $env:GO_TEST_DATABASE_URL
$previousRecoveryPhase = $env:GO_TEST_RECOVERY_PHASE
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
    $env:GO_TEST_RECOVERY_PHASE = $null
    Push-Location (Join-Path $PSScriptRoot '..')
    try {
        go test -count=1 ./internal/database ./internal/migration ./internal/catalog
        if ($LASTEXITCODE -ne 0) { throw 'Database integration checks failed' }
        $env:GO_TEST_RECOVERY_PHASE = 'seed'
        go test -count=1 -run '^TestRecoveryFixture$' ./internal/migration
        if ($LASTEXITCODE -ne 0) { throw 'Recovery fixture seed failed' }
        $fingerprintSQL = @'
SELECT md5(jsonb_build_array(
 (SELECT jsonb_agg(to_jsonb(o) ORDER BY id) FROM public."Order" o),
 (SELECT jsonb_agg(to_jsonb(m) ORDER BY id) FROM public."MpesaTransaction" m),
 (SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM public._prisma_migrations p))::text);
'@
        $before = $fingerprintSQL | docker exec -i $containerName psql -X -At -v ON_ERROR_STOP=1 -U bliss_test -d bliss_foundation_test
        if ($LASTEXITCODE -ne 0) { throw 'Pre-backup fingerprint failed' }
        docker exec $containerName pg_dump -U bliss_test -d bliss_foundation_test --format=custom --schema=public --file=/tmp/r06-backup.dump
        if ($LASTEXITCODE -ne 0) { throw 'Backup failed' }
        $env:GO_TEST_RECOVERY_PHASE = 'adopt'
        go test -count=1 -run '^TestRecoveryFixture$' ./internal/migration
        if ($LASTEXITCODE -ne 0) { throw 'Pre-restore adoption failed' }
        docker exec $containerName psql -X -v ON_ERROR_STOP=1 -U bliss_test -d bliss_foundation_test -c 'DROP SCHEMA public CASCADE'
        if ($LASTEXITCODE -ne 0) { throw 'Disposable restore preparation failed' }
        docker exec $containerName pg_restore -U bliss_test -d bliss_foundation_test --exit-on-error /tmp/r06-backup.dump
        if ($LASTEXITCODE -ne 0) { throw 'Restore failed' }
        $after = $fingerprintSQL | docker exec -i $containerName psql -X -At -v ON_ERROR_STOP=1 -U bliss_test -d bliss_foundation_test
        if ($LASTEXITCODE -ne 0 -or $before -ne $after) { throw 'Restored data/history fingerprint differs' }
        go test -count=1 -run '^TestRecoveryFixture$' ./internal/migration
        if ($LASTEXITCODE -ne 0) { throw 'Restored database adoption failed' }
        Write-Host 'R06 backup/restore/re-adoption drill passed; full data/history fingerprint preserved.'
    } finally { Pop-Location }
} finally {
    $env:GO_TEST_DATABASE_URL = $previousTestURL
    $env:GO_TEST_RECOVERY_PHASE = $previousRecoveryPhase
    docker rm --force $containerName *> $null
}
