$ErrorActionPreference = 'Stop'
$PSNativeCommandUseErrorActionPreference = $false

kubectl apply -f (Join-Path $PSScriptRoot '..\infra\k8s\base\namespace.yaml') | Out-Null

$existingSecrets = kubectl get secret bliss-secrets -n bliss-local --ignore-not-found -o name
$existingTls = kubectl get secret bliss-tls -n bliss-local --ignore-not-found -o name
if ($existingSecrets -and $existingTls) {
    Write-Host 'bliss-secrets and bliss-tls already exist in namespace bliss-local; preserving existing credentials.'
    exit 0
}

$tmpDir = Join-Path ([System.IO.Path]::GetTempPath()) ('bliss-k8s-secrets-' + [guid]::NewGuid().ToString('N').Substring(0, 8))
New-Item -ItemType Directory -Path $tmpDir -Force | Out-Null

try {
    if (-not $existingSecrets) {
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
        if ($LASTEXITCODE -ne 0) { throw 'Failed to apply bliss-secrets' }
        Write-Host 'Created bliss-secrets in namespace bliss-local.'
    } else {
        Write-Host 'bliss-secrets already exists in namespace bliss-local; preserving existing credentials.'
    }

    if (-not $existingTls) {
        $tlsKey = Join-Path $tmpDir 'tls.key'
        $tlsCrt = Join-Path $tmpDir 'tls.crt'
        $openssl = Get-Command openssl -ErrorAction SilentlyContinue
        if ($openssl) {
            cmd /c "`"$($openssl.Source)`" req -x509 -nodes -days 30 -newkey rsa:2048 -keyout `"$tlsKey`" -out `"$tlsCrt`" -subj `"/CN=bliss.local/O=Bliss Atelier Local`" 2>nul"
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
        if ($LASTEXITCODE -ne 0) { throw 'Failed to apply bliss-tls' }
        Write-Host 'Created bliss-tls in namespace bliss-local.'
    } else {
        Write-Host 'bliss-tls already exists in namespace bliss-local; preserving existing certificate.'
    }
} finally {
    Remove-Item -Path $tmpDir -Recurse -Force -ErrorAction SilentlyContinue
}
