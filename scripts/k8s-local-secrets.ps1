$ErrorActionPreference = 'Stop'

kubectl apply -f (Join-Path $PSScriptRoot '..\infra\k8s\base\namespace.yaml') | Out-Null

$tmpDir = Join-Path ([System.IO.Path]::GetTempPath()) ('bliss-k8s-secrets-' + [guid]::NewGuid().ToString('N').Substring(0, 8))
New-Item -ItemType Directory -Path $tmpDir -Force | Out-Null

try {
    $adminPw = 'local_admin_' + [guid]::NewGuid().ToString('N').Substring(0, 12)
    $migratorPw = 'local_migrator_' + [guid]::NewGuid().ToString('N').Substring(0, 12)
    $runtimePw = 'local_runtime_' + [guid]::NewGuid().ToString('N').Substring(0, 12)
    $catalogPw = 'local_catalog_' + [guid]::NewGuid().ToString('N').Substring(0, 12)
    $rabbitPw = 'local_rabbit_' + [guid]::NewGuid().ToString('N').Substring(0, 12)
    $managerSecret = 'local_mgr_' + [guid]::NewGuid().ToString('N').Substring(0, 16)

    [System.IO.File]::WriteAllText((Join-Path $tmpDir 'admin_password'), $adminPw)
    [System.IO.File]::WriteAllText((Join-Path $tmpDir 'migrator_password'), $migratorPw)
    [System.IO.File]::WriteAllText((Join-Path $tmpDir 'runtime_password'), $runtimePw)
    [System.IO.File]::WriteAllText((Join-Path $tmpDir 'catalog_password'), $catalogPw)
    [System.IO.File]::WriteAllText((Join-Path $tmpDir 'rabbitmq_password'), $rabbitPw)
    [System.IO.File]::WriteAllText((Join-Path $tmpDir 'manager_secret'), $managerSecret)
    [System.IO.File]::WriteAllText((Join-Path $tmpDir 'migrator_url'), "postgresql://bliss_migrator:$migratorPw@db:5432/bliss_dev?sslmode=disable")
    [System.IO.File]::WriteAllText((Join-Path $tmpDir 'runtime_url'), "postgresql://bliss_runtime:$runtimePw@db:5432/bliss_dev?sslmode=disable")
    [System.IO.File]::WriteAllText((Join-Path $tmpDir 'catalog_url'), "postgresql://bliss_catalog:$catalogPw@db:5432/bliss_dev?sslmode=disable")
    [System.IO.File]::WriteAllText((Join-Path $tmpDir 'rabbitmq_url'), "amqp://bliss:$rabbitPw@rabbitmq:5672/")

    kubectl create secret generic bliss-secrets `
        --namespace bliss-local `
        --from-file=$tmpDir `
        --dry-run=client -o yaml | kubectl apply -f - | Out-Null

    $tlsKey = Join-Path $tmpDir 'tls.key'
    $tlsCrt = Join-Path $tmpDir 'tls.crt'
    $openssl = Get-Command openssl -ErrorAction SilentlyContinue
    if ($openssl) {
        & $openssl.Source req -x509 -nodes -days 30 -newkey rsa:2048 -keyout $tlsKey -out $tlsCrt -subj "/CN=bliss.local/O=Bliss Atelier Local" 2>$null
    } else {
        docker run --rm -v "${tmpDir}:/certs" alpine:3.19 sh -c "apk add --no-cache openssl >/dev/null && openssl req -x509 -nodes -days 30 -newkey rsa:2048 -keyout /certs/tls.key -out /certs/tls.crt -subj '/CN=bliss.local/O=Bliss Atelier Local' 2>/dev/null" | Out-Null
    }
    if (-not (Test-Path $tlsKey) -or -not (Test-Path $tlsCrt)) {
        throw 'Failed to generate self-signed TLS certificate for bliss.local'
    }

    kubectl create secret tls bliss-tls `
        --namespace bliss-local `
        --key=$tlsKey `
        --cert=$tlsCrt `
        --dry-run=client -o yaml | kubectl apply -f - | Out-Null

    Write-Host 'Created/updated bliss-secrets and bliss-tls in namespace bliss-local.'
} finally {
    Remove-Item -Path $tmpDir -Recurse -Force -ErrorAction SilentlyContinue
}
