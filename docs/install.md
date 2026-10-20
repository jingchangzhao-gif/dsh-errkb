# Installing, upgrading and uninstalling the Error Knowledge Base

This page describes the Windows installation path for version 0.1.0 of the Error Knowledge Base (the `errkb` command) and what it actually does today. Where the release is incomplete, it says so instead of describing the intended behavior.

> **Status note.** The clean-VM acceptance is blocked: no hypervisor is available in the
> environment the release was built in, and the installer does not yet apply schema
> migrations (finding 1) or register autostart (finding 2). See
> [`docs/acceptance-vm.md`](acceptance-vm.md) for the evidence and the harness.
> `packaging/windows/release.ps1` refuses to package a version without a recorded
> acceptance for it, so no public release exists until those findings are fixed.

## Requirements

- Windows 10 or 11, x64.
- PostgreSQL 16 (EDB installer or an existing local instance). The installer can create a
  dedicated cluster; it never bundles a server.
- A standard (non-administrator) user account. The installer elevates itself through UAC;
  the application installs per user.
- About 50 MB of disk for the application and binaries, plus the PostgreSQL data directory
  (the installer default is a dedicated cluster under `%LOCALAPPDATA%\errkb\pg\data`).

## Artifacts

| Artifact | How it is produced |
| --- | --- |
| `dist\errkb.exe` | `packaging\windows\build.ps1` — version-stamped, Windows resources, reproducible |
| `dist\errkb.exe.sha256` | Written by `build.ps1` |
| `dist\errkb-<version>-setup.exe` | `packaging\windows\release.ps1 -Mode package -IncludeInstaller` (requires Inno Setup 6) |
| `dist\release\errkb-<version>\` | Release bundle with `SHA256SUMS`, `manifest.json`, `sbom.json`, `THIRD-PARTY-NOTICES.md`, `RELEASE-NOTES.md`, `SIGNING.md` |

The release is not code-signed. Verify `errkb.exe` and the setup executable against
`SHA256SUMS` before installing, and expect a SmartScreen warning for an unsigned
installer.

## Interactive install

1. Run `errkb-<version>-setup.exe` and accept the UAC prompt.
2. Choose where to install (default: `%LOCALAPPDATA%\errkb`).
3. Choose the PostgreSQL cluster option:
   - **Dedicated cluster** (recommended): creates a new cluster on port **5433** next to the
     application, generates a password, and starts it. It never touches an existing
     PostgreSQL instance.
   - **Reuse an existing instance**: you supply the DSN; nothing is created.
   - **Other port**: same as dedicated, with a port you choose. A busy port is detected and
     rejected in the wizard.
4. Finish the wizard. The installer offers to start the cluster; it also copies
   `errkb.exe`, `postgres.ps1`, `README.md` and the uninstaller into the install directory.

## Silent install

`errkb-<version>-setup.exe /VERYSILENT /SUPPRESSMSGBOXES` takes the defaults: install into
`%LOCALAPPDATA%\errkb`, create a dedicated cluster on port 5433, start it. The wizard's
three-way cluster question cannot be answered silently, so the automated acceptance run
exercises the dedicated-cluster branch only; reuse and other-port branches are a manual
gate.

## What lands where

| Path | Contents |
| --- | --- |
| `%LOCALAPPDATA%\errkb\` | application binaries, `postgres.ps1`, `README.md`, uninstaller |
| `%LOCALAPPDATA%\errkb\pg\data\` | the dedicated PostgreSQL cluster (data directory) |
| `%LOCALAPPDATA%\errkb\pg\credentials.json` | port, database, superuser, generated password, data directory; owner-only ACL |
| `%LOCALAPPDATA%\errkb\logs\` | cluster/tool output written by `postgres.ps1` |
| `%LOCALAPPDATA%\errkb\health.json` | daemon health document (when the daemon runs with default paths) |
| `%LOCALAPPDATA%\errkb\logs\daemon.log` | daemon log; redacted before writing, rotated by size |

The daemon's default paths follow `%LOCALAPPDATA%`; pass `--health-file` and `--log-file`
to put them elsewhere.

## First run in 0.1.0 is incomplete

The installer creates and starts PostgreSQL, but it does **not** apply the schema
migrations, and the CLI has no `db migrate` subcommand yet. The daemon therefore refuses
to start on a fresh install with:

```text
errkb daemon: schema migration check failed: applied version "", expected "0008"
(run `errkb db migrate` first)
```

Until a follow-up card closes this gap, the supported route is the rehearsal path in
[`docs/acceptance-vm.md`](acceptance-vm.md): apply the eight migrations with the Go
migrator from the repository checkout, then start the daemon.

## Start the daemon

```powershell
errkb daemon --dsn "postgres://postgres@127.0.0.1:5433/errkb?sslmode=disable"
```

Useful flags: `--pipe` (Named Pipe name, default `\\.\pipe\errkb`), `--lock-name` (named
mutex for the single instance), `--health-file`, `--log-file`, `--log-max-bytes`,
`--health-interval`. The daemon never writes logs to stdout. Stop it with `Ctrl+C` (or
close the tray application); shutdown is graceful and waits for in-flight requests.

Check the installation with:

```powershell
errkb doctor
```

The doctor reports eight checks — version and database connectivity, migration ledger,
optional `pg_trgm`, Named Pipe, connectors, offline queue, privacy patterns, data
directory writability — each with a readable fix when a check fails.

## Reusing an existing PostgreSQL

Point `database.dsn` (config file) or `--dsn` (daemon flag) at the instance, create the
database, apply migrations, and run `errkb doctor`. Nothing else changes; the installer's
cluster options are only for the bundled path.

## Backups

The knowledge base is an ordinary PostgreSQL database. Back it up with `pg_dump`:

```powershell
$cred = Get-Content "$env:LOCALAPPDATA\errkb\pg\credentials.json" | ConvertFrom-Json
$env:PGPASSWORD = $cred.password
$pgDump = Join-Path ${env:ProgramFiles} "PostgreSQL\16\bin\pg_dump.exe"
& $pgDump -Fc -h 127.0.0.1 -p $cred.port -U $cred.superuser -d $cred.database -f errkb.dump
```

Restore into a **fresh** database (the Go helper refuses to overwrite an existing one):

```powershell
& pg_restore -h 127.0.0.1 -p 5433 -U postgres -d errkb_restored errkb.dump
```

The repository's `internal/database` package also exposes `BackupDatabase` /
`RestoreDatabase` (custom-format dump, SHA-256 manifest, table counts, ledger and
constraint verification) for scripted backups.

## Upgrade

- **Application:** run a newer installer over the same installation. Data survives; the
  uninstaller and binaries are replaced.
- **Schema:** migrations are applied by the Go migrator. A failed migration rolls back in
  one transaction; an applied migration must never be edited (the ledger checksum fails).
  A newer ledger version is refused explicitly.
- **PostgreSQL cluster upgrade:** not automated by the installer yet. Back up with
  `pg_dump` first; `packaging/windows/postgres.ps1` and the Go manager expose an upgrade
  path, but a release-grade cluster upgrade is still a follow-up.

## Uninstall

The uninstaller asks whether to **keep data** or **delete data**:

- **Keep:** removes the application files and uninstaller, leaves the cluster and
  `%LOCALAPPDATA%\errkb\pg\` in place. A reinstall detects and can reuse the retained
  cluster.
- **Delete:** stops the cluster, removes the data directory and credentials, and then
  checks that no service, port or directory is left behind. The acceptance harness checks
  the residue: service, port, process and files.

`packaging/windows/postgres.ps1 uninstall -DeleteData` refuses an implausibly short data
path (a guard against deleting the wrong directory).

## Troubleshooting

| Symptom | Likely cause and fix |
| --- | --- |
| `schema migration check failed` | The ledger is missing or behind. Apply the migrations (0.1.0 has no `db migrate`; see the note above). |
| `the local API is not reachable` | The daemon is not running or the pipe name differs. Start `errkb daemon` and check `errkb doctor`. |
| `database: connection refused` | PostgreSQL is not running, or the DSN points at the wrong port. Check `errkb doctor`. |
| Port 5433 already in use | Another cluster is listening. Choose another port in the wizard or stop the other instance. |
| Daemon log shows an access error under `%TEMP%` | Windows denies log writes in that directory for this process. Leave the default paths under `%LOCALAPPDATA%`. |
| `errkb run -- <command>` complains about `--` | Pass `--connector` and `--session` before `--` (known EKB-031 parsing bug). |

## Related

- [`docs/acceptance-vm.md`](acceptance-vm.md) — clean-VM harness, rehearsal evidence, blockers.
- [`docs/privacy.md`](privacy.md) — what is stored and how it is protected.
- [`packaging/windows/`](../packaging/windows) — build, release and installer scripts.
