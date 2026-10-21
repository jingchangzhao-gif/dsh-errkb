# Changelog

All notable changes to errkb are recorded here. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and the versions follow
[Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Sections are written for the people using errkb: what the release can do, what
changed, what breaks, what was fixed, and what is known to be missing or broken.
They never claim more than the acceptance records in this repository prove.

## [Unreleased]

### Added

- Web toolbox (local branch, not yet released): `errkb ui --web` serves an
  embedded loopback interface with a per-launch token and a strict content
  security policy. It covers search, issue detail with version history and
  outcome counts, appending a solution version, issue and solution status
  changes, confirmation-gated deletion, measured statistics, the audit trail
  with filters, and the connector/privacy settings page. Every edit goes
  through the daemon and leaves an audit row in the same database transaction.
- The release bundle now carries `errkb-toolbox.zip` (the binary plus the
  launcher and documentation) next to the installer, with its hash in
  `SHA256SUMS` and a documented ZIP reproducibility exception in the manifest.
- The installer registers a Start-menu shortcut for the toolbox and ships the
  launcher.

### Fixed

- The installed product now applies database migrations during setup
  (`errkb db migrate`) instead of leaving the first run stuck on a missing
  ledger.
- Logon autostart uses the per-user Run key; registering a Scheduled Task
  requires administrator rights and silently failed for standard users.
- The PostgreSQL lifecycle has exactly one implementation: rrkb pg creates,
  starts, stops and uninstalls the local cluster, and postgres.ps1 only forwards
  the installer's arguments.

## [0.1.0] - 2026-10-21

The first release. It is a local error knowledge base: capture the failure once,
see the known fix next time.

### Added

- `errkb` command-line client: `search`, `show`, `record`, `list`, `feedback`,
  `stats`, `run -- <command>`, `daemon`, `ui`, `doctor`, `mcp` and `version`.
  Human output and `--json` share one contract, and JSON consumers get the
  validated payload instead of a re-rendered approximation.
- Local PostgreSQL knowledge base: exact lookup by issue id, fingerprint, error
  code or command first; approximate candidates are always marked as
  approximate and never auto-reused. Retrieval stays under the p95 150 ms gate
  at 100,000 issues on the reference machine (see `docs/benchmarks.md`).
- `errkb run -- <command>` wrapper: runs the command, keeps its output visible,
  and records only failures; transient network failures are skipped so they do
  not create noise.
- `errkb daemon`: single instance per Windows user session, graceful shutdown,
  a machine-readable health document, and logs that are redacted and rotated.
- `errkb doctor`: eight read-only checks (version and database, migrations,
  optional `pg_trgm`, Named Pipe, connectors, offline queue, privacy patterns,
  data directory writability) with a readable fix per finding.
- Tray icon, browser-style search window, desktop notification model, issue
  detail and solution editor, connector and privacy settings page, and metrics
  and audit views.
- Connectors: DSH plugin (injected known-fix hints, request-error diagnosis
  hand-back through `errkb` tools), Codex wrapper plus MCP registration, Claude
  Code hook plus wrapper, and a generic CLI/log template for other hosts.
- MCP stdio server with eight tools for hosts that speak MCP.
- Offline queue: failures recorded while the daemon is down are kept on disk
  (bounded) and replayed in arrival order when it returns.
- Backup, restore into a fresh database, retention classes and scoped deletion
  with an audit trail.
- Windows packaging: `errkb.exe` with version resources, a reproducible build,
  an Inno Setup installer that creates a dedicated PostgreSQL cluster on port
  5433, and a clean-VM acceptance harness.

### Changed

- The DSH plugin is now a thin adapter over the local core: capture, lookup and
  notice delivery go through the daemon, so the CLI, MCP and the plugin share
  one retrieval path and one trust signal.
- Fix trust is computed by the core (`errkb_trust()`); the plugin no longer
  keeps its own trust counters.
- The plugin package ships its built `lib/` entry points so the package can be
  reviewed and loaded as a fixed source snapshot.

### Breaking changes

- Not applicable: this is the first release. The database schema is versioned
  and upgrades from every earlier migration are covered by
  `internal/database/upgrade_test.go`; a newer schema or protocol version is
  refused explicitly rather than guessed at.
- The one planned breaking change for the next minor version: the state file
  format changed with the offline queue. The development state is migrated in
  place when it can be read; unreadable files are set aside, not deleted.

### Fixed

- The Windows store lock no longer fails under the concurrent tests: exclusive
  creation treats `EPERM`/`EBUSY` as contention, and the delete-pending read
  path tolerates `ENOENT` and `EPERM`.
- Long log lines and repeated failures no longer flood the knowledge base: the
  tail records bounded duplicate bursts and the wrapper skips transient
  failures.
- The redaction rules are generated from one source (`spec/redact.cue`) into
  Go, the TypeScript patterns and the privacy guard, so the three can no longer
  drift.
- Request-id patterns no longer reject the redacted label
  `request_id: <request-id>`.
- The Windows installer no longer expands `{app}` before the directory page is
  initialised, and the cluster password generator works under Windows
  PowerShell 5.1.
- The CI database job applies only the top-level migrations and no longer
  replays the historical fixtures in `db/migrations/testdata`.

### Known issues

- The clean-VM acceptance (EKB-052) is still **blocked**: the current
  environment has no hypervisor, so the install → first error → solution →
  outcome loop has only been rehearsed on the development host. The release
  pipeline refuses to publish without a recorded acceptance for the version.
- The installer creates and starts the PostgreSQL cluster but does not apply
  the schema migrations, and the CLI has no `db migrate` subcommand yet; the
  daemon therefore refuses to start after a fresh install until the migrations
  are applied by hand. Tracked in the plan (EKB-052 finding 1).
- Nothing restarts after a reboot: the installer does not register the cluster
  as a Windows service and there is no daemon/tray autostart yet.
- The release is not code-signed. Verify the artifacts against `SHA256SUMS`;
  `SIGNING.md` in the bundle states the risk and the plan.
- PostgreSQL is a runtime prerequisite and is not bundled. The knowledge base
  needs a local PostgreSQL 16 instance; the installer can create a dedicated
  cluster on port 5433.
- The tray and the search window are the only Win32 surfaces wired to the
  running daemon. The detail, settings and statistics screens exist as models
  with tests; their windows are not hooked up yet.
- `errkb run -- <command>` without any option before `--` reports that the
  command must follow `--` even though it does. Pass `--connector` and
  `--session` (the adapters do), or use `errkb run --connector <id> -- <cmd>`.
- The DSH plugin is pending the store review; installing it by hand is the
  supported path for now.

[Unreleased]: https://github.com/jingchangzhao-gif/error-knowledge-base/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/jingchangzhao-gif/error-knowledge-base/releases/tag/v0.1.0
