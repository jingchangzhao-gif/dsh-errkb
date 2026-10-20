# Wraps one Codex CLI invocation through `errkb run` so a failing Codex process
# is recorded in the local knowledge base and the known fix is printed. This is
# the channel B capture path described in probe.md (EKB-040).
#
# Usage:
#   .\errkb-codex.ps1 exec --skip-git-repo-check "Say hi."
#
# Environment:
#   ERRKB_BIN      path to errkb.exe (default: errkb.exe on PATH)
#   ERRKB_PIPE     daemon pipe, e.g. \\.\pipe\errkb (default: the client default)
#   ERRKB_PROJECT  project key for the issues (default: default)
param([Parameter(ValueFromRemainingArguments = $true)][string[]]$CodexArgs)

$errkb = if ($env:ERRKB_BIN) { $env:ERRKB_BIN } else { "errkb.exe" }
$project = if ($env:ERRKB_PROJECT) { $env:ERRKB_PROJECT } else { "default" }
$session = "codex-" + [guid]::NewGuid().ToString()

& $errkb run --connector "codex-cli" --session $session --project $project -- codex @CodexArgs
exit $LASTEXITCODE
