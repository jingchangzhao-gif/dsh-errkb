# Acceptance report — 2026-10-22

[简体中文](acceptance-report-2026-10-22.zh-CN.md)

Status: **FAILED — release blocked**

This report records the first acceptance pass after the implementation sequence was completed. It separates verified product behavior from remaining test, documentation and environment gates. Database credentials are intentionally omitted.

## Source and artifact identity

| Item | Observed value | Result |
| --- | --- | --- |
| Working branch | `local/work-state-2026-10-22` | Pass |
| HEAD | `257190376164e7c16bccdcef212b5f2bbbb8c28b` | Pass |
| Git state before acceptance | clean | Pass |
| Release bundle | `dist/release/errkb-0.1.0` | Present |
| Bundled EXE identity | `0.1.0`, commit `fa2585e`, built `2026-10-22T00:00:00Z` | Pass |
| Changes after artifact commit | three acceptance/workflow Markdown files only | Pass |
| Git tag `v0.1.0` | absent | **Fail** |

## Automated gates

| Gate | Result | Evidence |
| --- | --- | --- |
| `pnpm typecheck` | Pass | exit 0 |
| `pnpm lint` | Pass with warnings | exit 0; 10 unused/generated-code warnings |
| `pnpm test` | Pass | 16 files, 733 tests; 99.4% statement/line coverage |
| `pnpm format:check` | Pass | all matched files formatted |
| `pnpm build` | Pass | `lib/index.js`, source map and declarations regenerated |
| `go vet ./...` | Pass | exit 0 |
| `go test ./... -count=1` without live DSN | Pass | all non-database gates pass; database tests skip by design |
| `cue vet ./spec/...` | Pass | exit 0 |
| Windows build script | Pass | temporary `0.1.0` EXE reports HEAD `2571903`; checksum matches |

## Live PostgreSQL acceptance

PostgreSQL 16 was running locally. The test DSN came from the documented developer configuration and was provided only to the child test process.

| Gate | Result | Evidence |
| --- | --- | --- |
| `go test ./... -count=1` with live DSN | **Fail** | `internal/database.TestCrashSafety`: “1 advisory locks survived the crash” |
| `go test ./internal/database/ -run '^TestCrashSafety$' -v -count=3` | Pass | 3/3 passes, about 28 seconds |
| `go test -p 1 ./... -count=1` with live DSN | Pass | every package including database, service, Web shell and E2E passes |

Finding: the crash test counts advisory locks across the database while Go tests different packages concurrently. A lock held by another migration test can therefore be reported as a crash-helper leak. Serial execution proves the product rollback path, but the documented parallel release gate remains nondeterministic. This is tracked as WF-013.

## Release-bundle checks

| Check | Result |
| --- | --- |
| All nine `SHA256SUMS` entries | Pass |
| Installer artifact phase | 3 passed, 0 failed |
| Portable ZIP allowlist | Pass: `CHANGELOG.md`, `errkb.exe`, `LICENSE`, `README.md`, `start-errkb.cmd` |
| Credentials, health data, logs or PostgreSQL data in ZIP | None found |
| Authenticode signature | Not signed; matches the documented current signing policy |
| `release.ps1 -Mode check -Version 0.1.0 -Tag v0.1.0 -VmAcceptanceVersion 0.1.0` | Returns OK even though the tag is absent |

Finding: `Resolve-ReleaseTag` checks only that the supplied text equals `v<version>`; it does not resolve the Git ref or compare it with the accepted commit. This false-positive release gate is added to WF-012.

## Machine acceptance boundary

The local `artifacts` phase passed 3/3. The local `preflight` rehearsal passed 5/5 only with `-AllowAdmin -AllowDirtyHost`; it is not clean-VM evidence. Installation, a real standard-user session, reboot recovery, interactive launcher behavior and both uninstall choices were not executed on this development machine. WF-011 remains blocked pending a clean Windows VM.

## Documentation consistency

`docs/project-status.md` still lists several resolved items as current blockers, including the missing Git worktree, launcher, installed-credential daemon recovery and Web detail/statistics/settings surfaces. The status document cannot serve as the delivery authority until it is reconciled with the current source and this report. This is tracked as WF-014.

## Decision

The implementation is suitable for continued acceptance testing, but **version 0.1.0 is not accepted for release**. Resolve WF-013 and WF-014, complete WF-011 on a clean VM, then close WF-012 by enforcing a real accepted Git tag and rebuilding the final bundle once.
