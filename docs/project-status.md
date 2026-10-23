# Project status

[简体中文](project-status.zh-CN.md)

Status date: 2026-10-23

This page is the current delivery authority: it states what exists in the present Git worktree, what has been exercised, what is publicly released and which limitations remain. Historical task notes describe how the project arrived here; they do not override this page.

## Executive summary

Error Knowledge Base is a local, multi-host error-reuse toolbox rather than a DeepSeek-only plugin:

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

The reusable core, PostgreSQL schema, CLI, daemon, MCP server, first host connectors and embedded Windows Web toolbox are implemented. The v0.1.1 installer includes managed PostgreSQL setup, migrations, logon recovery, a launcher, a Start-menu shortcut and both uninstall choices. WF-011A/WF-011B make the installed lifecycle and recovery a mandatory Windows CI gate. WF-012 published the verified tag and bundle on 2026-10-23, so v0.1.1 is the first supported public release.

## Status vocabulary

| Word | Meaning |
| --- | --- |
| Implemented | Source code and automated tests exist |
| Verified | The behavior ran successfully in the stated environment |
| Accepted | Feature-specific, common and required manual gates passed |
| Releasable | One tagged artifact passed the mandatory Windows lifecycle/recovery gate and provenance checks |

Implemented does not automatically mean verified, accepted or releasable.

## Implemented now

- Deterministic classification, headline extraction, redaction, normalisation and fingerprinting.
- PostgreSQL migrations `0001`–`0008`, idempotent recording, versioned solutions, outcomes, trust, exact search and trigram/Jaccard retrieval.
- Windows daemon with owner-scoped Named Pipe access, a health document, redacted logging and a single-instance lock.
- CLI search/show/record/list/feedback/stats/run/tail/Markdown exchange, database and PostgreSQL management, doctor, MCP, UI and version commands.
- MCP stdio plus documented DSH, Codex CLI, Claude Code and generic CLI connector paths.
- Embedded loopback Web toolbox for search, issue detail, solution/status writes, statistics, audit, connector/privacy settings and scoped deletion.
- Credential-aware daemon recovery: `ui --web` resolves `pg/credentials.json` and the installed migrations directory before starting a missing daemon.
- Per-user Windows installer source with schema migration, logon recovery, launcher, Start-menu shortcut, keep-data uninstall and delete-data uninstall.
- Release provenance gate that rejects a missing tag, a tag on another commit, a dirty worktree and an explicit commit mismatch.

## Verified now

| Area | Evidence |
| --- | --- |
| TypeScript adapter | 733 tests; 99.4% statement/line coverage in the 2026-10-22 acceptance run |
| Go and PostgreSQL | Full package-parallel `go test ./... -count=1` with PostgreSQL 16 passed three consecutive fresh runs after WF-013 |
| Crash safety | Four real process-kill scenarios pass; the regression ignores an unrelated advisory lock but detects a crash-helper lock |
| CUE contracts | `cue vet ./spec/...` passes locally |
| Repository quality | typecheck, lint, format, build and `go vet ./...` passed in the recorded acceptance run |
| CLI reuse loop | Real database record → search → solution → reuse → feedback evidence is recorded in `e2e-cli-loop.md` |
| DSH / Codex / Claude connectors | Measured versions and any unverified cells are explicit in `connector-matrix.md` |
| Installer/release rehearsal | Artifact checks, installer migration/logon behavior and the five provenance tests pass on the development host |
| Windows lifecycle/recovery | WF-011A/WF-011B build the real installer on an ephemeral Windows runner, test install/reuse/retention/deletion and deterministic daemon/PostgreSQL recovery, and upload structured evidence |

The automated gate does not claim a physical reboot or visual review of interactive windows; those remain optional manual evidence.

## Current release boundaries

1. **v0.1.1 — released and verified.** Tag `v0.1.1` points to commit `a60a32f`; the tag workflow reran WF-011A/WF-011B and published the installer, toolbox ZIP, standalone EXE, manifest, SBOM and hashes. The downloaded bundle identity and all `SHA256SUMS` entries were verified.
2. **Signing limitation — accepted but visible.** The release is not Authenticode-signed. This is not hidden and does not weaken the checksum/provenance gates; users must verify `SHA256SUMS` until a signing certificate is introduced.

WF-013 (parallel database determinism) and WF-014 (delivery-status reconciliation) are complete.

## Safe use now

### Development and acceptance

Build from the current worktree and use an explicit PostgreSQL 16 DSN, or exercise the installer through the Windows acceptance scripts. The CLI, MCP server and measured connectors may be exercised; do not present the rehearsal bundle as an official release.

```powershell
go build -o errkb.exe ./cmd/errkb
.\errkb.exe db migrate --dsn "postgres://postgres@127.0.0.1:5433/errkb?sslmode=disable" --migrations .\db\migrations
.\errkb.exe daemon --dsn "postgres://postgres@127.0.0.1:5433/errkb?sslmode=disable" --migrations .\db\migrations
.\errkb.exe ui --web --dsn "postgres://postgres@127.0.0.1:5433/errkb?sslmode=disable" --migrations .\db\migrations
```

### End-user installation

Download `errkb-0.1.1-setup.exe` from the [v0.1.1 release](https://github.com/jingchangzhao-gif/error-knowledge-base/releases/tag/v0.1.1), verify the installer against `SHA256SUMS`, then run the installer as a standard user. `errkb-toolbox.zip` is the portable alternative.

## Release checklist

A release is ready only when every answer is yes:

- Does `errkb version` report the intended tag and accepted commit?
- Does the installer contain that exact EXE and does the installed EXE have the same hash?
- Does the Start-menu shortcut open the toolbox without a visible terminal?
- Can the toolbox start or reconnect to its managed PostgreSQL and daemon from installed credentials?
- Do search, detail/edit, statistics/audit and settings work through the intended local interfaces?
- Does the same known error return its recorded fix without a model call?
- Does WF-011B restore one daemon, the cluster, Named Pipe, health and saved knowledge after deterministic process failure?
- Do keep-data and delete-data uninstall paths behave as documented?
- Are database integration tests, formatting/privacy checks and release hashes green?
- Are the English and Chinese documents synchronized?

## Document authority

| Document | Purpose |
| --- | --- |
| `project-status.md` | Current delivery truth and release boundaries |
| `workflow/README.md` | Active-work control center and repair order |
| `features-and-implementation.md` | User capabilities and implementation paths |
| `implementation-plan.md` | Product decisions, task order and acceptance criteria |
| `toolbox-ui.md` | UI architecture, routes and delivery evidence |
| `acceptance-vm.md` | Required Windows CI acceptance and optional clean-machine review |
| `connector-matrix.md` | Host/version capability evidence |
| `README.md` | Short project entry point |
