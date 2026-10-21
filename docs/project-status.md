# Project status

[简体中文](project-status.zh-CN.md)

Status date: 2026-10-22

This document answers one question: **what can be trusted and used in the project right now?** The implementation plan describes the target and task order; this page records the current delivery reality. If a status claim here conflicts with an older completion note, re-test the behavior and update both documents in the same change.

## Executive summary

The Error Knowledge Base is no longer only a DSH plugin. Its core architecture is a local, multi-host error-reuse platform:

```text
DSH / Codex / Claude / generic CLI / MCP
                    │
                    ▼
       capture → redact → normalise → fingerprint
                    │
                    ▼
             PostgreSQL knowledge base
                    │
                    ▼
          exact search → fuzzy top 3 → known fix
```

The core, database, CLI, daemon, MCP server and first host connectors are substantially implemented. The project is **not yet a finished Windows toolbox product** because the executable, launcher, installer and release bundle do not yet represent one consistent revision.

## Status vocabulary

| Word | Meaning |
| --- | --- |
| Implemented | Source code and automated tests exist |
| Verified | The behavior ran successfully in the stated real environment |
| Accepted | All card-specific and common gates passed, including required manual checks |
| Releasable | One versioned artifact passed clean-machine install, launch, restart, upgrade and uninstall |

An implemented feature is not automatically verified, accepted or releasable.

## What is implemented

- Deterministic classification, headline extraction, redaction, normalisation and fingerprinting.
- PostgreSQL migrations `0001`–`0008`, idempotent event recording, solution versioning, outcomes, trust calculation, exact search and trigram/Jaccard retrieval.
- Windows daemon with a Named Pipe API, health document, redacted log and single-instance lock.
- CLI commands for search, show, record, list, feedback, stats, wrapping commands, database migration/status, doctor, MCP and version output.
- MCP stdio server and the DSH, Codex CLI and Claude Code connector paths documented in the compatibility matrix.
- Native tray/search models and service models for issue detail, settings, statistics, auditing and notifications.
- Installer-source support for schema migration and per-user HKCU logon startup.

## What is verified

| Area | Evidence level |
| --- | --- |
| TypeScript adapter | 733 tests pass; 99.4% statement/line coverage in the 2026-10-22 local check |
| Go packages without a required live database | `go test ./...`, `go vet ./...` and a fresh build pass locally |
| CUE contracts | `cue vet ./spec/...` passes locally |
| Repository formatting | `pnpm format:check` passes on 2026-10-22; historical task cards are treated as an archive |
| Generic CLI loop | Recorded in `e2e-cli-loop.md`; live-database tests require `ERRKB_TEST_DATABASE_URL` |
| DSH / Codex / Claude connectors | Measured with the pinned versions in `connector-matrix.md`; unmeasured fields remain marked |
| Installer migration and logon startup | Rehearsed on the development host; clean standard-user VM evidence is still missing |

Database-dependent Go tests skip when `ERRKB_TEST_DATABASE_URL` is absent. A green unit-test run without that variable is therefore not proof of the complete persistence/retrieval loop.

## Current release blockers

1. **The repository copy is not a Git worktree.** The local directory has no `.git`, so builds report `commit local` or `commit unknown`, rollback is unavailable and local-only UI changes cannot be traced safely.
2. **The Web toolbox is not on the current remote launch path.** The local copy contains `cmd/errkb/web.go`, embedded assets and a `ui --web` dispatch, but the current GitHub `main` and existing release binaries reject `--web`.
3. **The installer creates no toolbox launcher.** There is no `[Icons]` section in `installer.iss`, and `start-errkb.cmd` is not installed or included in the current release bundle.
4. **The UI cannot recover the installed daemon reliably.** When the pipe is unavailable, the Web shell starts a daemon with `ERRKB_DSN`/`--dsn`; it does not read `%LOCALAPPDATA%\errkb\pg\credentials.json`, and its default migration path depends on the current working directory.
5. **Release artifacts disagree.** `dist\errkb.exe`, the installed EXE and `dist\release\errkb-0.1.0\errkb.exe` were built at different revisions; the release-folder binary does not contain `errkb db`.
6. **The Web UI is search-only.** Issue detail, editing, statistics, audit and settings routes/screens remain to be implemented.
7. **Clean-VM acceptance is blocked.** A real standard-user install, reboot, toolbox launch and both uninstall choices still need evidence.

## Safe ways to use the project now

### Development use

Use a binary built from the current source, a PostgreSQL 16 instance and an explicit DSN:

```powershell
go build -o errkb.exe ./cmd/errkb
.\errkb.exe db migrate --dsn "postgres://postgres@127.0.0.1:5433/errkb?sslmode=disable" --migrations .\db\migrations
.\errkb.exe daemon --dsn "postgres://postgres@127.0.0.1:5433/errkb?sslmode=disable" --migrations .\db\migrations
.\errkb.exe doctor --dsn "postgres://postgres@127.0.0.1:5433/errkb?sslmode=disable" --migrations .\db\migrations
```

Then use the CLI, MCP server or one of the measured connectors.

### End-user installation

There is no supported end-user installation yet. Existing setup files are diagnostic/acceptance artifacts, not a public release. Do not publish or redistribute them as v0.1.0 until one clean build passes the release checklist below.

## Ordered path to a releasable toolbox

1. Restore a real Git worktree from the current GitHub `main` and preserve the local-only UI changes on a named branch.
2. Complete EKB-057: integrate `ui --web`, credential-aware daemon startup, search smoke tests and the portable toolbox bundle.
3. Complete EKB-058: issue detail and solution editing in the Web shell.
4. Complete EKB-059: statistics, audit and connector/privacy settings in the Web shell.
5. Complete EKB-060: installer files, Start-menu shortcut, portable ZIP and clean-VM UI evidence.
6. Run all quality gates with a real PostgreSQL database and remove the untracked probe artifacts.
7. Build once from a tagged commit and verify that the EXE, installer, manifest, checksums and installed binary identify the same revision.

## Release checklist

A release is ready only when all answers are yes:

- Does `errkb version` report the intended tag and commit?
- Does the installer contain that exact EXE?
- Does the Start-menu shortcut open the toolbox without a terminal?
- Can the toolbox start or reconnect to its managed PostgreSQL and daemon using installed credentials?
- Do search, detail/edit, statistics/audit and settings work through the local pipe?
- Does the same known error return its recorded fix without a model call?
- Does a real reboot restore the cluster and daemon for the same standard user?
- Do keep-data and delete-data uninstall paths behave as documented?
- Are all database integration tests, formatting checks, privacy checks and release hashes green?
- Are the English and Chinese documents synchronized?

## Document authority

| Document | Purpose |
| --- | --- |
| `project-status.md` | Current delivery truth and blockers |
| `workflow/README.md` | Control center for active issues, file repair order and cleanup decisions |
| `features-and-implementation.md` | User capabilities and their source, contract and data implementation paths |
| `implementation-plan.md` | Product decisions, task order and acceptance criteria |
| `toolbox-ui.md` | UI architecture and EKB-057–060 scope |
| `acceptance-vm.md` | Clean-machine evidence and remaining manual gates |
| `connector-matrix.md` | Host/version capability evidence |
| `README.md` | Short project entry point |

