<#
.SYNOPSIS
Emits the errkb context hint when a Claude Code session starts.

.DESCRIPTION
Claude Code runs this script as a SessionStart hook. It prints the JSON hook
output that adds one line to the session context, so the model knows an error
knowledge base is available through the `mcp__errkb__*` tools. The script never
fails the turn: problems are ignored and the process exits 0.
#>
$hint = '[errkb] Errors are tracked by the errkb plugin. A context line starting with [errkb] names a known error; call the mcp__errkb__err_lookup tool with the error text before diagnosing a failure, and record a fix with mcp__errkb__err_record_solution.'
try {
    [Console]::In.ReadToEnd() | Out-Null
} catch {
}
$output = [ordered]@{
    hookSpecificOutput = [ordered]@{
        hookEventName      = 'SessionStart'
        additionalContext  = $hint
    }
}
try {
    $output | ConvertTo-Json -Depth 4 -Compress
} catch {
}
exit 0
