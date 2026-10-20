# Claude Code connector

Connects Claude Code to the errkb core so tool failures are recorded and the
model can look up a known fix before diagnosing the same error again.

Channel level **A** for tool failures (structured `PostToolUseFailure` hook),
with channel **B** as the fallback (`errkb run -- claude -p …`). See
[probe.md](probe.md) for the measured evidence behind that claim.

## What it uses

| Path | Mechanism | Purpose |
| ---- | --------- | ------- |
| Capture | `PostToolUseFailure` hook → [errkb-claude-hook.ps1](errkb-claude-hook.ps1) → errkb capture operation | Records a failed tool call (text, command, tool name, exit code) |
| Capture fallback | `errkb run -- …` around `claude -p` | Records model/API failures (no hook event exists for them) |
| Query and hand-back | MCP stdio server `errkb mcp` | `mcp__errkb__err_lookup`, `err_record_solution`, `err_feedback` inside the session |
| Context hint | `SessionStart` hook → [errkb-session-hint.ps1](errkb-session-hint.ps1) | Tells the model the knowledge base exists |

The connector contains no classification, redaction, fingerprinting or
matching code. The core derives those from the observation (EKB-036 contract);
the hook only forwards what Claude Code already provides.

## Install

1. Build the CLI and start the daemon (see the repository README):

   ```powershell
   go build -o errkb.exe ./cmd/errkb
   .\errkb.exe daemon --dsn "postgres://postgres:postgres@127.0.0.1:5432/errkb?sslmode=disable" `
       --migrations .\db\migrations --pipe '\\.\pipe\errkb'
   ```

2. Register the MCP server (or load [mcp.json](mcp.json) with
   `--mcp-config` for one run):

   ```powershell
   claude mcp add --scope user --transport stdio errkb -- <path>\errkb.exe mcp
   claude mcp list   # prints "errkb: ... mcp - √ Connected"
   ```

3. Merge the hooks from [hooks.json](hooks.json) into
   `~/.claude/settings.json` (user level) or `.claude/settings.json` (project
   level), replacing `<REPO>` with this repository's absolute path. Use forward
   slashes in hook command paths; Claude Code runs hook commands through a
   POSIX-style shell, so `C:\…` loses its separators and the hook fails with
   exit code 127.

4. Optional: use the fallback wrapper so model/API failures are recorded too:

   ```powershell
   errkb run --connector claude-cli --session claude-1 --project myproject -- `
       powershell -NoProfile -ExecutionPolicy Bypass -File connectors\claude\errkb-claude.ps1 `
       -Prompt "fix the failing build"
   ```

## Degradation

- Without hooks: channel B only (wrapper captures the command exit and its
  output); tool-level detail such as the tool name is missing.
- Without MCP: capture still works, but the model cannot query or record fixes
  inside the session, so there is no pre-diagnosis hand-back.
- Pipe unavailable: the hook logs the problem to
  `%LOCALAPPDATA%\errkb\claude-hook.log` and exits 0; a Claude Code turn is
  never failed by the connector.

## Configuration

| Setting | Default | Meaning |
| ------- | ------- | ------- |
| `-Pipe` (hook) | `\\.\pipe\errkb` | Named Pipe of the errkb daemon |
| `-Connector` (hook) | `claude-code` | Connector name recorded in the knowledge base |
| `-Project` (hook) | leaf of the hook `cwd` | Project key for the observation |
| `ERRKB_PIPE` (MCP) | `\\.\pipe\errkb` | Pipe the MCP server talks to |

## Verification

Live checks behind this connector (2026-10-20, Claude Code 2.1.282):

- `claude mcp list` printed `errkb: … mcp - √ Connected`.
- A real headless session delivered `mcp__errkb__err_stats` through the pipe.
- `PostToolUseFailure` fired for a failing `ls` and carried the error text,
  tool input, tool use id and duration; the hook stored issue
  `28c2ad66-c8cc-4122-a31b-98e472302e75` on channel A, and feeding the same
  payload again returned `created=False` (idempotent).
- The CLI wrapper captured a real, non-transient Claude Code failure (exit code
  1, unknown flag) as issue `1e7c32f3-3ec3-43e6-9a73-f96229033abf` on channel B.
- After a solution was recorded and a `success` outcome sent, the next wrapped
  run printed the fix with `source claude trust 0.60`; MCP `err_lookup` returned
  the same solution for the hook-captured issue.
- A transient `ECONNREFUSED` API failure was classified by the core as
  transient and deliberately not recorded (EKB-016 policy), not a capture gap.

Boundaries: the local model proxy was down during the probe, so hosted-model
turns were only exercised against a local mock endpoint for the hook path; the
tool failure itself and the CLI failure are real. Re-run the probe on a host
with a working model endpoint before relying on long-lived numbers.
