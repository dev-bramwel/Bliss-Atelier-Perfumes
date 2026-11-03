param(
    [ValidateSet('postgres','redis','rabbitmq','kafka','prometheus','grafana','collector')]
    [string[]]$Service = @('postgres','redis','rabbitmq','kafka','prometheus','grafana','collector'),
    [string]$OutputPath = 'docs/evidence/r03-service-baselines.json'
)

# Disposable, synthetic, sequential baseline probes; not a production deployment or load test.
$ErrorActionPreference = 'Stop'
$runId = 'bap-r03-' + [guid]::NewGuid().ToString('N').Substring(0,8)
$results = New-Object System.Collections.Generic.List[object]
function Invoke-Docker {
    param([string[]]$DockerArgs, [switch]$AllowFailure)
    $previousPreference = $ErrorActionPreference
    $ErrorActionPreference = 'Continue'
    try { $output = & docker @DockerArgs 2>&1; $code = $LASTEXITCODE }
    finally { $ErrorActionPreference = $previousPreference }
    if ($code -ne 0) {
        if ($AllowFailure) { return $null }
        throw "docker $($DockerArgs[0]) failed: $($output -join [Environment]::NewLine)"
    }
    return ($output -join [Environment]::NewLine).Trim()
}
$engine = Invoke-Docker @('info','--format','{{json .}}') | ConvertFrom-Json
$definitions = @{
    postgres = @{Image='postgres:17-alpine'; Memory='512m'; Extra=@('-e','POSTGRES_PASSWORD=synthetic-local-probe-only'); Ready=@('pg_isready','-U','postgres'); Disk='/var/lib/postgresql/data'}
    redis = @{Image='redis:7-alpine'; Memory='256m'; Extra=@(); Command=@('redis-server','--appendonly','yes','--maxmemory','128mb','--maxmemory-policy','noeviction'); Ready=@('redis-cli','ping'); Disk='/data'}
    rabbitmq = @{Image='rabbitmq:4'; Memory='512m'; Extra=@('--hostname','localhost','-e','RABBITMQ_NODENAME=rabbit@localhost','-e','RABBITMQ_DEFAULT_USER=probe','-e','RABBITMQ_DEFAULT_PASS=synthetic-local-probe-only'); Ready=@('gosu','rabbitmq','rabbitmq-diagnostics','-q','check_running'); Disk='/var/lib/rabbitmq'}
    kafka = @{Image='apache/kafka:4.0.1'; Memory='1536m'; Extra=@('--hostname','localhost','-e','KAFKA_HEAP_OPTS=-Xms256m -Xmx256m'); Ready=@('/opt/kafka/bin/kafka-topics.sh','--bootstrap-server','localhost:9092','--list'); Disk='/tmp/kraft-combined-logs'}
    prometheus = @{Image='prom/prometheus:v3.5.0'; Memory='512m'; Extra=@(); Ready=@('wget','-q','-O','-','http://127.0.0.1:9090/-/ready'); Disk='/prometheus'}
    grafana = @{Image='grafana/grafana:12.1.0'; Memory='384m'; Extra=@('-e','GF_SECURITY_ADMIN_PASSWORD=synthetic-local-probe-only','-e','GF_ANALYTICS_REPORTING_ENABLED=false','-e','GF_ANALYTICS_CHECK_FOR_UPDATES=false','-e','GF_PLUGINS_PREINSTALL_DISABLED=true'); Ready=@('wget','-q','-O','-','http://127.0.0.1:3000/api/health'); Disk='/var/lib/grafana'}
    collector = @{Image='otel/opentelemetry-collector:0.133.0'; Memory='128m'; Extra=@(); Ready=@(); Disk=$null}
}
function Save-Results {
    $report = [ordered]@{
        runId=$runId; capturedUtc=[DateTime]::UtcNow.ToString('o'); evidence='local isolated sequential baseline'
        engine=@{version=$engine.ServerVersion; architecture=$engine.Architecture; cpus=$engine.NCPU; memoryBytes=$engine.MemTotal}
        limitations=@('No simultaneous stack or application traffic','Samples are not continuous peaks','Restart is same-container, not restore/recreation','No public ports, provider calls or customer data','Versions are probe inputs, not approved production pins')
        services=@($results.ToArray())
    }
    $parent = Split-Path -Parent $OutputPath
    if ($parent -and -not (Test-Path -LiteralPath $parent)) { New-Item -ItemType Directory -Path $parent -Force | Out-Null }
    $report | ConvertTo-Json -Depth 12 | Set-Content -LiteralPath $OutputPath -Encoding UTF8
}
foreach ($item in $Service) {
    $definition = $definitions[$item]
    $name = "$runId-$item"
    $samples = New-Object System.Collections.Generic.List[object]
    $record = [ordered]@{service=$item; imageTag=$definition.Image; memoryLimit=$definition.Memory; cpuLimit=1; status='in-progress'; samples=@()}
    try {
        Write-Output "[$item] Pulling image; then testing in isolation"
        $pullWatch = [Diagnostics.Stopwatch]::StartNew()
        Invoke-Docker @('pull',$definition.Image) | Out-Null
        $record.pullSeconds = [math]::Round($pullWatch.Elapsed.TotalSeconds,2)
        $metadata = Invoke-Docker @('image','inspect',$definition.Image) | ConvertFrom-Json
        $record.imageDigest = $metadata[0].RepoDigests[0]
        $record.imageSizeBytes = $metadata[0].Size
        $record.hostFreeMemoryBeforeMiB = [math]::Round((Get-CimInstance Win32_OperatingSystem).FreePhysicalMemory/1024)
        $runArguments = @('run','-d','--name',$name,'--label',"bap.r03.run=$runId",'--network','none','--memory',$definition.Memory,'--memory-swap',$definition.Memory,'--cpus','1','--pids-limit','512') + $definition.Extra + @($record.imageDigest) + $definition.Command
        $startup = [Diagnostics.Stopwatch]::StartNew()
        Invoke-Docker $runArguments | Out-Null
        $ready = $false
        while ($startup.Elapsed.TotalSeconds -lt 180) {
            if ($item -eq 'collector') {
                Start-Sleep -Seconds 5
                $state = Invoke-Docker @('inspect','--format','{{json .State}}',$name) | ConvertFrom-Json
                $ready = $state.Running # Process-only check; no pipeline receiver/exporter configured by this probe.
            } else {
                $answer = Invoke-Docker -DockerArgs (@('exec',$name) + $definition.Ready) -AllowFailure
                $ready = $null -ne $answer
            }
            if ($ready) { break }
            $state = Invoke-Docker @('inspect','--format','{{json .State}}',$name) | ConvertFrom-Json
            if (-not $state.Running) { throw 'Service exited before readiness' }
            Start-Sleep -Seconds 2
        }
        if (-not $ready) { throw 'Readiness timeout (180 seconds)' }
        $record.readySeconds = [math]::Round($startup.Elapsed.TotalSeconds,2)
        $record.readiness = $(if ($item -eq 'collector') {'process only'} else {'service-specific readiness'})
        for ($sample = 1; $sample -le 5; $sample++) {
            $raw = Invoke-Docker @('stats','--no-stream','--format','{{json .}}',$name) | ConvertFrom-Json
            $samples.Add([ordered]@{sample=$sample;cpu=$raw.CPUPerc;memory=$raw.MemUsage;memoryPercent=$raw.MemPerc;blockIO=$raw.BlockIO;pids=$raw.PIDs})
            Start-Sleep -Seconds 2
        }
        $record.samples = @($samples.ToArray())
        if ($definition.Disk) { $record.dataDirectoryKiB = Invoke-Docker -DockerArgs @('exec',$name,'du','-sk',$definition.Disk) -AllowFailure }
        if ($item -eq 'postgres') {
            Invoke-Docker @('exec',$name,'psql','-U','postgres','-v','ON_ERROR_STOP=1','-c','CREATE TABLE probe AS SELECT generate_series(1,1000) AS id;') | Out-Null
        } elseif ($item -eq 'redis') {
            Invoke-Docker @('exec',$name,'redis-cli','set','r03-probe','synthetic-marker') | Out-Null
        } elseif ($item -eq 'kafka') {
            Invoke-Docker @('exec',$name,'/opt/kafka/bin/kafka-topics.sh','--bootstrap-server','localhost:9092','--create','--topic','r03-probe','--partitions','1','--replication-factor','1') | Out-Null
        }
        $state = Invoke-Docker @('inspect','--format','{{json .State}}',$name) | ConvertFrom-Json
        $record.oomKilledBeforeRestart = $state.OOMKilled
        $record.restartCountBefore = [int](Invoke-Docker @('inspect','--format','{{.RestartCount}}',$name))
        Invoke-Docker @('restart','--time','30',$name) | Out-Null
        $restart = [Diagnostics.Stopwatch]::StartNew()
        $recovered = $false
        while ($restart.Elapsed.TotalSeconds -lt 180) {
            if ($item -eq 'collector') {
                Start-Sleep -Seconds 5
                $state = Invoke-Docker @('inspect','--format','{{json .State}}',$name) | ConvertFrom-Json
                $recovered = $state.Running
            } else { $recovered = $null -ne (Invoke-Docker -DockerArgs (@('exec',$name) + $definition.Ready) -AllowFailure) }
            if ($recovered) { break }
            Start-Sleep -Seconds 2
        }
        if (-not $recovered) { throw 'Restart readiness timeout' }
        $record.postRestartReadySeconds = [math]::Round($restart.Elapsed.TotalSeconds,2)
        $record.restartCheck = 'passed'
        if ($item -eq 'postgres') {
            $count = Invoke-Docker @('exec',$name,'psql','-U','postgres','-tAc','SELECT count(*) FROM probe;')
            if ($count -ne '1000') { throw 'Synthetic rows missing after restart' }
            $record.persistence = '1000 rows survived same-container restart'
        } elseif ($item -eq 'redis') {
            $value = Invoke-Docker @('exec',$name,'redis-cli','get','r03-probe')
            if ($value -ne 'synthetic-marker') { throw 'Redis marker missing after restart' }
            $record.persistence = 'AOF marker survived same-container restart'
        } elseif ($item -eq 'kafka') {
            $topics = Invoke-Docker @('exec',$name,'/opt/kafka/bin/kafka-topics.sh','--bootstrap-server','localhost:9092','--list')
            if ($topics -notmatch 'r03-probe') { throw 'Kafka topic missing after restart' }
            $record.persistence = 'Topic metadata survived same-container restart; message durability not tested'
        } else { $record.persistence = 'Not tested' }
        $state = Invoke-Docker @('inspect','--format','{{json .State}}',$name) | ConvertFrom-Json
        $record.oomKilledAfterRestart = $state.OOMKilled
        $record.status = 'passed'
        Write-Output "[$item] Baseline and restart check passed"
    } catch {
        $record.status = 'failed'
        $record.error = $_.Exception.Message
        $record.diagnosticLog = Invoke-Docker -DockerArgs @('logs','--tail','30',$name) -AllowFailure
        $failedState = Invoke-Docker -DockerArgs @('inspect','--format','{{json .State}}',$name) -AllowFailure
        if ($failedState) {
            $failedState = $failedState | ConvertFrom-Json
            $record.exitCode = $failedState.ExitCode
            $record.oomKilledAtFailure = $failedState.OOMKilled
        }
        Write-Output "[$item] Failed: $($record.error)"
    } finally {
        $existing = Invoke-Docker -DockerArgs @('inspect',$name) -AllowFailure
        if ($existing) {
            $ownedContainer = ($existing | ConvertFrom-Json)[0]
            if ($ownedContainer.Config.Labels.'bap.r03.run' -eq $runId) { Invoke-Docker @('rm','-f','-v',$name) | Out-Null }
        }
        $record.samples = @($samples.ToArray())
        $results.Add($record)
        Save-Results
    }
}
if (@($results | Where-Object {$_.status -ne 'passed'}).Count) { exit 1 }
