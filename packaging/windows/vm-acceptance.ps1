<#
.SYNOPSIS
    Clean-VM acceptance for the errkb Windows release (EKB-052).

.DESCRIPTION
    Runs the automatable half of the clean-VM acceptance: artifact checks,
    pre-flight, silent install, first-run checks, the real error loop, the
    after-reboot check and uninstall residue checks. Steps that need a human
    (the first-run wizard, the standard-user session, the reboot itself and the
    delete-data choice on uninstall) are printed as MANUAL rows and never make
    the run pass on their own.

    Every step writes to the evidence directory; the run ends with a table, a
    results.json file and a non-zero exit code when any automated step failed.
    A failure is never retried silently -- rerun the phase only after fixing
    the cause and keep both evidence folders.

.PARAMETER Phase
    artifacts, preflight, install, firstrun, loop, retention, rebootprepare,
    afterreboot, uninstall, or all (artifacts -> preflight -> install ->
    firstrun -> loop).

.PARAMETER Installer
    Path to errkb-<version>-setup.exe. The default resolves to dist\ of the
    repository that contains this script.

.PARAMETER ExpectedSha256
    SHA256 recorded in docs/acceptance-vm.md for the installer under test.
    Required by the artifacts phase: acceptance needs a checksum match.

.PARAMETER ExpectedVersion
    Version string `errkb version` must print (default 0.1.0).

.PARAMETER UninstallData
    keep (default, what a silent uninstall chooses) or delete. The delete path
    passes /DELETE_DATA=1 to the uninstaller so both choices are repeatable.

.NOTES
    Run from an ordinary (non-elevated) PowerShell window on the clean VM.
    PostgreSQL 16 is an explicit dependency and must be installed before this
    script runs. The script never installs PostgreSQL and never edits the
    installer.
#>
[CmdletBinding()]
param(
    [ValidateSet('all', 'artifacts', 'preflight', 'install', 'firstrun', 'loop', 'retention', 'rebootprepare', 'afterreboot', 'uninstall')]
    [string]$Phase = 'all',

    [string]$Installer = '',

    [string]$ExpectedSha256 = '',

    [string]$ExpectedVersion = '0.1.0',

    [int]$Port = 5433,

    [ValidateSet('fresh', 'existing')]
    [string]$ExpectedConnectorState = 'fresh',

    [ValidateSet('keep', 'delete')]
    [string]$UninstallData = 'keep',

    [string]$EvidenceDir = '',

    # Dry runs on a development host: relaxes "standard user" and "clean host".
    [switch]$AllowAdmin,

    [switch]$AllowDirtyHost,

    # CI validates the unattended product path. Human-only UI and real reboot
    # checks are retained in the report as explicit SKIP rows, never as PASS.
    [switch]$Automated
)

$ErrorActionPreference = 'Stop'

$RepoRoot = Split-Path -Parent (Split-Path -Parent $PSScriptRoot)
$AppDir = Join-Path $env:LOCALAPPDATA 'errkb'
$Exe = Join-Path $AppDir 'errkb.exe'
$Credentials = Join-Path $AppDir 'pg\credentials.json'
$Migrations = Join-Path $AppDir 'migrations'
$PipeName = 'errkb'

if ([string]::IsNullOrWhiteSpace($Installer)) {
    $Installer = Join-Path $RepoRoot ("dist\errkb-{0}-setup.exe" -f $ExpectedVersion)
}
if ([string]::IsNullOrWhiteSpace($EvidenceDir)) {
    $EvidenceDir = Join-Path $env:TEMP ('errkb-vm-acceptance-{0}' -f (Get-Date -Format 'yyyyMMdd-HHmmss'))
}
New-Item -ItemType Directory -Force -Path $EvidenceDir | Out-Null
# Inno's uninstaller executes the actual removal from a clone under %TEMP%.
# A relative /LOG path is therefore resolved against a different working
# directory and aborts the uninstaller before InitializeUninstall can run.
$EvidenceDir = (Get-Item -LiteralPath $EvidenceDir).FullName

$script:Results = New-Object System.Collections.Generic.List[object]

function Add-Result {
    param(
        [string]$Step,
        [string]$Expected,
        [string]$Actual,
        [bool]$Pass,
        [ValidateSet('PASS', 'FAIL', 'SKIP', 'MANUAL')]
        [string]$Status = '',
        [string]$Evidence = ''
    )
    if ([string]::IsNullOrWhiteSpace($Status)) { $Status = $(if ($Pass) { 'PASS' } else { 'FAIL' }) }
    $script:Results.Add([pscustomobject]@{
            Step     = $Step
            Expected = $Expected
            Actual   = $Actual
            Status   = $Status
            Evidence = $Evidence
        })
    $line = '{0,-12} {1}  ({2})' -f $Status, $Step, $Actual
    if ($Status -eq 'PASS') { Write-Host $line -ForegroundColor Green }
    elseif ($Status -eq 'FAIL') { Write-Host $line -ForegroundColor Red }
    elseif ($Status -eq 'MANUAL') { Write-Host $line -ForegroundColor Yellow }
    else { Write-Host $line -ForegroundColor DarkGray }
}

function Add-Manual {
    param([string]$Step, [string]$Expected)
    if ($Automated) {
        Add-Result -Step $Step -Expected $Expected -Actual 'outside automated CI scope' -Pass $true -Status SKIP
    } else {
        Add-Result -Step $Step -Expected $Expected -Actual 'needs a human' -Pass $true -Status MANUAL
    }
}

function Assert-Step {
    param([string]$Step, [string]$Expected, [bool]$Condition, [string]$Actual, [string]$Evidence = '')
    Add-Result -Step $Step -Expected $Expected -Actual $Actual -Pass $Condition -Evidence $Evidence
}

function Save-Evidence {
    param([string]$Name, [string]$Content)
    $path = Join-Path $EvidenceDir $Name
    $Content | Out-File -FilePath $path -Encoding utf8
    return $path
}

function Invoke-Logged {
    param([string]$Name, [scriptblock]$Action)
    try {
        $output = & $Action 2>&1
        $code = $LASTEXITCODE
        $path = Save-Evidence "$Name.log" (($output | ForEach-Object { "$_" }) -join "`n")
        return [pscustomobject]@{ Output = (($output | ForEach-Object { "$_" }) -join "`n"); ExitCode = $code; Evidence = $path }
    } catch {
        $path = Save-Evidence "$Name.error.log" ($_ | Out-String)
        return [pscustomobject]@{ Output = ($_ | Out-String); ExitCode = -1; Evidence = $path }
    }
}

function Start-And-Wait {
    param([string]$FilePath, [string[]]$Arguments, [string]$Name)
    try {
        # Start-Process -Wait waits for the complete descendant process tree on
        # Windows. The installer intentionally leaves PostgreSQL running, so
        # -Wait would never return even after Setup itself exits. Wait on the
        # direct process handle instead.
        Save-Evidence ("$Name.command.txt") ($FilePath + "`n" + ($Arguments -join "`n")) | Out-Null
        $process = Start-Process -FilePath $FilePath -ArgumentList $Arguments -PassThru
        $process.WaitForExit()
        Save-Evidence ("$Name.exit.txt") ("exit code: {0}" -f $process.ExitCode) | Out-Null
        return [pscustomobject]@{ ExitCode = [int]$process.ExitCode; Evidence = (Join-Path $EvidenceDir ("$Name.exit.txt")) }
    } catch {
        $path = Save-Evidence "$Name.error.log" ($_ | Out-String)
        return [pscustomobject]@{ ExitCode = -1; Evidence = $path }
    }
}

function Test-IsAdmin {
    $identity = [Security.Principal.WindowsIdentity]::GetCurrent()
    $principal = New-Object Security.Principal.WindowsPrincipal($identity)
    return $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}

function Test-PortOpen {
    param([int]$TargetPort, [int]$TimeoutMs = 400)
    try {
        $client = New-Object System.Net.Sockets.TcpClient
        $task = $client.ConnectAsync('127.0.0.1', $TargetPort)
        if (-not $task.Wait($TimeoutMs)) { $client.Close(); return $false }
        $open = $client.Connected
        $client.Close()
        return $open
    } catch {
        return $false
    }
}

function Test-Pipe {
    param([string]$Name, [int]$TimeoutMs = 1000)
    try {
        $stream = New-Object System.IO.Pipes.NamedPipeClientStream('.', $Name, [System.IO.Pipes.PipeDirection]::InOut)
        $stream.Connect($TimeoutMs)
        $stream.Dispose()
        return $true
    } catch {
        return $false
    }
}

function Wait-PathAbsent {
    param([string]$Path, [int]$TimeoutSeconds = 15)
    $deadline = (Get-Date).AddSeconds($TimeoutSeconds)
    do {
        if (-not (Test-Path -LiteralPath $Path)) { return $true }
        Start-Sleep -Milliseconds 200
    } while ((Get-Date) -lt $deadline)
    return (-not (Test-Path -LiteralPath $Path))
}

function Get-ErkbCredentials {
    if (-not (Test-Path $Credentials)) { throw "credential file $Credentials does not exist" }
    return Get-Content $Credentials -Raw | ConvertFrom-Json
}

function Get-Dsn {
    $cred = Get-ErkbCredentials
    return ('postgres://{0}:{1}@127.0.0.1:{2}/{3}?sslmode=disable' -f $cred.superuser, $cred.password, $cred.port, $cred.database)
}

function Stop-InstalledDaemon {
    $exeFull = [System.IO.Path]::GetFullPath($Exe)
    foreach ($candidate in @(Get-CimInstance Win32_Process -Filter "Name = 'errkb.exe'" -ErrorAction SilentlyContinue)) {
        if ($candidate.ExecutablePath -and
            ([System.IO.Path]::GetFullPath($candidate.ExecutablePath) -eq $exeFull)) {
            Stop-Process -Id $candidate.ProcessId -Force -ErrorAction SilentlyContinue
        }
    }
    $deadline = (Get-Date).AddSeconds(10)
    while ((Get-Date) -lt $deadline) {
        if (-not (Test-Pipe $PipeName -TimeoutMs 200)) { return }
        Start-Sleep -Milliseconds 200
    }
    throw 'the installer-started daemon did not release the Named Pipe within 10 seconds'
}

# One-shot MCP call: initialize, then tools/call, then close stdin. The server
# answers in order and exits on EOF; each call is independent of the next.
function Invoke-Mcp {
    param([string]$Tool, [hashtable]$Arguments)
    $env:ERRKB_PIPE = '\\.\pipe\' + $PipeName
    $psi = New-Object System.Diagnostics.ProcessStartInfo
    $psi.FileName = $Exe
    $psi.Arguments = 'mcp'
    $psi.RedirectStandardInput = $true
    $psi.RedirectStandardOutput = $true
    $psi.RedirectStandardError = $true
    $psi.UseShellExecute = $false
    $process = [System.Diagnostics.Process]::Start($psi)
    $initialize = @{ jsonrpc = '2.0'; id = 1; method = 'initialize'; params = @{ protocolVersion = '2024-11-05'; capabilities = @{}; clientInfo = @{ name = 'vm-acceptance'; version = '1' } } }
    $initialized = @{ jsonrpc = '2.0'; method = 'notifications/initialized'; params = @{} }
    $call = @{ jsonrpc = '2.0'; id = 2; method = 'tools/call'; params = @{ name = $Tool; arguments = $Arguments } }
    foreach ($frame in @($initialize, $initialized, $call)) {
        $process.StandardInput.WriteLine(($frame | ConvertTo-Json -Depth 10 -Compress))
    }
    $process.StandardInput.Close()
    $stdout = $process.StandardOutput.ReadToEnd()
    $stderr = $process.StandardError.ReadToEnd()
    $process.WaitForExit(30000) | Out-Null
    Save-Evidence ("mcp-{0}.stdout.json" -f $Tool.Replace('err_', '')) $stdout | Out-Null
    Save-Evidence ("mcp-{0}.stderr.log" -f $Tool.Replace('err_', '')) $stderr | Out-Null
    foreach ($line in ($stdout -split "`r?`n")) {
        if ([string]::IsNullOrWhiteSpace($line)) { continue }
        $frame = $line | ConvertFrom-Json
        if ($frame.id -eq 2) {
            if ($frame.error) { throw ("MCP {0} failed: {1}" -f $Tool, $frame.error.message) }
            return $frame.result
        }
    }
    throw ("MCP {0} produced no answer; stderr: {1}" -f $Tool, $stderr.Trim())
}

function Start-ErkbDaemon {
    # Setup starts the production daemon immediately. Stop that exact installed
    # executable before starting the evidence-scoped instance below; otherwise
    # two different mutex names still contend for the same Named Pipe.
    Stop-InstalledDaemon
    $dsn = Get-Dsn
    $health = Join-Path $EvidenceDir 'health.json'
    $log = Join-Path $EvidenceDir 'daemon.log'
    $arguments = @('daemon', '--dsn', ('"' + $dsn + '"'), '--migrations', ('"' + $Migrations + '"'),
        '--pipe', ('"\\.\pipe\' + $PipeName + '"'),
        '--lock-name', 'Local\errkb-vm-acceptance', '--health-file', ('"' + $health + '"'), '--log-file', ('"' + $log + '"'))
    $process = Start-Process -FilePath $Exe -ArgumentList $arguments -PassThru -WindowStyle Hidden
    $deadline = (Get-Date).AddSeconds(45)
    while ((Get-Date) -lt $deadline) {
        if ($process.HasExited) {
            $tail = if (Test-Path $log) { (Get-Content $log -Tail 5) -join ' | ' } else { 'no log' }
            throw ("the daemon exited during startup (exit {0}): {1}" -f $process.ExitCode, $tail)
        }
        if (Test-Path $health) {
            try {
                $document = Get-Content $health -Raw | ConvertFrom-Json
                if ($document.status -eq 'ok') { return $process }
            } catch { }
        }
        Start-Sleep -Milliseconds 500
    }
    throw 'the daemon did not become healthy within 45 seconds (see daemon.log)'
}

function Stop-ErkbDaemon {
    param($Process)
    if ($null -ne $Process -and -not $Process.HasExited) {
        Stop-Process -Id $Process.Id -Force -ErrorAction SilentlyContinue
        $Process.WaitForExit(10000) | Out-Null
    }
}

function Invoke-Erkb {
    param([string[]]$Arguments)
    return Invoke-Logged ('errkb-' + ($Arguments[0])) { & $Exe @Arguments }
}

# ---------------------------------------------------------------- artifacts
function Step-Artifacts {
    Write-Host "`n== artifacts ==" -ForegroundColor Cyan
    Assert-Step 'installer exists' $Installer (Test-Path $Installer) ($(if (Test-Path $Installer) { 'found' } else { 'missing' }))
    if (-not (Test-Path $Installer)) { return }
    $size = (Get-Item $Installer).Length
    Assert-Step 'installer size' 'more than 3 MB' ($size -gt 3MB) ("{0:N0} bytes" -f $size)
    $hash = (Get-FileHash $Installer -Algorithm SHA256).Hash
    Save-Evidence 'installer.sha256' ("{0}  {1}" -f $hash, (Split-Path -Leaf $Installer)) | Out-Null
    if ([string]::IsNullOrWhiteSpace($ExpectedSha256)) {
        Assert-Step 'checksum recorded' 'pass -ExpectedSha256 from docs/acceptance-vm.md' $false ('measured ' + $hash)
    } else {
        Assert-Step 'checksum matches' $ExpectedSha256 ($hash -eq $ExpectedSha256.ToUpperInvariant()) $hash
    }
}

# ---------------------------------------------------------------- preflight
function Step-Preflight {
    Write-Host "`n== preflight ==" -ForegroundColor Cyan
    $os = Get-CimInstance Win32_OperatingSystem
    $isAdmin = Test-IsAdmin
    if ($isAdmin -and $AllowAdmin) {
        Add-Result -Step 'standard user' -Expected 'not an administrator' -Actual 'administrator runner allowed for CI' -Pass $true -Status SKIP
    } else {
        Assert-Step 'standard user' 'not an administrator' (-not $isAdmin) ('admin={0}' -f $isAdmin)
    }
    Save-Evidence 'preflight-os.txt' (("OS: {0} {1} ({2})`nRAM: {3:N0} MB`nComputer: {4}" -f $os.Caption, $os.Version, $os.OSArchitecture, ($os.TotalVisibleMemorySize / 1024), $env:COMPUTERNAME)) | Out-Null
    $dirty = @()
    foreach ($tool in @('go', 'node', 'npm', 'pnpm', 'psql')) {
        $found = Get-Command $tool -ErrorAction SilentlyContinue
        if ($found) { $dirty += ("{0}={1}" -f $tool, $found.Source) }
    }
    if ($env:GOROOT -or $env:GOPATH) { $dirty += 'GOROOT/GOPATH set' }
    if ($dirty.Count -gt 0 -and $AllowDirtyHost) {
        Add-Result -Step 'clean host' -Expected 'no Go/Node/pnpm/psql development tools on PATH' -Actual 'ephemeral CI image contains build tools' -Pass $true -Status SKIP
    } else {
        Assert-Step 'clean host' 'no Go/Node/pnpm/psql development tools on PATH' ($dirty.Count -eq 0) ($(if ($dirty.Count) { $dirty -join '; ' } else { 'clean' }))
    }
    $pgCandidates = @(
        (Join-Path $env:ProgramFiles 'PostgreSQL\16\bin\pg_ctl.exe'),
        (Join-Path ${env:ProgramFiles(x86)} 'PostgreSQL\16\bin\pg_ctl.exe'))
    if ($env:ERRKB_PG_BIN) { $pgCandidates += (Join-Path $env:ERRKB_PG_BIN 'pg_ctl.exe') }
    $pgFound = $pgCandidates | Where-Object { Test-Path $_ } | Select-Object -First 1
    Assert-Step 'PostgreSQL 16 present' 'pg_ctl.exe found (explicit dependency)' ($null -ne $pgFound) ($(if ($pgFound) { $pgFound } else { 'missing' }))
    Assert-Step 'port free' ("port {0} is free" -f $Port) (-not (Test-PortOpen -TargetPort $Port)) ('open={0}' -f (Test-PortOpen -TargetPort $Port))
    Assert-Step 'not installed yet' 'no errkb.exe in the app directory' (-not (Test-Path $Exe)) ($(if (Test-Path $Exe) { 'present: ' + $Exe } else { 'absent: ' + $Exe }))
}

# ---------------------------------------------------------------- install
function Step-Install {
    Write-Host "`n== install ==" -ForegroundColor Cyan
    if (-not (Test-Path $Installer)) { Assert-Step 'install' 'installer exists' $false 'installer missing'; return }
    $log = Join-Path $EvidenceDir 'install.log'
    $run = Start-And-Wait -FilePath $Installer -Arguments @('/VERYSILENT', '/SUPPRESSMSGBOXES', '/NORESTART', ('/LOG="' + $log + '"')) -Name 'install'
    Assert-Step 'installer exit code' '0' ($run.ExitCode -eq 0) ("exit {0}" -f $run.ExitCode) $run.Evidence
    if (-not (Test-Path $Exe)) { Assert-Step 'application installed' $Exe $false 'errkb.exe missing'; return }
    Assert-Step 'application installed' $Exe $true 'errkb.exe present'
    $versionRun = Invoke-Erkb @('version')
    $versionOk = ($versionRun.ExitCode -eq 0) -and ($versionRun.Output -match [regex]::Escape($ExpectedVersion)) -and ($versionRun.Output -match 'go go\d')
    Assert-Step 'version matches' ("errkb {0}" -f $ExpectedVersion) $versionOk ($versionRun.Output.Trim().Replace("`n", ' | ')) $versionRun.Evidence
    Assert-Step 'cluster listening' ("port {0} accepts connections" -f $Port) (Test-PortOpen -TargetPort $Port) ('open={0}' -f (Test-PortOpen -TargetPort $Port))
    Assert-Step 'credentials file' 'credentials.json present' (Test-Path $Credentials) $Credentials
    if (Test-Path $Credentials) {
        $acl = Get-Acl $Credentials
        $sids = @($acl.Access | ForEach-Object { $_.IdentityReference.Translate([Security.Principal.SecurityIdentifier]).Value } | Sort-Object -Unique)
        $allowed = @(
            [Security.Principal.WindowsIdentity]::GetCurrent().User.Value,
            'S-1-5-18',
            'S-1-5-32-544'
        ) | Sort-Object -Unique
        $unexpected = @($sids | Where-Object { $_ -notin $allowed })
        $missing = @($allowed | Where-Object { $_ -notin $sids })
        $restricted = $unexpected.Count -eq 0 -and $missing.Count -eq 0 -and $acl.AreAccessRulesProtected
        Assert-Step 'credentials restricted' 'only the current user, SYSTEM and Administrators with inheritance disabled' $restricted (($acl.Access | ForEach-Object { $_.IdentityReference.Value }) -join '; ')
    }
    Add-Manual 'first-run wizard' 'run the interactive installer once: pick "Create a dedicated cluster", confirm the database page and the default 5433 port; the wizard must reach the finished page without an error box'
}

# ---------------------------------------------------------------- firstrun
function Step-FirstRun {
    Write-Host "`n== first run ==" -ForegroundColor Cyan
    try { $dsn = Get-Dsn } catch { Assert-Step 'credential file readable' 'read the DSN from credentials.json' $false $_.Exception.Message; return }
    $doctor = Invoke-Logged 'doctor-first' { & $Exe doctor --dsn $dsn --migrations $Migrations --json }
    try { $report = $doctor.Output | ConvertFrom-Json } catch { $report = $null }
    if ($null -eq $report) { Assert-Step 'doctor output' 'valid JSON' $false 'doctor did not print JSON'; return }
    $byName = @{}
    foreach ($check in $report.checks) { $byName[$check.name] = $check }
    foreach ($required in @('database', 'migrations', 'extension')) {
        $check = $byName[$required]
        $ok = ($null -ne $check) -and ($check.status -eq 'ok')
        Assert-Step ("doctor " + $required) 'status ok' $ok ('status=' + $(if ($check) { $check.status } else { 'missing' }))
    }
    $connectorCheck = $byName['connectors']
    $connectorCount = if ($connectorCheck) { [int]$connectorCheck.detail.count } else { -1 }
    $connectorStateOK = if ($ExpectedConnectorState -eq 'fresh') {
        ($null -ne $connectorCheck) -and ($connectorCheck.status -eq 'degraded') -and ($connectorCount -eq 0)
    } else {
        ($null -ne $connectorCheck) -and ($connectorCheck.status -eq 'ok') -and ($connectorCount -ge 1)
    }
    $connectorExpected = if ($ExpectedConnectorState -eq 'fresh') {
        'degraded with zero registered connectors on a fresh install'
    } else {
        'ok with at least one connector preserved after reinstall'
    }
    Assert-Step 'doctor connectors' $connectorExpected $connectorStateOK `
        ('status={0}, count={1}' -f $(if ($connectorCheck) { $connectorCheck.status } else { 'missing' }), $(if ($connectorCheck) { $connectorCheck.detail.count } else { '?' }))
    $pipeCheck = $byName['pipe']
    $pipeDefaultOk = ($null -ne $pipeCheck) -and ($pipeCheck.status -eq 'ok') -and
        ($pipeCheck.summary -match [regex]::Escape('\\.\pipe\'))
    Assert-Step 'doctor default: Named Pipe' 'pipe enabled by default' $pipeDefaultOk `
        ('config: ' + $(if ($pipeCheck) { $pipeCheck.summary } else { 'unknown' })) $doctor.Evidence
    $process = $null
    try {
        $process = Start-ErkbDaemon
        Assert-Step 'daemon starts' 'health file reports status ok' $true 'daemon healthy'
    } catch {
        Assert-Step 'daemon starts' 'health file reports status ok' $false $_.Exception.Message
        return
    }
    try {
        Assert-Step 'pipe listening' ("\\.\pipe\{0} accepts a client" -f $PipeName) (Test-Pipe $PipeName) ('pipe reachable=' + (Test-Pipe $PipeName))
        $listeners = @(Get-NetTCPConnection -OwningProcess $process.Id -State Listen -ErrorAction SilentlyContinue)
        Assert-Step 'HTTP disabled' 'the daemon opens no TCP listener' ($listeners.Count -eq 0) ('{0} listener(s)' -f $listeners.Count)
        $doctor2 = Invoke-Logged 'doctor-daemon' { & $Exe doctor --dsn (Get-Dsn) --migrations $Migrations --json }
        try { $report2 = $doctor2.Output | ConvertFrom-Json } catch { $report2 = $null }
        $nonOK = if ($report2) { @($report2.checks | Where-Object { $_.status -ne 'ok' }) } else { @() }
        $expectedRuntimeState = if ($ExpectedConnectorState -eq 'fresh') {
            ($null -ne $report2) -and ($report2.status -eq 'degraded') -and
                ($nonOK.Count -eq 1) -and ($nonOK[0].name -eq 'connectors') -and
                ($nonOK[0].status -eq 'degraded') -and ([string]$nonOK[0].detail.count -eq '0')
        } else {
            ($null -ne $report2) -and ($report2.status -eq 'ok') -and ($nonOK.Count -eq 0)
        }
        $runtimeExpected = if ($ExpectedConnectorState -eq 'fresh') {
            'all runtime checks ok; only zero connectors is degraded'
        } else {
            'all checks ok with the retained connector'
        }
        Assert-Step 'doctor operational' $runtimeExpected $expectedRuntimeState `
            ('status={0}, non-ok={1}' -f $(if ($report2) { $report2.status } else { 'invalid json' }), $(if ($nonOK.Count) { ($nonOK.name -join ',') } else { 'none' }))
    } finally {
        Stop-ErkbDaemon -Process $process
    }
}

# ---------------------------------------------------------------- loop
function Step-Loop {
    Write-Host "`n== real error loop ==" -ForegroundColor Cyan
    $process = $null
    try { $process = Start-ErkbDaemon } catch { Assert-Step 'loop daemon' 'daemon healthy' $false $_.Exception.Message; return }
    try {
        $session = 'vm-acceptance-' + (Get-Date -Format 'yyyyMMddHHmmss')
        $first = Invoke-Logged 'run-first' {
            & $Exe run --connector 'vm-acceptance' --session $session --project 'vm-acceptance' --json -- cmd.exe /c 'exit 9'
        }
        try { $envelope = $first.Output | ConvertFrom-Json } catch { $envelope = $null }
        $record = if ($envelope) { $envelope.record } else { $null }
        Assert-Step 'failed command recorded' 'a new issue with an issue id' (($null -ne $record) -and $record.created -eq $true -and $record.issue_id) ('created=' + $(if ($record) { $record.created } else { 'no json' })) $first.Evidence
        if ($null -eq $record -or -not $record.issue_id) { return }
        $headline = 'command exited with status 9'
        try {
            $solution = Invoke-Mcp 'err_record_solution' @{
                schema_version = 'v1'
                issue_id       = [string]$record.issue_id
                body           = 'Check the exit code and the command line before rerunning.'
                preconditions  = 'PostgreSQL 16 installed and the daemon running'
                source         = 'vm-acceptance'
                actor          = 'vm-acceptance'
            }
            $payload = $solution.content[0].text | ConvertFrom-Json
            Assert-Step 'solution recorded' 'solution_version_id returned' ($null -ne $payload.solution_version_id) ('version ' + $(if ($payload) { $payload.version } else { '?' }))
            $solutionVersionID = [string]$payload.solution_version_id
        } catch {
            Assert-Step 'solution recorded' 'err_record_solution succeeds' $false $_.Exception.Message
            return
        }
        $second = Invoke-Logged 'run-second' {
            & $Exe run --connector 'vm-acceptance' --session $session --project 'vm-acceptance' --json -- cmd.exe /c 'exit 9'
        }
        $showsFix = $second.Output.Contains('Check the exit code and the command line before rerunning.') -and $second.Output.Contains([string]$record.issue_id)
        Assert-Step 'second run returns the fix' 'output quotes the issue id and the fix body' $showsFix ("contains fix={0}" -f $showsFix) $second.Evidence
        $feedback = Invoke-Logged 'feedback' { & $Exe feedback ([string]$record.issue_id) $solutionVersionID --outcome success --json }
        Assert-Step 'feedback accepted' 'exit 0' ($feedback.ExitCode -eq 0) ("exit {0}" -f $feedback.ExitCode) $feedback.Evidence
        $search = Invoke-Logged 'search' { & $Exe search $headline --json }
        try { $searchReport = $search.Output | ConvertFrom-Json } catch { $searchReport = $null }
        $hasCandidate = ($null -ne $searchReport) -and ($searchReport.candidates.Count -ge 1)
        Assert-Step 'search finds the issue' 'at least one candidate' $hasCandidate ('candidates=' + $(if ($searchReport) { $searchReport.candidates.Count } else { 'no json' })) $search.Evidence
        $stats = Invoke-Logged 'stats' { & $Exe stats --json }
        try { $statsReport = $stats.Output | ConvertFrom-Json } catch { $statsReport = $null }
        Assert-Step 'stats reachable' 'totals present' (($null -ne $statsReport) -and ($null -ne $statsReport.totals)) ('issues=' + $(if ($statsReport) { $statsReport.totals.issues } else { 'no json' })) $stats.Evidence
        Add-Manual 'human confirmation' 'watch the second run print the known fix, and confirm the issue page shows the solution version and the outcome'
    } finally {
        Stop-ErkbDaemon -Process $process
    }
}

# ---------------------------------------------------------------- retention
function Step-Retention {
    Write-Host "`n== retained data after reinstall ==" -ForegroundColor Cyan
    if (-not (Test-Path $Exe)) {
        Assert-Step 'retention application' 'errkb.exe is installed again' $false 'errkb.exe missing'
        return
    }
    $process = $null
    try { $process = Start-ErkbDaemon } catch { Assert-Step 'retention daemon' 'daemon healthy' $false $_.Exception.Message; return }
    try {
        $search = Invoke-Logged 'retention-search' { & $Exe search 'command exited with status 9' --json }
        try { $report = $search.Output | ConvertFrom-Json } catch { $report = $null }
        $hasCandidate = ($search.ExitCode -eq 0) -and ($null -ne $report) -and ($report.candidates.Count -ge 1)
        Assert-Step 'retained issue searchable' 'the pre-uninstall issue is still returned after reinstall' $hasCandidate `
            ('candidates=' + $(if ($report) { $report.candidates.Count } else { 'invalid json' })) $search.Evidence

        $third = Invoke-Logged 'retention-run' {
            & $Exe run --connector 'vm-acceptance' --session 'retained-data' --project 'vm-acceptance' --json -- cmd.exe /c 'exit 9'
        }
        $showsFix = $third.Output.Contains('Check the exit code and the command line before rerunning.')
        Assert-Step 'retained solution reused' 'the known solution is returned after reinstall' $showsFix `
            ('contains fix={0}' -f $showsFix) $third.Evidence
    } finally {
        Stop-ErkbDaemon -Process $process
    }
}

# ---------------------------------------------------------------- reboot
function Step-RebootPrepare {
    Write-Host "`n== reboot prepare ==" -ForegroundColor Cyan
    $os = Get-CimInstance Win32_OperatingSystem
    $marker = Join-Path $EvidenceDir 'reboot.json'
    @{ boot_time = $os.LastBootUpTime.ToString('o'); prepared_at = (Get-Date).ToString('o') } |
        ConvertTo-Json | Out-File -FilePath $marker -Encoding utf8
    Write-Host ('marker written to {0}; reboot the VM now, then run -Phase afterreboot' -f $marker) -ForegroundColor Yellow
    Add-Manual 'reboot' 'reboot the VM, then rerun with -Phase afterreboot (the after-reboot steps judge the results)'
}

function Step-AfterReboot {
    Write-Host "`n== after reboot ==" -ForegroundColor Cyan
    $marker = Join-Path $EvidenceDir 'reboot.json'
    $rebooted = $false
    if (Test-Path $marker) {
        $previous = Get-Content $marker -Raw | ConvertFrom-Json
        $os = Get-CimInstance Win32_OperatingSystem
        $rebooted = ([datetime]$os.LastBootUpTime) -gt ([datetime]$previous.prepared_at)
    }
    Assert-Step 'machine rebooted' 'boot time is newer than the prepare marker' $rebooted ('rebooted=' + $rebooted)
    Assert-Step 'cluster after reboot' ("port {0} listens without manual start" -f $Port) (Test-PortOpen -TargetPort $Port) ('open=' + (Test-PortOpen -TargetPort $Port))
    Assert-Step 'daemon after reboot' 'Named Pipe is served without manual start' (Test-Pipe $PipeName) ('pipe=' + (Test-Pipe $PipeName))
    $instances = @(Get-Process -Name 'errkb' -ErrorAction SilentlyContinue)
    Assert-Step 'single instance' 'exactly one errkb process after boot (tray or daemon)' ($instances.Count -eq 1) ('{0} process(es)' -f $instances.Count)
    Add-Manual 'tray restored' 'log in as the same user and confirm the tray icon shows the capture state without a second instance'
}

# ---------------------------------------------------------------- uninstall
function Step-Uninstall {
    Write-Host "`n== uninstall ==" -ForegroundColor Cyan
    $uninstaller = Join-Path $AppDir 'unins000.exe'
    if (-not (Test-Path $uninstaller)) { Assert-Step 'uninstaller present' 'unins000.exe exists' $false $uninstaller; return }
    $keep = ($UninstallData -eq 'keep')
    $log = Join-Path $EvidenceDir 'uninstall.log'
    $arguments = @('/VERYSILENT', '/SUPPRESSMSGBOXES', '/NORESTART', ('/LOG="' + $log + '"'))
    # InitializeUninstall asks whether retained data should be kept. Silent
    # automation must answer explicitly: the suppressed three-way prompt uses
    # Cancel as its safe default, which exits before the uninstall log opens.
    $arguments += $(if ($keep) { '/KEEP_DATA=1' } else { '/DELETE_DATA=1' })
    $run = Start-And-Wait -FilePath $uninstaller -Arguments $arguments -Name 'uninstall'
    Assert-Step 'uninstaller exit code' '0' ($run.ExitCode -eq 0) ("exit {0}" -f $run.ExitCode) $run.Evidence
    Assert-Step 'port released' ("port {0} is free" -f $Port) (-not (Test-PortOpen -TargetPort $Port)) ('open=' + (Test-PortOpen -TargetPort $Port))
    Assert-Step 'no postgres process left' 'no postgres.exe serving the app data directory' (-not (Get-CimInstance Win32_Process -Filter "Name = 'postgres.exe'" | Where-Object { $_.CommandLine -like ('*' + $AppDir + '*') })) 'checked'
    Assert-Step 'no errkb process left' 'no errkb.exe running' (-not (Get-Process -Name 'errkb' -ErrorAction SilentlyContinue)) 'checked'
    $services = @(Get-Service -ErrorAction SilentlyContinue | Where-Object { $_.Name -like '*errkb*' })
    Assert-Step 'no orphan service' 'no errkb service remains' ($services.Count -eq 0) ('{0} service(s)' -f $services.Count)
    $runKey = 'HKCU:' + '\Software\Microsoft\Windows\CurrentVersion\Run'
    $runValue = Get-ItemProperty -Path $runKey -Name 'errkb-autostart' -ErrorAction SilentlyContinue
    Assert-Step 'autostart removed' 'the per-user Run entry is absent' ($null -eq $runValue) ($(if ($runValue) { 'present' } else { 'absent' }))
    if ($keep) {
        Assert-Step 'application removed' 'the application directory is gone' (-not (Test-Path $Exe)) ('exe present=' + (Test-Path $Exe))
        Assert-Step 'data kept' 'the cluster data directory still exists' (Test-Path (Join-Path $AppDir 'pg\data\PG_VERSION')) 'kept'
        Assert-Step 'credentials kept' 'protected credentials remain with retained data for reinstall' (Test-Path $Credentials) ($(if (Test-Path $Credentials) { 'kept' } else { 'missing' }))
        Add-Manual 'data kept migration' 'reinstall once and confirm the retained cluster is detected and reused (the wizard offers to reuse it)'
    } else {
        Assert-Step 'data deleted' 'the data directory is gone' (-not (Test-Path (Join-Path $AppDir 'pg\data'))) 'deleted'
        # Inno returns the original uninstaller's exit code before its temporary
        # clone removes unins000.exe and the now-empty application directory.
        # Give that documented second-phase cleanup a short bounded grace period.
        $appDirectoryDeleted = Wait-PathAbsent -Path $AppDir
        Assert-Step 'application directory deleted' 'the complete per-user application directory is gone' $appDirectoryDeleted `
            ($(if (Test-Path $AppDir) { 'present' } else { 'deleted' }))
    }
}

# ---------------------------------------------------------------- run phases
$phases = @()
if ($Phase -eq 'all') { $phases = @('artifacts', 'preflight', 'install', 'firstrun', 'loop') } else { $phases = @($Phase) }

foreach ($name in $phases) {
    switch ($name) {
        'artifacts' { Step-Artifacts }
        'preflight' { Step-Preflight }
        'install' { Step-Install }
        'firstrun' { Step-FirstRun }
        'loop' { Step-Loop }
        'retention' { Step-Retention }
        'rebootprepare' { Step-RebootPrepare }
        'afterreboot' { Step-AfterReboot }
        'uninstall' { Step-Uninstall }
    }
}

# ---------------------------------------------------------------- results
$failures = @($script:Results | Where-Object { $_.Status -eq 'FAIL' })
$manual = @($script:Results | Where-Object { $_.Status -eq 'MANUAL' })
$skipped = @($script:Results | Where-Object { $_.Status -eq 'SKIP' })
Write-Host "`n== summary ==" -ForegroundColor Cyan
$script:Results | Format-Table -AutoSize Step, Status, Expected, Actual | Out-String -Width 220 | Write-Host
Write-Host ('passed: {0}  failed: {1}  manual: {2}  skipped: {3}' -f (($script:Results.Count - $failures.Count - $manual.Count - $skipped.Count), $failures.Count, $manual.Count, $skipped.Count))
$resultsJSON = $script:Results | ConvertTo-Json -Depth 4
$resultsJSON | Out-File -FilePath (Join-Path $EvidenceDir ('results-{0}.json' -f $Phase)) -Encoding utf8
$resultsJSON | Out-File -FilePath (Join-Path $EvidenceDir 'results.json') -Encoding utf8
Write-Host ('evidence: {0}' -f $EvidenceDir)
if ($failures.Count -gt 0) {
    Write-Host 'RESULT: FAIL -- fix or record each failure with its evidence before calling the card accepted' -ForegroundColor Red
    exit 1
}
if ($manual.Count -gt 0) {
    Write-Host 'RESULT: automated steps passed; the MANUAL steps above are still required for EKB-052 acceptance' -ForegroundColor Yellow
    exit 0
}
Write-Host 'RESULT: PASS' -ForegroundColor Green
exit 0
