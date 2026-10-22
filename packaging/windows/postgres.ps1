# PostgreSQL lifecycle wrapper for the errkb installer.
#
# The lifecycle itself lives in Go (internal/service/pg_manage.go, exposed as
# `errkb pg install|start|stop|uninstall|status|discover`). This script only maps
# the installer's arguments onto that command so packaging and service logic have
# exactly one implementation. The installer (installer.iss) and the logon
# autostart script (autostart.ps1) both call this file.
#
# The release is an installed application: the cluster lives under the user
# profile and PostgreSQL itself stays an explicit dependency.
#
# Examples:
#   powershell -File postgres.ps1 install -DataDir "$env:LOCALAPPDATA\errkb\pg\data" -Port 5433
#   powershell -File postgres.ps1 start
#   powershell -File postgres.ps1 stop
#   powershell -File postgres.ps1 uninstall -DeleteData
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true, Position = 0)]
    [ValidateSet('install', 'start', 'stop', 'uninstall', 'status', 'discover')]
    [string]$Action,

    [string]$DataDir = (Join-Path $env:LOCALAPPDATA 'errkb\pg\data'),
    [int]$Port = 5433,
    [string]$Database = 'errkb',
    [string]$Superuser = 'postgres',
    [switch]$DeleteData,
    [string]$Choice = 'keep',
    [string]$Exe = ''
)

$ErrorActionPreference = 'Stop'

$root = Split-Path -Parent $DataDir
if (-not $Exe) { $Exe = Join-Path $PSScriptRoot 'errkb.exe' }
if (-not (Test-Path $Exe)) {
    throw "errkb.exe was not found next to this script. Pass -Exe <path> or run the packaged layout."
}

# The installer writes a marker when the uninstaller wants the data removed:
# {param:} does not read uninstaller command lines, so postgres.ps1 cannot see
# /DELETE_DATA directly.
$marker = Join-Path (Split-Path -Parent $root) '.errkb-delete-data'
$keepMarker = Join-Path (Split-Path -Parent $root) '.errkb-keep-data'
$removeData = $DeleteData -or ($Choice -in @('delete', '1', 'true', 'yes', 'delete_data')) -or (Test-Path $marker)

$effectiveAction = $Action
$credentialsPath = Join-Path $root 'credentials.json'
$clusterVersionPath = Join-Path $DataDir 'PG_VERSION'
if ($Action -eq 'install') {
    $hasData = Test-Path $clusterVersionPath
    $hasCredentials = Test-Path $credentialsPath
    if ($hasData -xor $hasCredentials) {
        throw 'A retained cluster can only be reused with its protected credentials.json. Restore both together or explicitly delete the retained data before installing.'
    }
    if ($hasData -and $hasCredentials) {
        $stored = Get-Content $credentialsPath -Raw | ConvertFrom-Json
        $storedDataDir = [System.IO.Path]::GetFullPath([string]$stored.data_dir)
        $requestedDataDir = [System.IO.Path]::GetFullPath($DataDir)
        if ([int]$stored.port -ne $Port -or [string]$stored.database -ne $Database -or
            $storedDataDir -ne $requestedDataDir) {
            throw 'The retained cluster credentials do not match the requested data directory, port or database. Choose the matching settings or explicitly delete the retained data.'
        }
        $effectiveAction = 'start'
        Write-Output ('reusing     retained cluster at ' + $DataDir)
    }
}

$arguments = @('pg', $effectiveAction, '--data-dir', $DataDir, '--port', $Port, '--database', $Database, '--superuser', $Superuser)
$migrations = Join-Path (Split-Path -Parent $root) 'migrations'
if ($effectiveAction -eq 'install') { $arguments += @('--migrations', $migrations) }
if ($Action -eq 'stop' -or $Action -eq 'uninstall') {
    if ($removeData) { $arguments += '--delete-data' }
}

& $Exe @arguments
$code = $LASTEXITCODE
if ($code -ne 0) { exit $code }

if ($Action -eq 'uninstall') {
    if ($removeData) {
        $appRoot = Split-Path -Parent $root
        if ($appRoot.Length -ge 8) {
            foreach ($extra in @((Join-Path $appRoot 'health.json'), (Join-Path $appRoot 'logs'), $root)) {
                if (Test-Path $extra) { Remove-Item -Recurse -Force $extra -ErrorAction SilentlyContinue }
            }
        }
        Write-Output 'deleted    the cluster data was removed'
    } else {
        Write-Output 'kept       the cluster data was kept (run uninstall with -DeleteData to remove it)'
    }
    foreach ($choiceMarker in @($marker, $keepMarker)) {
        if (Test-Path $choiceMarker) { Remove-Item -Force $choiceMarker }
    }
}
exit 0
