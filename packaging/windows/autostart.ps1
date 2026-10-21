# Start at logon for the installed product (EKB-056).
#
# The release installs per user: the PostgreSQL cluster and the errkb daemon
# both run as the signed-in user. Windows does not let a standard user register
# a Scheduled Task, so this script uses the per-user Run key
# (HKCU\Software\Microsoft\Windows\CurrentVersion\Run) instead. It can also run
# the same start path by hand.
#
# Actions:
#   install    write the logon Run entry and start the cluster and daemon now
#   uninstall  remove the Run entry and stop a daemon started from this app dir
#   start      start the cluster (idempotent) and the daemon (idempotent)
#   status     print the Run entry, the pipe reachability and the health file
#
# The script only packages decisions made elsewhere: credentials come from
# postgres.ps1 (EKB-051), the cluster lifecycle is postgres.ps1, the daemon
# single-instance lock is EKB-028. It never prints the password.
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true, Position = 0)]
    [ValidateSet('install', 'uninstall', 'start', 'status')]
    [string]$Action,

    [string]$AppDir = (Split-Path -Parent $PSScriptRoot),
    [int]$Port = 5433,
    [string]$PipeName = 'errkb',
    [string]$RunValueName = 'errkb-autostart',
    [string]$LegacyTaskName = 'errkb-autostart',
    [int]$WaitSeconds = 45
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

$Exe = Join-Path $AppDir 'errkb.exe'
$PostgresScript = Join-Path $AppDir 'postgres.ps1'
$DataDir = Join-Path $AppDir 'pg\data'
$CredentialsPath = Join-Path $AppDir 'pg\credentials.json'
$MigrationsDir = Join-Path $AppDir 'migrations'
$LogDir = Join-Path $env:LOCALAPPDATA 'errkb\logs'
$HealthFile = Join-Path $env:LOCALAPPDATA 'errkb\health.json'
$DaemonLog = Join-Path $LogDir 'daemon.log'
$PipePath = '\\.\pipe\' + $PipeName
$DaemonLock = 'Local\errkb-daemon'
$RunKey = 'HKCU:' + '\Software\Microsoft\Windows\CurrentVersion\Run'

function Get-Credentials {
    if (-not (Test-Path $CredentialsPath)) {
        throw ("no credentials at " + $CredentialsPath + "; run the installer first (it creates the cluster and the file).")
    }
    return (Get-Content $CredentialsPath -Raw | ConvertFrom-Json)
}

function Get-Dsn {
    $credentials = Get-Credentials
    # The installer generates a base64url password, so it needs no escaping in
    # a URL. A hand-edited file with other characters is the operator's problem.
    return ('postgres://{0}:{1}@127.0.0.1:{2}/{3}?sslmode=disable' -f `
            $credentials.superuser, $credentials.password, $credentials.port, $credentials.database)
}

function Test-Pipe {
    param([string]$Name)
    $stream = New-Object System.IO.Pipes.NamedPipeClientStream('.', $Name, [System.IO.Pipes.PipeDirection]::InOut)
    try {
        $stream.Connect(400)
        return $true
    } catch {
        return $false
    } finally {
        $stream.Dispose()
    }
}

function Start-ClusterOnce {
    if (-not (Test-Path (Join-Path $DataDir 'PG_VERSION'))) {
        throw ("no cluster at " + $DataDir + "; run the installer first.")
    }
    & powershell.exe -NoProfile -ExecutionPolicy Bypass -File $PostgresScript start `
        -DataDir $DataDir -Port $Port -Database (Get-Credentials).database
    if ($LASTEXITCODE -ne 0) {
        throw "the managed PostgreSQL cluster did not start (see the logs next to the data directory)."
    }
}

function Start-DaemonOnce {
    if (Test-Pipe $PipeName) {
        Write-Output ('daemon     already listening on ' + $PipePath)
        return
    }
    if (-not (Test-Path $Exe)) {
        throw ("errkb.exe is missing from " + $AppDir + "; run the installer again.")
    }
    if (-not (Test-Path $MigrationsDir)) {
        throw ("the migrations directory is missing from " + $AppDir + "; run the installer again.")
    }
    if (-not (Test-Path $LogDir)) { New-Item -ItemType Directory -Force -Path $LogDir | Out-Null }

    $dsn = Get-Dsn
    $arguments = 'daemon --dsn "' + $dsn + '" --migrations "' + $MigrationsDir + '"' +
        ' --lock-name "' + $DaemonLock + '" --pipe "' + $PipePath + '"' +
        ' --health-file "' + $HealthFile + '" --log-file "' + $DaemonLog + '"'
    # The Run entry starts this script with -WindowStyle Hidden, so the daemon
    # has no visible console to hide; re-check this on the clean VM if a future
    # host hangs when starting the daemon from the entry.
    Start-Process -FilePath $Exe -ArgumentList $arguments -ErrorAction Stop

    $deadline = (Get-Date).AddSeconds($WaitSeconds)
    while ((Get-Date) -lt $deadline) {
        if (Test-Pipe $PipeName) {
            Write-Output ('daemon     started, listening on ' + $PipePath)
            return
        }
        Start-Sleep -Milliseconds 250
    }
    throw ("the daemon did not start listening within " + $WaitSeconds + " seconds; see " + $DaemonLog + " (a schema migration check failure means the installer did not run `errkb db migrate`).")
}

function Start-All {
    Start-ClusterOnce
    Write-Output ('cluster    listening on port ' + $Port)
    Start-DaemonOnce
}

function Get-RunCommand {
    $shell = Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe'
    return ('"' + $shell + '" -NoProfile -WindowStyle Hidden -ExecutionPolicy Bypass -File "' + $PSCommandPath + '"' +
        ' -Action start -AppDir "' + $AppDir + '" -Port ' + $Port + ' -PipeName "' + $PipeName + '"')
}

function Install-Autostart {
    if (-not (Test-Path $Exe)) {
        throw ("errkb.exe is missing from " + $AppDir + "; run the installer again.")
    }
    if (-not (Test-Path $RunKey)) { New-Item -Path $RunKey -Force | Out-Null }
    $command = Get-RunCommand
    New-ItemProperty -Path $RunKey -Name $RunValueName -Value $command -PropertyType String -Force | Out-Null
    Write-Output ('autostart  registered ' + $RunValueName + ' (current user sign-in)')
}

function Uninstall-Autostart {
    $existing = Get-ItemProperty -Path $RunKey -Name $RunValueName -ErrorAction SilentlyContinue
    if ($null -ne $existing) {
        Remove-ItemProperty -Path $RunKey -Name $RunValueName -ErrorAction SilentlyContinue
        Write-Output ('autostart  removed ' + $RunValueName)
    } else {
        Write-Output ('autostart  not registered ' + $RunValueName)
    }
    # Remove a task left by a pre-release build; the current installer never
    # creates one, and a denial here must not fail the uninstall.
    try {
        $legacy = Get-ScheduledTask -TaskName $LegacyTaskName -ErrorAction SilentlyContinue
        if ($null -ne $legacy) {
            Unregister-ScheduledTask -TaskName $LegacyTaskName -Confirm:$false -ErrorAction Stop
            Write-Output ('legacy task removed ' + $LegacyTaskName)
        }
    } catch {
        Write-Output ('legacy task check skipped: ' + $_.Exception.Message)
    }
    $exeFull = [System.IO.Path]::GetFullPath($Exe)
    $stopped = 0
    foreach ($process in @(Get-CimInstance Win32_Process -Filter "Name = 'errkb.exe'" -ErrorAction SilentlyContinue)) {
        if ($process.ExecutablePath -and ([System.IO.Path]::GetFullPath($process.ExecutablePath) -eq $exeFull)) {
            Stop-Process -Id $process.ProcessId -Force -ErrorAction SilentlyContinue
            $stopped++
        }
    }
    Write-Output ('daemon     stopped ' + $stopped + ' process(es) from ' + $AppDir)
}

function Show-Status {
    $existing = Get-ItemProperty -Path $RunKey -Name $RunValueName -ErrorAction SilentlyContinue
    if ($null -eq $existing) {
        Write-Output ('autostart  not registered ' + $RunValueName)
    } else {
        Write-Output ('autostart  ' + $RunValueName + ' -> ' + $existing.$RunValueName)
    }
    Write-Output ('cluster    port ' + $Port + ' open=' + (Test-PortOpen -Port $Port))
    Write-Output ('daemon     pipe reachable=' + (Test-Pipe $PipeName))
    if (Test-Path $HealthFile) {
        try {
            $health = Get-Content $HealthFile -Raw | ConvertFrom-Json
            Write-Output ('health     status=' + $health.status + ' migrations=' + $health.migrations.current + '/' + $health.migrations.expected)
        } catch {
            Write-Output 'health     unreadable'
        }
    } else {
        Write-Output 'health     missing'
    }
}

function Test-PortOpen {
    param([int]$TargetPort)
    $client = New-Object System.Net.Sockets.TcpClient
    try {
        $async = $client.BeginConnect('127.0.0.1', $TargetPort, $null, $null)
        if (-not $async.AsyncWaitHandle.WaitOne(400)) { return $false }
        $client.EndConnect($async)
        return $true
    } catch {
        return $false
    } finally {
        $client.Dispose()
    }
}

switch ($Action) {
    'install' {
        Install-Autostart
        Start-All
        Write-Output 'autostart: OK (the cluster and daemon start again at the next sign-in)'
    }
    'uninstall' {
        Uninstall-Autostart
    }
    'start' {
        Start-All
    }
    'status' {
        Show-Status
    }
}
