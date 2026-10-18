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
| ☐ | EKB-017 | Candidate reranking and explanations | 015-016 | Score, matched fields, environment differences and approximate flag; no auto merge |
| ☐ | EKB-018 | Cross-language differential suite | 012-017 | Entire corpus matches; every intentional divergence documented and tested |

P2 gate: the core package performs no I/O, model call or network access.

### P3 PostgreSQL authority

| State | ID | Task | Prerequisite | Output and acceptance |
| --- | --- | --- | --- | --- |
| ☐ | EKB-019 | Migration framework and schema v1 | 005, 011 | All entities, constraints, audit fields and idempotent migrations |
| ☐ | EKB-020 | PL/pgSQL idempotent recording | 019 | Replays deduplicate; 50 concurrent exact fingerprints produce one issue and 50 occurrences |
| ☐ | EKB-021 | Solution versions and outcomes | 019 | Candidate, verified, deprecated and blocked with environment-specific results |
| ☐ | EKB-022 | Exact retrieval | 019-021 | Indexed ID, fingerprint, code, command and tool lookup |
| ☐ | EKB-023 | `pg_trgm` candidates | 019 | Weighted fields and measured GiST/GIN choice; bounded fallback |
| ☐ | EKB-024 | Go reranking and environment filters | 017, 022-023 | Explained top three; zero false auto merges on corpus |
| ☐ | EKB-025 | Markdown import and export | 019-024 | Import current seed, export readable by current parser, preserve solution history |
| ☐ | EKB-026 | Backup, restore, retention and deletion | 019 | Empty-db restore, scoped deletion and retained audit |
| ☐ | EKB-027 | Performance and crash gates | 020-026 | 100k-issue plan recorded; target p95 under 150 ms; committed data survives interruption |

### P4 Local service, CLI and high-reliability capture

| State | ID | Task | Prerequisite | Output and acceptance |
| --- | --- | --- | --- | --- |
| ☐ | EKB-028 | `errkb daemon` | 019-027 | Single instance, graceful stop, health and controlled logs |
| ☐ | EKB-029 | Named Pipe API | 008, 028 | No TCP by default; Windows-user access control; versioned requests |
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
| EKB-027 | `internal/database/bench_test.go` (100k-issue dataset) → query plans recorded in `docs/benchmarks.md` |
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
