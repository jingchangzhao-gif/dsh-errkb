# Test record

Durable record of full-suite verification runs, so a workflow item does not have
to stay open only to remember that the gates passed. Per-item evidence stays in
the implementation plan, the CHANGELOG and the acceptance documents; this file
only answers "which commands ran, where, and what came out".

## 2026-10-22 — development host, WF-010

Machine: Windows 11 Pro for Workstations 10.0.26200, Go 1.26.4 windows/amd64,
Node v24.16.0, pnpm 11.7.0, PostgreSQL 16.15 on `127.0.0.1:5432` (external
development instance). The database-backed tests ran against a throwaway
database through `ERRKB_TEST_DATABASE_URL`; every test creates and drops its own
schema.

Commands:

```powershell
pnpm.cmd typecheck
pnpm.cmd lint
pnpm.cmd test
pnpm.cmd format:check
gofmt -l cmd internal tests
go vet ./...
cue vet ./spec/...
$env:ERRKB_TEST_DATABASE_URL = "postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable"
go test ./... -count=1
```

Result:

- `pnpm typecheck`: clean. `pnpm lint`: 10 warnings, 0 errors (the warnings are
  the known control-flow comparisons in `src/`). `pnpm test`: 16 files, 733
  tests passed with coverage thresholds met. `pnpm format:check`: clean.
- `gofmt -l`: empty. `go vet ./...` and `cue vet ./spec/...`: clean.
- `go test ./... -count=1`: all packages green. Selected timings: cmd/errkb
  22.1 s, internal/database 63.6 s, internal/service 43.9 s, internal/capture
  8.4 s, internal/audit 2.6 s, internal/webshell 0.1 s, tests/e2e 11.5 s.
- The e2e package covered the CLI loop, the toolbox detail/edit and
  statistics/settings flows, `errkb tail --file` and the Markdown
  import/export round trip on that PostgreSQL instance.

Notes and limits:

- Runner: `TestTrustIsDatabaseOnly` now counts `errkb_trust` only in its own
  schema; a database-wide count raced with parallel test packages and with
  schemas left behind by interrupted runs.
- Interrupted test runs can leave `errkb_*` schemas behind; drop them before a
  database-wide assertion (`drop schema if exists … cascade`).
- This is a development-host run, not clean-VM acceptance. EKB-052 / WF-011
  still owns the clean standard-user run with the installer, reboot and both
  uninstall modes.
