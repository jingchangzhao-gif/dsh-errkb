# Codex CLI capability probe (EKB-040)

Probed on 2026-10-20 against **Codex CLI 0.156.1** on Windows 11 (10.0.26200) with
an isolated `CODEX_HOME` (`%TEMP%\codex-probe\home`) so the probe never touched
the user's real configuration.

## What was probed

| #   | Interface             | Command                                                                                                         | Result                                                                                                                                             |
| --- | --------------------- | --------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1   | Installed tools       | `codex --version`, `codex mcp --help`, `codex plugin --help`                                                    | `codex-cli 0.156.1`; `codex mcp add/list/get/remove/login`; `codex plugin add/list/marketplace/remove`                                             |
| 2   | MCP registration      | `codex mcp add errkb -- <path>\errkb.exe mcp`                                                                   | `Added global MCP server 'errkb'`; `codex mcp list` shows `errkb` enabled, stdio, `args: mcp`; `codex mcp get errkb` prints transport/command/args |
| 3   | Lifecycle hooks       | `[[hooks.Stop]]` + `[[hooks.Stop.hooks]]` in the isolated `config.toml`, `command` hook writing stdin to a file | fired after a turn: `hook: Stop` / `hook: Stop Completed`, payload captured                                                                        |
| 4   | PostToolUse hook      | same config with `[[hooks.PostToolUse]]`                                                                        | did **not** fire: no tool was executed (see limits)                                                                                                |
| 5   | Plugin manifest hooks | marketplace plugin `browser` manifest `.codex-plugin/plugin.json`                                               | supports `hooks` of type `mcp_tool` with `${hook_event_name}` / `${session_id}` / `${turn_id}` templates; also `mcpServers`, `skills`              |
| 6   | Session transcripts   | `%TEMP%\codex-probe\home\sessions\2026\10\21\rollout-*.jsonl`                                                   | newline JSON: `session_meta`, `event_msg` (`task_started`, `item_completed`), `response_item`, `turn_context`, `world_state`                       |

Measured `Stop` payload (captured verbatim, file id redacted):

```json
{
  "session_id": "01a159aa-7a70-7d62-b411-e928c6d62492",
  "turn_id": "01a159aa-7aa0-75a1-bd66-1b1935432242",
  "transcript_path": "C:\\Users\\Administrator\\AppData\\Local\\Temp\\codex-probe\\home\\sessions\\2026\\10\\21\\rollout-2026-10-21T01-15-07-01a159aa-7a70-7d62-b411-e928c6d62492.jsonl",
  "cwd": "C:\\Users\\ADMINI~1\\AppData\\Local\\Temp\\codex-probe\\work",
  "hook_event_name": "Stop",
  "model": "qwen3.5:9b",
  "permission_mode": "bypassPermissions",
  "stop_hook_active": false,
  "last_assistant_message": "Running the command as requested: ..."
}
```

`codex exec` also supports `--json` (JSONL events on stdout), `-o/--output-last-message`,
`-c key=value`, `--dangerously-bypass-hook-trust`, `--skip-git-repo-check`,
`--ignore-user-config`, `--ephemeral`, `-s/--sandbox` and `--output-schema`.

## What is not available (recorded honestly)

1. **No lifecycle hook reports LLM API failures.** The documented ten events are
   `SessionStart`, `SubagentStart`, `PreToolUse`, `PermissionRequest`,
   `PostToolUse`, `PreCompact`, `PostCompact`, `UserPromptSubmit`, `SubagentStop`
   and `Stop`; none carries `code` + `message` for a failed model request.
2. **PostToolUse could not be exercised on this machine.** The hosted account is
   out of quota (`ERROR: Quota exceeded. Check your plan and billing details.`),
   and the local Ollama model (`qwen3.5:9b`) exposes no tools to Codex, so the
   model never makes a tool call and the event never fires. The hook is part of
   the host contract and is wired in the sample `hooks.toml`, but no payload was
   measured.
3. **Command hooks are trust-gated**: non-managed command hooks require review
   (`/hooks`) unless `--dangerously-bypass-hook-trust` is passed; the adapter's
   wrapper does not depend on hooks.

## Field mapping (ErrorEvent v1 ← capture source)

| Capture source                                   | ErrorEvent fields                                                                                                                                                                                                                                                                                |
| ------------------------------------------------ | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| `errkb run -- codex exec …` (wrapper, channel B) | `connector_id`, `session_id`, `observed_at`, `category`, `severity`, `transient`, `headline`, `normalized_message`, `redacted_sample`, `command`, `exit_code`, `error_code`, `os`, `shell`, `runtime_versions`, `project_key`, `evidence`, `confidence`; the core adds `fingerprint`, `event_id` |
| `Stop` hook (channel C, not implemented)         | `session_id`, `observed_at`, `headline` (from `last_assistant_message` or transcript), `evidence` (transcript path)                                                                                                                                                                              |
| MCP tools (query/record path)                    | none — the tools only read or write the knowledge base                                                                                                                                                                                                                                           |

## Declared level and degradation

- **Channel B (CLI wrapper)** is the implemented capture path: `errkb run -- codex exec …`
  records the failing process output, exit code and environment summary, then asks
  the core for the known fix.
- **Query/record path is MCP** (`codex mcp add errkb -- <path>\errkb.exe mcp`): the
  host can call the eight tools (`err_lookup`, `err_search`, `err_record`,
  `err_feedback`, `err_list`, `err_stats`, `err_record_solution`, `err_set_status`).
- **Fallback C (session transcript)** is documented, not implemented: the `Stop`
  hook hands the transcript path, and the transcript is structured JSON, so a
  follow-up can parse failed tool items without scraping the UI.
- The model-facing hand-back (`notice_before_diagnosis`) is `false`: a wrapper
  cannot inject context in front of the model. Level A hook/plugin capture is
  not claimed.

## Live closure (real error → query → known fix → outcome)

The failure is real: the hosted account has no quota left.

```text
# 1. first failure: recorded, no fix yet
errkb run --connector codex-cli --session codex-live-5 --project codex-e2e -- ^
  codex exec --skip-git-repo-check -C <work> "Say hi."
  ERROR: Quota exceeded. Check your plan and billing details.
  errkb: failure recorded as issue 6a14be33-9fda-4f05-859e-2dcde5f6fa69 (d0e12d3976ad)

# 2. the known fix is recorded through the MCP tool the host uses
err_record_solution {issue_id: 6a14be33…, body: "The Codex CLI account is out of quota…",
  environment: {os: "windows", connector: "codex-cli"}, source: "codex-adapter"}
  → solution 1de32471…, version df9a5f0f…, recorded true

# 3. lookup returns the fix with trust, and the outcome is recorded
err_lookup {fingerprint: "d0e12d3976ad", project_key: "codex-e2e"}
  → exact_fingerprint, fix body, source "codex-adapter", trust 0.5
err_feedback {issue_id: …, solution_version_id: df9a5f0f…, outcome: "success"}
  → recorded true

# 4. the second failure now returns the known fix
errkb run --connector codex-cli --session codex-live-6 --project codex-e2e -- …
  errkb: failure recorded as issue 6a14be33-… (d0e12d3976ad)
  fix The Codex CLI account is out of quota. Top up the plan or switch to a model
      with quota left, then rerun the command.
  source codex-adapter  trust 0.60
  applies to os=windows, connector=codex-cli
```

Database rows behind the transcript (`errkb_live_…`):

| Table                             | Row                                                                                                            |
| --------------------------------- | -------------------------------------------------------------------------------------------------------------- |
| `issues`                          | `6a14be33-9fda-4f05-859e-2dcde5f6fa69`, fingerprint `d0e12d3976ad`, hits 2, status `resolved`                  |
| `events`                          | 2 (one per failing run)                                                                                        |
| `retrievals`                      | 3 (one per lookup)                                                                                             |
| `solutions` / `solution_versions` | `1de32471…` verified, version 1, source `codex-adapter`, applies_to `{"os":"windows","connector":"codex-cli"}` |
| `outcomes`                        | `success`, note "Fix confirmed by the adapter probe."                                                          |
| `errkb_trust()`                   | 0.6 (1 success, 0 failures)                                                                                    |

No model call and no network were involved in the knowledge-base side: the core
ran locally (Named Pipe + PostgreSQL), and the only network activity was Codex's
own failed model request, which the wrapper observed.

## Reproduce

```powershell
# 1. build the CLI and point the wrapper at a running daemon
go build -o bin\errkb.exe .\cmd\errkb
$env:ERRKB_PIPE = "\\.\pipe\errkb"

# 2. register the MCP server with Codex (any profile)
codex mcp add errkb -- "$PWD\bin\errkb.exe" mcp

# 3. capture a real failure
.\bin\errkb.exe run --connector codex-cli --session <session> --project default -- `
  codex exec --skip-git-repo-check -C <workdir> "Say hi."

# 4. optional hook probe (isolated home, keep the user config untouched)
$env:CODEX_HOME = "$env:TEMP\codex-probe\home"
# add [[hooks.Stop]] blocks, run a turn, inspect the payload the hook wrote
```

## Limits and risks

- Capture is process-level (channel B): failures that Codex handles internally
  (a retried LLM call that eventually succeeds) are invisible; only the CLI's
  final non-zero exit is recorded.
- `PostToolUse` remains unmeasured until an account with quota or a tool-capable
  local model is available; do not claim hook-level capture before that probe.
- Hooks are trust-gated and versioned with the host; treat `hooks.toml` as a
  sample, not a supported contract.
- Every capture requires a reachable daemon; `--connector` and `--session` are
  identities the wrapper names (the core creates the rows).
