<#
.SYNOPSIS
    WF-011B deterministic Windows recovery acceptance.

.DESCRIPTION
    Simulates the recoverable effects of a Windows restart without rebooting
    the runner: the installed daemon and managed PostgreSQL cluster are stopped,
    the installed logon script is replayed, and the Named Pipe, port, health,
    single-instance rule and retained knowledge are verified. A real OS reboot
    remains optional manual evidence and is not claimed by this script.
#>
[CmdletBinding()]
param(
    [string]$AppDir = (Join-Path $env:LOCALAPPDATA 'errkb'),
    [int]$Port = 5433,
    [string]$PipeName = 'errkb',
    [string]$EvidenceDir = '',
    [int]$WaitSeconds = 45
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

if ([string]::IsNullOrWhiteSpace($EvidenceDir)) {
    $EvidenceDir = Join-Path $env:TEMP ('errkb-recovery-acceptance-{0}' -f (Get-Date -Format 'yyyyMMdd-HHmmss'))
}
New-Item -ItemType Directory -Path $EvidenceDir -Force | Out-Null

$Exe = Join-Path $AppDir 'errkb.exe'
$Autostart = Join-Path $AppDir 'autostart.ps1'
$Postgres = Join-Path $AppDir 'postgres.ps1'
$DataDir = Join-Path $AppDir 'pg\data'
$Credentials = Join-Path $AppDir 'pg\credentials.json'
$Migrations = Join-Path $AppDir 'migrations'
$HealthFile = Join-Path $AppDir 'health.json'
$RunKey = 'HKCU:\Software\Microsoft\Windows\CurrentVersion\Run'
$RunValueName = 'errkb-autostart'
$script:Results = New-Object System.Collections.Generic.List[object]

function Add-Result {
    param([string]$Step, [string]$Expected, [bool]$Pass, [string]$Actual, [string]$Evidence = '')
    $status = if ($Pass) { 'PASS' } else { 'FAIL' }
    $script:Results.Add([pscustomobject]@{
            Step = $Step; Expected = $Expected; Actual = $Actual; Status = $status; Evidence = $Evidence
        })
    $line = '{0,-12} {1}  ({2})' -f $status, $Step, $Actual
    Write-Host $line -ForegroundColor $(if ($Pass) { 'Green' } else { 'Red' })
}

function Save-Evidence {
    param([string]$Name, [string]$Content)
    $path = Join-Path $EvidenceDir $Name
    $Content | Out-File -LiteralPath $path -Encoding utf8
    return $path
}

function Test-PortOpen {
    $client = New-Object System.Net.Sockets.TcpClient
    try {
        $task = $client.ConnectAsync('127.0.0.1', $Port)
        if (-not $task.Wait(500)) { return $false }
        return $client.Connected
    } catch {
        return $false
    } finally {
        $client.Dispose()
    }
}

function Test-Pipe {
    $stream = New-Object System.IO.Pipes.NamedPipeClientStream('.', $PipeName, [System.IO.Pipes.PipeDirection]::InOut)
    try {
        $stream.Connect(500)
        return $true
    } catch {
        return $false
    } finally {
        $stream.Dispose()
    }
}

function Get-InstalledDaemonProcesses {
    $expected = [System.IO.Path]::GetFullPath($Exe)
    return @(Get-CimInstance Win32_Process -Filter "Name = 'errkb.exe'" -ErrorAction SilentlyContinue |
        Where-Object { $_.ExecutablePath -and ([System.IO.Path]::GetFullPath($_.ExecutablePath) -eq $expected) })
}

function Stop-InstalledDaemon {
    foreach ($process in @(Get-InstalledDaemonProcesses)) {
        Stop-Process -Id $process.ProcessId -Force -ErrorAction SilentlyContinue
    }
    $deadline = (Get-Date).AddSeconds(10)
    while ((Get-Date) -lt $deadline) {
        if (@(Get-InstalledDaemonProcesses).Count -eq 0 -and -not (Test-Pipe)) { return }
        Start-Sleep -Milliseconds 250
    }
    throw 'installed daemon did not stop within 10 seconds'
}

function Invoke-LoggedScript {
    param([string]$Name, [scriptblock]$Action)
    try {
        $output = & $Action 2>&1
        $exitCode = $LASTEXITCODE
        $path = Save-Evidence "$Name.log" (($output | ForEach-Object { "$_" }) -join "`n")
        return [pscustomobject]@{ ExitCode = $exitCode; Output = (($output | ForEach-Object { "$_" }) -join "`n"); Evidence = $path }
    } catch {
        $path = Save-Evidence "$Name.error.log" ($_ | Out-String)
        return [pscustomobject]@{ ExitCode = -1; Output = ($_ | Out-String); Evidence = $path }
    }
}

function Wait-Operational {
    $deadline = (Get-Date).AddSeconds($WaitSeconds)
    while ((Get-Date) -lt $deadline) {
        if ((Test-PortOpen) -and (Test-Pipe) -and (@(Get-InstalledDaemonProcesses).Count -eq 1)) {
            return $true
        }
        Start-Sleep -Milliseconds 500
    }
    return $false
}

function Start-InstalledProduct {
    param([string]$EvidenceName)
    $run = Invoke-LoggedScript $EvidenceName {
        & $Autostart start -AppDir $AppDir -Port $Port -PipeName $PipeName -WaitSeconds $WaitSeconds
    }
    Add-Result "$EvidenceName exit" 'autostart.ps1 exits 0' ($run.ExitCode -eq 0) ("exit {0}" -f $run.ExitCode) $run.Evidence
    $ready = Wait-Operational
    Add-Result "$EvidenceName operational" 'port, Named Pipe and exactly one installed daemon recover' $ready `
        ('port={0}, pipe={1}, processes={2}' -f (Test-PortOpen), (Test-Pipe), @(Get-InstalledDaemonProcesses).Count)
}

Write-Host "`n== WF-011B recovery acceptance ==" -ForegroundColor Cyan
foreach ($required in @($Exe, $Autostart, $Postgres, $Credentials, $Migrations)) {
    Add-Result ('prerequisite ' + (Split-Path -Leaf $required)) 'installed file exists' (Test-Path $required) $required
}

$runValue = $null
$runItem = Get-ItemProperty -Path $RunKey -Name $RunValueName -ErrorAction SilentlyContinue
if ($null -ne $runItem) { $runValue = [string]$runItem.$RunValueName }
$runConfigured = -not [string]::IsNullOrWhiteSpace($runValue) -and
    $runValue.Contains($Autostart) -and $runValue.Contains('-Action start') -and $runValue.Contains("-Port $Port")
Add-Result 'autostart configuration' 'HKCU Run invokes the installed start script with the configured port' $runConfigured `
    ($(if ($runValue) { $runValue } else { 'missing' }))

if (@($script:Results | Where-Object { $_.Status -eq 'FAIL' }).Count -eq 0) {
    Start-InstalledProduct 'baseline-start'

    # Idempotence is the single-instance check: replaying the logon command may
    # not create a second daemon or a second pipe owner.
    Start-InstalledProduct 'idempotent-start'
    $count = @(Get-InstalledDaemonProcesses).Count
    Add-Result 'single instance after replay' 'exactly one installed daemon' ($count -eq 1) ("$count process(es)")

    # Daemon-only failure: PostgreSQL stays available while the pipe owner is
    # terminated. Replaying the same startup path must restore only the daemon.
    Stop-InstalledDaemon
    Add-Result 'daemon failure isolated' 'pipe closed while PostgreSQL remains reachable' `
        ((-not (Test-Pipe)) -and (Test-PortOpen)) ('port={0}, pipe={1}' -f (Test-PortOpen), (Test-Pipe))
    Start-InstalledProduct 'daemon-recovery'

    # Full user-session recovery: both managed processes are stopped, then the
    # exact HKCU Run target is replayed.
    Stop-InstalledDaemon
    $stop = Invoke-LoggedScript 'cluster-stop' {
        & $Postgres stop -DataDir $DataDir -Port $Port -Exe $Exe
    }
    Add-Result 'cluster stop exit' 'postgres.ps1 exits 0' ($stop.ExitCode -eq 0) ("exit {0}" -f $stop.ExitCode) $stop.Evidence
    $stopped = (-not (Test-PortOpen)) -and (-not (Test-Pipe)) -and (@(Get-InstalledDaemonProcesses).Count -eq 0)
    Add-Result 'full failure simulated' 'port and pipe closed with no installed daemon' $stopped `
        ('port={0}, pipe={1}, processes={2}' -f (Test-PortOpen), (Test-Pipe), @(Get-InstalledDaemonProcesses).Count)
    Start-InstalledProduct 'full-recovery'

    $health = $null
    if (Test-Path $HealthFile) {
        try { $health = Get-Content $HealthFile -Raw | ConvertFrom-Json } catch { $health = $null }
    }
    $healthOK = ($null -ne $health) -and ($health.status -eq 'ok') -and
        ($health.migrations.current -eq $health.migrations.expected)
    Add-Result 'health after recovery' 'status ok and migration ledger current' $healthOK `
        ($(if ($health) { 'status={0}, migrations={1}/{2}' -f $health.status, $health.migrations.current, $health.migrations.expected } else { 'missing or invalid' }))

    $search = Invoke-LoggedScript 'search-after-recovery' {
        & $Exe search 'command exited with status 9' --json
    }
    try { $searchReport = $search.Output | ConvertFrom-Json } catch { $searchReport = $null }
    $knowledgeOK = ($search.ExitCode -eq 0) -and ($null -ne $searchReport) -and ($searchReport.candidates.Count -ge 1)
    Add-Result 'knowledge after recovery' 'the issue recorded before recovery is searchable' $knowledgeOK `
        ('candidates={0}' -f $(if ($searchReport) { $searchReport.candidates.Count } else { 'invalid json' })) $search.Evidence
}

$resultPath = Join-Path $EvidenceDir 'results.json'
$script:Results | ConvertTo-Json -Depth 5 | Out-File -LiteralPath $resultPath -Encoding utf8
$failures = @($script:Results | Where-Object { $_.Status -eq 'FAIL' })
Write-Host "`n== summary ==" -ForegroundColor Cyan
$script:Results | Format-Table -AutoSize Step, Status, Expected, Actual | Out-String -Width 220 | Write-Host
Write-Host ("evidence: {0}" -f $EvidenceDir)
if ($failures.Count -gt 0) {
    Write-Host ("WF-011B RESULT: FAIL ({0} failed assertion(s))" -f $failures.Count) -ForegroundColor Red
    exit 1
}
Write-Host 'WF-011B RESULT: PASS' -ForegroundColor Green
exit 0
