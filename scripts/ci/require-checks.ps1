[CmdletBinding()]
param([switch]$Apply)
$ErrorActionPreference = 'Stop'
$repository = 'dev-bramwel/Bliss-Atelier-Perfumes'
$root = Split-Path (Split-Path $PSScriptRoot -Parent) -Parent
$backupDirectory = Join-Path $root '.local/ruleset-backups'
New-Item -ItemType Directory -Force -Path $backupDirectory | Out-Null
foreach ($entry in @(@{Id=25595832; Branch='develop'}, @{Id=24680337; Branch='main'})) {
    $raw = gh api "repos/$repository/rulesets/$($entry.Id)"
    if ($LASTEXITCODE -ne 0) {throw 'Could not inspect current ruleset'}
    $current = $raw | ConvertFrom-Json
    $timestamp = [DateTime]::UtcNow.ToString('yyyyMMddHHmmssfff')
    [IO.File]::WriteAllText((Join-Path $backupDirectory "$($entry.Id)-$timestamp-before.json"),($current | ConvertTo-Json -Depth 40),[Text.UTF8Encoding]::new($false))
    if ($entry.Branch -eq 'develop') {
        $current.conditions.ref_name.include = @($current.conditions.ref_name.include | ForEach-Object {
            if ($_ -eq 'refs/heads/"develop"') {'refs/heads/develop'} else {$_}
        })
        if ('refs/heads/develop' -notin $current.conditions.ref_name.include) {throw 'Unexpected develop branch pattern; review the ruleset'}
    }
    $status = @($current.rules | Where-Object type -eq 'required_status_checks')
    if ($status.Count -eq 0) {
        $status = @([pscustomobject]@{type='required_status_checks'; parameters=[pscustomobject]@{strict_required_status_checks_policy=$true; do_not_enforce_on_create=$false; required_status_checks=@()}})
        $current.rules = @($current.rules) + $status
    }
    $status[0].parameters.strict_required_status_checks_policy = $true
    foreach ($name in @('Docker core validation','Security and contracts')) {
        $existing = @($status[0].parameters.required_status_checks | Where-Object context -eq $name)
        if ($existing.Count -eq 0) {
            $status[0].parameters.required_status_checks = @($status[0].parameters.required_status_checks) + [pscustomobject]@{context=$name; integration_id=15368}
        }
    }
    $body = [ordered]@{name=$current.name; target=$current.target; enforcement=$current.enforcement; bypass_actors=@($current.bypass_actors); conditions=$current.conditions; rules=@($current.rules)}
    $path = Join-Path $backupDirectory "$($entry.Id)-$timestamp-proposed.json"
    [IO.File]::WriteAllText($path,($body | ConvertTo-Json -Depth 40),[Text.UTF8Encoding]::new($false))
    if ($Apply) {
        gh api --method PUT "repos/$repository/rulesets/$($entry.Id)" --input $path | Out-Null
        if ($LASTEXITCODE -ne 0) {throw 'Ruleset update failed; preserved the original backup'}
    }
    Write-Host "$($entry.Branch): proposed Docker core validation and Security and contracts; applied=$Apply; existing review/deletion/force-push policies preserved."
}
