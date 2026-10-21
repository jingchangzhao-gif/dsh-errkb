# Connector compatibility matrix

What each host can actually give the knowledge base, measured rather than assumed.
A row only exists for something that was run on this machine; anything else is
marked **unverified** and left out of the support claims. A window title is not
an interface contract (plan §3.2), so no row here relies on one.

Measured on **2026-10-20** with:

| Fact | Value |
| --- | --- |
| errkb | this tree: `main` `84a151d4f38c` plus the EKB-042 branch (PR #69, `dcb43ad`), built with `go1.26.4` windows/amd64, wrapper binary reports `errkb dev` |
| DSH host | 0.2.0-rc.2 (bundled desktop host) with the `dsh-errkb` plugin built from the same tree |
| Codex CLI | 0.156.1 |
| Claude Code | 2.1.282 |
| Machine | Windows 11 Pro for Workstations 10.0.26200, PostgreSQL 16.15 on `127.0.0.1:5432` |

Versions are pinned on purpose: a host upgrade can change hook names, payload
shapes or exit-code conventions, and the matrix then needs a re-measurement
rather than a guess.

## Legend

| Mark | Meaning |
| --- | --- |
| ✅ | Measured: the host or the wrapper reports this field structurally. |
| 🟡 | Derived by the core from captured text (headline, classifier, redaction) or parsed from a marker; the value exists but is not a structured host field. |
| ⬜ | Not available through this channel. |
| ❓ | Not measured yet (reported as a gap, never as support). |

Optional `ErrorEvent v1` fields (`error_code`, `command`, `exit_code`,
`tool_name`, `shell`, `runtime_versions`) are absent when the host does not
supply them; the required fields are always filled by the core pipeline.

## Hosts

| Host | Measured version | Channel | Pre-diagnosis return | Fallback | Acceptance record |
| --- | --- | --- | --- | --- | --- |
| Generic CLI (`errkb run --`) | errkb built from this tree; witness run `errkb dev` (2026-10-19) | **B** wrapper | No — the known fix is printed after the failure | 1) wrap the command; 2) tail the host log with `errkb tail --file <path>` (channel C, wired 2026-10-22), or copy one line through the wrapper; 3) manual pipe (E) | `docs/e2e-cli-loop.md`; EKB-042 live `generic-cli` issue `f1b0ec03…` |
| DSH (plugin adapter) | DSH 0.2.0-rc.2 + `dsh-errkb` 0.0.0 (this tree) | **A** plugin hooks | Yes — standing system-prompt hint plus post-execute notices; verified in the headless session log | Wrap the agent command with `errkb run --` (loses the host session and tool events), or manual E | EKB-039 plan row, PR #66 |
| Codex CLI | 0.156.1 | **B** wrapper for capture; **MCP** eight tools for query/record | No — `capability.json` records `notice_before_diagnosis: false` | Parse the `Stop` hook transcript for failed tool items (documented, not implemented); interim: scan one log line through the wrapper | `connectors/codex/probe.md` (probe 2026-10-20T14:15:07Z), PR #67 |
| Claude Code | 2.1.282 | **A** `PostToolUseFailure` hook; **B** wrapper for model-API failures; **MCP** eight tools | Yes — `SessionStart` hint script and MCP tools are available before diagnosis | Wrap `claude -p` with `errkb run --` (loses `tool_name`, keeps command/exit code/text) | `connectors/claude/probe.md` (probe 2026-10-20T15:55:00Z), PR #68 |

Template categories (EKB-042) are not hosts: `connectors/templates/cli-log`
documents level B (copy of the generic CLI row), level C (`errkb tail --file`
follows a log file and records through the daemon) and level E (a human-selected
message). C is covered by `tests/e2e/tail_capture_test.go` on the development
host (clean-VM run pending); E is **not measured live** — see the gaps below.

## Field coverage per host

Column values are the measured capability for the channel in the Hosts table.
Column B hosts (CLI, Codex) share the wrapper pipeline; DSH and Claude use the
hook/plugin path and differ in what the host itself reports.

| `ErrorEvent v1` field | Generic CLI (B) | DSH (A) | Codex (B) | Claude Code (A) |
| --- | --- | --- | --- | --- |
| `schema_version` | ✅ core | ✅ core | ✅ core | ✅ core |
| `event_id` | ✅ | ✅ | ✅ | ✅ |
| `connector_id` | ✅ argument | ✅ config | ✅ argument | ✅ connector id from the hook config |
| `session_id` | ✅ argument | ✅ host session | ✅ argument | ✅ `session_id` from the hook payload |
| `observed_at` | ✅ | ✅ | ✅ | ✅ |
| `channel` | ✅ B | ✅ A | ✅ B | ✅ A |
| `category` | ✅ `command-exit` | ✅ `tool` / `command-exit` / `llm` / `agent` | ✅ `command-exit` | ✅ `tool` (API failures go through B) |
| `severity` | 🟡 core classifier | 🟡 core classifier | 🟡 core classifier | 🟡 core classifier |
| `transient` | 🟡 core classifier | 🟡 core classifier | 🟡 core classifier | 🟡 core classifier |
| `headline` | 🟡 core | 🟡 core | 🟡 core | 🟡 core |
| `normalized_message` | 🟡 core | 🟡 core | 🟡 core | 🟡 core |
| `redacted_sample` | 🟡 core redaction | 🟡 core redaction | 🟡 core redaction | 🟡 core redaction |
| `error_code` | 🟡 extracted from the headline text | ✅ `llm` failure code; 🟡 `tool` text extraction | 🟡 extracted from the headline text | ⬜ (declared missing in `capability.json`) |
| `command` | ✅ argv | ✅ `tool` path (`exec` command); ⬜ `llm`/`agent` | ✅ argv | ✅ `tool_input.command` (measured) |
| `exit_code` | ✅ process exit code | ✅ `tool` path, parsed from the `[exit code: N]` marker; ⬜ `llm`/`agent` | ✅ process exit code | 🟡 parsed from the hook error text (measured `Exit code 2`) |
| `tool_name` | 🟡 wrapped executable name (`codex.exe`), not a host tool name | ✅ host tool name; 🟡 agent id on `agent/error` | 🟡 wrapped executable name | ✅ host tool name (`Bash`, measured) |
| `os` | ✅ wrapper summary | 🟡 core default (`runtime.GOOS` of the daemon), not host-reported | ✅ wrapper summary | ⬜ |
| `shell` | ✅ `COMSPEC`/`SHELL` basename | ⬜ | ✅ wrapper summary | ⬜ |
| `runtime_versions` | 🟡 errkb's own Go runtime only | ⬜ (adapter sends none) | 🟡 errkb's own Go runtime only | ⬜ |
| `project_key` | ✅ argument | ✅ config (defaults to `default`) | ✅ argument | ✅ derived from the hook `cwd` |
| `fingerprint` | ✅ core | ✅ core | ✅ core | ✅ core |
| `evidence` | ✅ wrapper | ✅ adapter | ✅ adapter | ✅ adapter |
| `confidence` | ✅ | ✅ | ✅ | ✅ |

Notes:

- `channel` and `category` are independent axes: the channel is the capture
  route (§3.1), the category is the capture source that the core classifier and
  fingerprint use.
- For channel B, `tool_name` and `runtime_versions` are honest about what they
  are: the wrapped executable and errkb's Go runtime, not the host's internal
  tool name or the host version.
- Claude Code's hook payload has no `error_code`, `os`, `shell` or
  `runtime_versions`; `exit_code` comes from the error text the hook receives.

## Pre-diagnosis return

"Return before diagnosis" means the host can show the known fix before the
failure is repeated:

| Host | Measured behavior |
| --- | --- |
| DSH | **Yes.** The plugin injects a standing system-prompt hint (verified in a decoded headless session log) and delivers notices on `post-execute`/`pre-step`; the model can also call `err_lookup` through the tool bridge. |
| Claude Code | **Yes.** The `SessionStart` hook print the hint, and the eight MCP tools (`mcp__errkb__err_*`) are available before any command runs; measured with a mock API endpoint driving a tool call. |
| Codex CLI | **No.** The wrapper can only show the fix after the wrapped command failed; MCP tools are available for manual/agent queries, but there is no hook that fires before a failure. |
| Generic CLI | **No.** Same as Codex: wrapper-only, the fix appears after the failure. |
| Templates C/E | **No.** C has no CLI entry point yet; E is a human-selected message. |

## Fallbacks and field loss

| When this fails | Use instead | Fields lost (compared to the primary row) |
| --- | --- | --- |
| Codex hooks/plugins unavailable | Wrap `codex exec` with `errkb run --` (the live acceptance path) | `tool_name` (wrapped executable only); the host's own retry outcome; structured host events |
| Codex needs tool-level failures | Parse the `Stop` hook transcript (documented, **not implemented**) | Current rows mark this ❓; until implemented, capture stays at the wrapper level |
| DSH plugin cannot be installed | Wrap the agent command with `errkb run --` | `session_id` (host session), `tool_name`, agent/tool event structure |
| Claude hooks unavailable | Wrap `claude -p` with `errkb run --` | `tool_name`, and the pre-diagnosis return path |
| No hook, only a log file | Take one candidate line and send it through `errkb run --` (C intent, measured as B capture of a chosen line) | `exit_code`, structured fields; weaker fingerprints because timestamps, pids and paths in the text differ |

## Gaps and their impact

| Gap | Hosts | Impact |
| --- | --- | --- |
| `shell` and `runtime_versions` missing | DSH, Claude Code | Environment detail in `retrievals.environment_diff` is thinner; fingerprint and match quality are unaffected (the fingerprint uses category + normalized text only). |
| `error_code` absent or text-derived | Claude Code (absent), CLI/Codex (text-derived), DSH `tool` (text-derived) | Exact-code matching is weaker across hosts; recall falls back to the normalized-text path. |
| `tool_name` not a host tool name | CLI, Codex | Tool-scoped rerank (`tool_name` weight 0.05) contributes less on these hosts. |
| DSH `tools/result` path not exercised live | DSH | The capture path is unit-tested, but the live acceptance used `agent/request-error`; a real tool failure in a DSH session is **unverified**. |
| Codex `PostToolUse` not measured | Codex | Hidden tool failures inside Codex are invisible to the wrapper; the Stop-transcript fallback is documented, not built (**unverified**). |
| Template C measured on the development host only | Template hosts | `errkb tail --file` follows a log file and records classified failures through the daemon (`tests/e2e/tail_capture_test.go`); no clean-VM run yet (**unverified on a clean machine**). |
| Template E not measured | Manual hosts | Manual entry is the wrapper path with a human-chosen line; no separate acceptance run exists (**unverified**). |
| Text-derived headlines in B/C | CLI, Codex, Claude wrapper | Same failure with different timestamps, pids or temp paths may not merge; the normalizer removes known noise but long-tail variance remains. This is the documented cost of channels B and C. |

## Measurement sources

| Source | Covers |
| --- | --- |
| `docs/e2e-cli-loop.md` (2026-10-19) | Generic CLI wrapper loop: failure → issue → solution via the core API → feedback; no model calls. |
| EKB-039 plan row / PR #66 | DSH 0.2.0-rc.2 headless profile: `agent/request-error` capture, session hint, `err_lookup`/`err_stats` handback, three adapter contract fixes. |
| `connectors/codex/probe.md` + `connectors/codex/capability.json` (probe 2026-10-20T14:15:07Z) | Codex CLI 0.156.1: hooks/plugins/MCP surfaces, `Stop` payload, unmeasured `PostToolUse`, wrapper closure. |
| `connectors/claude/probe.md` + `connectors/claude/capability.json` (probe 2026-10-20T15:55:00Z) | Claude Code 2.1.282: hook shell rules, measured `SessionStart`/`PostToolUseFailure`/`Stop` payloads, JSON output, MCP registration, hook closure. |
| `connectors/templates/cli-log/README.md` + live `errkb doctor` run (EKB-042) | Template levels B/C/E, `generic-cli` channel-B failure and doctor output. |
| `tests/e2e/tail_capture_test.go` (2026-10-22) | Template C: `errkb tail --file` records a permission-denied line through the daemon, the connector row is channel C, and a replay reuses the issue (`issues=1 events=2 hits=2`). |

## Unverified list

- Codex `PostToolUse` hook payload and tool-level capture (local OSS model has no
  tool calling; hosted-model quota was exhausted during the probe).
- DSH `tools/result` capture in a real session (unit tests only).
- Template C on a clean machine (the development-host e2e test exists; clean-VM run pending).
- Template E manual channel as its own acceptance run.
- Any host version other than the pinned ones in the header.

Until a row's source exists, it must stay unverified; the matrix is the brake
against "it looks supported" claims in READMEs (plan §12).
