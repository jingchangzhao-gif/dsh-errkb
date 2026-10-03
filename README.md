# dsh-errkb

![Status](https://img.shields.io/badge/status-P5%20complete-yellow)
![License](https://img.shields.io/github/license/jingchangzhao-gif/dsh-errkb)
![DeepSeek Harness](https://img.shields.io/badge/DeepSeek%20Harness-plugin-blue)

[简体中文](README.zh-CN.md)

> Recycles errors: the same failure is diagnosed once, recorded, and handed back as a known fix the next time it appears.

`dsh-errkb` is a [DeepSeek Harness](https://github.com/deepseek-ai/deepseek-harness) (`dsh`) plugin that turns the errors a model hits into a numbered, human-editable knowledge base — then pushes the recorded fix back into context *before* the model starts diagnosing.

> **Status: P5 complete.** The design document is written and under review —
> six of the eight decisions in its §17 are still open (Q2 and Q4 are decided) —
> and the work is broken into [eighteen tasks](#roadmap) across seven milestones.
> **T01–T15 are done:** the package installs, builds, type checks, lints, formats
> and tests (with the 99% coverage gate enforced), CI runs all five on every
> push to `main` and every pull request, and the pure local layer is complete:
> knowledge-base directory resolution (T05), error normalization and fingerprinting (T06), mandatory
> redaction (T07), the `ERRORS.md` store (T08) and matching (T09). The capture
> layer is wired: classification and the noise rule (T10), and the first two
> hooks, `agent/error` and `tools/result` (T11). The injection layer speaks:
> notice text, caps and fix trust (T12) are wired to the four injection points
> (T13), and resolution detection (T14) closes the loop. The five agent tools
> (T15) let the model look errors up and write fixes down.
>
> **The plugin records and injects, once installed.** Loaded into a profile,
> `apply` logs where the knowledge base is, records tool failures, non-zero
> command exits and turn-level errors into `ERRORS.md`, and hands a recorded
> fix back to the model when the same error repeats, within the caps below.
> When a recorded error stops failing, its fix earns trust, and an entry with
> no fix gets one prompt asking the model to record it with `err_record` (T14,
> T15), which writes it into `ERRORS.md`. Still missing: LLM request-failure
> capture (T16), and installation into a profile (T17) — nothing installs it
> for you yet, so the tools only reach a model once you add the plugin to a
> profile yourself. Anything below tied to T16–T18 is still a description of
> the intended path.
>
> - Design document: [`docs/设计说明书.md`](docs/设计说明书.md) — 19 sections, Chinese
> - What the plugin will do: [How it works](#how-it-works)
> - Where your data will land: [Where the knowledge base lives](#where-the-knowledge-base-lives)
> - What is still open: [Open decisions](#open-decisions) — Q2 and Q4 of §17 are decided, six remain

## Contents

- [What works today](#what-works-today)
- [Why](#why)
- [Features](#features)
- [How it works](#how-it-works)
- [Where the knowledge base lives](#where-the-knowledge-base-lives)
- [The ERRORS.md format](#the-errorsmd-format)
- [Injection: when it speaks and how much](#injection-when-it-speaks-and-how-much)
- [Cost model](#cost-model)
- [Requirements and compatibility](#requirements-and-compatibility)
- [Installation](#installation)
- [Usage](#usage)
- [Configuration](#configuration)
- [Privacy and redaction](#privacy-and-redaction)
- [Failure modes and safety](#failure-modes-and-safety)
- [Known limitations](#known-limitations)
- [Verifying it works](#verifying-it-works)
- [Roadmap](#roadmap)
- [Development](#development)
- [Troubleshooting](#troubleshooting)
- [Uninstall](#uninstall)
- [Open decisions](#open-decisions)
- [Future work](#future-work)
- [Related projects](#related-projects)
- [Contributing](#contributing)
- [License](#license)

## What works today

| Piece                                                                     | State                      |
| ------------------------------------------------------------------------- | -------------------------- |
| `docs/设计说明书.md` — the design document (19 sections)                  | ✅ Written, pending review |
| `README.md`, `README.zh-CN.md`                                            | ✅ In place                |
| `LICENSE`                                                                 | ✅ In place                |
| `.github/` — CI, privacy guard and PR template                            | ✅ In place                |
| `package.json`, `tsconfig.json`, `tsdown.config.ts`, `vitest.config.ts`   | ✅ In place (T01–T03)      |
| `.prettierignore`, `.gitattributes` — format and line-ending policy       | ✅ In place                |
| `cordis.patch.yml` — bundle patch                                         | ✅ In place (T04)          |
| `src/index.ts` — plugin entry (`name`, `inject`, `Config`, `apply`)       | ✅ In place (T04); `apply` reads the settings, registers the `agent/error` and `tools/result` listeners (T11), the four injection points (T13) and the five tools (T15) |
| `src/plugin.ts` — the capture pipeline and the injection wiring           | ✅ In place (T11, T13–T15), 100% statements and lines — T15 adds `write()` and the notice counters `err_stats` reads |
| `src/paths.ts` — KB directory resolution                                  | ✅ In place (T05), 100% covered |
| `src/signature.ts` — normalization and fingerprinting                     | ✅ In place (T06), 100% covered — used by capture and matching |
| `src/redact.ts`, `src/redact-patterns.ts` — mandatory redaction           | ✅ In place (T07), 100% covered — applied by the store on every write |
| `src/store.ts` — parse, render, append, archive                           | ✅ In place (T08), 100% statements and lines — written by the T11 listeners and the T15 tools; `archive(id, reason)` added for `err_forget` |
| `seeds/ERRORS.seed.md` — three curated, redacted seed entries             | ✅ In place (T08) — not copied into any knowledge base yet |
| `src/match.ts` — exact, fuzzy and fallback matching                       | ✅ In place (T09), 100% statements and lines — consulted before every write |
| `src/state.ts` — `state.json`, `.machine.json`, environment fingerprint   | Hit counters and fix-trust counters in `state.json`: ✅ In place (§4.3, T12), 100% statements and lines. `.machine.json` and the environment fingerprint: ⛔ Not started |
| `src/capture.ts` — classification, headline extraction and the noise rule | ✅ In place (T10), 100% statements and lines — called by the T11 listeners |
| `src/inject.ts` — notice generation, hard caps and fix trust              | ✅ In place (T12), 100% statements and lines — wired to the four injection points (T13) |
| `src/resolve-detect.ts` — resolution detection                            | ✅ In place (T14), 100% statements and lines — fed by `tools/result`; `recordFix()` is called by `err_record` (T15) |
| `src/tools.ts` — the five agent tools                                     | ✅ In place (T15), 100% statements and lines — `err_lookup`, `err_record`, `err_list`, `err_forget`, `err_stats`, registered through `ctx.tools.register()` |
| `tests/`                                                                  | ✅ 554 cases (one skipped off Windows): `paths` 39 (T05), `signature` 30 (T06), `redact` 52 (T07), `store` 62 and `seeds` 5 (T08, T15), `match` 40 (T09), `capture` 67 (T10), `plugin` 44 (T11, T14), `inject` 70 (T12–T15), `injection` 68 (T13, T14), `resolve-detect` 19 (T14), `tools` 40 (T15), `state` 18 (§4.3) |
| Installed into the `web` profile                                          | ⛔ Not started             |
| Published to npm                                                          | ⛔ Not started — no task covers it yet, see [Roadmap](#roadmap) |

## Why

An agent has no memory of the last time it hit an error, so it re-derives the diagnosis from scratch: reasoning, a failed attempt, another failed attempt. That costs somewhere between 800 and 3000 tokens.

For a failure you see once, that is fine. For a failure you see every week — an `EPERM` from a locked `node_modules`, a `CONTEXT_OVERFLOW`, a misspelled provider name — it is pure waste. A human recognizes "this is the one from last time" instantly. The model does not, because the path changed, the line number changed, and the PID is different.

`dsh-errkb` closes that gap by keeping the knowledge on disk and injecting what it already knows.

## Features

> **Designed, mostly implemented, not installed.** Everything below is
> specified in the design document and covered by a test plan. Recording and
> injection run: stable IDs, one ID per error and mandatory redaction apply to
> what the T11 listeners write, a recorded fix is injected before diagnosis
> (T13), a resolved error is noticed and asked for its fix once (T14), and the
> five tools write the answer down and keep the ledger (T15). Nothing installs
> the plugin into a profile until T17 — see
> [What works today](#what-works-today).

| Behaviour                     | Detail                                                                          |
| ----------------------------- | ------------------------------------------------------------------------------- |
| **Stable IDs**                | Every distinct error gets one, `E-0001` onward, and keeps it forever            |
| **Same error, one ID**        | Changed paths, line numbers, PIDs and timestamps still count as the same error  |
| **Injected before diagnosis** | On a hit the recorded fix enters context — about 80 tokens                      |
| **Human-editable**            | `ERRORS.md` is plain Markdown; a fix you type by hand is used immediately       |
| **Mandatory redaction**       | Secrets, emails and absolute paths are scrubbed before anything is stored       |
| **Cross-device by git**       | Append-only blocks merge cleanly; counters stay local and never conflict        |
| **An auditable ledger**       | `err_stats` reports hits, injections and estimated tokens saved                 |
| **Compaction-friendly**       | Facts live on disk and are injected on demand, not held in conversation history |
| **Fully local**               | Matching, storage and lookup call no model and touch no network                 |
| **Bounded by construction**   | Every injection is capped, and the caps are part of the specification           |

## How it works

```
1. CAPTURE      agent/error · tools/result · non-zero exits
                (agent/request-error: not yet wired, T16)
                observe only — never throws, never takes over retries
                                     │
                                     ▼
2. FINGERPRINT  normalize: strip ANSI; ts / pid / port / line / uuid / tmp → placeholders;
                absolute paths → <path>, filename kept
                signature = sha256(category + "\0" + message)[0:12]
                                     │
                                     ▼
3. MATCH        exact signature → Jaccard fuzzy (≥ 0.72) → short-message
                same-code fallback → otherwise assign a new ID
                                     │
                       ┌─────────────┴─────────────┐
                    hit│                           │miss
                       ▼                           ▼
4. INJECT     ≤ 120 tokens/notice             5. RECORD   append block to ERRORS.md
   ≤ 1/step, 3/turn, 2/ID/session                         assign next ID (append-only)
                       ▲                           ▲
                       └─────────────┬─────────────┘
                                     │
6. RESOLVE      model or user writes the fix via `err_record` (the sole entry point)
                resolution detection → one-shot prompt → status becomes `fixed`
```

The hard part is step 3, not step 2. Normalizing a message is mechanical; deciding that two different-looking messages *are* the same error is where a knowledge base either helps or quietly poisons the context with the wrong fix. Matching runs in three tiers — exact signature, then fuzzy token similarity, then a same-code fallback for messages too short to compare — and a near miss is labelled as one in the injected text rather than passed off as certain.

**One rule holds the whole thing together:** `ERRORS.md` is the single source of truth. `errors.index.json` is a derived cache that can be deleted and rebuilt; `state.json` holds local counters that can be lost without consequence. None of the three ever writes another's facts.

### What earns an ID

Not every failure deserves a number. Transient ones are counted and only promoted when they keep happening, because a rate limit is not knowledge.

| Source                               | Hook                                 | Captured as                                                    | Gets an ID?                                                                        |
| ------------------------------------ | ------------------------------------ | -------------------------------------------------------------- | ---------------------------------------------------------------------------------- |
| LLM API failure                      | `agent/request-error` (waterfall)    | `code` + normalized message                                    | Permanent codes: `AUTH`, `QUOTA`, `INVALID_REQUEST`, `CONTEXT_OVERFLOW`, `NO_ADAPTER`, `UNKNOWN` → immediately — **not yet wired (T16)** |
| Turn-level exception                 | `agent/error` (emit)                 | `error.message` / `code` / `name`                              | Immediately (safely stringified when `unknown`)                                     |
| Tool failure                         | `tools/result` (emit)                | `exec.name` + `result.error.message` + `result.error.info.code` | Immediately (`result.isError`)                                                      |
| Command exited non-zero              | `tools/result` content sniffing      | matches `\[exit code: (\d+)\]` with N ≠ 0                      | Immediately, unless `captureExitCodes` is off                                       |
| Transient LLM failure                | `agent/request-error`                | `RATE_LIMIT`, `SERVER`, `TIMEOUT`, `TRANSPORT`, `EMPTY_RESPONSE` | ❌ counted only — promoted after `transientThreshold` (default 5) in one session — **not yet wired (T16)** |

When it lands (T16), the listener on `agent/request-error` must `await next()` and return the downstream result unchanged. It observes; it never recovers.

**What is wired today (T11).** `src/plugin.ts` holds the listener bodies; `apply` registers two of them. `agent/error` hands its `error` to classification as a turn-level exception. `tools/result` records a failed result (`result.isError`) as a tool failure with `result.error.message` and `result.error.info?.code`; any other result has its text blocks sniffed for `[exit code: N]`, with the command taken from the call's `command`, `cmd` or `script` argument. Each classified error is matched against the entries already in `ERRORS.md`: a hit bumps that entry's hits and last-seen in this machine's `state.json` and leaves `ERRORS.md` untouched (§4.3), a miss appends the next ID. A `count-only` error (a transient one below the threshold, or one after its promotion) only bumps an entry that already has its signature, the same way, and writes nothing otherwise. Transient counts are kept per session, by `Agent.id`; a payload with no agent counts under one plugin-wide counter. `agent/request-error` is not registered yet (T16), so no LLM failure reaches the pipeline.

**Promotion happens once.** A transient error's count is kept per session and per signature, and it takes an ID on the occurrence that *reaches* `transientThreshold` — not on every occurrence after it. With a source left out of `capture`, nothing is produced for it at all, not even a count.

**One line decides the signature (decided, [`docs/discussions.md`](docs/discussions.md) §2a).** Multi-line output — `tsc` with forty errors, a `pnpm install` log, a Python traceback — is reduced to one headline before fingerprinting, so a forty-first error or a reordered log keeps the same ID. `src/capture.ts` picks:

1. for a Python traceback (`Traceback (most recent call last):`), the last non-empty line — checked first, because a traceback quotes source lines such as `raise ValueError(...)`;
2. otherwise the first line matching `ERR_[A-Z0-9_]+|E[A-Z]{2,}|[A-Z]\w*Error|error TS\d+` (log-level words such as `ERR` and `ERROR` do not count);
3. otherwise the last non-empty line.

The headline is capped at 200 characters, the code it names (`ERR_PNPM_…`, `EPERM`, `TS2307`, `ModuleNotFoundError`) becomes the entry's `code` when the source gives none, and the full text is kept as the raw sample. For a command, the harness's own `[exit code: N]` marker is left out of the headline. When a command's headline names no code — a silent failure reads `exit code 1`, a test runner's last line reads `1 test failed` — the command that ran leads it (`pnpm test → exit code 1`, its first line, cut to 120 characters), so silent failures of two different commands never share an ID.

## Where the knowledge base lives

`kbDir` resolves in three tiers, in order:

| # | Condition                                                                             | Resolution                                                 |
| - | ------------------------------------------------------------------------------------- | ---------------------------------------------------------- |
| 1 | `kbDir` is set                                                                        | Relative → against the **plugin package root**; absolute → as-is. Device-local differences belong here |
| 2 | `kbDir` is empty, the plugin root is writable and **not inside `node_modules`**       | `<plugin root>/errors/`                                    |
| 3 | Otherwise — global npm install, read-only package directory, unwritable disk          | `$DSH_HOME/errkb/`                                         |

At startup the plugin logs the resolved path on one line, so the location is never a guess. `err_stats` prints it too.

**Real entries belong in a private repository** (§17 Q4). Redaction by text rules cannot be complete, so this public repository ignores `errors/` entirely and only carries curated, redacted seed entries in [`seeds/ERRORS.seed.md`](seeds/ERRORS.seed.md). To sync your knowledge base across devices, clone a private repository of your own on each machine and point `kbDir` at it — an absolute path, or a path relative to the plugin root. The `link:` development install would otherwise land in `errors/` inside this repository, where git now ignores it. Either way, **no absolute path is ever written into the document**.

Inside that directory:

| File                 | Role                                                                              | Treat it as                                        |
| -------------------- | --------------------------------------------------------------------------------- | -------------------------------------------------- |
| `ERRORS.md`          | **The single source of truth** — numbered entries, append-only                     | Hand-editable; commit it to your private KB repository |
| `ERRORS.archive.md`  | Entries archived past `maxEntries`, append-only                                    | Commit it to the same private repository           |
| `errors.index.json`  | Derived cache: signature → ID                                                      | Deletable, rebuilt from `ERRORS.md`; gitignore it  |
| `state.json`         | Machine-local state: each entry's hits and `lastSeen` on this machine, and fix-trust counters (the environment fingerprint is not built yet) | Deletable; **machine-local, never commit it**      |
| `.machine.json`      | Device slug (`deviceSlug`, e.g. `DESKTOP-A`) used for the entry's device field     | Deletable/regenerated; device-local, keep it out of git |
| `.lock`              | Transient write lock, removed when the write finishes                              | Ignore it                                          |

A sensible `.gitignore` for that private knowledge base repository:

```gitignore
state.json
errors.index.json
.machine.json
.lock
```

The design document names `state.json` and `errors.index.json` explicitly; `.machine.json` and `.lock` are device-local or transient for the same reason.

That split is the whole cross-device story: knowledge merges by appending to `ERRORS.md`, counters stay on the machine that produced them, and the two never fight. See [Roadmap](#roadmap) for what is not built.

## The ERRORS.md format

IDs start at `E-0001`, built from `idPrefix` (default `E-`) and `idWidth` (default `4`), and are **append-only — an ID is never changed once written**, not even to resolve a merge conflict.

One entry, in the default English labels, with machine fields carried in an HTML comment so the rendered document stays clean and hand-editing stays safe:

````md
## E-0007 · [tool:pwsh] EPERM: operation not permitted, rename
<!-- errkb: sig=3f2a1c9d0b71 cat=tool code=EPERM first=2026-09-14T09:12:33Z -->

- Fingerprint: `3f2a1c9d0b71`
- Category: `tool / pwsh`
- First seen: 2026-09-14 09:12 · Last seen: 2026-09-14 15:40 · Hits: 5
- Trigger: `pnpm install` writing `node_modules` under a non-ASCII path while another process holds it
- Raw message:
  ```text
  EPERM: operation not permitted, rename '<path>\node_modules\.pnpm\<hash>'
  ```
- Fix:
  Close the editor or real-time antivirus scan holding the directory, then re-run `pnpm install`; if it persists, use `pnpm install --config.node-linker=hoisted`.
- Status: `fixed`
- Notes:
````

**Labels:** English by default. Set `labels: 'zh'` to write the Chinese labels of the design document's §8 instead (`指纹`, `分类`, `首次`, `最近`, `命中`, `触发`, `原始信息`, `解法`, `状态`, `备注`). The parser reads both sets — and a full-width `：` — whatever the setting, so a document may mix them, and an updated entry keeps the language it was written in. Machine keys in the comment are always English.

**Hits and Last seen:** the values written in a block are the ones it was created with (`Hits: 1`, the time of capture) or whatever a person typed there since. A repeat never rewrites the block: it is counted in this machine's `state.json`, keyed by ID, so two devices that both hit the same error never touch the same lines and their git histories merge cleanly (§4.3). Every count the plugin shows — notices, the session digest, `err_lookup`, `err_list`, `err_stats` — is the block's hits plus this machine's, with the later of the two last-seen times, so the numbers may differ between devices while the knowledge agrees. Deleting `state.json` drops the counts back to what the blocks say. Editing a fix, status or notes (by hand or through `err_record`) still rewrites that one block.

**Parsing rules:** blocks split on `^## (E-\d+) ·`; the `<!-- errkb: ... -->` comment supplies the machine fields; `- Fix:` (or `- 解法:`) runs until the next line that opens a known field, so a fix may hold blank lines, bullets and code. **Your hand edits win over the index** — the index is only a cache, so a fix you type in any Markdown editor is used on the very next hit. Every block keeps its exact text: reading and writing a document back is byte-identical, and an update rewrites only the entry it changes.

**Strict where it matters:** a git conflict marker, a malformed entry header, a missing machine comment, a duplicate ID, an unknown status or an unterminated code fence makes the document unparseable. It is then saved aside once as `ERRORS.corrupt-<timestamp>.md`, new entries are only appended, and updates are refused until it is repaired.

**Entry status:** `open`, `fixed`, or `wontfix`. Setting `wontfix` (or flagging an entry as a misjudgment) takes it out of automatic injection permanently while it keeps counting, so one bad record cannot keep poisoning the context.

> **Field language is decided (§17 Q2):** English labels by default, `labels: 'zh'` for Chinese, both always parsed. The `device` and `proj` machine fields in the design document's sample are left out here; they arrive with the capture layer, and whether public mode hashes or drops them is still open (`docs/discussions.md` §5).

## Injection: when it speaks and how much

| Situation                                  | Injection point                                                       | What arrives                                                                 |
| ------------------------------------------ | --------------------------------------------------------------------- | ---------------------------------------------------------------------------- |
| A tool or command failed in this step      | `tools/post-execute` → `additionalContexts`                           | Hit: the known fix. Miss under `inject: 'always'`: `recorded as E-0011 (no fix yet).`, on the next step (see below) |
| The turn died (an LLM or turn failure reported as `agent/error`) | The next `agent/pre-step` → `{kind: 'enter', messages}` | The notice for the entry that matches the failed step, appended after the step's own messages |
| Session opens                              | `agent/session-start` → `agent.inject()`                              | Whatever `sessionDigest` says: `off`, `counts` (one line), or `index` (up to 10 entry titles) |
| Always                                     | `ctx.systemPrompt.section({name: 'plugin:errkb', order: 10400})`      | ~50 tokens of standing behaviour: try a known fix before re-diagnosing, state new fixes |

On a hit the model does not receive a report — it receives an instruction, capped at 120 tokens:

```
[errkb] E-0007 known (5 hits) | cause: node_modules locked by an editor during
pnpm install | fix: close the locking process and re-run; if it persists, use
pnpm install --config.node-linker=hoisted. Known fix: try this first, before
re-diagnosing or researching.
```

(Wrapped here for reading; the real notice is one line.) The closing sentence orders the work instead of forbidding any, so it does not contradict a research nudge from another plugin on the same step ([`docs/discussions.md`](docs/discussions.md) §1.2). The other wordings, exactly as `src/inject.ts` (T12) writes them:

| Case                                   | Notice                                                                                     |
| -------------------------------------- | ------------------------------------------------------------------------------------------ |
| Near hit (fuzzy or code fallback)      | `… \| fix: … Approximate match, verify first.`                                            |
| The fix recurred after a notice        | `… \| fix: … This fix failed here last time; verify before applying.`                     |
| Hit on an entry with no fix            | `[errkb] E-0007 seen before (5 hits), no fix recorded yet.` — short, about 15 tokens        |
| Miss, with `inject: 'always'`          | `[errkb] recorded as E-0011 (no fix yet).` — silent under the default `hit-only`           |
| An entry with no fix looks resolved    | `[errkb] E-0011 looks resolved. Record the fix with err_record in one sentence so it can be reused.` — once per entry per session, on the next step (T14) |
| `wontfix` or misjudged entry           | Nothing, ever                                                                              |

**Hard caps, and the caps are the point.** One notice per step, three per turn, two per ID per session; each notice ≤ 120 tokens by a conservative estimate (every non-ASCII character one token, ASCII three characters a token) and ≤ 400 characters. A long cause gives way first, then the fix; the closing instruction is never cut. The plugin-source `summary` goes through dsh-llm's own `boundContextSummary`, so it is ≤ 120 characters, for the `{kind: 'plugin', plugin: 'err-kb', form: 'notice', summary}` message shape (`MessageSourceMap['plugin']` in `@deepseek-ai/dsh-llm`). An entry whose status is `fixed` announces itself once and then goes quiet. An uncapped version of this plugin would be worse than no plugin: a hit rate bought with constant noise turns a saving into a cost.

**A fix that does not work stops being pushed** ([`docs/discussions.md`](docs/discussions.md) §4). When the same entry is captured again later in the turn that injected its fix, that counts as a recurrence: after one, the notice changes to "This fix failed here last time; verify before applying."; after two with no recorded success, the entry is not injected on this machine any more. Editing the entry's fix starts the count again. The counters are machine-local and never touch `ERRORS.md`: they live in `state.json` under `trust`, so a suppressed fix stays suppressed after a restart, on this machine only. Deleting `state.json` resets them.

Every cap is configurable downwards, and `inject: 'off'` stops every notice while capture keeps recording. The session digest and the standing section have their own switches, `sessionDigest: 'off'` and `systemPromptHint: false`.

**How the four points are wired (T13).** The hook types are read off the installed packages: `tools/post-execute` (`PostToolDecision.additionalContexts`, `@deepseek-ai/dsh-tools`), `agent/pre-step` (`PreStepDecision`, `{kind: 'enter', messages}`), `agent/session-start` and `Agent.inject()` (`@deepseek-ai/dsh-agent`), and `SystemPrompt.section()` (`PromptSection`, `@deepseek-ai/dsh-system-prompt`). Both waterfalls `await next()` and return the downstream result with one message appended; with nothing to add, or when anything of ours fails, the downstream object itself comes back.

- **The hot path reads, the write stays off the turn.** A tool failure is classified and matched inside `tools/post-execute` against the cached index; the write still happens afterwards from `tools/result`. The read waits behind writes already queued, so an error recorded in the previous step is there to hit.
- **A miss's ID is announced only once it exists.** Inside the hook the new ID is not assigned yet, so under `inject: 'always'` the miss notice is not guessed or worded as "pending": it rides the next `agent/pre-step`, once the write has named the ID, and says nothing if the write fails.
- **A dead turn's notice rides the next step.** A turn error is looked up as it is captured and appended to the next step's entry messages. LLM request failures reach this path only when they end the turn as an `agent/error`; `agent/request-error` itself is T16.
- **Per-session budgets.** One cap tracker per session (`Agent.id`), for the 64 most recently active sessions. A new `turn` number at `agent/pre-step` starts a turn, every pre-step starts a step, and `agent/session-start` (including `clear` and `compact`) starts the session's budgets afresh. A tool call without an agent is never injected.
- **The digest** under `counts` is one line, `[errkb] 37 known errors; known fixes are shown when an error repeats.`; `index` adds up to 10 of the most-hit entries, `wontfix` and misjudged ones left out. An empty knowledge base gets no digest.
- **Resolution detection (T14, `src/resolve-detect.ts`).** Once a tool call in a session is recorded against an entry — a new entry or a hit, injected or not — the entry is watched under a key: the command line for a non-zero exit, the tool name for a tool failure. A later successful call on the same key, in the same session, within the **window** — the rest of that turn plus the whole next turn — resolves it. A recurrence of the entry restarts its watch from the recurrence, under the key that failed last, so a success that only follows an older occurrence resolves nothing; a new session lifecycle (`clear`, `compact`) forgets every watch. A resolved entry's fix counts a success in fix trust, which lifts a suppression. An entry with no fix, under `captureFix: 'prompt-once'` (the default) and any `inject` but `off`, gets the one-shot prompt above on the next step: it spends the step and turn budgets like any notice but has its own per-ID budget, waits for a later step if a cap refuses it, and is dropped unsaid once its window closes. It is never asked twice in a session, and `wontfix` or misjudged entries are never asked. The model's answer is not parsed out of its free text: `err_record` (T15) is the only way a fix is written, through the recorder's `recordFix(id, fix)`, which writes the fix (redacted by the store) and sets `fixed`, off the turn and in line with the other writes. A turn error (`agent/error`) has no tool to succeed later and is not watched.
- **The standing section** is, verbatim: `Errors are tracked by the errkb plugin. A context line starting with [errkb] names a known error and, when one is recorded, its fix: try that fix before re-diagnosing. When you resolve an error that has no recorded fix, record a working fix with err_record.` 46 words; T15 pointed its last sentence at the tool.

## Cost model

You should be able to decide for yourself whether this is worth it, so here is the arithmetic.

| Item                                        | Cost / saving                                                        |
| ------------------------------------------- | -------------------------------------------------------------------- |
| One hit, injected                           | ~80 tokens, replacing a re-diagnosis that typically costs 800–3000     |
| Standing system-prompt section              | ~50 tokens per request (switch off with `systemPromptHint: false`)     |
| Session-opening digest                      | ~40 tokens per session (`sessionDigest: 'counts'`)                     |
| Matching, fingerprinting, storage, `err_stats` | Zero — entirely local, no model call, no network                    |
| **Break-even**                              | **Recovered at a hit rate of roughly 5% or better**                    |

Below that hit rate the fixed session cost exceeds the savings, and the honest answer is to turn `systemPromptHint` off or not install it. `err_stats` reports hits, injections, estimated tokens saved and the resolved KB path locally, so the decision can be made from numbers rather than a feeling.

## Requirements and compatibility

| Requirement    | Value                                                                                        |
| -------------- | -------------------------------------------------------------------------------------------- |
| dsh            | `0.1.5-rc.2` (the version this design was verified against)                                   |
| Cordis         | `4.0.2`                                                                                       |
| `dsh-tools`    | `0.1.5-rc.2`                                                                                  |
| `dsh-llm`      | `0.1.5-rc.2`                                                                                  |
| schemastery    | `3.18.2`                                                                                      |
| Build tooling  | `tsdown 0.22.2`, `vitest`, `typescript`, `oxlint`, `prettier`                                  |
| Node / pnpm    | Node `>=22.13` (`engines.node`), pnpm `11.7.0` (`packageManager`), both in `package.json`     |
| Platform       | Developed and verified on Windows. No other platform has been tested and no OS support statement exists yet |

The extension points below were verified against the local installation, not against published documentation — file and line references are in §2 of the design document:

`agent/request-error` · `agent/error` · `tools/result` · `tools/post-execute` · `agent/pre-step` · `agent/session-start` · `agent.inject()` · `ctx.systemPrompt.section()` · `defineTool` + `ctx.tools.register()` · the `{kind: 'plugin', plugin, form: 'notice', summary}` message shape.

`dsh` is at a release candidate. Until a range is pinned, treat every `dsh` upgrade as a possible breaking change.

## Installation

> **Nothing is published, and the plugin is not installed anywhere yet.** The
> first two commands work as of T01–T04 — `pnpm install` and `pnpm build` both
> exit 0 and produce `lib/index.js` — but adding the package to a profile is
> untested until T17. Once added, it records and injects as described in
> [What works today](#what-works-today).

```sh
cd <repo-root>
pnpm install
pnpm build
dsh plugin --profile web add .        # relative path, anchored to the current directory
```

Then **restart** `dsh web`. A change to `dsh.profile.bundles` is not covered by `patchReload: live`, so a restart is required rather than optional.

A package that declares `dsh.bundle.patch` is merged into `dsh.profile.bundles` automatically — no manual profile editing. Because the development install is a `link:`, an unconfigured knowledge base lands in `errors/` inside this repository, which git ignores; set `kbDir` to a private repository of your own to sync it across devices. See [Where the knowledge base lives](#where-the-knowledge-base-lives).

## Usage

> **Implemented (T15), not installed.** The five tools exist and are registered
> by `apply`, but nothing installs the plugin into a profile until T17, so a
> model only sees them once you add the plugin yourself.

Ask the agent about a failure, or call a tool directly:

```json
{
  "query": "EPERM: operation not permitted, rename",
  "full": false
}
```

That block in [Injection](#injection-when-it-speaks-and-how-much) is the entire product. It replaces a diagnosis that would otherwise cost 800–3000 tokens.

### Parameters

| Tool         | Parameters                                                          | Defaults / notes                                                | Returns                                                     |
| ------------ | ------------------------------------------------------------------- | --------------------------------------------------------------- | ----------------------------------------------------------- |
| `err_lookup` | `query` (raw text, a signature, or an ID), `full?`                  | `full: false` returns the entry without the raw sample; tried as an ID (`E-7` finds `E-0007`), then a fingerprint, then matched as text across every category | The matching entry (ID, category, hits, status, fix); on a miss, `null` plus the three closest by Jaccard |
| `err_record` | `id?`, `message?`, `fix?`, `status?`, `note?`, `category?`          | `id` and `message` — exactly one; `status` is `fixed`/`wontfix`/`open`; a fix alone sets `fixed`; a note is added as a line of the notes | The ID and whether it was newly created              |
| `err_list`   | `cat?`, `status?`, `limit?`                                         | `limit` defaults to 20, at most 200; `cat` is a capture category (`tool`) or a display one (`tool / bash`) | ID, title and hit count — no bodies                          |
| `err_forget` | `id`, `reason?`                                                     | Moves the entry to `ERRORS.archive.md` with `Archived <date>: <reason>` in its notes; nothing is really deleted, and its ID is never reused | Whether the entry was archived |
| `err_stats`  | `scope?`                                                            | `scope` is `session` or `all` (default); it selects the notice counters, entry figures are always the whole knowledge base | Entries, hits, notices injected, estimated tokens saved, open entries without a fix, distrusted fixes, resolved path |

All five declare their output through `output.schema` + `render` (`ToolOutputDefinition` in `@deepseek-ai/dsh-tools`), as dsh-note does, so what reaches the model is controlled plain text; they are built with `defineTool` and registered with `ctx.tools.register()`.

- **`err_record` is the only way a fix is written.** With `id`, the fix goes through the recorder's `recordFix()` (the entry becomes `fixed`), then status and note in one more write, so an explicit `status` wins. With `message`, the text is matched like a captured error (its headline, under `category` or every category); only an exact hit updates that entry (without counting a hit); a near hit — fuzzy or by code — writes nothing and returns `closest match is E-0007 (approximate, by fuzzy); nothing was written. Call err_record with id: "E-0007" to confirm, or reword message`, so a fix never lands on a similar but different entry; a miss appends a new entry under `category`, default `agent`. Matching and appending run in one write, so two calls about one new error create one entry.
- **Every write takes the capture path.** All of them queue on the knowledge base's one write chain, with the same 500 ms budget and retries; the store redacts every text. A busy lock or a failed write comes back as an error, never as a throw.
- **Errors are values.** A wrong argument type or an unknown `status`/`scope` is refused by `defineTool`'s validation (`ToolArgsError`); everything else — both or neither of `id` and `message`, an unknown ID, an empty fix, an unreadable `ERRORS.md` — returns one `error` line, such as `err_record: no entry E-0042`.
- **The token estimate is labelled an estimate.** `err_stats` computes *notices that carried a fix × 800 − the tokens of every notice delivered*. 800 is the low end of the 800–3000 tokens §7 puts on a re-diagnosis; the standing costs (the system-prompt section on every request, the session digest) are not subtracted. The figure never goes below 0: when the notices cost more than they are assumed to have saved — notices delivered, none with a fix — it shows 0 and the line spells out the negative arithmetic, e.g. `0 fix notices × 800 − 60 notice tokens = −60, shown as 0`.

## Configuration

### Where these settings go

Settings live in the profile patch, not in a separate config file:

```yaml
# cordis.patch.yml
- insert:
    - id: err-kb
      name: dsh-errkb
      config:
        fuzzyThreshold: 0.72
        inject: hit-only
```

### Settings

| Setting              | Default                            | Purpose                                                                                        |
| -------------------- | ---------------------------------- | ---------------------------------------------------------------------------------------------- |
| `kbDir`              | `''`                               | Empty resolves automatically: config → plugin root `errors/` → `$DSH_HOME/errkb/` ([details](#where-the-knowledge-base-lives)). A relative path is resolved against the plugin package root |
| `idPrefix`           | `'E-'`                             | Entry ID prefix (§8 of the design document; it is not in §10's table). Letters, digits, `-` and `_`, not ending in a digit; anything else uses `E-`                           |
| `idWidth`            | `4`                                | Zero-padding width for IDs, a whole number from 1 to 9                                                                      |
| `capture`            | `['tool','command','llm','agent']` | Capture toggles, each can be off                                                                |
| `captureExitCodes`   | `true`                             | Record commands that exited non-zero                                                            |
| `transientThreshold` | `5`                                | Occurrences before a transient LLM error earns an ID; rounded up to a whole number, at least 1                                            |
| `fuzzyThreshold`     | `0.72`                             | Similarity needed for a fuzzy match, from 0.5 to 1.0                                             |
| `captureFix`         | `'prompt-once'`                    | `prompt-once` or `off`: ask the model once per session to state the fix of an entry that has none, when it looks resolved; any other value falls back to `prompt-once` |
| `inject`             | `'hit-only'`                       | `hit-only`, `always` or `off`; any other value falls back to `hit-only`                         |
| `sessionDigest`      | `'counts'`                         | Session-opening digest: `off`, `counts` or `index` (at most 10 entries); any other value falls back to `counts` |
| `systemPromptHint`   | `true`                             | The 50-token behavioural section                                                                |
| `providers`          | `['*']`                            | Restrict which providers are recorded (e.g. `deepseek-official`) — not read yet; the `agent/request-error` listener that uses it arrives in T16                                 |
| `share`              | `'public'`                         | Redaction strength — `public` or `private` ([details](#privacy-and-redaction))                   |
| `maxEntries`         | `200`                              | Above this, entries archive to `ERRORS.archive.md`; a whole number, at least 1                                              |
| `maxSampleChars`     | `500`                              | Cap on the stored raw sample, a whole number, at least 0; `0` means no cap                                                                  |
| `exportDir`          | `''`                               | Optional device-local export; empty disables it (e.g. an Obsidian vault path on one machine) — not read yet (T18)     |
| `labels`             | `'en'`                             | Language of the field labels in newly written entries: `en` or `zh`. Both are always parsed ([details](#the-errorsmd-format)) |

A numeric setting outside its range, or an `idPrefix` of the wrong shape, does not stop the plugin: it starts with the nearest allowed value instead (a fraction in a whole-number setting rounds down, except in `transientThreshold`, which rounds up; a value that is not a number, or a bad `idPrefix`, takes the default) and logs one warning naming the setting, the value given and the value used.

## Privacy and redaction

Everything the plugin does is local: no network calls, no telemetry, and no model is asked to classify, match or summarise anything. The only thing that leaves the machine is whatever you commit.

Redaction is **mandatory and happens before storage, not before sharing** — unredacted text never reaches the disk at all. It exists in memory only, long enough to compute a signature. Before any text is written, these are replaced:

- credentials and headers: `sk-*`, `Bearer *`, `api[_-]?key=*`, `token=*` (and `password=`, `secret=`), `authorization:`;
- provider and platform keys: GitHub (`ghp_`, `gho_`, …, `github_pat_`), AWS (`AKIA…`), xAI (`xai-`), Google (`AIza…`);
- long base64 runs, hex strings of 32 characters or more, e-mail addresses;
- the raw `requestId`;
- the user name in a home directory (`/home/<name>`, `/Users/<name>`, `<drive>:\Users\<name>` become `~`);
- with `share: 'public'` (the default) additionally: absolute paths collapse to `<path>` and the raw sample is capped at `maxSampleChars` (500).

With `share: 'private'` project-relative and other absolute paths are kept, which makes self-diagnosis easier but assumes the file stays private.

The credential patterns live in one place, `src/redact-patterns.ts`. The CI privacy guard is a shell `grep` and keeps its own copy of the expression; a test parses `.github/workflows/privacy-guard.yml` and fails when a family there has no equivalent in that file, so the two cannot drift apart unnoticed. Text-rule redaction is still not complete — internal host names, `user@host` in SSH errors and short tokens get through — which is why the next section keeps real entries out of public repositories.

**Real entries never go into a public repository (§17 Q4).** Redaction is the safety net, not the plan: the knowledge base is meant to live in a private repository that `kbDir` points to, which is also how it syncs across devices. This repository ignores `errors/` and only publishes the curated entries in `seeds/`; a test runs `redact()` over every `seeds/*.md` and fails if it would change a character. With a private repository, `share: 'private'` is a reasonable choice.

There is no `SECURITY.md` yet, and no security policy — until there is, treat any leak of unredacted text into the document as a bug and report it. See [Open decisions](#open-decisions).

## Failure modes and safety

The plugin's own mistakes must never become the agent's problem. Each risk below has a matching implementation constraint in §13 of the design document.

| Risk                                              | Constraint                                                                                             |
| ------------------------------------------------- | ------------------------------------------------------------------------------------------------------ |
| A listener throws and the turn closes             | Every listener is wrapped in `try/catch`; failure degrades to "not recorded this time", counted (in memory) and reported at a throttled rate |
| Stealing recovery from `dsh-llm-retry`            | `agent/request-error` must `await next()` and return the result unchanged — observe, never take over    |
| A write blocking the turn                         | Local write, 3 retries, 500 ms timeout; a timeout skips that record silently and never throws            |
| Multiple processes writing at once (web + headless) | `.lock` file opened with `wx` (10 s expiry, preemptible; a waiter removes only the stale lock it saw, by token) plus a temp file and an atomic `rename`        |
| Read-only disk or permission failure              | Falls back to `$DSH_HOME/errkb/`; if that fails too, that record is skipped silently                     |
| A corrupted document                              | Strict parsing; on parse failure the original is saved aside as `ERRORS.corrupt-<timestamp>.md` and new entries are appended only — **a corrupt document is never rewritten** |
| Secrets or privacy leaking into a public repo     | Mandatory redaction before storage; with `share: 'public'` even the `requestId` is dropped               |
| Unbounded growth slowing the context              | `maxEntries` archiving, 400-character entries, injection de-duplication and the per-step/turn/session caps |

**How much of this runs today (T11).** The listeners are wrapped as above: a failure is counted, logged at most once a minute through the plugin's logger, and never thrown. Failure counts stay in memory for the life of the process and are not written to `state.json`; only hit counters and fix-trust counters are. A `state.json` that is not valid JSON or not version 1 is never fatal: it reads as empty (the counts fall back to the blocks), is reported once, and is saved aside as `state.corrupt-<timestamp>.json` by the next write. Writes to `state.json` take the same `.lock` as `ERRORS.md`, replace the file atomically, and share the write's 500 ms budget. Writes happen off the turn, one at a time per knowledge base, each with a 500 ms budget for up to 3 retries and the lock wait; a lock still busy when the budget runs out skips the record silently. A write that already holds the lock is not cut off midway, since the store cannot abandon one safely. A document that does not parse is not retried and stops recording until it is repaired. The redaction, lock, atomic-write and corrupt-document rows are the store's (T07–T08) and apply to every write. No promise the plugin starts is left without a handler: the lookup `tools/post-execute` starts before `await next()` is guarded the moment it is created, so a malformed tool result (say `isError` with no `error`) is counted and the downstream decision passes through, instead of an unhandled rejection that could take the host process down.

**What it will never do:** take over retries, throw into a turn, rewrite a corrupt document, block a turn on a write, or send anything off the machine.

## Known limitations

**This does not take over retries, by design.** `dsh-llm-retry` owns retry, and every retry is billed again. The `agent/request-error` listener must `await next()` and return the result unchanged. A plugin that "helpfully" recovered here would be fighting the retry owner's correctness contract.

**Fuzzy matching can merge two errors that only look alike.** At 0.72 similarity a near miss is still a miss. The mitigation is social rather than algorithmic: near misses are labelled as approximate in the injected text, and any entry can be marked as a misjudgment to take it out of injection permanently. Writes are guarded too: `err_record` with a `message` updates an entry only on an exact match; on an approximate match it writes nothing and returns the closest ID, which the model must confirm by calling `err_record` with that `id`.

**0.72 is a placeholder, not a measured value.** The T09 tests prove the threshold behaves as coded at 0.71 / 0.72 / 0.73 and that Chinese text is tokenized into bigrams; they do not prove 0.72 is the right number. That needs the labelled corpus of real errors and look-alike pairs proposed in [`docs/discussions.md`](docs/discussions.md) §3, which does not exist yet.

**The knowledge base is only as good as what gets written into it.** An entry recorded without a fix will be injected without a fix — a notice that spends tokens to say nothing. This is why new entries prompt once for the fix, and why `err_stats` reports how many open entries are doing nothing useful.

**It can be a net loss.** Below roughly a 5% hit rate the standing cost exceeds the savings; the caps exist so the loss stays small, not so it disappears. See [Cost model](#cost-model).

**Knowledge travels by git, and that is the only channel.** No cloud service, no remote sharing. Two devices converge by merging append-only Markdown, which is reliable but manual.

**One shared knowledge base across all projects.** There is no per-project isolation, and the project and device fields described in §8 of the design document are not recorded yet: nothing writes them, so the matcher's same-project tie-break never fires in practice. Whether public mode will hash or drop those fields once they are recorded is still open ([`docs/discussions.md`](docs/discussions.md) §5.3).

**No GUI panel.** Rejected for now. The design document records it as a post-release candidate.

## Verifying it works

### Acceptance criteria

Seven criteria define "done" (§1 of the design document), each with its own verification method:

| #  | Criterion                                                              | How to verify                                                          | Status today |
| -- | ---------------------------------------------------------------------- | ---------------------------------------------------------------------- | ------------ |
| S1 | Errors are filed automatically and get a unique ID from `E-0001`        | Trigger one failing tool call → a new numbered block appears in `ERRORS.md` | Covered by unit and integration tests against a fake host; real host after T17 |
| S2 | The same error is never numbered twice — even as path, line, PID and timestamp change | Re-run in a different temp directory with a different line number → hit count +1, same ID | Covered by unit and integration tests against a fake host; real host after T17 |
| S3 | A hit injects a ≤ 120-token known fix                                   | A `plugin`-sourced notice appears in the session and the model stops re-diagnosing | Covered by unit and integration tests against a fake host; real host after T17 |
| S4 | A new error gets its fix written into the entry after resolution        | The model calls `err_record`, or answers the one-shot prompt             | Covered by unit and integration tests against a fake host; real host after T17 |
| S5 | No plugin failure interrupts a turn or affects retry                    | Unit test injects an exception → the listener swallows it and `agent/request-error` still returns the downstream result | Swallowing is tested for the listeners that exist; the `agent/request-error` part waits for T16 |
| S6 | The document is editable by hand and by model, and the index can be rebuilt | Hand-edit a fix in `ERRORS.md` → the next hit reads the edited text   | Covered by unit and integration tests against a fake host; real host after T17 |
| S7 | Cross-device safe: no absolute paths, no secrets                         | Full-text search for `D:\`, `sk-`, `Bearer` returns zero hits           | Covered by redaction tests and the CI privacy guard; real host after T17 |

### Manual acceptance

These steps need the plugin installed in a real `dsh`, so they can be run only after T17 (installation).

1. `dsh --profile web --dump-config` shows the `err-kb` entry (proving the bundle layer works).
2. Trigger a command that must fail (for example, a drive letter that does not exist) → `## E-0001` appears.
3. Re-run the same class of failure from a different temp directory or line → still `E-0001`, hit count +1.
4. A plugin notice line appears in the session, and the model does **not** re-diagnose.
5. `err_stats` prints the resolved KB path, the hit rate and the estimated savings.

## Roadmap

The design is complete and the work is broken into **eighteen tasks** across seven milestones. Each task's deliverable and acceptance criteria are in [Full task breakdown](#full-task-breakdown) below.

| State | Tasks                                                                                   |
| ----- | --------------------------------------------------------------------------------------- |
| ✅    | T01–T09 — project skeleton (package, tsconfig, tsdown, vitest, bundle patch), paths, signature, redact, store and match |
| ✅    | T10 — classification, headline extraction and the transient-noise rule                  |
| ✅    | T11 — the first two hooks: `agent/error` and `tools/result`                             |
| ✅    | T12 — notice text, hard caps and fix trust                                              |
| ✅    | T13 — the four injection points                                                         |
| ✅    | T14 — resolution detection, the last of the injection layer                             |
| ✅    | T15 — the five agent tools                                                              |
| 🔜    | T16–T17 — LLM failure integration, and installation into the web profile                |
| 🔜    | T18 — optional: Obsidian export                                                         |

T01–T15 are checked off and merged upstream (T14 and T15 in PR #12). The rest are open; T18 can be dropped at any point without touching the main line.

### Milestone mapping

The design document plans in phases P0–P7 (§15); the task list numbers the same work T01–T18. They line up like this:

| Phase          | Tasks   | Completion gate                                                              |
| -------------- | ------- | ---------------------------------------------------------------------------- |
| P0             | —       | The design document is written and **you have answered §17** — two of eight answered (Q2, Q4) |
| P1             | T01–T04 | ✅ `pnpm typecheck` passes — verified locally and in CI                       |
| P2             | T05–T09 | ✅ Unit tests green; every T05–T09 module at 100% statements and lines |
| P3             | T10–T11 | ✅ A guaranteed-failing command produces `E-0001` — shown end to end against a temporary knowledge base; the 99% gate now runs in `pnpm test` |
| P4             | T12–T14 | ✅ A repeated failure is injected and the model stops re-diagnosing — T12 (notices and caps), T13 (the four injection points) and T14 (resolution detection) are done; seeing the model stop re-diagnosing in a live session waits for T17 |
| P5             | T15     | ✅ The model can call `err_lookup` and `err_record` — shown through the plugin's own hooks and tools against a temporary knowledge base: a recorded fix lands in `ERRORS.md` as `fixed`, is found by the original message, and rides the next failure's notice; a live session waits for T17 |
| P6             | T16–T17 | `--dump-config` shows the entry; one cloud and one local error are each recorded |
| P7 (optional)  | T18     | The exported file is readable                                                |

### Not covered by any task

Two gaps are worth stating plainly rather than hiding behind the checkboxes:

- **Publishing has no task.** The status table lists "Published to npm" and §17 Q1 originally left the npm name unchecked (both `dsh-errkb` and `err-kb` were unclaimed on 2026-10-01), but none of T01–T18 covers reserving the name or publishing a release. CI is not in the task list either — it landed as repository infrastructure instead, see [What works today](#what-works-today).
- **`err_export` is missing from the README's task list.** §15 P7 mentions an `err_export` single-file JSON backup alongside the Obsidian export; T18 only mentions the Obsidian export. Either the tool returns or the phase description is trimmed.

### Full task breakdown

| #     | Task                                   | Deliverable                                                                  | Acceptance                                                                                           |
| ----- | -------------------------------------- | ---------------------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------- |
| ☑ T01 | Initialize `package.json`              | Name, entry point, bundle patch, peer dependencies                           | `pnpm install` succeeds                                                                              |
| ☑ T02 | Configure tsconfig and tsdown          | `tsconfig.json`, `tsdown.config.ts`                                          | `pnpm build` emits `lib/index.js`                                                                    |
| ☑ T03 | Configure vitest and the coverage gate | `vitest.config.ts`, `tests/`                                                 | `pnpm test` runs                                                                                     |
| ☑ T04 | Bundle patch and an empty `apply`      | Plugin entry: `name`, `inject`, `Config`, `apply`                            | `pnpm typecheck` passes                                                                              |
| ☑ T05 | Resolve the KB path                    | `src/paths.ts` — three-tier resolution, startup log                          | Each of the three cases returns the expected path                                                    |
| ☑ T06 | Normalize and fingerprint              | `src/signature.ts`                                                           | Changed path, line, PID, timestamp or UUID → same signature; different errors → different signatures |
| ☑ T07 | Redact                                 | `src/redact.ts`                                                              | Zero hits in the output, asserted                                                                    |
| ☑ T08 | Store the document                     | `src/store.ts` — parse, render, append, archive, lock, atomic write          | Round-trip identity; hand-edited fixes read back; 50 concurrent records yield 50 unique IDs          |
| ☑ T09 | Match                                  | `src/match.ts` — exact, fuzzy, code fallback, mis-flag fallback              | Boundary values 0.71 / 0.72 / 0.73 behave as specified                                               |
| ☑ T10 | Classify, and suppress noise           | `src/capture.ts`                                                             | Transient errors get no ID until the threshold; capture off writes nothing                           |
| ☑ T11 | Wire the first two hooks               | `agent/error`, `tools/result` listeners                                      | A guaranteed-failing command produces `E-0001`                                                       |
| ☑ T12 | Generate notices                       | `src/inject.ts` — template, caps, dedup                                      | Caps hold; source shape and summary length are exact                                                 |
| ☑ T13 | Wire the four injection points         | `tools/post-execute`, `agent/pre-step`, `agent/session-start`, system prompt | A repeated failure is injected, and the model stops re-diagnosing                                    |
| ☑ T14 | Detect resolution                      | `src/resolve-detect.ts`                                                      | A `fixed` entry goes silent after one notice                                                         |
| ☑ T15 | Build the five tools                   | `src/tools.ts`                                                               | The model can call `err_lookup` and `err_record`                                                     |
| ☐ T16 | Listen to `agent/request-error`        | The listener, plus LLM failure classification                                | The returned value is object-identical to the downstream result                                      |
| ☐ T17 | Install into the web profile           | `dsh plugin --profile web add .`                                             | `--dump-config` shows the entry; one cloud and one local error are each recorded                     |
| ☐ T18 | Optional: Obsidian export              | `exportDir` export                                                           | The exported file is readable                                                                        |

## Development

The skeleton exists (T01–T04), so all of these work. `build`, `typecheck`, `lint`, `test` and `format:check` are exactly what CI runs on every push to `main` and every pull request:

```sh
pnpm install
pnpm build            # tsdown → lib/
pnpm test             # vitest with coverage; fails under 99% statements or lines
pnpm typecheck        # tsc --noEmit
pnpm lint             # oxlint
pnpm format           # prettier --write .
pnpm format:check     # prettier --check .  (what CI runs)
```

### Test plan

The test plan is §14 of the design document. Coverage gate: statements and lines ≥ 99, enforced by `pnpm test` since T11. Seven groups are planned:

1. `signature` — path, line, PID, timestamp and UUID changes keep the signature stable; different errors differ; empty, ANSI-only and very long strings do not crash.
2. `match` — exact hits, Jaccard boundaries at 0.71 / 0.72 / 0.73, short-message code fallback, mis-flagged entries never injected.
3. `redact` — `sk-`, `Bearer`, `api_key=`, long hex, e-mail and absolute paths all replaced; zero hits asserted in the output.
4. `store` — write/parse round-trip identity; hand-edited fixes read back; a corrupt file takes the save-aside + append-only path; the archive threshold fires; IDs increase monotonically; **50 concurrent records yield 50 unique IDs and a still-parseable document**.
5. `capture` — four payload classes classified correctly; transient rate limits uncounted until the threshold; every capture toggle off means zero writes.
6. `inject` — text length caps, ≤ 1 notice per step, ≤ 3 per turn, source shape `{kind:'plugin',plugin:'err-kb',form:'notice',summary}` with `summary` ≤ 120 characters. In `tests/inject.test.ts` (T12), together with a CJK-heavy fix, ≤ 2 notices per ID per session, a `fixed` entry speaking once, the near-hit and miss wordings under each `inject` mode, silent non-injectable entries, and the fix-trust wording change and stop at two recurrences.
7. `plugin` — a fake context confirms listeners are registered, exceptions are swallowed, and the `agent/request-error` return value is identical (object identity asserted) to the downstream result. The first two parts are in `tests/plugin.test.ts` (T11), along with a failing command producing `E-0001` and a repeat bumping it; the `agent/request-error` part arrives with T16. `tests/injection.test.ts` (T13) plays the host for the four injection points: a repeated failure gets its known fix in `additionalContexts` within the caps, a dead turn's notice rides the next `agent/pre-step`, each `sessionDigest` mode, the section present or absent with `systemPromptHint`, `inject: 'off'` silent while capture records, and both waterfalls returning the downstream result untouched (object identity asserted), including when our internals throw.

The concurrency test in group 4 is the one that matters most, because it is the failure mode that would corrupt the only authoritative file.

## Troubleshooting

Everything below is a specification of intended behaviour, not a report of observed behaviour — nothing runs yet.

| Symptom                                            | What to check                                                                                                   |
| -------------------------------------------------- | --------------------------------------------------------------------------------------------------------------- |
| "Is it even loaded?"                               | The one-line startup log prints the resolved KB path; `dsh --profile web --dump-config` shows the `err-kb` entry. Restart after installing — `dsh.profile.bundles` is not hot-reloaded |
| "Where did my errors go?"                          | `err_stats` prints the resolved path. Start from [Where the knowledge base lives](#where-the-knowledge-base-lives) |
| "Nothing is being recorded."                       | Check the `capture` toggles, `captureExitCodes`, and whether the failure is transient (`RATE_LIMIT`, `SERVER`, `TIMEOUT`, `TRANSPORT`, `EMPTY_RESPONSE`) — those are counted, per session and in memory, until `transientThreshold`; once an entry exists, its repeats are counted in `state.json` |
| "Writes are silently skipped."                     | That is the designed behaviour when the KB directory is unwritable, the lock cannot be taken, or a write times out after 3 retries and 500 ms. Check the KB directory's permissions and whether the fallback to `$DSH_HOME/errkb/` took effect |
| "Injection is too noisy."                          | `inject: 'off'` stops it entirely while capture keeps recording; `systemPromptHint: false` removes the standing 50-token section; `sessionDigest: 'off'` removes the opening digest |
| "One entry keeps being injected with the wrong fix." | Mark it `wontfix` via `err_record`, or flag it as a misjudgment — it stops being injected automatically but still counts |
| "`ERRORS.md` looks broken."                        | The plugin never rewrites a document it cannot parse: the original is saved as `ERRORS.corrupt-<timestamp>.md` and new entries append after it. Repair the saved copy and restore it |
| "I edited a fix by hand and nothing changed."      | Hand edits win over the index, so this should not happen — check that the edit is inside the `- Fix:` (or `- 解法:`) field and above the next field label, then delete `errors.index.json` to force a rebuild |
| "I want to start over."                            | Delete `state.json` (this machine's hit and fix-trust counters: counts fall back to the Hits written in `ERRORS.md`, trust starts over), delete `errors.index.json` (cache). Both are recreated. Deleting `ERRORS.md` deletes the knowledge — that is the only file that matters |
| "Counters disagree across my two machines."        | Expected: counters are machine-local and deliberately not in git. Only the knowledge is shared |

## Uninstall

**Not specified yet.** The design document does not cover removing the plugin, and there is no task for it — see [Open decisions](#open-decisions).

What is true today: installing a plugin adds an entry to `dsh.profile.bundles` (via the bundle patch), so a clean removal is removing that entry and restarting `dsh web`. The knowledge base is nothing but files in a directory the plugin only ever appends to; nothing the plugin does will delete it, so back it up or delete it by hand.

## Open decisions

**Two are decided, six remain.** Q2 and Q4 were answered for T07–T08 and are recorded in §17. The rest still decide what later tasks build: Q5 (scope of reuse) affects matching in T09, and Q6 (`captureExitCodes`) affects classification in T10 (see [Milestone mapping](#milestone-mapping)). Write answers in the "your answer" column of §17, or anything at all in the §19 annotation area of [`docs/设计说明书.md`](docs/设计说明书.md).

| # | Question                                                             | Proposed default                                                        | State |
| - | -------------------------------------------------------------------- | ----------------------------------------------------------------------- | ----- |
| 1 | Package name / plugin id / tool prefix / ID prefix                    | `dsh-errkb` / `err-kb` / `err_` / `E-` — both npm names were unclaimed when checked on 2026-10-01 (registry 404) | Open |
| 2 | Language of the document's field names                                | Chinese labels + English machine keys                                    | **Decided:** English labels by default, `labels: 'zh'` for Chinese; both are always parsed |
| 3 | Accept that counters stay out of git and only knowledge syncs          | Accept                                                                   | Open  |
| 4 | Commit `errors/` into the GitHub repository                            | Yes, with mandatory redaction as the safety net                           | **Decided:** no — the real KB lives in a private repository (`kbDir` points to it); this repository ignores `errors/` and only carries curated `seeds/` |
| 5 | Scope of reuse                                                         | One shared KB for all projects, with a project field on each entry        | Open  |
| 6 | `captureExitCodes` default                                             | On — command failures are the most reusable case                           | Open  |
| 7 | Which P7 extras (GUI panel / auto-running fixes / Obsidian export)     | Obsidian export only for now                                              | Open  |
| 8 | Package root location                                                  | `<repo-root>`                                  | Open  |

Two further gaps are **not** in §17 and will need an answer before the README can stop saying "not specified":

- **Requirements**: Node and pnpm are pinned in `package.json` (`engines.node >=22.13`, `packageManager pnpm@11.7.0`), but there is still no OS support statement.
- **Distribution and maintenance**: no task covers publishing to npm, checking that the npm name is free, CI, or uninstalling.

## Future work

Recorded in §18 of the design document. None of it is scheduled; T18 can be dropped at any point without touching the main line.

1. **Pre-flight instead of post-mortem** — a `tools/pre-execute` guard that recognises a known-to-fail command and blocks it with the recorded fix attached, collapsing a three-round failure loop into one message. Complements `dsh-repeat-tool-reminder`, which catches repeated calls: that plugin handles repetition, this one handles "known to fail".
2. **`err_fix` — replay the fix** — when an entry records a deterministic command, offer `err_fix <id>` to re-run it after approval, closing the recycle → reuse loop. Requires `dsh-user-approval`, so off by default.
3. **Expose the knowledge base as a skill** — publish it as `SKILL.md` through `dsh-skill`'s filesystem discovery so the model reads the parts it needs instead of the whole document.
4. **Environment fingerprint and fix confidence** — record os / node / pnpm / shell / provider / model per entry, and warn "may not apply here" when a hit comes from a different environment.
5. **Compaction-friendly by construction** — `dsh-compaction-tool-result-pruner` drops old tool results, but these facts live on disk and are injected on demand; that is the structural advantage over keeping fixes in conversation history.
6. **A visible token ledger** — integrate `@deepseek-ai/dsh-token-meter` / `dsh-session-stats` so `err_stats` can print "injected 0.9k / at least 8k saved by the mechanism". Without numbers, thresholds cannot be tuned.
7. **Seed entries** — ship `CONTEXT_OVERFLOW`, `NO_ADAPTER` and the Windows Chinese-path `EPERM` as pre-filled entries so a fresh install is valuable on day one.
8. **Team sharing** — because the document is in git, putting `errors/` in a team repository makes it a shared pitfall knowledge base; an `err_promote` could raise a local entry to a public one.
9. **Web GUI panel** — the full version of the rejected option: a third-party package can declare `dsh.client` (`platform: 'web'`) plus a client module for a list view, hit curves and a settings page. Rebuilding the web artifacts makes it expensive; evaluate after P6 is stable.
10. **Reverse use** — `err_list --status open` is a ready-made "what is still broken in this project" list, usable as an issue draft or an onboarding document for newcomers.

## Related projects

Surveyed from the [`dsh-plugin`](https://github.com/topics/dsh-plugin) GitHub topic on 2026-09-30 and re-checked on 2026-10-01. Several plugins capture errors; none gives an error a stable ID across sessions and pushes the recorded fix back before the model starts diagnosing.

| Project | What it does | How it differs |
| ------- | ------------ | -------------- |
| [166767/dsh-error-audit](https://github.com/166767/dsh-error-audit) | Listens to the same three hooks (`agent/error`, `agent/request-error`, `tools/result`), writes every error to `$DSH_HOME/error-audit/`, notifies the model, and exposes a `read_error_logs` tool | A log, not a knowledge base: no deduplication and no stored fix |
| [d86e/dsh-doctor](https://github.com/d86e/dsh-doctor) | Captures failed tool results and classifies them as transient / agent / business | Observes and classifies; never injects a fix |
| [Wanbinyu/dsh-error-lens](https://github.com/Wanbinyu/dsh-error-lens) | Redacted diagnostics for provider errors (401, 403, 429, context overflow, …) | Session-scoped, nothing persisted. Its README states that text-rule redaction cannot be complete — the same limit applies to §4.4 here |
| [Leitarkkk/dsh-research-nudge](https://github.com/Leitarkkk/dsh-research-nudge) | Uses the same injection point (`tools/post-execute` → `additionalContexts`) to nudge the model to research once failures, repetitions and tool calls pile up | In-session only; can fire on the same step as an `err-kb` notice |

Adjacent, outside `dsh`:

- [vshulcz/deja-vu](https://github.com/vshulcz/deja-vu) — local search over past agent sessions, no LLM involved.
- [thedotmack/claude-mem](https://github.com/thedotmack/claude-mem), [MemTensor/MemOS](https://github.com/MemTensor/MemOS) — general-purpose agent memory; both spend model calls (summarisation or embeddings) to build it.
- [ankitkr3/compounded](https://github.com/ankitkr3/compounded) — learned lessons gain trust with clean uses and are demoted when they steer a task wrong.
- [Sentry event grouping](https://docs.sentry.io/concepts/data-management/event-grouping/) — the established prior art for fingerprinting and grouping errors.

Where `dsh-errkb` sits: **stable, human-readable IDs** (`E-0007`) that survive across sessions and machines; the **fix is injected before diagnosis**, in at most 120 tokens; **Markdown is the source of truth**, edited by hand and merged by git; and **zero model calls** — capture, matching and injection are all local text processing.

## Contributing

T01–T15 are implemented and tested; T16 and T17 are open, and nothing is installed or published yet. Contributions are welcome — fixes, tests, a review of the design, or disagreement with it while the open §17 decisions are still cheap to change.

- **The design document is the source of truth.** If this README and [`docs/设计说明书.md`](docs/设计说明书.md) disagree, the design document wins (and this README has a bug worth reporting).
- **Feedback goes in the document.** §17 is a table with an empty "your answer" column; §19 is an annotation area. Both are meant to be written in directly.
- **Keep the two READMEs in sync.** `README.md` is English and `README.zh-CN.md` is Chinese, and they have already drifted once. Any change to one belongs in the same change as the other.
- **Language rules for the repository**: English for `README.md` and for code, comments, commits and documentation; Chinese for `README.zh-CN.md` and for the design document, which is written in Chinese by choice.
- **`pnpm test` enforces the coverage gate.** It runs vitest with coverage and fails under 99% statements or lines; run it with `pnpm typecheck`, `pnpm lint`, `pnpm format:check` and `pnpm build` before you push — CI runs the same five.
- **The privacy guard must pass.** `.github/workflows/privacy-guard.yml` rejects absolute user paths, personal e-mail addresses and credential-shaped tokens anywhere in the tree; write `<repo-root>` and other placeholders instead.
- **One task per commit**, with a conventional prefix and the task tag: `feat: add the five err_ tools (T15)`, `fix: … (T15)`, `docs: …`.

## License

[MIT](LICENSE)

---

<sub>A third-party plugin for DeepSeek Harness. Not affiliated with DeepSeek.</sub>
