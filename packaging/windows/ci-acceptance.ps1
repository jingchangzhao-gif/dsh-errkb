<#
.SYNOPSIS
    WF-011A unattended Windows acceptance orchestrator.

.DESCRIPTION
    Exercises the real installer and installed executable on an ephemeral
    Windows runner. The sequence proves installation, migrations, daemon/pipe,
    error recording and reuse, keep-data uninstall, retained-data reinstall,
    and delete-data uninstall. Each stage has its own evidence directory and a
    combined results.json is written at the root.

    This script deliberately does not claim to test the interactive wizard or
    a physical Windows reboot. Those checks remain optional manual evidence;
    recovery without an OS reboot is covered separately by WF-011B.
#>
[CmdletBinding()]
param(
    [string]$Installer = '',
    [string]$ExpectedVersion = '0.1.0',
    [int]$Port = 5433,
    [string]$EvidenceDir = ''
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$RepoRoot = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
$Harness = Join-Path $PSScriptRoot 'vm-acceptance.ps1'
$RecoveryHarness = Join-Path $PSScriptRoot 'recovery-acceptance.ps1'
$Shell = (Get-Command pwsh -ErrorAction Stop).Source
if ([string]::IsNullOrWhiteSpace($Installer)) {
    $Installer = Join-Path $RepoRoot ("dist\errkb-{0}-setup.exe" -f $ExpectedVersion)
}
if ([string]::IsNullOrWhiteSpace($EvidenceDir)) {
    $EvidenceDir = Join-Path $RepoRoot 'artifacts\windows-acceptance'
}
New-Item -ItemType Directory -Path $EvidenceDir -Force | Out-Null

if (-not (Test-Path $Harness)) { throw "acceptance harness is missing: $Harness" }
if (-not (Test-Path $RecoveryHarness)) { throw "recovery harness is missing: $RecoveryHarness" }
if (-not (Test-Path $Installer)) { throw "installer is missing: $Installer" }
$Installer = (Resolve-Path $Installer).Path
$sha256 = (Get-FileHash -LiteralPath $Installer -Algorithm SHA256).Hash

$stages = New-Object System.Collections.Generic.List[object]

function Invoke-AcceptanceStage {
    param(
        [string]$Name,
        [string]$Phase,
        [string[]]$ExtraArguments = @()
    )
    $stageDir = Join-Path $EvidenceDir $Name
    New-Item -ItemType Directory -Path $stageDir -Force | Out-Null
    $arguments = @(
        '-NoProfile', '-File', $Harness,
        '-Phase', $Phase,
        '-Installer', $Installer,
        '-ExpectedVersion', $ExpectedVersion,
        '-Port', [string]$Port,
        '-EvidenceDir', $stageDir,
        '-Automated'
    ) + $ExtraArguments

    Write-Host ("`n######## {0} ########" -f $Name) -ForegroundColor Cyan
    & $Shell @arguments
    $exitCode = $LASTEXITCODE
    $resultPath = Join-Path $stageDir ("results-{0}.json" -f $Phase)
    $results = if (Test-Path $resultPath) { @(Get-Content $resultPath -Raw | ConvertFrom-Json) } else { @() }
    $stages.Add([pscustomobject]@{
            stage     = $Name
            phase     = $Phase
            exit_code = $exitCode
            results   = $results
        })
    if ($exitCode -ne 0) {
        $stages | ConvertTo-Json -Depth 8 | Out-File (Join-Path $EvidenceDir 'results.json') -Encoding utf8
        throw "WF-011A stage '$Name' failed with exit code $exitCode"
    }
}

function Invoke-RecoveryStage {
    $name = '06-recovery'
    $stageDir = Join-Path $EvidenceDir $name
    New-Item -ItemType Directory -Path $stageDir -Force | Out-Null
    Write-Host ("`n######## {0} ########" -f $name) -ForegroundColor Cyan
    & $Shell -NoProfile -File $RecoveryHarness -Port $Port -EvidenceDir $stageDir
    $exitCode = $LASTEXITCODE
    $resultPath = Join-Path $stageDir 'results.json'
    $results = if (Test-Path $resultPath) { @(Get-Content $resultPath -Raw | ConvertFrom-Json) } else { @() }
    $stages.Add([pscustomobject]@{
            stage     = $name
            phase     = 'recovery'
            exit_code = $exitCode
            results   = $results
        })
    if ($exitCode -ne 0) {
        $stages | ConvertTo-Json -Depth 8 | Out-File (Join-Path $EvidenceDir 'results.json') -Encoding utf8
        throw "WF-011B recovery stage failed with exit code $exitCode"
    }
}

try {
    Invoke-AcceptanceStage '01-artifacts' 'artifacts' @('-ExpectedSha256', $sha256)
    Invoke-AcceptanceStage '02-preflight' 'preflight' @('-AllowAdmin', '-AllowDirtyHost')
    Invoke-AcceptanceStage '03-install' 'install'
    Invoke-AcceptanceStage '04-first-run' 'firstrun'
    Invoke-AcceptanceStage '05-error-reuse-loop' 'loop'
    Invoke-RecoveryStage
    Invoke-AcceptanceStage '07-uninstall-keep' 'uninstall' @('-UninstallData', 'keep')
    Invoke-AcceptanceStage '08-reinstall-retained' 'install' @('-ExpectedConnectorState', 'existing')
    Invoke-AcceptanceStage '09-retained-first-run' 'firstrun' @('-ExpectedConnectorState', 'existing')
    Invoke-AcceptanceStage '10-retained-data' 'retention' @('-ExpectedConnectorState', 'existing')
    Invoke-AcceptanceStage '11-uninstall-delete' 'uninstall' @('-UninstallData', 'delete')
} finally {
    $stages | ConvertTo-Json -Depth 8 | Out-File (Join-Path $EvidenceDir 'results.json') -Encoding utf8
}

$failures = @($stages | ForEach-Object { $_.results } | Where-Object { $_.Status -eq 'FAIL' })
if ($failures.Count -gt 0) {
    throw "WF-011A produced $($failures.Count) failed assertion(s)"
}
Write-Host "`nWF-011A RESULT: PASS" -ForegroundColor Green
Write-Host ("evidence: {0}" -f $EvidenceDir)
