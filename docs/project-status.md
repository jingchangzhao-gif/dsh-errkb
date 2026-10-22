# Project status

[简体中文](project-status.zh-CN.md)

Status date: 2026-10-22

This page is the current delivery authority: it states what exists in the present Git worktree, what has been exercised, and what still prevents a public release. Historical task notes describe how the project arrived here; they do not override this page.

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

The reusable core, PostgreSQL schema, CLI, daemon, MCP server, first host connectors and embedded Windows Web toolbox are implemented. The installer source includes managed PostgreSQL setup, migrations, logon recovery, a launcher, a Start-menu shortcut and both uninstall choices. The project is still an **acceptance candidate**, not a public release: the clean standard-user VM run and the final build from a real accepted Git tag remain open.

## Status vocabulary

| Word | Meaning |
| --- | --- |
| Implemented | Source code and automated tests exist |
| Verified | The behavior ran successfully in the stated environment |
| Accepted | Feature-specific, common and required manual gates passed |
| Releasable | One tagged artifact passed clean-machine install, launch, restart and uninstall |

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

The installer rehearsal is not a substitute for the clean-VM run.

## Current release boundaries

1. **WF-011 — clean Windows acceptance.** One coherent candidate must be installed by a standard user on the clean Windows 11 VM, launched, used for the knowledge-reuse loop, recovered after a real reboot, and uninstalled once with data retained and once with data deleted. The evidence directory must be saved outside generated `dist/`.
2. **WF-012 — real tagged release.** This repository has no accepted `v0.1.0` tag yet. Existing files under `dist/` are rehearsal artifacts and must not be published. After WF-011 passes, commit the accepted tree, create the real tag, build the release once from that tag, and verify that source, EXE, installer, installed EXE, manifest and hashes identify the same version, commit and build time.
3. **Signing limitation — accepted but visible.** The candidate is not Authenticode-signed. This is not hidden and does not weaken the checksum/provenance gates; users must verify `SHA256SUMS` until a signing certificate is introduced.

WF-013 (parallel database determinism) and WF-014 (delivery-status reconciliation) are complete.

## Safe use now

### Development and acceptance

Build from the current worktree and use an explicit PostgreSQL 16 DSN, or use the installer only as the WF-011 acceptance candidate. The CLI, MCP server and measured connectors may be exercised; do not present the rehearsal bundle as an official release.

```powershell
go build -o errkb.exe ./cmd/errkb
.\errkb.exe db migrate --dsn "postgres://postgres@127.0.0.1:5433/errkb?sslmode=disable" --migrations .\db\migrations
.\errkb.exe daemon --dsn "postgres://postgres@127.0.0.1:5433/errkb?sslmode=disable" --migrations .\db\migrations
.\errkb.exe ui --web --dsn "postgres://postgres@127.0.0.1:5433/errkb?sslmode=disable" --migrations .\db\migrations
```

### End-user installation

There is no supported public installer yet. The next valid transition is WF-011 clean-VM acceptance followed by WF-012 tagged packaging; no feature implementation cards are waiting ahead of those gates.

## Release checklist

A release is ready only when every answer is yes:

- Does `errkb version` report the intended tag and accepted commit?
- Does the installer contain that exact EXE and does the installed EXE have the same hash?
- Does the Start-menu shortcut open the toolbox without a visible terminal?
- Can the toolbox start or reconnect to its managed PostgreSQL and daemon from installed credentials?
- Do search, detail/edit, statistics/audit and settings work through the intended local interfaces?
- Does the same known error return its recorded fix without a model call?
- Does a real reboot restore the cluster and daemon for the same standard user?
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
| `acceptance-vm.md` | Clean-machine procedure and evidence |
| `connector-matrix.md` | Host/version capability evidence |
| `README.md` | Short project entry point |
