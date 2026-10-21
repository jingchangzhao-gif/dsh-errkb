# Installation candidate, upgrade and uninstall

[简体中文](install.zh-CN.md)

This page separates the **current installer source**, the **locally built acceptance
candidate** and the **older release bundle**. They are not interchangeable.

> **Status note (2026-10-22).** EKB-056 added `errkb db migrate`, the installer migration
> step and HKCU logon startup to the current source. Those changes were rehearsed on the
> development host, but the clean-VM run is still blocked. The existing files under
> `dist\release\errkb-0.1.0\` predate EKB-056 and must not be presented as the current
> installer. There is no supported public release yet. See
> [`project-status.md`](project-status.md) and [`acceptance-vm.md`](acceptance-vm.md).

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

Do not combine files from `dist\` and `dist\release\errkb-0.1.0\`. In the audited local
copy, those directories contain different EXE revisions and command sets. A release must
be rebuilt once from one tagged Git commit.

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
4. Finish the wizard. In the current source, the installer starts the cluster, applies
   migrations `0001`–`0008`, registers the per-user logon command, and copies `errkb.exe`,
   the migrations and lifecycle scripts into the install directory.

The current installer does **not** create a toolbox launcher or Start-menu shortcut. That
is EKB-060 work. Running `errkb.exe` without a subcommand prints CLI usage; it does not
open the toolbox.

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

## Current source versus existing artifacts

The current source contains the first-run plumbing:

```powershell
errkb db migrate --credentials "$env:LOCALAPPDATA\errkb\pg\credentials.json" `
  --migrations "$env:LOCALAPPDATA\errkb\migrations"
```

The installer source runs that command before registering HKCU startup. A missing or
failed migration stops the install instead of leaving a nominally successful but unusable
daemon.

The older EXE in `dist\release\errkb-0.1.0\` does not contain the `db` command. If that
binary is paired with a new installer definition, first run fails because the schema
cannot be created. This is why existing artifacts are acceptance evidence only.

An unmigrated database produces an error similar to:

```text
errkb daemon: schema migration check failed: applied version "", expected "0008"
(run `errkb db migrate` first)
```

Until one coherent tagged build passes clean-VM acceptance, the supported route is the
development flow: build the current source, run `db migrate`, start the daemon with an
explicit DSN, and run `doctor`.

## Start the daemon

```powershell
errkb db migrate --dsn "postgres://postgres@127.0.0.1:5433/errkb?sslmode=disable" --migrations .\db\migrations
errkb daemon --dsn "postgres://postgres@127.0.0.1:5433/errkb?sslmode=disable"
```

Useful flags: `--pipe` (Named Pipe name, default `\\.\pipe\errkb`), `--lock-name` (named
mutex for the single instance), `--health-file`, `--log-file`, `--log-max-bytes`,
`--health-interval`. The daemon never writes logs to stdout. Stop a console-started daemon
with `Ctrl+C`; shutdown is graceful and waits for in-flight requests.

Check the installation with:

```powershell
errkb doctor --dsn "postgres://postgres@127.0.0.1:5433/errkb?sslmode=disable" --migrations .\db\migrations
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
| `schema migration check failed` | The ledger is missing or behind. Use a current source build and run `errkb db migrate`; the older release-folder EXE has no `db` command. |
| `the local API is not reachable` | The daemon is not running or the pipe name differs. Start `errkb daemon` and check `errkb doctor`. |
| `database: connection refused` | PostgreSQL is not running, or the DSN points at the wrong port. Check `errkb doctor`. |
| `flag provided but not defined: -web` | The EXE does not contain the local Web-shell integration. Existing release binaries cannot open it. |
| `ui --web` waits and exits 4 | The daemon is down and the UI has no DSN. Credential-aware installed startup is unfinished; start the managed cluster/daemon or pass a DSN during development. |
| Port 5433 already in use | Another cluster is listening. Choose another port in the wizard or stop the other instance. |
| Daemon log shows an access error under `%TEMP%` | Windows denies log writes in that directory for this process. Leave the default paths under `%LOCALAPPDATA%`. |

## Related

- [`docs/acceptance-vm.md`](acceptance-vm.md) — clean-VM harness, rehearsal evidence, blockers.
- [`docs/project-status.md`](project-status.md) — current source/artifact truth and release blockers.
- [`docs/toolbox-ui.md`](toolbox-ui.md) — UI status and EKB-057–060 scope.
- [`docs/privacy.md`](privacy.md) — what is stored and how it is protected.
- [`packaging/windows/`](../packaging/windows) — build, release and installer scripts.
