# Claude Code capability probe (2026-10-20)

This probe records what Claude Code 2.1.282 actually offers, measured on the
development host, so the connector's declared channel level is a fact rather
than a guess. Re-run the commands below before trusting the notes on a newer
host version.

## Host and environment

| Item            | Value                                                                                           | How it was read                                                                                                                          |
| --------------- | ----------------------------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------- |
| Claude Code     | `2.1.282` (native, win32-x64)                                                                   | `claude doctor`, `claude --version`                                                                                                      |
| Executable      | `<user-home>\.local\bin\claude.exe`                                                             | `where claude` / `Get-Command claude`                                                                                                    |
| Config root     | `<user-home>\.claude\` (`settings.json`, `settings.local.json`, `projects\`)                    | directory listing                                                                                                                        |
| Model routing   | `ANTHROPIC_BASE_URL` in the user settings points at a local proxy on `127.0.0.1:15721`          | `claude doctor` reported "custom ANTHROPIC_BASE_URL"; the probe process inherited no proxy env because the proxy service was not running |
| Probe date      | 2026-10-20                                                                                      | —                                                                                                                                        |
| Probe workspace | temporary profile copies under `%TEMP%\claude-probe`; the user's own settings were never edited | —                                                                                                                                        |

Observed consequence of the proxy being down: every hosted-model turn ends with
`API Error: Connection refused — a firewall or proxy may be blocking it
(ECONNREFUSED)` and exit code 1. That failure was used to prove the wrapper
path, but the core classified it transient (EKB-016) and deliberately did not
record it; the recorded channel-B closure used a non-transient CLI failure
(unknown flag) instead. It is also why no live hosted-model turn could be made.

## Structured output

`claude -p "<prompt>" --output-format json` prints one JSON object. Measured
fields on an API failure (2026-10-20):

```json
{
  "type": "result",
  "subtype": "success",
  "is_error": true,
  "result": "API Error: Connection refused — a firewall or proxy may be blocking it (ECONNREFUSED)",
  "session_id": "...",
  "uuid": "...",
  "duration_ms": 177315,
  "num_turns": 1,
  "terminal_reason": "api_error",
  "stop_reason": "stop_sequence",
  "modelUsage": {},
  "usage": { "input_tokens": 0, "output_tokens": 0 },
  "permission_denials": [],
  "api_error_status": null
}
```

`claude -p --output-format stream-json --verbose --include-hook-events` emits
one JSON object per line. Measured line types: `system` (`subtype` `init`,
`hook_started`, `hook_response`, `api_retry`), `assistant`, `user` (tool
results), `result`. The `init` line carries `session_id`, `cwd`, `tools`,
`mcp_servers`, `model`, `permissionMode`, `claude_code_version`, `plugins` and
`slash_commands`; it is emitted before the first model call, so a capture hook
can use it even when the API is down. The `assistant` line for an API failure
carries `"is_api_error_message": true` and the error text.

## Lifecycle hooks

Hook configuration lives in settings files (`~/.claude/settings.json`, project
`.claude/settings.json`, `--settings <file>`), under `hooks.<Event>[].hooks[]`
entries of `{type: "command", command, timeout}`. Hooks ran in this probe with
`--settings` and `--permission-mode bypassPermissions`.

Measured events (each payload captured verbatim under
`%TEMP%\claude-probe\events\`, two independent runs):

| Event                | Fired                      | Payload fields (measured)                                                                                                                                                                                  |
| -------------------- | -------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `SessionStart`       | yes                        | `session_id`, `transcript_path`, `cwd`, `hook_event_name`, `source` (`"startup"`)                                                                                                                          |
| `PreToolUse`         | yes                        | `session_id`, `transcript_path`, `cwd`, `prompt_id`, `permission_mode`, `effort:{level}`, `hook_event_name`, `tool_name`, `tool_input`, `tool_use_id`; MCP tools add `mcp_server:{name, source:"dynamic"}` |
| `PostToolUse`        | yes, successful tools only | the PreToolUse fields plus `tool_response:{stdout, stderr, interrupted, isImage, noOutputExpected}`, `duration_ms`                                                                                         |
| `PostToolUseFailure` | yes, failed tools          | the PreToolUse fields plus `error:"Exit code 2\nls: cannot access …: No such file or directory"`, `is_interrupt`, `duration_ms`                                                                            |
| `Stop`               | yes, at turn end           | `session_id`, `transcript_path`, `cwd`, `prompt_id`, `permission_mode`, `effort`, `stop_hook_active`, `last_assistant_message`, `background_tasks`, `session_crons`                                        |

Measured example (failing `Bash` call, `PostToolUseFailure`):

```json
{
  "session_id": "f0e64bca-…",
  "transcript_path": "C:\\Users\\Administrator\\.claude\\projects\\…\\f0e64bca-….jsonl",
  "cwd": "D:\\miaomiao tool\\dsh-errkb-main",
  "prompt_id": "650807ce-…",
  "permission_mode": "bypassPermissions",
  "effort": { "level": "high" },
  "hook_event_name": "PostToolUseFailure",
  "tool_name": "Bash",
  "tool_input": { "command": "ls C:/errkb-probe-missing.txt" },
  "tool_use_id": "toolu_probe_1",
  "error": "Exit code 2\nls: cannot access 'C:/errkb-probe-missing.txt': No such file or directory",
  "is_interrupt": false,
  "duration_ms": 1564
}
```

Facts that matter for the adapter:

- A failing tool produces **only** `PostToolUseFailure`; `PostToolUse` does not
  fire for the same call. Both carry the tool name, the input and the error
  text, which is enough to build an `ErrorEvent` without scraping UI or logs.
- No hook event exists for the model API failure itself (`Stop` did not fire on
  the API-error runs), so the LLM-failure path needs the CLI wrapper channel.
- Hook commands run through a POSIX-style shell here: Windows paths must use
  forward slashes (`C:/Users/...`), a doubled backslash path silently loses its
  separators and the command fails with exit code 127.
- Hooks are only observable with `--include-hook-events` (`--output-format
stream-json`); without it hooks still run, they just do not appear in the
  stream.

## MCP

- `claude mcp add --scope user --transport stdio errkb -- <path>\errkb.exe mcp`
  registers the server; `claude mcp list` health-checks it and printed
  `errkb: … mcp - √ Connected` (2026-10-20).
- `claude mcp get errkb` reports scope, type `stdio`, command, args, environment;
  `claude mcp remove errkb -s user` removes it.
- `--mcp-config <file> --strict-mcp-config` loads servers from a file for one
  run without touching user or project config. A tool call
  `mcp__errkb__err_stats` was delivered into a real headless session (the
  tool result was an error because no daemon was listening, which proves the
  transport).
- MCP tool names in Claude Code are `mcp__<server>__<tool>`; the six errkb
  tools therefore appear as `mcp__errkb__err_lookup` and so on.

## Capability matrix

| Capability                        | Available                                     | Evidence                                                                                                      |
| --------------------------------- | --------------------------------------------- | ------------------------------------------------------------------------------------------------------------- |
| Structured tool-failure event     | yes                                           | `PostToolUseFailure` payload above                                                                            |
| Tool input and command            | yes                                           | `tool_input.command` on the payload                                                                           |
| Command exit code                 | partial                                       | only the number inside `error` (`Exit code 2`); parsed by the connector                                       |
| Structured output / JSON events   | yes                                           | `--output-format json` / `stream-json`                                                                        |
| MCP retrieval and recording       | yes                                           | `mcp add`, health check, live tool call                                                                       |
| Session identity                  | yes                                           | `session_id` on every hook payload                                                                            |
| LLM API failure event             | **no**                                        | no hook fired; channel B wrapper catches CLI exits, transient API errors are skipped by core policy (EKB-016) |
| Pre-diagnosis hand-back in prompt | yes, via MCP tools and `SessionStart` context | MCP tool call measured; `SessionStart` payload measured                                                       |
| UI scraping needed                | no                                            | not used                                                                                                      |

## Chosen channel

- **Capture:** channel **A** through the `PostToolUseFailure` hook for tool
  failures, plus channel **B** (`errkb run -- claude -p …`) for model/API
  failures and for users who prefer no hook configuration.
- **Query and hand-back:** MCP stdio tools (`err_lookup`, `err_record_solution`,
  `err_feedback`), plus a `SessionStart` hook that can add the errkb hint to
  the context.
- Degradation: without hooks the connector is channel B; without MCP it can
  still capture but cannot hand a solution back inside the prompt.

Limits recorded honestly: the hosted model path could not be exercised because
the local proxy was down. Hook payloads for model turns were driven by a local
mock Anthropic endpoint (the tool call and the failing `ls` are real; only the
model answer is synthetic). The wrapper captured a real, non-transient CLI
failure (exit code 1, unknown flag) on channel B; a first `ECONNREFUSED` run
was classified transient by the core and deliberately not recorded (EKB-016
policy), which is correct behaviour and not a capture gap. Re-run this probe on
a host with a working model endpoint before treating the matrix as long-term.

## Closure record (2026-10-20)

Run against the core built from this workspace, a throwaway database
(`errkb_claude_*`) and a real `claude` 2.1.282 binary.

| Step                         | Evidence                                                                                                                                                                                                                                                        |
| ---------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Capture (hook, channel A)    | `PostToolUseFailure` for `ls C:/errkb-probe-missing.txt` -> issue `28c2ad66-c8cc-4122-a31b-98e472302e75`, fingerprint `96d3ee6f967a`, headline `ls: cannot access '<path>/errkb-probe-missing.txt': No such file or directory`, `tool_name=Bash`, `exit_code=2` |
| Query (MCP)                  | `err_lookup` returned the issue with `match_reason=jaccard_rerank`, `approximate=true` and, after the solution existed, its body, source and trust `0.5`                                                                                                        |
| Solution (MCP)               | `err_record_solution` -> solution `01049257-01e3-427c-84cc-f11a2ffcff79`, version `ef7ba96b-8cd5-4e09-b921-ad47c3e397f4`                                                                                                                                        |
| Outcome (MCP)                | `err_feedback --outcome success` -> outcome `ef756a0b-8603-41bb-8043-c4779838f012`; `errkb_trust` became `0.60`                                                                                                                                                 |
| Idempotent replay            | feeding the same hook payload again reported `created=False` for the same issue (one event, one occurrence)                                                                                                                                                     |
| Capture (wrapper, channel B) | `errkb run -- … claude … --definitely-bad-flag` -> issue `1e7c32f3-3ec3-43e6-9a73-f96229033abf`, fingerprint `af58f82e2e21`, `category=command-exit`, `channel=B`                                                                                               |
| Hand-back                    | after recording solution `77f76db9-c642-48de-bfe2-f977763b9f38` and a success outcome, the next wrapped run printed `fix The Claude Code CLI rejected an unknown flag…`, `source claude trust 0.60`, `version 77f76db9…`                                        |

Database totals at the end: issues 2, events 4 (channels A and B), occurrences 4,
solutions 2, solution_versions 2, outcomes 2, retrievals 5, both issues
`resolved`, both solutions at trust `0.60`.
