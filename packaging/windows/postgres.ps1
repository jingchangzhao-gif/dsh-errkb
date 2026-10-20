# PostgreSQL lifecycle for the errkb installer (EKB-051).
#
# This script is a thin orchestrator: it finds the PostgreSQL binaries, calls
# initdb/pg_ctl/psql in a fixed order, and reports what it did. The decisions
# that need evidence (credentials format, health, upgrade rollback, orphan
# checks) live in internal/service/pg_manage.go; the installer (installer.iss)
# drives this script and asks the user about data retention.
#
# The release is an installed application, not a the
# cluster lives under the user profile and PostgreSQL itself stays an explicit
# dependency.
#
# Examples:
#   powershell -File postgres.ps1 install -DataDir "$env:LOCALAPPDATA\errkb\pg\data" -Port 5433
#   powershell -File postgres.ps1 start
#   powershell -File postgres.ps1 stop
#   powershell -File postgres.ps1 uninstall -DeleteData
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true, Position = 0)]
    [ValidateSet('install', 'start', 'stop', 'uninstall', 'status')]
    [string]$Action,

    [string]$DataDir = (Join-Path $env:LOCALAPPDATA 'errkb\pg\data'),
    [int]$Port = 5433,
    [string]$BinDir = '',
    [string]$Database = 'errkb',
    [string]$Superuser = 'postgres',
    [switch]$DeleteData
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version Latest

function Resolve-BinDir {
    param([string]$Candidate)
    $tools = @('pg_ctl.exe', 'initdb.exe', 'psql.exe')
    $dirs = New-Object System.Collections.Generic.List[string]
    if ($Candidate) { $dirs.Add($Candidate) }
    if ($env:ERRKB_PG_BIN) { $dirs.Add($env:ERRKB_PG_BIN) }
    foreach ($base in @($env:ProgramW6432, $env:ProgramFiles, ${env:ProgramFiles(x86)})) {
        if (-not $base) { continue }
        foreach ($version in @('17', '16', '15')) {
            $dirs.Add((Join-Path $base "PostgreSQL\$version\bin"))
        }
    }
    foreach ($dir in $dirs) {
        $present = $true
        foreach ($tool in $tools) {
            if (-not (Test-Path (Join-Path $dir $tool))) { $present = $false; break }
        }
        if ($present) { return $dir }
    }
    throw "PostgreSQL 16 was not found. Install it first (it stays an explicit dependency) or pass -BinDir / set ERRKB_PG_BIN."
}

function Get-CredentialsPath {
    return (Join-Path (Split-Path -Parent $DataDir) 'credentials.json')
}

function Test-Cluster {
    return (Test-Path (Join-Path $DataDir 'PG_VERSION'))
}

function Test-PortOpen {
    param([int]$Port)
    $client = New-Object System.Net.Sockets.TcpClient
    try {
        $async = $client.BeginConnect('127.0.0.1', $Port, $null, $null)
        if (-not $async.AsyncWaitHandle.WaitOne(400)) { return $false }
        $client.EndConnect($async)
        return $true
    } catch {
        return $false
    } finally {
        $client.Dispose()
    }
}

function New-ClusterPassword {
    $bytes = New-Object byte[] 24
    # RandomNumberGenerator.Fill is .NET 6+ only; the installer runs the
    # script under Windows PowerShell 5.1, so use the classic instance API.
    $rng = [System.Security.Cryptography.RandomNumberGenerator]::Create()
    try { $rng.GetBytes($bytes) } finally { $rng.Dispose() }
    return [Convert]::ToBase64String($bytes).Replace('+', '-').Replace('/', '_').TrimEnd('=')
}

function Invoke-PgCtl {
    param([string[]]$Arguments)
    $pgCtl = Join-Path $BinDir 'pg_ctl.exe'
    # pg_ctl leaves the server running, and the server inherits pg_ctl's
    # standard handles. Capturing output through a PowerShell pipeline would
    # keep this process waiting until the server exits, so redirect to files.
    $log = Join-Path ([System.IO.Path]::GetTempPath()) ("errkb-pgctl-" + [guid]::NewGuid().ToString('n') + ".log")
    $errLog = $log + ".err"
    try {
        $quoted = @()
        foreach ($argument in $Arguments) {
            if ($argument -match '[\s"]') { $quoted += ('"' + $argument + '"') } else { $quoted += $argument }
        }
        $process = Start-Process -FilePath $pgCtl -ArgumentList $quoted -NoNewWindow -Wait -PassThru -RedirectStandardOutput $log -RedirectStandardError $errLog
        $text = @()
        if (Test-Path $log) { $text += Get-Content $log }
        if (Test-Path $errLog) { $text += Get-Content $errLog }
        if ($process.ExitCode -ne 0) {
            throw ("pg_ctl failed (" + $process.ExitCode + "): " + ($text -join ' '))
        }
        return $text
    } finally {
        Remove-Item $log, $errLog -ErrorAction SilentlyContinue
    }
}

function Write-Credentials {
    param([string]$Password, [string]$Path)
    $document = [ordered]@{
        version  = 1
        port     = $Port
        database = $Database
        superuser = $Superuser
        password = $Password
        data_dir = $DataDir
    }
    $json = $document | ConvertTo-Json -Depth 4
    $parent = Split-Path -Parent $Path
    if (-not (Test-Path $parent)) { New-Item -ItemType Directory -Force -Path $parent | Out-Null }
    [System.IO.File]::WriteAllText($Path, $json + "`n", (New-Object System.Text.UTF8Encoding($false)))
}

function Wait-ForServer {
    param([int]$TimeoutSeconds = 120)
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    $ready = Join-Path $BinDir 'pg_isready.exe'
    while ((Get-Date) -lt $deadline) {
        if (Test-PortOpen -Port $Port) {
            if (-not (Test-Path $ready)) { return }
            & $ready -h 127.0.0.1 -p $Port -q 2>&1 | Out-Null
            if ($LASTEXITCODE -eq 0) { return }
        }
        Start-Sleep -Milliseconds 250
    }
    $logDir = Join-Path (Split-Path -Parent $DataDir) 'logs'
    throw ("the cluster did not start listening on port $Port within $TimeoutSeconds seconds (see " + (Join-Path $logDir 'postgresql.log') + ")")
}

function Start-Server {
    if (Test-PortOpen -Port $Port) { return }
    $logDir = Join-Path (Split-Path -Parent $DataDir) 'logs'
    if (-not (Test-Path $logDir)) { New-Item -ItemType Directory -Force -Path $logDir | Out-Null }
    $server = Join-Path $BinDir 'postgres.exe'
    $logFile = Join-Path $logDir 'postgresql.log'
    $errorFile = Join-Path $logDir 'postgresql.err.log'
    # Create the postmaster through WMI so it inherits none of the caller's
    # handles. Any ordinary spawn (pg_ctl, Start-Process, cmd /c start) leaves
    # the server holding the caller's standard handles, and a captured pipeline
    # then waits forever for EOF. The command redirects the server output to
    # files inside cmd.
    $line = 'cmd.exe /c ""{0}" -D "{1}" > "{2}" 2> "{3}""' -f $server, $DataDir, $logFile, $errorFile
    $created = Invoke-CimMethod -ClassName Win32_Process -MethodName Create -Arguments @{ CommandLine = $line; CurrentDirectory = $logDir }
    if ($null -eq $created -or $created.ReturnValue -ne 0) {
        throw ("starting the postmaster failed (WMI return value " + $created.ReturnValue + ")")
    }
    Wait-ForServer -TimeoutSeconds 120
}

function Install-Cluster {
    if (Test-Cluster) {
        throw "A PostgreSQL cluster already exists in $DataDir. Reuse it with 'start', remove it first, or choose another -DataDir."
    }
    if (Test-PortOpen -Port $Port) {
        throw "Port $Port is already in use. Choose another -Port, or adopt the existing instance instead of creating a new cluster."
    }

    $root = Split-Path -Parent $DataDir
    if (-not (Test-Path $root)) { New-Item -ItemType Directory -Force -Path $root | Out-Null }
    $password = New-ClusterPassword
    $passwordFile = Join-Path $env:TEMP ("errkb-pgpass-" + [guid]::NewGuid().ToString('N') + ".tmp")
    try {
        [System.IO.File]::WriteAllText($passwordFile, $password + "`n", (New-Object System.Text.UTF8Encoding($false)))
        $initdb = Join-Path $BinDir 'initdb.exe'
        $output = & $initdb -D $DataDir -U $Superuser "--pwfile=$passwordFile" --auth-host=scram-sha-256 --auth-local=scram-sha-256 -E UTF8 --locale=C 2>&1
        if ($LASTEXITCODE -ne 0) { throw ("initdb failed: " + ($output -join ' ')) }

        Add-Content -Path (Join-Path $DataDir 'postgresql.conf') -Value @("", "# errkb managed cluster (EKB-051)", "port = $Port", "listen_addresses = '127.0.0.1'")

        Start-Server

        $psql = Join-Path $BinDir 'psql.exe'
        $previous = $env:PGPASSWORD
        $env:PGPASSWORD = $password
        try {
            & $psql -h 127.0.0.1 -p $Port -U $Superuser -d postgres -v ON_ERROR_STOP=1 -c ('create database "' + $Database + '"') 2>&1 | Out-Null
            if ($LASTEXITCODE -ne 0) { throw "creating the application database failed" }
        } finally {
            if ($null -eq $previous) { Remove-Item Env:PGPASSWORD -ErrorAction SilentlyContinue } else { $env:PGPASSWORD = $previous }
        }

        Write-Credentials -Password $password -Path (Get-CredentialsPath)
        Write-Output "installed  $DataDir  (port $Port, database $Database)"
        Write-Output ("credentials " + (Get-CredentialsPath))
        Write-Output "The password is stored in the credentials file; it is never printed."
    } catch {
        # A failed install must not leave a half-written cluster behind.
        try { Invoke-PgCtl @('-D', $DataDir, '-m', 'immediate', 'stop') | Out-Null } catch { }
        if (Test-Path $DataDir) { Remove-Item -Recurse -Force $DataDir -ErrorAction SilentlyContinue }
        throw
    } finally {
        if (Test-Path $passwordFile) { Remove-Item -Force $passwordFile -ErrorAction SilentlyContinue }
    }
}

function Start-Cluster {
    if (-not (Test-Cluster)) { throw "No cluster found in $DataDir. Run 'install' first." }
    if (Test-PortOpen -Port $Port) {
        Write-Output "already running on port $Port"
        return
    }
    $logDir = Join-Path (Split-Path -Parent $DataDir) 'logs'
    if (-not (Test-Path $logDir)) { New-Item -ItemType Directory -Force -Path $logDir | Out-Null }
    Start-Server
    Write-Output "started    $DataDir on port $Port"
}

function Stop-Cluster {
    if (-not (Test-Cluster)) {
        Write-Output "no cluster in $DataDir"
        return
    }
    if (-not (Test-PortOpen -Port $Port)) {
        Write-Output "already stopped $DataDir"
        return
    }
    Invoke-PgCtl @('-D', $DataDir, '-m', 'fast', '-w', '-t', '90', 'stop') | Out-Null
    Write-Output "stopped    $DataDir"
}

function Uninstall-Cluster {
    if (Test-Cluster) {
        Stop-Cluster
        if ($DeleteData) {
            $parent = Split-Path -Parent $DataDir
            if ([string]::IsNullOrWhiteSpace($DataDir) -or $DataDir.Length -lt 8) {
                throw "refusing to delete a path that is too broad: $DataDir"
            }
            Remove-Item -Recurse -Force $DataDir
            Write-Output "deleted    $DataDir (data removed because -DeleteData was passed)"
        } else {
            Write-Output "kept       $DataDir (re-run with -DeleteData to remove it)"
        }
    } else {
        Write-Output "no cluster in $DataDir"
    }
    $credentials = Get-CredentialsPath
    if (Test-Path $credentials) {
        Remove-Item -Force $credentials
        Write-Output "removed    $credentials"
    }
    if (Test-PortOpen -Port $Port) { throw "port $Port is still open after uninstall" }
    Write-Output "port       $Port is free"
}

function Show-Status {
    $installed = Test-Cluster
    $open = Test-PortOpen -Port $Port
    Write-Output "data_dir   $DataDir"
    Write-Output "installed  $installed"
    Write-Output "port       $Port"
    Write-Output "listening  $open"
    if ($installed) {
        $versionFile = Join-Path $DataDir 'PG_VERSION'
        Write-Output ("cluster    PostgreSQL " + (Get-Content $versionFile -Raw).Trim())
    }
}

$BinDir = Resolve-BinDir -Candidate $BinDir
switch ($Action) {
    'install' { Install-Cluster }
    'start' { Start-Cluster }
    'stop' { Stop-Cluster }
    'uninstall' { Uninstall-Cluster }
    'status' { Show-Status }
}


