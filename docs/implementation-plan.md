# errkb Technical Error Reuse Toolbox Implementation Plan

[中文](implementation-plan.zh-CN.md)

This document is the sole execution baseline for upgrading the existing DSH plugin into a multi-host technical-error reuse toolbox. Start at `EKB-001` and proceed in numeric order. A card is complete only after both its automated checks and its manual acceptance pass.

## 1 Product decision and version scope

The finished product is neither a plugin tied to one model nor a desktop monitor that claims to read a model's private reasoning. It is a local core platform connected to hosts through small adapters:

```text
Host hooks plugins CLI logs manual input
                 |
                 v
          standard ErrorEvent
                 |
                 v
redact classify headline fingerprint match deduplicate
                 |
                 v
       PostgreSQL knowledge base
                 |
          +------+------+
          v             v
     errkb.exe      MCP local API
   search manage   return known fixes
```

Version one covers observable and verifiable technical failures only:

- environment, toolchain and runtime failures;
- dependency, version, configuration, authentication, permission and network failures;
- compile, test, code-runtime and command-exit failures;
- tool-call, plugin, hook, MCP and protocol failures.

Version one explicitly excludes automatic detection of requirement misunderstandings or hallucinations, access to undisclosed model reasoning, always-on OCR or screenshots, automatic execution of fixes, cloud sharing, team permissions and vector retrieval. These belong only to the post-v1 candidates and must not block v1.

## 2 Final delivery form

The user-facing v1 product is a Windows installer and one primary program, `errkb.exe`. One binary exposes service, CLI, MCP and diagnostic modes:

```text
errkb.exe                    tray and search UI
errkb.exe daemon             local background service
errkb.exe run -- <command>   wrap and observe a CLI process
errkb.exe search <query>     search the knowledge base
errkb.exe mcp                MCP stdio server
errkb.exe doctor             environment and connector diagnostics
```

Host adapters only translate observable failures into `ErrorEvent` and, where the host permits it, query and return known fixes. Classification, redaction, storage, search, solution trust and audit belong to the core platform and must not be reimplemented per plugin.

Because PostgreSQL and PL/pgSQL are explicit requirements, v1 is an installed local application, not a claimed zero-dependency portable executable. Development uses an external PostgreSQL 16 instance. `EKB-051` implements the release-time local-instance lifecycle. A truly single-file portable build requires a database decision change and cannot be promised together with mandatory PostgreSQL.

This decision gates `EKB-051` (local-instance lifecycle) and `EKB-050` (release packaging); changing it requires editing this section first and notifying the dependent cards. `EKB-045` is the omnibox card and does not depend on this decision.

## 3 Capture and reuse strategy

### 3.1 Reliability levels

| Level | Channel | Purpose | v1 policy |
| --- | --- | --- | --- |
| A | hooks, plugins, structured event APIs | Obtain codes, commands, exit status, sessions and tool results | Preferred |
| B | CLI wrappers and structured logs | Obtain stdout, stderr, exit status and process context | Required for MVP |
| C | ordinary log tailing | Support processes that cannot be wrapped | Required for MVP |
| D | Windows UI Automation | Read accessibility text deliberately exposed by a desktop app | Per-host work after v1 |
| E | OCR, clipboard and manual hotkey | Fallback without a structured interface | OCR deferred; manual input allowed |

MCP lets a host query and record knowledge; it is not passive universal error capture. Passive capture belongs in hooks, plugins, CLI wrappers or log channels.

### 3.2 Initial v1 hosts

Close the first integrations in this order:

1. Generic CLI through `errkb run --`.
2. The existing DSH plugin, reduced to a core-platform adapter with an explicit offline fallback.
3. Codex, after probing the then-current hook, plugin and MCP capabilities.
4. Claude Code, after probing hooks, structured output and MCP capabilities.

DeepSeek, Kimi, Qianwen and other hosts initially use generic CLI, log or manual channels. Add a named adapter only when a stable, testable interface exists. A window title is not an interface contract.

## 4 Core data model

Do not store every captured string as a new error. The database contains at least:

| Entity | Purpose |
| --- | --- |
| `connectors` | Host, version, channel and capability state |
| `sessions` | One host or CLI session |
| `events` | Append-only redacted observations |
| `issues` | Stable problem IDs grouping equivalent events |
| `occurrences` | Each issue occurrence in an environment |
| `solutions` | Candidate solutions for an issue |
| `solution_versions` | Solution text, prerequisites, risk and history |
| `outcomes` | Worked, failed or unknown per environment |
| `retrievals` | What a search matched and why |
| `notices` | Content returned to a host and estimated tokens |
| `audit_log` | Edits, imports, exports, deletion and controlled actions |

`events`, `issues`, `solutions` and `outcomes` are separate. An occurrence is not a new issue, and an issue cannot be assigned one eternally correct fix.

`ErrorEvent v1` includes at least:

```text
event_id connector_id session_id observed_at channel
category severity transient
headline normalized_message redacted_sample
error_code command exit_code tool_name
os shell runtime_versions project_key
fingerprint schema_version evidence confidence
```

CUE validates events, configuration and MCP contracts. Go implements runtime behavior. PL/pgSQL owns database uniqueness, idempotent writes, counters and transactional state transitions.

## 5 Retrieval and grouping

The UI may behave like a browser omnibox, but retrieval is layered:

1. Issue ID, error code and exact fingerprint.
2. Deterministic filters over normalized message, command and tool name.
3. PostgreSQL `pg_trgm` candidate generation.
4. Go reranking using Jaccard, CJK bigrams, environment versions and host agreement.
5. At most three candidates with match explanations.

Approximate matches are candidates, not automatic permanent merges. Automatic solution reuse requires an exact fingerprint or a user-confirmed relationship. A false merge is worse than a miss, so the labelled corpus first targets zero false merges and then recall.

V1 uses no embeddings, model classification or external search service.

## 6 Security and privacy boundary

- Redact before database writes, logs and offline queues.
- Captured text is data, never an instruction or command.
- Use a Windows Named Pipe by default; local HTTP is off by default.
- If loopback HTTP is enabled, require a random token and bind only to loopback.
- Secrets, cookies, authorization values, personal paths and raw request IDs never enter storage.
- Apply separate retention periods to samples, normalized text and audit data.
- Allow global pause and deletion by connector, project and time.
- V1 never automatically executes fixes.
- UIA and OCR may operate only on an explicit allowlist; OCR frames are never stored.

## 7 Verified current baseline

This review inspected every project file under the root, `.github`, `docs`, `seeds`, `src` and `tests`, and ran the existing toolchain.

| Item | Verified result |
| --- | --- |
| Git state | The directory has no `.git`; it is an ordinary source copy without safe branches, commits or rollback |
| TypeScript source | 13 modules implementing most of the DSH in-process loop |
| Tests | 15 test files; 598 tests discovered locally |
| Result | 596 pass and 2 fail; the suite is not currently green |
| Passing commands | install, build, typecheck, lint and format check |
| Failing command | `pnpm test` |
| Failure one | Under real Windows filesystem concurrency, `.lock` creation returns `EPERM`; only `EEXIST` is treated as contention |
| Failure two | `mtimeMs(<file>/child)` maps Windows `ENOENT` to `undefined`, while the test expects rejection |
| Dependency resolution | The lock installs `dsh-llm` and `dsh-tools` `0.1.5-rc.3`, while the README still mainly names rc.2 |
| Toolbox code | `cmd`, `internal`, `db`, `spec` and `packaging` do not exist |

The repository is therefore a heavily tested DSH prototype, not yet a validated multi-host product.

### 7.1 Measured toolchain facts (EKB-002)

The commands below were run on this machine during the review; outputs are kept verbatim and set the version baseline for later cards.

| Tool | Measured command | Output |
| --- | --- | --- |
| Node.js | `node -v` | `v24.16.0` |
| pnpm | `pnpm -v` | `11.7.0` |
| Go | `go version` | `go version go1.26.4 windows/amd64` |
| PostgreSQL | `psql --version` | `psql (PostgreSQL) 16.15` |
| PostgreSQL service | `Get-Service postgresql*` | `postgresql-x64-16` running, start type `Automatic` |
| CUE | `cue version` | `cue version v0.17.1` (CUE language v0.17.1, Go go1.26.4, CGO_ENABLED 1) |
| DSH host | `dsh --version` | No such command: the host is a desktop application without a `dsh` CLI |
| Installed packages | `node_modules/@deepseek-ai/*/package.json` | `dsh-llm 0.1.5-rc.3`, `dsh-tools 0.1.5-rc.3`, `cordis 4.0.2`, `schemastery 3.18.2`; `@deepseek-ai/dsh` is not installed |
| Frozen install | `pnpm install --frozen-lockfile` | `Already up to date. Done in 287ms using pnpm v11.7.0`, exit code 0 |
| Lockfile | `Get-FileHash pnpm-lock.yaml -Algorithm SHA256` | `96BBD6FB51412A529F0BD1208EAF59BFC82453ECAB48C1DB74A91BD6B40F54DB` |

The DSH host version has no clean command source: `@deepseek-ai/dsh` is not a dependency and the desktop host exposes no CLI. The only checkable ecosystem evidence is the peer `@deepseek-ai/dsh ^0.2.0-rc.2` in `~/.dsh/profiles/desktop/node_modules/dsh-ops/package.json`, which places the actual host in the 0.2.x generation; the README version table still naming `0.1.5-rc.2` is a historical claim, explained in 7.2.

### 7.2 dsh-llm / dsh-tools rc.2 to rc.3

- Packages: `@deepseek-ai/dsh-llm` and `@deepseek-ai/dsh-tools`. The peer range stays `^0.1.5-rc.2`, which already admits `0.1.5-rc.3`, so neither an upgrade nor a downgrade declaration is needed and the range must not change.
- Actually installed: `pnpm-lock.yaml` resolves both to `0.1.5-rc.3`, confirmed in `node_modules` (see 7.1).
- README claim: `README.md:347` and `README.zh-CN.md:336` still name `0.1.5-rc.2`, meaning "the version this design was verified against" — a historical caliber, not the installed one. This card changes only `package.json`, `pnpm-lock.yaml` and this plan, so the READMEs are left alone and this section is the authoritative statement.
- Impact on the 13 `src/` modules: they use only `createUserMessage`, `UserMessage`, `MessageSourceMap` and `ContentBlock` from `@deepseek-ai/dsh-llm`, and `defineTool`, `ToolDefinition`, `ToolRunContext` from `@deepseek-ai/dsh-tools`; no API removal or rename was found. `pnpm typecheck`, `pnpm build` and `pnpm test` (598 passing) all pass on rc.3, so behavior is unchanged.
- Implementation caliber: the platform core is written in Go; the DSH connector targets the lockfile's `0.1.5-rc.3` and host 0.2.x generation; no compatibility branch is maintained for either rc.2 or rc.3. The README version table is left for the EKB-055 documentation pass.

## 8 Existing file disposition

Language ownership: Go (`cmd/`, `internal/`) is checked by `gofmt` and `go vet`; SQL and PL/pgSQL (`db/migrations/`) by migration checks and concurrency tests; CUE (`spec/`) by `cue vet`; TypeScript (`src/`, `tests/`) by the five pnpm checks; PowerShell (`packaging/`) only packages and carries no business logic. `prettier` stays scoped to TypeScript and Markdown; Go, SQL and CUE formatting is owned by their own tools.

New files are not registered here — this section disposes of existing files only. The files each card creates or modifies, and the order in which they are touched, are listed in section 11.1; a card touches only the files it declares.

### 8.1 Root and infrastructure

| File | Decision | Action |
| --- | --- | --- |
| `.gitattributes` | Keep | Extend the LF policy to Go, SQL, CUE and PowerShell |
| `.gitignore` | Extend | Add Go, local DB, installer, generated CUE and local configuration outputs |
| `.prettierignore` | Keep | Scope it to TypeScript and Markdown |
| `LICENSE` | Keep | Continue MIT; add third-party notices separately |
| `package.json` | Keep | Describe the DSH adapter, not the whole toolbox runtime |
| `pnpm-lock.yaml` | Keep | Lock the DSH adapter and record actual resolved versions |
| `pnpm-workspace.yaml` | Extend only if needed | Add packages only for additional TypeScript adapters |
| `tsconfig.json` | Keep | DSH adapter and corpus generator only |
| `tsdown.config.ts` | Keep | Build the DSH adapter |
| `vitest.config.ts` | Extend | Keep coverage, add conformance tests, never hide Windows failures |
| `cordis.patch.yml` | Refactor | Register the DSH adapter and core-service connection settings |
| `README.md` | Rewrite | English platform entry point |
| `README.zh-CN.md` | Rewrite | Chinese entry point synchronized section by section |
| `README.toolbox.md` | Remove | Redundant after the main README and this plan |
| `README.toolbox.zh-CN.md` | Remove | Remove with its English peer |
| `.github/workflows/ci.yml` | Extend | Add four jobs: Go, CUE, a PostgreSQL service container and Windows filesystem. The Go job runs `gofmt -l`, `go vet` and `go test`; the CUE job runs `cue vet ./spec/...`; the database job runs migrations and the 50-way concurrency test inside a PostgreSQL 16 service container; the Windows filesystem job reproduces the two failures from section 7 on `windows-latest` to prevent regressions; the existing TypeScript job is unchanged |
| `.github/workflows/privacy-guard.yml` | Refactor | Validate generated rules instead of duplicating hand-written patterns |
| `.github/pull_request_template.md` | Rewrite | Use `EKB-nnn`, stage gates and bilingual checks |

### 8.2 TypeScript source

| File | Decision | New responsibility | New home (file · card) |
| --- | --- | --- | --- |
| `src/signature.ts` | Keep | DSH offline fallback and conformance reference; the platform core is implemented in Go | `internal/core/normalize.go`, `internal/core/fingerprint.go` (EKB-013) |
| `src/capture.ts` | Refactor | DSH payload conversion only; generic classification moves to Go | `internal/capture/pipeline.go`; generic classification in `internal/core/classify.go` (EKB-016, EKB-031, EKB-032) |
| `src/redact-patterns.ts` | Generate from shared rules | One source produces the TypeScript, Go and CI forms | single source `spec/redact.cue` generating `internal/redact/rules_gen.go` (EKB-014) |
| `src/redact.ts` | Keep | DSH offline fallback still uses it; the platform core is implemented in Go | `internal/redact/redact.go` (EKB-014) |
| `src/match.ts` | Demote | Offline DSH matching and conformance reference; authoritative search moves to PostgreSQL plus Go | `internal/core/tokenize.go`, `internal/core/jaccard.go` (EKB-015); candidates come from `errkb_search()` in `db/migrations/` (EKB-022 to EKB-024) |
| `src/inject.ts` | Keep | DSH-specific message caps and frequency | — (DSH adapter only) |
| `src/resolve-detect.ts` | Refactor | Emit outcomes only; the core database computes trust | trust in `errkb_trust()` in `db/migrations/0003_solutions.sql` (EKB-021) |
| `src/store.ts` | Demote | No longer the authority; Markdown import/export and offline queue conversion only | `internal/database/` (EKB-019 to EKB-026) |
| `src/state.ts` | Reduce | Connector health and unsent queue state only, never authoritative hits or trust | connector queue stays in TypeScript; daemon state in `internal/service/daemon.go` (EKB-028) and `internal/service/queue.go` (EKB-033) |
| `src/paths.ts` | Refactor | DSH configuration and offline queue paths only | — (DSH paths only) |
| `src/plugin.ts` | Refactor | DSH event adapter and solution-return client instead of a full pipeline | via Named Pipe to `internal/service/pipe.go` (EKB-038) |
| `src/tools.ts` | Refactor | Proxy DSH tools to the core API; define explicit degradation when disconnected | via Named Pipe to the core API; server side in `internal/mcp/tools.go` (EKB-037, EKB-038) |
| `src/index.ts` | Extend | Add `agent/request-error`, core service settings and live acceptance | — (EKB-039 changes the adapter only) |

### 8.3 Tests and seeds

| File | Decision | Action | New home (file · card) |
| --- | --- | --- | --- |
| `tests/signature.test.ts` | Keep | Generate Go conformance cases and keep constraining the DSH fallback | `spec/corpus/normalize.json`, `spec/corpus/fingerprint.json` via `tests/gen-corpus.ts` (EKB-009); Go side `internal/core/normalize_test.go` (EKB-013) |
| `tests/capture.test.ts` | Keep and split | Shared cases to corpus; DSH payload cases remain adapter tests | `spec/corpus/headline.json` (EKB-009); Go side `internal/capture/pipeline_test.go` (EKB-031, EKB-032) |
| `tests/redact.test.ts` | Keep and extend | Drive every output boundary from one sensitive-data case set | `spec/corpus/redact.json` (EKB-009); Go side `internal/redact/redact_test.go` (EKB-014) |
| `tests/redact-patterns.test.ts` | Refactor | Validate generated artifacts instead of two hand-written pattern sets | `internal/redact/rules_gen_test.go` (EKB-014) |
| `tests/match.test.ts` | Keep and extend | Add labelled corpus, a zero-false-merge gate and PostgreSQL candidate tests | `spec/corpus/tokenize.json`, `spec/corpus/match.json` (EKB-009); Go side `internal/core/rerank_test.go` (EKB-017), `internal/database/search_test.go` (EKB-024) |
| `tests/inject.test.ts` | Keep | Preserve DSH caps | `spec/corpus/notice.json` (EKB-009); no Go counterpart |
| `tests/resolve-detect.test.ts` | Refactor | Test outcome events, not connector-local trust | authoritative trust test in `internal/database/outcome_test.go` (EKB-021) |
| `tests/store.test.ts` | Fix, then demote | Fix both Windows failures; retain Markdown round trips | Markdown round trips in `internal/database/markdown_test.go` (EKB-025) |
| `tests/state.test.ts` | Rewrite | Queue and connector health only | daemon state in `internal/service/daemon_test.go` (EKB-028), `internal/service/queue_test.go` (EKB-033) |
| `tests/paths.test.ts` | Rewrite | Connector config and offline paths only | — |
| `tests/plugin.test.ts` | Extend | Request errors, disconnect and recovery | disconnect recovery in `internal/service/pipe_test.go` (EKB-029, EKB-038) |
| `tests/injection.test.ts` | Keep, route through core API | Prevent duplicate counting on the real connector path | `internal/mcp/tools_test.go` (EKB-037) |
| `tests/tools.test.ts` | Refactor | Test API proxy behavior and error mapping | `internal/mcp/tools_test.go` (EKB-037, EKB-038) |
| `tests/settings.test.ts` | Extend | Pipe, timeout and offline settings | config contract in `spec/config.cue` and `internal/contract/config_test.go` (EKB-007) |
| `tests/seeds.test.ts` | Keep | Verify PostgreSQL import and redaction | `internal/database/seed_test.go` (EKB-025) |
| `tests/.gitkeep` | Remove | The directory is not empty | — |
| `seeds/ERRORS.seed.md` | Keep and extend | Compatibility import sample and source for structured seed data | generated `seeds/*.json`; import tests (EKB-025) |

### 8.4 Documentation

| Previous file | Decision | Replacement |
| --- | --- | --- |
| `docs/设计说明书.md` | Removed | Chinese implementation plan |
| `docs/design-spec.md` | Removed | This file |
| `docs/discussions.zh-CN.md` | Removed | Decisions and open items are converted into plan sections and cards |
| `docs/discussions.md` | Removed | English plan sections |
| `docs/upgrade-tasks.zh-CN.md` | Removed | Continuous Chinese task sequence |
| `docs/upgrade-tasks.md` | Removed | Continuous English task sequence |

## 9 Target repository layout

```text
<repo-root>/
├── cmd/errkb/
├── internal/{contract,core,capture,database,service,mcp,ui,audit}/
├── db/migrations/
├── spec/{error-event.cue,config.cue,mcp-tools.cue,corpus/}
├── connectors/{codex,claude}/
├── src/                       # DSH TypeScript adapter
├── tests/
├── packaging/windows/
├── docs/{implementation-plan.md,implementation-plan.zh-CN.md}
└── seeds/
```

## 10 Task states and workflow

Use only `not started`, `in progress`, `accepted` and `blocked`. For every card: verify prerequisites, create `task/EKB-nnn-short-name`, mark it in progress, update tests before implementation, stay inside the declared scope, run card and common gates, demonstrate a real scenario, synchronize both languages, mark accepted with measured commands, then commit as `type: summary (EKB-nnn)`.

If a card fails acceptance twice, split it or move it out of v1. Do not lower the gate.

## 11 Ordered implementation cards

The files each card creates or modifies are listed in card order in section 11.1; that table is the working order.

### P0 Trusted baseline

| State | ID | Task | Prerequisite | Output and acceptance |
| --- | --- | --- | --- | --- |
| ☐ | EKB-001 | Establish a real Git workspace | None | Clone upstream `main`, copy this plan, record HEAD; rollback works |
| ☑ | EKB-002 | Freeze toolchain and dependency facts | 001 | Done: see 7.1 and 7.2; `node -v`, `pnpm -v`, `go version`, `psql --version` and `cue version` all measured; rc.2 vs rc.3 explained in writing; `pnpm install --frozen-lockfile` exits 0 |
| ☑ | EKB-003 | Repair the Windows test baseline | 001 | Done: PR #17 merged to `main`; `EPERM`/`EBUSY` count as lock contention and `mtimeMs` returns `undefined` only when the parent is a directory; `pnpm test` 15 files and 598 tests green, `store.ts` 99.32% statements / 97.17% branches; both failures were implementation bugs, tests unchanged |
| ☑ | EKB-004 | Approve product and DB distribution | 001 | Done: section 2 is final - the release does not promise zero dependencies (single answer: no); external PostgreSQL 16 in development, `EKB-051` manages the local instance at release; mandatory PostgreSQL and a single-file portable EXE cannot both be promised; downstream cards are `EKB-051` and `EKB-050` |
| ☑ | EKB-005 | Create repository skeleton and dual-toolchain CI | 002-004 | Done: Go 1.26.4 skeleton (`cmd/errkb`, eight `internal/*/doc.go` packages, three `spec/*.cue` files, `db/migrations/0001_init.sql`, `packaging/windows/build.ps1`); CI gained the go, cue, database and windows-filesystem jobs, and PR #21 has all six checks green; locally `gofmt -l` is empty, `go vet`/`go test`/`cue vet` exit 0, and `build.ps1` produces `errkb 0.1.0-skeleton windows/amd64`; the database job applies migrations in filename order and runs 50 concurrent sessions inside a PostgreSQL 16 container |

P0 gate: rollback works, the old suite is green, the new skeleton is green, and database distribution is documented.

### P1 Contracts and conformance corpus

| State | ID | Task | Prerequisite | Output and acceptance |
| --- | --- | --- | --- | --- |
| ☑ | EKB-006 | Define `ErrorEvent v1` | 005 | Done: closed `spec/error-event.cue` contract (`channel` uses the section 3.1 levels A-E, `category` reuses the four historical values from `src/capture.ts`, `#NoSensitive` carries the minimum redaction rules) and `internal/contract/error_event.go` rejects unknown fields and unredacted text; tests cover three valid events, one missing-required case per field (17), and four sensitive samples; `cue vet`, `go vet` and `go test` are green |
| ☑ | EKB-007 | Define configuration and connector capabilities | 006 | Done: `spec/config.cue` and `spec/connector-capability.cue` are the internal check (users still edit YAML/JSON) and `internal/contract/config.go` parses them with `gopkg.in/yaml.v3`, rejecting unknown keys; safe defaults (Named Pipe on, loopback HTTP off, HTTP needs a token of at least 32 characters and a loopback host), capture pause per connector/project, three separate retention periods, timeouts and offline spooling; the capability contract expresses degradation with a channel level plus missing fields, and host names stay open strings; six new tests, `cue vet`, `go vet` and `gofmt` are green. Deviation: `go.mod`/`go.sum` add `gopkg.in/yaml.v3` so the check accepts the YAML users actually write (outside this card four-file scope, stated in the PR) |
| ☑ | EKB-008 | Define MCP and local API contracts | 006 | Done: `spec/mcp-tools.cue` gives the six operations (lookup/search/record/feedback/list/stats) their own `schema_version`, candidates carry a match reason and an `approximate` flag with a ceiling of 3; `spec/local-api.cue` reuses the same payload definitions inside the Named Pipe envelope (envelope version, `request_id`, exactly one of `result`/`error`, closed `#ErrorCode`); `internal/contract/tools.go` and `local_api.go` share one `Validate()` set and strict JSON entry points; `record` reuses `ErrorEvent` validation, `search` and `lookup` stay separate, `feedback` only records an outcome; `cue vet`, `go vet`, `go test` and `gofmt` are green, and a 23-case probe passed (deleted afterwards because this card's file list has no test file) |
| ☑ | EKB-009 | Generate conformance data from current tests | 006 | Done: `tests/gen-corpus.ts` writes seven corpora, 133 cases total (normalize 57, fingerprint 19, headline 14, redact 20, tokenize 6, match 6, notice 11); reruns are byte-identical, `--check` exits 0 when current and 1 on drift; the six test files gained 133 corpus-reading assertions (original assertions kept), `pnpm test` 732 green; privacy-guard and acceptance-5 scans 0 hits |
| ☑ | EKB-010 | Build a labelled retrieval corpus | 009 | Done: 29 cases under `spec/corpus/labelled/` (same-error 10, distinct-lookalike 12, cross-language 7, one of them uncertain), each with paired inputs, a stated basis, baseline measurements, and confirmer/date (jingchangzhao-gif / 2026-10-19); four cases per lookalike kind; redaction and acceptance-5 scans 0 hits; the TS baseline wrongly auto-merges 6 of the 12 lookalikes and matches 12 of 16 same-error pairs |
| ☑ | EKB-011 | Define schema compatibility | 006-010 | Done: `spec/compat.cue` records fields, required sets, enums, unknown-field policy, version anchors and decoder bindings for the five contracts (error_event, config, connector_capability, mcp_tools, local_api) and defines rules R1-R16; a new `compat` CI job self-tests all 16 rules, exports the base and HEAD manifests and classifies the diff, requiring a strictly greater schema_version for breaking or meaning changes; measured: deleting the `command` field without a bump prints `violation [R9] ...` and exits 1, while the same deletion with a v2 bump prints `[R3] ... (breaking)` and exits 0; `cue vet ./spec/...` passes; the declared policy is reject, matching the four strict decoders (tolerant reads of persisted ErrorEvent rows are left to EKB-053) |

### P2 Deterministic Go core

| State | ID | Task | Prerequisite | Output and acceptance |
| --- | --- | --- | --- | --- |
| ☑ | EKB-012 | Safe error text and headline extraction | 009 | Done: `internal/core/headline.go` implements traceback-last-line, code-first and final-line fallback for all 14 corpus cases; imports only regexp/strings/unicode (zero I/O); repeated calls are identical; ANSI stripping and the 200-rune cap (199 chars + `…`) match the corpus; command-prefix composition stays in `capture()`, which has no corpus case, and is deferred to EKB-016/031; `go test ./internal/core/ -run Headline`, `go vet` and `gofmt -l` are clean and `spec/corpus/headline.json` is untouched |
| ☑ | EKB-013 | Normalization and fingerprinting | 009 | Done: `internal/core/normalize.go` shares the ANSI rule with headline extraction and matches all 57 corpus cases (UUID, hash, timestamp, duration, PID, port, line/column, temp dir, absolute-path collapsing, edge punctuation); `internal/core/fingerprint.go` only hashes `sha256(category + "\x00" + normalized)[:12]` and stays separate from normalization; production code on both sides is zero-I/O; 57 + 19 Go cases and 106 TypeScript assertions come from the same corpus; corpus unchanged (normalize.json `3237EF88…`, fingerprint.json `2B8383AA…`); `gofmt` / `go vet` clean |
| ☑ | EKB-014 | Shared redaction engine | 009, 011 | Done: `spec/redact.cue` is the single source (15 text rules, 3 Go home-prefix variants, 3 guard alternatives); `tests/gen-redact-rules.ts` generates `internal/redact/rules_gen.go`, `src/redact-patterns.ts` and the PATTERN block in `privacy-guard.yml`; the three artifacts share one SHA-256 source digest, `--check` is zero-diff and non-zero after tampering; `internal/redact/redact.go` is a pure zero-I/O function: public collapses absolute paths first and private collapses the home prefix first, then the rules apply in order; Go matches all 20 corpus cases and every rule hits its own example; `redact-patterns.test.ts` now validates generated results; deviations: exported `core.CollapseAbsolutePaths` wrapper and a `redact` contract in `spec/compat.cue` (tolerate); the write paths are not wired yet (EKB-019/031 call the engine before writing) |
| ☑ | EKB-015 | Tokenization and Jaccard | 009 | Done: `internal/core/tokenize.go` scans instead of using one regular expression (RE2 has no lookahead, so the order is placeholder, then a CJK run, then other letters/digits/underscores): placeholders stay whole, CJK runs become rune bigrams (a single character stays a unigram), Latin words pass through; the result is a sorted, duplicate-free slice that is byte-identical on a second call; `internal/core/jaccard.go` returns only a 0..1 number (never a merge boolean), and both-empty returns 0 to match TypeScript; all 6 `tokenize.json` cases pass and the 0.71/0.72/0.73 `match.json` boundary points hold within 1e-9; both files are zero-I/O; `gofmt` / `go vet` clean |
| ☑ | EKB-016 | Classification and transient noise | 012-015 | Done: `internal/core/classify.go` returns the four §1 classes (environment / dependency / build / tool-call) plus an explicit `unknown`; category priority tool-call → environment → dependency → build → unknown; transient priority is permanent code → transient code → permanent text → transient text → default permanent, so AUTH/QUOTA/INVALID_REQUEST/CONTEXT_OVERFLOW/NO_ADAPTER/UNKNOWN beat timeout and rate-limit wording, and the permanent-in-disguise cases such as `ENOTFOUND` have boundary tests; severity is warning (transient) or error (permanent); the classifier holds no threshold, no counter and no record decision, and never uses channel levels A–E as categories; `go test ./internal/core/ -run Classify` is green (several cases per class, priority, transient boundaries, cross combinations, determinism); both files are zero-I/O (regexp and strings only); deviation: the §1 category domain in `classify.go` and the `#Category` enum EKB-006 defined in `spec/error-event.cue` (tool/command-exit/llm/agent, a fingerprint input) are not unified yet, this card's scope is the classifier and its test only, and the connector mapping and contract alignment belong to EKB-031/032 |
| ☑ | EKB-017 | Candidate reranking and explanations | 015-016 | Done: pure `Rerank(query, candidates)` in `internal/core/rerank.go` with the weights declared in one place (token similarity 0.55 / error code 0.15 / command 0.10 / tool name 0.05 / environment 0.15; CJK bigrams are already inside `Tokenize` and get no separate weight) and the hard cap of 3; the result carries only `IssueID`/`Score`/`MatchedFields`/`Approximate`/`EnvironmentDiff` and no merge field (asserted with reflection); match reasons are the enumerable `fingerprint`/`error_code`/`command`/`token_similarity`/`environment`; environment differences are listed per field (os/shell/connector/runtime_versions.<key>) instead of only lowering the score; tie-break is score descending → exact first → `IssueID` ascending, identical for shuffled input; all 12 `distinct-lookalike` pairs pass the smoke test and none is reported as an exact hit. Key decision: exact means the fingerprint matches **and** the fields the fingerprint does not cover (error code, command, tool name) do not contradict each other — the same message under a different command is a different problem, which is exactly the shared-fingerprint trap in lookalike cases 005–008; `go test ./internal/core/ -run Rerank` is green and both files are zero-I/O (sort and strings only) |
| ☑ | EKB-018 | Cross-language differential suite | 012-017 | Done: `internal/core/diff_test.go` (an external `core_test` package, because `internal/redact` imports `internal/core` so the two cannot import each other from inside the package) compares all seven corpora field by field and reports corpus file, `case_id`, field name, Go value and TS value on failure; the TypeScript oracle is the expected value generated from the TS implementation by EKB-009 (the generator's `--check` keeps it current). Three registered divergences, each locked: `match` `matched`/`id`/`via`/`approximate` (`match.ts` is downgraded; Go owns tokenization and similarity, ranking is EKB-017 `Rerank`), `match.similarity` (TS defines it per route — 1 for exact, 0 for the code fallback, Jaccard for fuzzy — while Go only has the overlap, so the exact route differs), and `notice` (`inject.ts` is a DSH connector with no Go side). `spec/corpus/divergences.md` and the registry inside the test are kept in two-way sync, and a source scan fails if `internal/core` ever gains a `Notice` implementation or a `Match` function, which forces the registry update. P2 closure evidence: the zero-I/O import scan (13 non-test files across `internal/core` and `internal/redact`, no `os`/`net`/`net/http`/`os/exec`/`io/ioutil`/`math/rand`/`crypto/rand`) passes; `go test ./internal/core/ -run Diff` is green; all 12 labelled lookalike pairs still come back approximate; `pnpm test` passes 15 files / 732 tests |

P2 gate: the core package performs no I/O, model call or network access.

### P3 PostgreSQL authority

| State | ID | Task | Prerequisite | Output and acceptance |
| --- | --- | --- | --- | --- |
| ☑ | EKB-019 | Migration framework and schema v1 | 005, 011 | Done: `0001_init.sql` now carries all eleven entities of section 4 (events have no `issue_id`, `audit_log` has no foreign keys, redacted sample / normalized text / audit_log each have their own retention column, audit fields are present); `internal/database/migrate.go` records version + sha256 checksum in `schema_migrations`, takes a PostgreSQL advisory lock, uses one transaction per file and writes the ledger row only after success, and rejects an edited or missing applied file; 0001 was filled in place because it never touched persistent state (the local instance had zero tables, the CI job always starts a fresh container) and the evidence is recorded in `db/migrations/README.md`; six measured local PostgreSQL 16.15 tests are green (fresh schema with eleven tables, repeat run with no side effects, edited file rejected by checksum, failed file rolled back, concurrent migrators apply once, structural assertions) and the `psql -v ON_ERROR_STOP=1` path was measured too; deviation: new `github.com/jackc/pgx/v5` dependency (`go.mod`/`go.sum`); the `go` CI job has no database service, so the migration tests skip there and the local run carries the evidence |
| ☑ | EKB-020 | PL/pgSQL idempotent recording | 019 | Done: `errkb_record_event()` records inside the caller transaction with unique constraints plus `ON CONFLICT` (no Go-side check-then-write, so no advisory lock): an event is inserted once by `event_id` and never rewritten, and a replay that claims a different fingerprint raises `23505`; `issues` upsert on the exact `(fingerprint, project_key)` and maintain hits, first_seen and last_seen; every call appends one `occurrences` row (EKB-019's `(issue_id, event_id)` unique constraint is dropped, because it would collapse replays); the function computes no trust score (EKB-021's `errkb_trust()` owns that). Measured on local PostgreSQL 16.15 with `go test ./internal/database/ -v`, all green: ten replays of one `event_id` produce one event, one issue, ten occurrences and hits=10 while the stored headline stays unchanged; fifty concurrent calls (pgxpool, one connection per goroutine, one exact fingerprint) produce one issue, fifty occurrences and hits=50; a replay with a different fingerprint is rejected; two different messages produce two issues; a forced failure inside the transaction rolls events, issues and occurrences back together; an unknown connector returns a readable error. `gofmt -l` is empty and `go vet ./...` / `go test ./...` are green. Deviations: `internal/database/migrate_test.go` needed its repeat/concurrent assertions updated for the second migration (they now use the real `LoadMigrations` count instead of a hard-coded 1), and the function body starts with `#variable_conflict use_column` so OUT parameters do not collide with same-named columns |
| ☑ | EKB-021 | Solution versions and outcomes | 019 | Done: `0003_solutions.sql` moves `solutions` to the closed candidate/verified/deprecated/blocked machine (trigger `errkb_solution_status_transition` allows only candidate→verified/deprecated/blocked, verified→deprecated/blocked and blocked→deprecated, with deprecated terminal; every other move raises 23514; 0001's accepted→verified and rejected/superseded→deprecated are folded in before the new check); `solution_versions` gains `effective_at` and `applies_to` (section 6's applicable environment) and keeps body/preconditions/risk/source; `outcomes` gains a GIN index on environment; new deterministic `errkb_env_matches()` (os/shell/connector equality, every named runtime_versions key present, an empty environment matches everything) and the authoritative `errkb_trust(solution_id, environment)`: Laplace smoothing `(successes+2)/(successes+failures+4)`, so no evidence is 0.5, one success is 0.6 and five successes are 7/9; `unknown` counts as a sample but not in the denominator; same input, same output, no clock or randomness; no trust column exists, so a client has no write path. `internal/database/solution.go` provides CreateSolution, AddSolutionVersion (version = max+1 inside the statement, history cannot be overwritten), TransitionSolution, RecordOutcome (an empty environment falls back to the linked occurrence) and SolutionTrust. Measured on local PostgreSQL 16.15 with `go test ./internal/database/ -v`, all green: the legal chain passes, deprecated→verified and blocked→verified are rejected by the database, an out-of-enum value hits the CHECK, three revisions keep their history and the first body survives, windows success and linux failure are queried per environment while another runtime version has zero samples, an unknown outcome is stored as one sample, the 0.5/0.6/7/9 values are deterministic and failures lower the score, and `information_schema` has no trust column. `gofmt -l` is empty and `go vet ./...` / `go test ./...` are green |
| ☑ | EKB-022 | Exact retrieval | 019-021 | Done: `errkb_search()` in `db/migrations/0004_exact_search.sql` accepts exactly one selector (issue id, 12-hex fingerprint, error code, normalized message, command or tool name) with an optional `project_key`; anything else raises 22023, while a syntactically valid miss returns an empty set. The five equality predicates use `issues_pkey`, `issues_fingerprint_project_key_key`, `events_error_code_idx`, `events_command_idx`, `events_tool_name_idx` and `events_normalized_message_idx`, with the event dimensions walking back through `occurrences_event_idx` and `issues_pkey`. Matching is whole-value and case-sensitive; ids and fingerprints are format-checked before the equality, and there is no prefix, `LIKE` or `pg_trgm` path. The row order is fixed at hits desc, last_seen desc, id asc. `internal/database/search.go` is only a call wrapper; `search_test.go` covers hits and misses on all dimensions, prefix rejection, empty vs invalid, project scope, limit truncation, repeat-call stability, and, after seeding 3000 issues and events, `EXPLAIN` index scans without a sequential scan on all six plans (issue id is an Index Only Scan). Green on local PostgreSQL 16.15 |
| ☑ | EKB-023 | `pg_trgm` candidates | 019 | Done: `db/migrations/0005_trigram.sql` treats `pg_trgm` as an optional extension pinned to `public`; the three GIN trigram indexes (`events_normalized_message_trgm_idx`, `events_command_trgm_idx`, `events_tool_name_trgm_idx`) are created only when the extension is available, and `errkb_trigram_available()` reports it. `errkb_candidates(query, project, threshold, limit, message_weight, command_weight, tool_name_weight)` builds weighted candidates: the threshold must be greater than 0 and at most 1 (0 would degrade to a full scan), each similarity is computed once per field with `CASE WHEN <field> % $1 THEN similarity(...) ELSE 0 END`, matched fields are collected in `match_fields`, a field whose weight is zero cannot create a candidate on its own, the raw candidate pool is hard-capped at 500 before joining occurrences/issues, and the result is de-duplicated per issue and limited to `p_limit`; it returns `similarity`/`score`/`approximate` and no merge verdict field. When the extension is unavailable it takes a bounded fallback: whole-value equality on the three fields, the same `p_limit` cap, `degraded = true` and NULL similarity/score — never a silent empty set. `internal/database/candidates.go` is the typed wrapper (`CandidateQuery`/`Candidate`/`CandidateSet`, nullable values through `pgtype.Float8`, SQLSTATE 22023 mapped to a readable error). Measured on local PostgreSQL 16.15, `go test ./internal/database/ -run FuzzyCandidates -v` is green: related issues match with `normalized_message` in `match_fields`, unrelated issues do not appear; a reflection test proves `Candidate` has no merge field; weights are external (zeroing the command weight removes a command-only match); limit truncation; seven malformed-argument branches; inside a transaction `drop extension pg_trgm cascade` makes the call degrade to equality with `degraded` rows and rollback restores the extension; on 6000 rows `EXPLAIN` shows `Bitmap Index Scan` on `_trgm_idx`. Benchmarks (6000 rows, after `ResetTimer`): selective lookup 2.34 ms/op, command-heavy worst case 82.3 ms/op. Measured GIN vs GiST (3000 rows of one command pattern): a single-column hot lookup is faster with GiST (1.2–1.3 ms) than GIN (8.9–12.8 ms), but the full weighted function is comparable (GIN 53–62 ms, GiST 47–57 ms) and the GiST message index is already 1.67 MB on 6001 rows, so there is no clear overall win; the plan-mandated GIN is kept and the numbers are recorded. `gofmt -l` is empty and `go vet ./...` / `go test ./...` are green. Deviations: the extension is pinned to `schema public` and the test harness search_path is `<schema>, public` (`migrate_test.go`/`record_test.go`/`candidates_test.go`, otherwise `gin_trgm_ops` is invisible under a restricted search_path); the function carries a scoped `SET enable_seqscan = off` hint (221 ms vs 53 ms on a 23k-row probe), to be revisited against the 100k gate in EKB-027; benchmarks run `ANALYZE` first |
| ☑ | EKB-024 | Go reranking and environment filters | 017, 022-023 | Done: `internal/database/rerank.go` joins the five steps in section 5 order — exact issue id, exact fingerprint plus deterministic filters (error code, command, tool name), `pg_trgm` candidates (EKB-023), the pure Go reranker `core.Rerank` (EKB-017), and a top-three truncation that is persisted; `internal/core/rerank.go` is untouched and all SQL stays in the database layer. An exact fingerprint match is sorted first (score 1, `approximate=false`); every other candidate is `approximate=true`, and `AutoReusable()` is true only for a non-approximate candidate whose matched fields include `fingerprint`, so an approximate candidate can never be reused automatically. The candidate environment comes from the latest occurrence (`os`/`shell`/`runtime_versions`); a disagreement with the query environment is reported as `environment_diff` instead of silently lowering the score. Every search writes the top three rows (`candidate_rank`/`score`/`match_fields`/`approximate`/`environment_diff`) to `retrievals`, and a failed insert fails the search rather than losing the audit trail. Tests (`rerank_test.go`, green on local PostgreSQL 16.15): all twelve `distinct-lookalike` pairs produce zero false automatic merges (005–008 share the message and fingerprint and are kept approximate by the command contradiction); `same-error` matches 10/10 (nine fingerprint-equal pairs are exact; `same-error-009` is recorded as a recall gap and no threshold was lowered for it); all seven `cross-language` cases are found through the shared error code; four related issues are truncated to exactly three; approximate candidates are not auto-reusable while the exact one is; the `retrievals` row count equals the returned candidate count with ranks 1..3 and complete fields; the environment diff exposes `os` and `runtime_versions.node`; an empty request is rejected. Architecture boundary: `TestDiffNoIOImports` still passes, so `internal/core` has no I/O. `gofmt -l` is empty and `go vet ./...` / `go test ./...` are green |
| ☑ | EKB-025 | Markdown import and export | 019-024 | Done: `internal/database/markdown.go` parses and renders the legacy format. `ParseMarkdown` accepts `## E-\d+ · title` headers, the `<!-- errkb: sig= cat= code= first= device= proj= -->` metadata comment (values percent-encoded for `[%\s>]`), English and Chinese field labels, a ` ```text` fence for the raw message, indented `- Fix:`/`- Notes:` continuations and `<!-- errkb-history: vN -->` markers, and rejects conflict markers, a missing metadata comment, unknown statuses, duplicate ids and unterminated fences. The fingerprint is recomputed as `core.Signature(category, core.Normalize(raw))`, matching the seed's `sig` bytes exactly (`88fe8fb1a189` / `bcf6cca60510` / `3a12e3627689`). Import writes issues/events/occurrences/solutions/solution_versions directly in one transaction (import is an exchange, not a new observation, so it does not call `errkb_record_event()`); the event id is derived from fingerprint plus project as a UUID, so a repeat import updates the same occurrence instead of duplicating it; every text goes through `redact` before the write. The legacy mapping is explicit: issue `open/fixed/wontfix` → `open/resolved/wontfix` (and `misjudged`), solution `open→candidate`, `fixed→verified`, `wontfix→blocked`, and back `candidate→open`, `verified→fixed`, `blocked→wontfix`, `deprecated→open`. The multi-version export policy is explicit: the newest body is the visible Fix, older revisions follow as plain text behind `<!-- errkb-history: vN -->` markers, which the TypeScript parser reads as-is; `Notes` are stored as the newest version's `risks` and exported back unchanged, while `preconditions` have no slot in the old format and are not exported. Export orders by `created_at, fingerprint, project_key` (rows created in one transaction share `created_at` and `id` is random) and renumbers `E-0001…`. `seeds/ERRORS.seed.md` stays read-only; the structured `seeds/ERRORS.seed.json` (v1, 3 entries) is produced by `ExportSeedJSON`, rewritten with `ERRKB_WRITE_SEED_JSON=1`. The export is provably readable by the current `src/store.ts`: the test bundles a probe that imports it with tsdown and runs it under node, skipping when pnpm/node are absent (the CI go job has no Node). Measured on local PostgreSQL 16.15, `go test ./internal/database/` is green: seed parsing/import/statuses, a second import adds no version, export re-imported into an empty schema is byte-identical, hand edits win after re-import, the three legacy statuses round-trip, a backwards transition is rejected by the database, redaction happens before the write (no plaintext secret in events/issues/versions, `<secret>` present), and device/project/hits round-trip; there is no Markdown read-authority path. Privacy-guard false positives were fixed: JSON and Go literals with a colon followed by an escaped newline look like a Windows path, so the exporter writes the colon and newline as `\u003a\u000a` and the test fixture builds the document from `[]string` lines. `gofmt -l` is empty, `go vet ./...` and `go test ./...` are green (database 22.2 s), and the whole-tree privacy scan has 0 hits. Deviations: occurrences are selected then updated (0002 dropped the `(issue_id,event_id)` unique constraint) and export order relies on `created_at, fingerprint, project_key` for cross-database determinism |
| ☑ | EKB-026 | Backup, restore, retention and deletion | 019 | Done: `db/migrations/0006_retention.sql` adds the indexes retention and scoped deletion need and makes "the audit survives business deletion" a database rule: a BEFORE DELETE trigger on `audit_log` raises 55000 for every caller except the retention job (session setting `errkb.audit_retention=on`), and BEFORE TRUNCATE rejects a truncate; `errkb_purge_retention(sample_days, normalized_days, audit_days, now)` keeps the three retention classes independent — samples past the sample window are cleared in place (only on rows still inside the normalized window), events past the normalized window are deleted whole, audit rows past the audit window are removed, and every run writes one `retention.purge` audit row with the three counts (negative days raise 22023); `errkb_delete_preview(project, connector, before)` previews the row counts of the eight business kinds (at least one filter is required; audit is always 0), and `errkb_delete_scope(...)` really deletes the derived data in one transaction (outcomes → retrievals → notices → occurrences → solution_versions → solutions → events → issues) and writes one `delete.scope` audit row with the scope, the counts, the actor and the time; audit rows are never deleted with the business data. `internal/database/backup.go` backs up and restores the whole database through the external `pg_dump -Fc` / `pg_restore`: the backup writes a `<dump>.json` manifest (sha256, size, per-table row counts, ledger row count and highest version) and verifies with `pg_restore --list` that the archive contains all eleven business tables; a restore only accepts a database that does not exist yet, checks the checksum and the size first, then creates the database, runs `pg_restore --exit-on-error`, reconnects, compares every table count and the ledger, and proves the `events` fingerprint CHECK and the `issues` fingerprint unique index survived; a tampered dump and an existing target are both refused. Measured on local PostgreSQL 16.15, `go test ./internal/database/ -run 'TestBackup|TestDelete|TestRetention'` is green: after the restore all eleven table counts, the ledger, the solution body and the issue title match; a second restore into the same database is refused; a tampered dump fails the checksum; a project deletion's preview equals the real counts, the `delete.scope` row records the actor and the counts, and the older audit row is still there; the time boundary keeps the row exactly at the cutoff (strictly-before comparison); a connector deletion keeps the other connector's events and the aggregated issue; all three retention classes act independently (a 10-day sample is cleared, a 100-day event is removed, a 400-day audit row is removed) while the new sample and the new audit row stay; a direct `delete from audit_log` is rejected with 55000. `gofmt -l` empty, `go vet ./...` and `go test ./...` green (database 30.1 s), whole-tree privacy scan 0 hits. Deviations/boundaries: backup and restore depend on the local `pg_dump`/`pg_restore` (`ERRKB_PG_BIN` can point at the directory; PATH and `%ProgramFiles%\PostgreSQL\16\bin` are the defaults) — a documented shortcut; the rebuilt DSN supports only `sslmode=disable|require`; deleting by connector does not remove the issue, because an issue aggregates evidence across connectors; OCR frames never reaching the database is a capture-layer guarantee, not part of this card |
| ☑ | EKB-027 | Performance and crash gates | 020-026 | Done: the benchmark, environment, data and query distribution, and the `EXPLAIN ANALYZE` plans are recorded in `docs/benchmarks.md`; `internal/database/bench_test.go` generates 100,000 issues / 221,803 events with deterministic SQL (90,000 single occurrence, 9,900 small tail, 100 heavy tail, categories 45/35/15/5, 11.1% non-ASCII, five projects of 20,000) and reproduces the same distribution from the same parameters (`TestPerformanceGeneratorIsDeterministic`); the smoke (4,000 issues, runs whenever a database is configured) is green and the four crash scenarios use a real `Process.Kill`. The first 100k gate missed (`pg_trgm` p95 155.3 ms, full retrieval p95 226.0 ms), so the card was marked ⚠ blocked; both causes were then fixed: (1) `db/migrations/0007_occurrence_lookup.sql` adds `occurrences_issue_latest_idx (issue_id, occurred_at desc, id desc)` — EKB-020 dropped the `(issue_id, event_id)` unique constraint and with it the only index covering the latest-occurrence lookup, so one lateral cost 206.8–236.9 ms and now costs 0.025–0.28 ms; (2) `internal/database/candidates.go` adds `TrigramQueryMaxChars = 64`, capping the `pg_trgm` candidate input at 64 characters (the GIN scan grows with the number of query trigrams: 100 characters 74–89 ms, 64 characters 47–58 ms), while the Go reranker still ranks with the full message and the EKB-024 gates are unchanged (`distinct-lookalike` 12/12, `same-error` 10/10 with `same-error-009` still the recorded recall gap); `TestFuzzyCandidatesCapTheQueryText` locks the cap. Re-measured with the same generator, samples and method (200 rounds per class): exact p50 0.0 / p95 0.6 / p99 0.6 ms; `pg_trgm` p50 65.5 / p95 81.7 / p99 84.7 ms; full retrieval p50 68.7 / p95 84.5 / p99 87.9 ms — all three under 150 ms. The crash gate: uncommitted write, committed write, batch write and a migration in flight all leave committed data intact, no orphan occurrence, a consistent ledger (7 rows after the kill, then a rerun applies only the dynamic probe version 0008 and reaches 8) and no advisory lock residue. GiST measured 444–593 ms (six to seven times worse than GIN), so EKB-023's GIN choice stands. `gofmt -l` is empty, `go vet ./...` and `go test ./...` are green (database 55.9 s, including the smoke and crash suites); the full 100k gate stays manual behind `ERRKB_PERF_BENCH=1`, and the P3 gate is met |

### P4 Local service, CLI and high-reliability capture

| State | ID | Task | Prerequisite | Output and acceptance |
| --- | --- | --- | --- | --- |
| ☑ | EKB-028 | `errkb daemon` | 019-027 | Done: `internal/service/daemon.go` enforces a single instance with a Windows named mutex (default `Local\errkb-daemon` through kernel32 `CreateMutexW`/`CloseHandle`), never a PID file; the kernel releases the mutex when the process dies, and a test kills a real child process that holds it and then takes the mutex and restarts the daemon successfully. Lifecycle: `StartDaemon` takes the mutex, opens the log, opens the pool and verifies that the highest `schema_migrations` version equals the last `LoadMigrations` entry (a mismatch fails the start, so the daemon never serves from an unknown schema); `BeginRequest` registers in-flight work, and `Shutdown` marks the daemon closing, rejects new requests, waits for the in-flight count to reach zero, then closes the owned pool, closes the log and releases the mutex (no `os.Exit`, so no half-written transaction). The test calls `Shutdown` before committing an in-flight transaction, proves it keeps waiting, and finds exactly one committed row afterwards. `Health` is machine-readable: version, `ok`/`degraded`, database connectivity, current/expected migration and whether they match, queue backlog (an injectable hook returning 0 until EKB-033), pool total/in_use/idle connections and the in-flight count; `RefreshHealth` writes `health.json` atomically through a temp file and a rename. Every log line goes through `internal/redact.Text(share=public)` before the write, rotates at `--log-max-bytes` (default 4 MiB) keeping one `.1` backup, and only touches the file, never stdout (stdout stays free for the EKB-037 MCP stdio protocol). `cmd/errkb/daemon.go` provides `runDaemon` (`--dsn`, `--migrations`, `--lock-name`, `--health-file`, `--log-file`, `--log-max-bytes`, `--health-interval`, SIGINT/SIGTERM through `signal.NotifyContext`); the subcommand dispatch table belongs to EKB-030, so this card only provides the entry point. Measured on local Windows with `go test ./internal/service/ -v`, all green: a second instance is rejected with the mutex name in the error, a killed holder leaves no stale lock and the daemon restarts, a graceful stop waits for an in-flight transaction, the health fields and the JSON document are complete, an unmigrated schema is rejected without leaking the mutex, and logs are redacted, capped and do not reach stdout; `gofmt -l` is empty and `go vet ./...` / `go test ./...` are green (`internal/database` 53.7 s, `internal/service` 5.5 s). Deviations: Windows-only build (the named mutex and the Named Pipe host are both Windows targets); `runDaemon` is not called from `main()` yet (EKB-030 owns the dispatch); the queue backlog is a hook placeholder until EKB-033 |
| ☑ | EKB-029 | Named Pipe API | 008, 028 | Done: `internal/service/pipe.go` carries local RPC over a Windows Named Pipe (default `\\.\pipe\errkb`), `internal/service/api.go` only validates the envelope, checks the version, maps error codes and routes, and `pipe_test.go` covers 17 acceptance checks. **No TCP by default** is structural: the package never imports `net/http` and calls no `Listen` function, and a runtime check compares this process's listening TCP table (from `GetExtendedTcpTable`, sanity-checked with a listener the test opens itself) before and after the pipe starts — it is unchanged. The ACL grants only the current user and SYSTEM: the SDDL comes from the current process token (`D:P(A;;GA;;;<sid>)(A;;GA;;;SY)`, never Everyone, Users or Authenticated Users), and the test connects, reads the live DACL back with `GetSecurityInfo`, parses the ACE SIDs and asserts the set is exactly `{current user SID, S-1-5-18}` (Windows canonicalizes SIDs to aliases — an Administrator prints `LA` — so the check compares SIDs, not the input text). Versioning: the envelope and every operation payload require `schema_version="v1"` (`contract.DecodePipeRequest` / `requireVersion`); a missing or old version answers `invalid_request` with a readable `want "v1", got ...` message, and an unknown operation is rejected while the raw operation is echoed back to the caller. Error codes use the `spec/local-api.cue` closed set — `invalid_request`, `not_found`, `unavailable`, `conflict`, `internal`; a backend `*APIError` passes through, `context.Canceled`/`DeadlineExceeded` become retryable `unavailable`, everything else becomes `internal`. Disconnects are explicit: with no server `winio.DialPipeContext` fails fast (measured under 1 s, no hang); an oversized frame (1 MiB cap by default) answers `invalid_request` and closes the connection; the `PipeOptions.BeginRequest` hook rejects new requests with retryable `unavailable` while the daemon stops. Concurrency: each connection answers its frames sequentially on its own goroutine and connections do not share a stream, so 6 connections × 8 frames run concurrently with every `request_id` and echoed note verified — no interleaving. The layer neither logs captured text nor carries business logic: it never prints, `api.go` imports neither `internal/database` nor pgx, and it only hands validated requests to the `Backend` interface (the real backend is wired by EKB-030/037). One more test pins that a backend returning a payload the contract rejects is caught by the dispatcher and answered as `internal`. Measured locally: `go test ./internal/service/ -v` all green (17 pipe tests plus 7 daemon tests, 4.2 s); `gofmt -l` empty, `go vet ./...` and `go test ./...` green (`internal/database` 43.4 s). Deviations: adds `github.com/Microsoft/go-winio v0.6.2` and `golang.org/x/sys v0.32.0` (Named Pipes and ACLs require the Win32 API, so go.mod/go.sum change outside the three-file landing); the real `Backend` lands with EKB-030/037; per section 6 this card implements no HTTP at all (so “off by default” cannot be switched on by accident — a future HTTP path must default to false, bind loopback only and require a random token) |
| ☐ | EKB-030 | Base CLI | 024, 029 | search, show, record, list, feedback and stats with human and JSON output |
| ☐ | EKB-031 | `errkb run --` | 016, 029 | Capture stdout, stderr, exit status, command and environment; repeat finds same issue |
| ☐ | EKB-032 | Log tailing | 016, 029 | UTF-8, UTF-16, rotation, truncation and duplicate suppression |
| ☐ | EKB-033 | Redacted offline queue | 014, 028 | Redact before queue; idempotent replay; size and retention caps |
| ☐ | EKB-034 | `errkb doctor` | 028-033 | Versions, DB, migrations, extension, Pipe, connectors, queue and privacy status |
| ☐ | EKB-035 | CLI technical-error loop | 030-034 | First real failure creates an issue; second returns the known solution and records retrieval/outcome |

P4 is the first usable MVP gate and has no model dependency.

### P5 Host adapters and pre-diagnosis return

| State | ID | Task | Prerequisite | Output and acceptance |
| --- | --- | --- | --- | --- |
| ☐ | EKB-036 | Connector SDK and capability probe | 006-008, 029 | Uniform capture, lookup, notice and feedback with explicit degradation |
| ☐ | EKB-037 | MCP stdio server | 008, 030 | Real clients can call all core tools; docs state MCP is not passive capture |
| ☐ | EKB-038 | Refactor DSH into an adapter | 003, 036 | Plugin and tools call core API; explicit offline behavior; no duplicate counts |
| ☐ | EKB-039 | Finish missing DSH behavior | 038 | Request-error and providers work; live profile capture and return pass |
| ☐ | EKB-040 | Codex adapter | 036-037 | Probe current capabilities, then close through supported hooks/plugins/MCP; UI scraping is not primary |
| ☐ | EKB-041 | Claude Code adapter | 036-037 | Probe hooks, structured output and MCP, then close the loop |
| ☐ | EKB-042 | Templates for other hosts | 031-037 | Generic CLI/log/manual templates without false dedicated-support claims |
| ☐ | EKB-043 | Connector compatibility matrix | 038-042 | Version, channel, fields, pre-diagnosis return and fallback measured for each host |

### P6 Windows product UI

| State | ID | Task | Prerequisite | Output and acceptance |
| --- | --- | --- | --- | --- |
| ☐ | EKB-044 | Tray and capture state | 028, 034 | Running, paused, DB fault and queue states; clean exit |
| ☐ | EKB-045 | Browser-style omnibox | 024, 044 | Search IDs, text, codes or commands; keyboard navigation and match explanation |
| ☐ | EKB-046 | Issue detail and solution editing | 021, 045 | Occurrences, environment, versions and outcomes; audited edits |
| ☐ | EKB-047 | Connector and privacy settings | 007, 043-046 | Enablement, scope, retention and deletion |
| ☐ | EKB-048 | Notifications and one-click copy | 024, 044-047 | Top one to three with applicability and confidence; no focus steal or execution |
| ☐ | EKB-049 | Metrics and audit view | 021, 048 | Hit and solution rates, injected size and clearly estimated token savings |

### P7 Packaging, release and maintenance

| State | ID | Task | Prerequisite | Output and acceptance |
| --- | --- | --- | --- | --- |
| ☐ | EKB-050 | Windows release binary | 035, 049 | Starts without Go installed; reproducible resources and version info |
| ☐ | EKB-051 | PostgreSQL installation lifecycle | 004, 027, 050 | Install, start, upgrade, backup, stop and uninstall; no orphan service |
| ☐ | EKB-052 | Clean-VM acceptance | 050-051 | First-run wizard through a real error loop; standard-user and reboot behavior tested |
| ☐ | EKB-053 | Upgrade and compatibility | 011, 052 | Old config, schema, Markdown and adapters upgrade without loss and can roll back |
| ☐ | EKB-054 | Release workflow | 050-053 | Tags, changelog, checksums, SBOM, third-party notices and signing policy |
| ☐ | EKB-055 | Bilingual documentation closeout | 054 | READMEs, plan, install, uninstall, privacy and connector matrix synchronized; redundant toolbox READMEs removed |

### 11.1 File order by card

This table lists, in card order, the files each card must land; `→` means in that order. All paths are relative to the repository root. A file that reappears in a later card is modified further, not rebuilt. In P0 the cards 002, 003 and 004 may run in parallel after 001, and 005 waits for all three to pass; in P5 cards 040 and 041 may run in parallel but must follow 036 and 037.

| Card | Files (in order) |
| --- | --- |
| EKB-001 | No file change (clone the repository, add this file and its Chinese twin, record HEAD) |
| EKB-002 | `package.json`, `pnpm-lock.yaml` (freeze the version records); record local Go, PostgreSQL and CUE versions in this file |
| EKB-003 | `src/store.ts` → `tests/store.test.ts` |
| EKB-004 | Section 2 of this file (written decision on database distribution; no code) |
| EKB-005 | `go.mod`, `go.sum` → `cmd/errkb/main.go` → eight empty `internal/{contract,core,capture,database,service,mcp,ui,audit}/doc.go` packages → empty `spec/{error-event,config,mcp-tools}.cue` schemas → `db/migrations/0001_init.sql` shell → `packaging/windows/build.ps1` shell → modify `.github/workflows/ci.yml`, `.gitignore`, `.gitattributes` |
| EKB-006 | `spec/error-event.cue` → `internal/contract/error_event.go` → `internal/contract/error_event_test.go` |
| EKB-007 | `spec/config.cue` → `spec/connector-capability.cue` → `internal/contract/config.go` → `internal/contract/config_test.go` |
| EKB-008 | `spec/mcp-tools.cue` → `spec/local-api.cue` → `internal/contract/tools.go`, `internal/contract/local_api.go` |
| EKB-009 | `tests/gen-corpus.ts` → `spec/corpus/{normalize,headline,redact,fingerprint,tokenize,match,notice}.json` → the six existing test files gain corpus assertions; original assertions stay |
| EKB-010 | `spec/corpus/labelled/{same-error,distinct-lookalike,cross-language}.json` → `spec/corpus/labelled/README.md` |
| EKB-011 | `spec/compat.cue` → a compatibility job in `.github/workflows/ci.yml` |
| EKB-012 | `internal/core/headline.go` → `internal/core/headline_test.go` |
| EKB-013 | `internal/core/normalize.go` → `internal/core/fingerprint.go` → two tests against `normalize.json` and `fingerprint.json` |
| EKB-014 | `spec/redact.cue` (single source) → `tests/gen-redact-rules.ts` → `internal/redact/rules_gen.go`, `src/redact-patterns.ts` (becomes generated), rule validation in `privacy-guard.yml` → `internal/redact/redact.go` → `rules_gen_test.go`, `redact_test.go` |
| EKB-015 | `internal/core/tokenize.go` → `internal/core/jaccard.go` → matching tests |
| EKB-016 | `internal/core/classify.go` → `internal/capture/pipeline.go` → `internal/core/classify_test.go`, `internal/capture/pipeline_test.go` |
| EKB-017 | `internal/core/rerank.go` → its tests |
| EKB-018 | `internal/core/diff_test.go` (field-by-field corpus comparison); intentional divergences recorded in `spec/corpus/divergences.md` |
| EKB-019 | `db/migrations/0001_init.sql` (empty shell replaced with the section 4 entities) → `internal/database/migrate.go` → `internal/database/migrate_test.go` → `db/migrations/README.md` |
| EKB-020 | `db/migrations/0002_record.sql` (`errkb_record_event()`) → `internal/database/record.go` → `internal/database/record_test.go` (50-way concurrency) |
| EKB-021 | `db/migrations/0003_solutions.sql` (`errkb_trust()`) → `internal/database/solution.go` → `internal/database/outcome_test.go` |
| EKB-022 | `db/migrations/0004_exact_search.sql` → `internal/database/search.go` → `internal/database/search_test.go` |
| EKB-023 | `db/migrations/0005_trigram.sql` (`pg_trgm` + GIN) → `internal/database/candidates.go` → benchmark tests |
| EKB-024 | `internal/database/rerank.go` (consumes `match.json`) → tests with the zero-false-merge gate |
| EKB-025 | `internal/database/markdown.go` → `internal/database/markdown_test.go` → `internal/database/seed_test.go`; `seeds/ERRORS.seed.md` stays read-only, generated `seeds/*.json` |
| EKB-026 | `db/migrations/0006_retention.sql` → `internal/database/backup.go` → restore test (empty-database restore) |
| EKB-027 | `db/migrations/0007_occurrence_lookup.sql` → `internal/database/candidates.go` (64-rune trigram candidate cap) → `internal/database/bench_test.go` (100k-issue dataset) → query plans recorded in `docs/benchmarks.md` |
| EKB-028 | `cmd/errkb/daemon.go` → `internal/service/daemon.go` → `internal/service/daemon_test.go` |
| EKB-029 | `internal/service/pipe.go` → `internal/service/api.go` → `internal/service/pipe_test.go` |
| EKB-030 | `cmd/errkb/main.go` (subcommand dispatch) → `cmd/errkb/cli.go` → `internal/service/client.go` |
| EKB-031 | `internal/capture/run.go` → `internal/capture/run_test.go` |
| EKB-032 | `internal/capture/tail.go` → `internal/capture/tail_test.go` |
| EKB-033 | `internal/service/queue.go` → `internal/service/queue_test.go`; `src/state.ts` keeps only the connector-local queue |
| EKB-034 | `cmd/errkb/doctor.go` → `internal/service/doctor.go` |
| EKB-035 | `tests/e2e/` (real command scenario scripts) → one end-to-end demo (first issue created, second run returns the known solution) |
| EKB-036 | `spec/connector.cue` → `internal/capture/connector.go` → capability probe tests |
| EKB-037 | `internal/mcp/server.go` → `internal/mcp/tools.go` → `internal/mcp/tools_test.go` → `cmd/errkb/mcp.go` |
| EKB-038 | `src/plugin.ts` → `src/tools.ts` → `src/index.ts` → `tests/plugin.test.ts` (disconnect and recovery) |
| EKB-039 | `src/index.ts` (wire `agent/request-error`) → `src/resolve-detect.ts` → `tests/injection.test.ts` |
| EKB-040 | `connectors/codex/probe.md` (record current capability first) → `connectors/codex/` connector files |
| EKB-041 | `connectors/claude/probe.md` → `connectors/claude/` connector files |
| EKB-042 | `connectors/templates/cli-log/config.yaml` → `connectors/templates/cli-log/README.md` |
| EKB-043 | `docs/connector-matrix.md` (bilingual) |
| EKB-044 | `internal/ui/tray.go` → `internal/ui/status.go` → `cmd/errkb/ui.go` |
| EKB-045 | `internal/ui/omnibox.go` → `internal/service/search_api.go` → empty-state and keyboard-navigation tests |
| EKB-046 | `internal/ui/detail.go` → `internal/service/edit_api.go` → audit assertions |
| EKB-047 | `internal/ui/settings.go` → reuse `internal/contract/config.go` (no second config model) |
| EKB-048 | `internal/ui/notify.go` → `internal/service/notice.go` |
| EKB-049 | `internal/audit/log.go` → `internal/audit/stats.go` → `internal/ui/stats.go` |
| EKB-050 | `cmd/errkb/version.go` → `packaging/windows/versioninfo.json` → `packaging/windows/build.ps1` (real build) |
| EKB-051 | `packaging/windows/postgres.ps1` → `packaging/windows/installer.iss` → `internal/service/pg_manage.go` |
| EKB-052 | `docs/acceptance-vm.md` → `packaging/windows/vm-acceptance.ps1` |
| EKB-053 | `spec/compat.cue` (extended) → `db/migrations/testdata/upgrade/` → `internal/database/upgrade_test.go` |
| EKB-054 | `packaging/windows/release.ps1` → `.github/workflows/release.yml` → `CHANGELOG.md` |
| EKB-055 | `README.md` → `README.zh-CN.md` → remove `README.toolbox.md`, `README.toolbox.zh-CN.md` → `docs/install.md`, `docs/privacy.md` → synchronize this file with its Chinese twin section by section |

## 12 Common quality gate

Run the applicable commands for every card:

```powershell
pnpm install --frozen-lockfile
pnpm build
pnpm typecheck
pnpm lint
pnpm format:check
pnpm test

go build ./...
go vet ./...
go test ./...
gofmt -l .

cue vet ./spec/...
errkb db migrate --check
```

New behavior has tests; fixes have regressions; all boundaries validate inputs; all persisted text is redacted; capture and return failures never interrupt the host; approximate matches never auto-merge; READMEs make no unverified capability claims; both languages change together; every phase ends with a real scenario, not unit tests alone.

## 13 Metrics

Track exact-fingerprint hit rate, approximate top-one and top-three accuracy, false merges with a target of zero, verified solution success/failure, repeated diagnosis count, retrieval p50/p95, capture-to-notice latency, returned characters and estimated tokens, manual corrections, and DB/queue/connector faults.

Token savings are estimates, not exact facts. The system can measure what it injected and state the assumptions used to estimate avoided diagnosis.

## 14 Post-v1 candidates

Re-evaluate only after P7: per-host UI Automation where structured interfaces do not exist; opt-in non-persistent OCR; evidence-backed requirement-understanding and hallucination incidents; approved and audited fix execution; embeddings only after deterministic retrieval misses measured targets; team/cloud sync only after identity, authorization, key and deletion design.

## 15 Next action

Start `EKB-001`: create a fresh real Git clone, carry both plan files into it, and record upstream HEAD and the two current failing tests. Do not begin with Go implementation or window monitoring.
