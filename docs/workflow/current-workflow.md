# Current work workflow and open issues

[简体中文](current-workflow.zh-CN.md)

Status date: **2026-10-22**

This is the operational resume point for the next work session. It contains only unresolved work. When an issue passes its acceptance check, remove it from the active table in both languages; preserve historical evidence in the implementation plan, CHANGELOG, test record or acceptance document instead of keeping a growing “done” list here.

## Working rule

For each item, use this sequence:

```text
read current state
  → reproduce the problem
  → identify one owning layer
  → make the smallest coherent change
  → run focused tests
  → run common quality gates
  → update both document languages
  → remove the resolved item from this file
```

Only one item should be `IN PROGRESS` at a time. Do not start a dependent item while its prerequisite remains open. A source implementation is not complete until the feature is reachable through its intended user interface.

## Status meanings

| Status | Meaning |
| --- | --- |
| `NEXT` | The next item that should be worked on |
| `IN PROGRESS` | The single item currently being changed and verified |
| `OPEN` | Confirmed problem, waiting for its prerequisite |
| `BLOCKED` | Cannot be completed in the current environment; the missing external condition is named |

There is intentionally no `DONE` state in this file. Resolved problems are deleted from the active table.

## Active issues, in execution order

| ID | Status | Remaining problem | Evidence now | Completion condition |
| --- | --- | --- | --- | --- |
| WF-013 | `NEXT` | The documented live-database gate is nondeterministic when Go packages run in parallel | On 2026-10-22, `ERRKB_TEST_DATABASE_URL=<redacted> go test ./... -count=1` failed in `TestCrashSafety` because it counted one advisory lock owned by another concurrently tested package; the focused test passed 3/3 and `go test -p 1 ./... -count=1` passed | Scope the lock assertion to the ErrKB migration lock or its helper backend, add a parallel regression, and make the documented command pass repeatedly without `-p 1` |
| WF-014 | `OPEN` | The project-status document still presents resolved implementation gaps as current blockers | `docs/project-status.*` says this is not a Git worktree and that the launcher, installed-credential daemon recovery and Web detail/statistics/settings are missing, while the current tree and tests show those items are implemented | Re-audit every status claim against the current tree and the 2026-10-22 acceptance report, update both languages, and leave only live release boundaries |
| WF-011 | `BLOCKED` | Clean standard-user Windows acceptance is missing | No recorded clean install, real reboot, toolbox launch and both uninstall modes for one coherent artifact | Complete `vm-acceptance.ps1` plus manual gates on a clean VM and attach its evidence directory |
| WF-012 | `BLOCKED` | The release gate is fixed, but the final tagged bundle cannot be created before the test, documentation and clean-VM gates pass | `release.ps1` now resolves `refs/tags/<tag>`, requires the tag at a clean HEAD and rejects a mismatched explicit commit; `pnpm test:release` passes all 5 negative/positive cases. This repository still has no `v0.1.0` tag, and WF-011/WF-013/WF-014 remain open | Complete WF-011, WF-013 and WF-014; commit the accepted tree, create the real version tag, run the release check/package once, then verify the installed identity and all bundle hashes |

## Per-item update format

When beginning work:

1. Change exactly one row to `IN PROGRESS` in both language files.
2. Record the reproduction command or observed file evidence in the row.
3. Make changes only in the owning layer and its tests/documents.

When closing work:

1. Run the row's completion check.
2. Run the common gates below.
3. Move durable evidence to the appropriate implementation-plan card, CHANGELOG or acceptance document.
4. Delete the row from both language files.
5. Change the next dependency-satisfied row to `NEXT`.

## Common quality gates

Run the applicable subset during development and the complete set before a release:

```powershell
pnpm typecheck
pnpm lint
pnpm test
pnpm format:check
go vet ./...
go test ./...
cue vet ./spec/...
```

Database-backed acceptance additionally requires `ERRKB_TEST_DATABASE_URL`. Packaging work also requires the release script checks and clean-VM acceptance.
