# Test record

[简体中文](test-record.zh-CN.md)

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
$env:ERRKB_TEST_DATABASE_URL = "<redacted test DSN>"
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
- This is a development-host run, not Windows installer acceptance. Its former
  EKB-052 / WF-011 conclusion is historical; WF-011A/WF-011B now own the
  mandatory hosted-Windows lifecycle and deterministic recovery evidence.

## 2026-10-23 — release-audit verification (v0.1.1 published)

- `v0.1.1` points at `a60a32fc511fbf1e9f1732d3d1fb3c602f23edba`; the tag workflow reran WF-011A/WF-011B and published the release assets, and `v0.1.0` remains at `1438473e60c7f3a77ecdaaacc1054a400428c919`.
- Locally downloaded every `v0.1.1` release asset and checked `SHA256SUMS`: the installer matched (`9cffa7e24417bc4af580992049b303174c904e1ca9afc4a0149e00c0852aaecf`, 5,424,094 bytes).
- The development-host acceptance run used the `v0.1.0` release installer (`errkb-0.1.0-setup.exe`, product version `0.1.0`, commit `1438473`), because it was the published artifact at the start of the audit; on it the silent install exits 0, health is `ok` with migrations `0008/0008`, the HKCU Run autostart value and the Start-menu shortcuts exist, and the Named Pipe answers.
- Zero-config start paths verified on that install: `errkb db status`, `errkb doctor`, `errkb markdown export` and `errkb run -- cmd /c "exit 9"` work without arguments (the installed `pg\credentials.json` and migrations are resolved from the application directory); the `/DELETE_DATA=1` uninstall path was exercised as well. Those fixes ship in `v0.1.1`.
- This remains a development-host run. The mandatory installer lifecycle evidence is the hosted `windows-acceptance` CI job (WF-011A/WF-011B); a clean Windows machine is optional evidence per the EKB-052 owner decision.
