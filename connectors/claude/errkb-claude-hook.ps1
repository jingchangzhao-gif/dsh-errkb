<#
.SYNOPSIS
Captures a Claude Code tool failure into the errkb core over its local pipe.

.DESCRIPTION
Claude Code invokes this script from a PostToolUseFailure hook and passes the
hook payload as JSON on standard input. The script builds the EKB-036 capture
observation and sends it to the errkb daemon's Named Pipe API. It never fails
the host turn: every problem is written to the hook log and the process exits 0.

The pipe protocol is one JSON request per line and one JSON response per line.

.PARAMETER Pipe
Named Pipe the errkb daemon listens on. Default `\\.\pipe\errkb`.

.PARAMETER Connector
Connector name recorded in the knowledge base. Default `claude-code`.

.PARAMETER Project
Project key for the observation. Default: the last segment of the hook `cwd`.

.PARAMETER LogFile
Where problems are appended. Default %LOCALAPPDATA%\errkb\claude-hook.log.
#>
[CmdletBinding()]
param(
    [string]$Pipe = '\\.\pipe\errkb',
    [string]$Connector = 'claude-code',
    [string]$Project = '',
    [string]$LogFile = (Join-Path $env:LOCALAPPDATA 'errkb\claude-hook.log')
)

$ErrorActionPreference = 'Stop'

function Write-HookLog([string]$Message) {
    try {
        New-Item -ItemType Directory -Force -Path (Split-Path -Parent $LogFile) | Out-Null
        $stamp = [DateTime]::UtcNow.ToString('yyyy-MM-ddTHH:mm:ssZ')
        [System.IO.File]::AppendAllText($LogFile, "[$stamp] $Message`n")
    } catch {
        # A logging failure must not fail the host turn either.
    }
}

function New-DeterministicId([string]$Text) {
    $md5 = [System.Security.Cryptography.MD5]::Create()
    try {
        $hash = $md5.ComputeHash([System.Text.Encoding]::UTF8.GetBytes($Text))
    } finally {
        $md5.Dispose()
    }
    $bytes = [byte[]]::new(16)
    [Array]::Copy($hash, $bytes, 16)
    $bytes[6] = [byte](($bytes[6] -band 0x0F) -bor 0x40)
    $bytes[8] = [byte](($bytes[8] -band 0x3F) -bor 0x80)
    return ([guid]::new($bytes)).ToString()
}

function Send-Capture($Request) {
    $pipeName = ($Pipe -split '\\' | Where-Object { $_ -ne '' } | Select-Object -Last 1)
    $client = [System.IO.Pipes.NamedPipeClientStream]::new('.', $pipeName, [System.IO.Pipes.PipeDirection]::InOut)
    try {
        $client.Connect(3000)
        $writer = [System.IO.StreamWriter]::new($client, [System.Text.UTF8Encoding]::new($false))
        $writer.NewLine = "`n"
        $writer.AutoFlush = $true
        $writer.WriteLine($Request)
        $reader = [System.IO.StreamReader]::new($client, [System.Text.UTF8Encoding]::new($false))
        $line = $reader.ReadLine()
        if ([string]::IsNullOrWhiteSpace($line)) {
            throw 'the daemon answered with an empty frame'
        }
        $response = $line | ConvertFrom-Json
        if ($null -ne $response.error) {
            throw "daemon error $($response.error.code): $($response.error.message)"
        }
        return $response.result
    } finally {
        $client.Dispose()
    }
}

try {
    $raw = [Console]::In.ReadToEnd()
    if ([string]::IsNullOrWhiteSpace($raw)) {
        Write-HookLog 'no hook payload on stdin; nothing captured'
        exit 0
    }
    $payload = $raw | ConvertFrom-Json
    if ($payload.hook_event_name -ne 'PostToolUseFailure') {
        exit 0
    }

    $sessionId = [string]$payload.session_id
    $toolUseId = [string]$payload.tool_use_id
    $errorText = [string]$payload.error
    if ([string]::IsNullOrWhiteSpace($sessionId) -or [string]::IsNullOrWhiteSpace($errorText)) {
        Write-HookLog 'hook payload has no session_id or no error text; nothing captured'
        exit 0
    }

    $projectKey = $Project
    if ([string]::IsNullOrWhiteSpace($projectKey) -and -not [string]::IsNullOrWhiteSpace([string]$payload.cwd)) {
        $leaf = Split-Path -Leaf ([string]$payload.cwd).TrimEnd('/', '\')
        if (-not [string]::IsNullOrWhiteSpace($leaf)) { $projectKey = $leaf }
    }
    if ([string]::IsNullOrWhiteSpace($projectKey)) { $projectKey = 'claude' }

    $exitCode = $null
    if ($errorText -match 'Exit code (\d+)') { $exitCode = [int]$Matches[1] }

    $observation = [ordered]@{
        observed_at = [DateTime]::UtcNow.ToString('yyyy-MM-ddTHH:mm:ss.fffZ')
        source      = 'tool'
        message     = $errorText
        tool_name   = [string]$payload.tool_name
        project_key = $projectKey
        evidence    = ('claude code ' + [string]$payload.hook_event_name + ' ' + [string]$payload.tool_name)
    }
    $command = $null
    if ($null -ne $payload.tool_input) { $command = [string]$payload.tool_input.command }
    if (-not [string]::IsNullOrWhiteSpace($command)) { $observation.command = $command }
    if ($null -ne $exitCode) { $observation.exit_code = $exitCode }

    $identity = '{0}|{1}' -f $sessionId, $toolUseId
    $request = [ordered]@{
        schema_version = 'v1'
        request_id     = 'claude-hook-' + [guid]::NewGuid().ToString('N')
        operation      = 'capture'
        payload        = [ordered]@{
            schema_version = 'v1'
            connector_id   = $Connector
            session_id     = $sessionId
            channel        = 'A'
            observation_id = (New-DeterministicId $identity)
            observation    = $observation
        }
    }

    $result = Send-Capture ($request | ConvertTo-Json -Depth 8 -Compress)
    Write-HookLog ('captured ' + $payload.tool_name + ' failure into issue ' + $result.issue_id + ' (created=' + $result.created + ')')
    exit 0
} catch {
    Write-HookLog ('hook failed: ' + $_.Exception.Message)
    exit 0
}
