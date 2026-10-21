# File repair sequence

[简体中文](file-repair-sequence.zh-CN.md)

This file translates every active `WF-nnn` issue into a concrete edit order. Follow the phases sequentially. Do not edit a later phase merely because its files are easy to reach; the earlier phases establish the contracts and runtime paths that later UI and packaging work depend on.

## Global change protocol

For each workflow item:

1. Mark that row `IN PROGRESS` in both `current-workflow` files.
2. Reproduce the stated evidence before changing code.
3. Modify files in the listed order: contract → core/service → interface → tests → documentation.
4. Run focused tests after each layer and common gates at the end.
5. Store durable evidence in the implementation plan, project status, CHANGELOG or acceptance report.
6. Remove the resolved row from both `current-workflow` files and promote the next dependency-satisfied row to `NEXT`.

## Phase 0 — establish safe source control

### WF-001: restore a real Git worktree

Purpose: make every later change reviewable and recoverable.

File/process order:

1. Record the upstream URL and expected base commit outside the dirty directory.
2. Clone `https://github.com/jingchangzhao-gif/error-knowledge-base` into a new sibling directory.
3. Verify the base commit and default branch.
4. Copy only intentional local source/document changes; do not copy `node_modules/`, `coverage/`, `dist/` or probe output.
5. Create a named EKB-057 branch and inspect the full diff.
6. Continue all later phases in that Git worktree.

Completion gate: `git status`, `git diff --stat` and `errkb version` identify a traceable source state, while the current local-only Web files remain present.

## Phase 1 — remove confirmed generated garbage

### WF-002: execute the cleanup manifest

Files:

1. `packaging/windows/cleanup-local.ps1`
2. `.gitignore`
3. `unused-code-audit.md` and `unused-code-audit.zh-CN.md`

Procedure:

```powershell
pwsh -File packaging/windows/cleanup-local.ps1
pwsh -File packaging/windows/cleanup-local.ps1 -Apply
```

Confirm the two malformed Claude probe files, `coverage/` and `dist/` are absent. Do not delete `lib/`, `node_modules/`, migration fixtures, seeds or dormant source code.

Completion gate: a second preview reports all targets absent; the Git diff contains no generated artifacts.

## Phase 2 — finish the Web toolbox foundation

### WF-003: credential-aware daemon recovery and portable launch

Edit order:

1. `cmd/errkb/web.go`
   - add an explicit `--credentials` option;
   - resolve the installed default credentials path;
   - prefer an explicit DSN, otherwise load installed credentials;
   - resolve migrations relative to the executable/install directory instead of the caller's working directory;
   - keep all logged connection information redacted.
2. `internal/service/pg_manage.go`
   - reuse `ReadCredentials`; do not create a second credential parser.
3. `cmd/errkb/ui_test.go` or a focused new Web-launch test
   - cover running daemon, stopped daemon with credentials, missing credentials, invalid credentials and paths containing spaces.
4. `internal/webshell/server.go` and `server_test.go`
   - retain loopback-only binding, per-launch token, body limit and security headers;
   - add a health/readiness endpoint only if the launcher test needs it.
5. `packaging/windows/start-errkb.cmd`
   - pass installed credentials/migration paths without exposing the password.
6. `docs/toolbox-ui.*`, `docs/install.*`, `docs/project-status.*` and both current-workflow files.

Completion gate: from a directory other than the install directory, the launcher starts or reconnects to the daemon and a browser search reaches the real database.

## Phase 3 — install the launcher

### WF-004: installer and Start-menu entry

Edit order:

1. `packaging/windows/installer.iss`
   - install `start-errkb.cmd` and required assets;
   - add `[Icons]` entries for the Start menu and optional desktop shortcut;
   - preserve standard-user installation and silent-install behavior.
2. `packaging/windows/release.ps1`
   - include the launcher and its hash in the bundle, manifest and checksum set.
3. `packaging/windows/vm-acceptance.ps1`
   - assert installed launcher/shortcut targets and launch behavior.
4. `docs/install.*`, `docs/acceptance-vm.*`, `docs/project-status.*`.

Completion gate: a standard user can launch the toolbox from the Start menu without a terminal, and the installed executable identity matches the bundle manifest.

## Phase 4 — expose issue detail and solution editing

### WF-005 / EKB-058

Edit order:

1. `spec/local-api.cue` and contract tests
   - define detail/read and required edit operations before adding routes.
2. `internal/contract/local_api.go` and related contract types
   - validate every request/response and keep schema-version rejection explicit.
3. `internal/service/api.go`, `client.go`, `backend.go`
   - route the new operations through the daemon; the Web shell must not connect to PostgreSQL directly.
4. `internal/service/edit_api.go`
   - reuse existing history, audit, confirmation and read-only trust rules.
5. `internal/webshell/server.go`
   - add detail and edit routes with token checks, body limits and stable error envelopes.
6. `internal/webshell/assets/index.html`, `app.js`, `app.css`
   - add detail, version history and edit/confirm states without automatic fix execution.
7. Service, pipe, Web-route and browser-smoke tests.
8. `docs/features-and-implementation.*`, `docs/toolbox-ui.*`, project status and current workflow.

Completion gate: selecting a result loads complete history; adding a solution appends a version; status/deletion operations require confirmation and write audit entries; trust remains read-only.

## Phase 5 — expose statistics, audit and settings

### WF-006 / EKB-059

Edit order:

1. `spec/local-api.cue` and `spec/config.cue` for any missing wire operations.
2. `internal/contract/` request/response types.
3. `internal/audit/log.go`, `stats.go` and tests.
4. `internal/ui/settings.go` and its backing service/config operations.
5. `internal/service/api.go`, `client.go`, `backend.go`.
6. `internal/webshell/server.go` and route tests.
7. `internal/webshell/assets/` pages and UI smoke tests.
8. Privacy, toolbox, feature, status and workflow documents in both languages.

Completion gate: measured and estimated statistics are separated; audit filters work; configuration is validated by CUE; deletion always previews and confirms before mutation.

## Phase 6 — remove duplicated lifecycle ownership

### WF-007

Files to compare:

- Go: `internal/service/pg_manage.go`, `internal/database/backup.go` and tests.
- PowerShell/Inno: `packaging/windows/postgres.ps1`, `autostart.ps1`, `installer.iss`, `vm-acceptance.ps1`.

Recommended decision: keep PowerShell/Inno as the installer orchestration because it is already the production path, but first move any unique backup/rollback safety guarantees into a callable supported path. Then remove unreachable Go lifecycle operations rather than maintaining two implementations.

Completion gate: exactly one installed lifecycle path exists; install, upgrade rollback, keep/delete uninstall and credential permissions remain tested.

## Phase 7 — decide dormant compatibility features

### WF-008: generic log capture

Choose one path:

- integrate `internal/capture/tail.go` through a configured daemon/connector entry point and add a real-host acceptance test; or
- remove the component, its tests and generic-log support claims together.

Do not keep a tested library while claiming a user-facing feature that has no entry point.

### WF-009: Markdown import/export

Choose one path:

- add supported `errkb import` / `errkb export` commands backed by `internal/database/markdown.go`; or
- retire the compatibility promise and remove the implementation, seed-generation path and obsolete fixtures as one reviewed change.

Existing upgrade fixtures must remain if older releases are still promised a direct upgrade.

## Phase 8 — full database and machine acceptance

### WF-010: live PostgreSQL gates

Run with a disposable PostgreSQL 16 database:

```powershell
$env:ERRKB_TEST_DATABASE_URL = '<disposable PostgreSQL DSN>'
go test ./... -count=1
```

Also run the performance smoke, crash safety and CLI loop named in `docs/benchmarks.*` and `docs/e2e-cli-loop.*`. Save commands, versions, durations and failures; never save a plaintext password.

### WF-011: clean-VM acceptance

Use `packaging/windows/vm-acceptance.ps1` on a clean Windows 10/11 x64 VM as a standard user. Complete install, first launch, real reboot, single-instance recovery, knowledge reuse and both uninstall choices. Store the evidence outside generated `dist/` until the release bundle is finalized.

## Phase 9 — coherent release

### WF-012

Edit/run order:

1. `packaging/windows/build.ps1`
2. `packaging/windows/installer.iss`
3. `packaging/windows/release.ps1`
4. `packaging/windows/vm-acceptance.ps1`
5. `CHANGELOG.md`, release notes, install/status/acceptance documents

Build once from one accepted tag. Verify version, commit and build time across the source, EXE, installer, installed EXE, manifest and checksums. Do not copy any file from an earlier `dist/` directory.

## Common final gate

```powershell
pnpm typecheck
pnpm lint
pnpm test
pnpm format:check
go vet ./...
go test ./...
cue vet ./spec/...
```

The release gate additionally requires live-database results, clean-VM evidence and synchronized Chinese/English documentation.
