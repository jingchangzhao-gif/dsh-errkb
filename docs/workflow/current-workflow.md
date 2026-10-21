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
| WF-008 | `NEXT` | Claimed generic log capture has no production entry point | `internal/capture/tail.go` is tested, but no command, daemon startup or connector calls `NewTailer` | Add a configured connector entry point with real acceptance evidence, or remove generic log capture from supported claims and delete the dormant component |
| WF-009 | `OPEN` | Markdown compatibility has no supported user command | Import/export implementation and fixtures exist only through tests | Add an explicit import/export CLI/API path, or formally retire the compatibility promise and remove implementation plus fixtures |
| WF-010 | `BLOCKED` | Database-backed gates have not been rerun for the current final workspace | Tests requiring `ERRKB_TEST_DATABASE_URL` skip without a live disposable PostgreSQL database | Run all database, retrieval, crash and CLI-loop tests against a permitted PostgreSQL 16 instance and save the results |
| WF-011 | `BLOCKED` | Clean standard-user Windows acceptance is missing | No recorded clean install, real reboot, toolbox launch and both uninstall modes for one coherent artifact | Complete `vm-acceptance.ps1` plus manual gates on a clean VM and attach its evidence directory |
| WF-012 | `OPEN` | No coherent release artifact exists | Existing `dist` executables came from different revisions and do not expose the same command surface | Build EXE, installer, portable toolbox, manifest, SBOM and hashes once from one accepted tag; installed binary must report that same identity |

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
