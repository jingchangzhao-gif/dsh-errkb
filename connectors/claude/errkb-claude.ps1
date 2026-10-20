<#
.SYNOPSIS
Runs Claude Code headless and surfaces real failures in a form errkb can record.

.DESCRIPTION
Wraps `claude -p` so a model/API failure becomes one stable line instead of a
large JSON object. Intended to be started through the errkb wrapper:

    errkb run --connector claude-cli --session claude-1 --project myproject -- `
        powershell -NoProfile -ExecutionPolicy Bypass -File errkb-claude.ps1 -Prompt "fix the build"

The script prints `[claude code] <result>` when Claude reports an error and
exits with Claude's own exit code; otherwise it prints the result text and
exits 0. It never swallows a failure: a non-zero exit code is always preserved.

.PARAMETER Prompt
Prompt passed to `claude -p`. Required.

.PARAMETER PermissionMode
Forwarded `--permission-mode` value. Default `default`.

.PARAMETER OutputFormat
`json` (default) or `text`. JSON is required to read the error text.

.PARAMETER ExtraArgs
Additional arguments appended to the `claude -p` call.
#>
[CmdletBinding()]
param(
    [Parameter(Mandatory = $true)][string]$Prompt,
    [string]$PermissionMode = 'default',
    [ValidateSet('json', 'text')][string]$OutputFormat = 'json',
    [string[]]$ExtraArgs = @()
)

$ErrorActionPreference = 'Stop'

$claude = (Get-Command claude -ErrorAction SilentlyContinue)
if ($null -eq $claude) {
    [Console]::Error.WriteLine('[claude code] the claude executable is not on PATH')
    exit 1
}

$arguments = @('-p', $Prompt, '--permission-mode', $PermissionMode, '--output-format', $OutputFormat)
$arguments += $ExtraArgs

$raw = & $claude.Source @arguments 2>&1 | Out-String
$exitCode = $LASTEXITCODE

if ($OutputFormat -eq 'json') {
    $parsed = $null
    try {
        $parsed = $raw.Trim() | ConvertFrom-Json
    } catch {
        $parsed = $null
    }
    if ($null -ne $parsed) {
        if ($parsed.is_error -eq $true) {
            $message = [string]$parsed.result
            if ([string]::IsNullOrWhiteSpace($message)) { $message = 'claude code reported an error without a message' }
            Write-Output ('[claude code] ' + $message)
        } else {
            $message = [string]$parsed.result
            if ([string]::IsNullOrWhiteSpace($message)) { $message = 'claude code finished without a result' }
            Write-Output $message
        }
    } else {
        Write-Output $raw.Trim()
    }
} else {
    Write-Output $raw.Trim()
}

if ($exitCode -ne 0) {
    exit $exitCode
}
if ($null -ne $parsed -and $parsed.is_error -eq $true) {
    exit 1
}
exit 0
