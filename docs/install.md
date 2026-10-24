# Install, upgrade and uninstall

[简体中文](install.zh-CN.md)

This page covers the supported portable release: `errkb-toolbox.zip` from the
[latest release](https://github.com/jingchangzhao-gif/error-knowledge-base/releases/latest).
There is no installer and no Start-menu shortcut; the ZIP is the distribution.

## Requirements

- Windows 10 or 11, x64.
- PostgreSQL 16 installed (the EDB installer, or an existing local server). errkb never
  bundles a database, but `errkb pg install` uses the installed PostgreSQL binaries to
  create and manage its own cluster. Put `pg_ctl.exe` on `PATH`, or point
  `ERRKB_PG_BIN` at the PostgreSQL `bin` directory.
- A standard (non-administrator) account. The cluster, the daemon and the logon entry all
  run as the signed-in user; nothing needs elevation.

## What the ZIP contains

| File | Purpose |
| --- | --- |
| `errkb.exe` | The single release binary (CLI, daemon, MCP server, Web toolbox host) |
| `start-errkb.cmd` | Opens the Web toolbox (`errkb ui --web`), which starts the daemon |
| `autostart.ps1` | Per-user logon recovery via the `HKCU\...\Run` key |
| `postgres.ps1` | Thin wrapper over `errkb pg` (used by `autostart.ps1`) |
| `migrations\*.sql` | The schema `errkb pg install` applies |
| `README.md`, `CHANGELOG.md`, `LICENSE` | Documentation and license |

Unpack the ZIP into a directory you own (for example
`%LOCALAPPDATA%\errkb-portable`). The release is not code-signed; verify `errkb.exe`
against the `SHA256SUMS` from the release page.

## First run

1. Create the cluster, apply the schema and write the credentials:

   ```powershell
   .\errkb.exe pg install
   ```

   This starts a dedicated cluster on port 5433 under `.\pg\data`, applies
   `migrations\` and writes `.\pg\credentials.json` (mode 0600). Flags:
   `--data-dir`, `--port`, `--database`, `--superuser`, `--migrations`.

   To use an existing PostgreSQL server instead, skip `pg install` and run
   `errkb db migrate --dsn <your-dsn>` once.

2. Start the product:

   ```powershell
   .\start-errkb.cmd
   ```

   The toolbox opens at a local URL; `errkb ui --web` starts the daemon when it is not
   running and keeps it running after the window closes. CLI and MCP hosts use the same
   daemon (`\\.\pipe\errkb`).

3. Optional logon recovery:

   ```powershell
   powershell -NoProfile -ExecutionPolicy Bypass -File .\autostart.ps1 -Action install -AppDir "$PWD"
   ```

   It writes the per-user Run value `errkb-autostart`. Windows does not let a standard
   user register a Scheduled Task, so the Run key is the supported mechanism.

4. Check the state: `.\errkb.exe pg status` and the daemon health file.

## Hosts

- **Codex / Claude Code:** `.\errkb.exe mcp install --host codex` or `--host claude`
  registers the MCP server with the host CLI. DSH uses its plugin; the command prints the
  plugin route instead.
- **Any MCP host:** point it at `errkb.exe mcp`. The server resolves the installed
  `pg\credentials.json` itself and starts the daemon when the pipe is unreachable.

## Upgrade

1. Stop the daemon and replace `errkb.exe`, `migrations\` and the scripts with the new
   release's files.
2. Apply the schema:

   ```powershell
   .\errkb.exe pg status
   .\errkb db migrate --credentials .\pg\credentials.json --migrations .\migrations
   ```

   Migrations are forward-only. The upgrade matrix in `spec/compat.cue`
   (`compat.upgrade`) records which old schemas, configs and Markdown still load.

## Uninstall

```powershell
.\errkb.exe pg uninstall -DeleteData
powershell -NoProfile -ExecutionPolicy Bypass -File .\autostart.ps1 -Action uninstall -AppDir "$PWD"
```

Omit `-DeleteData` to keep the cluster. Delete the unpacked folder afterwards. Nothing
is registered outside the folder and `HKCU\Software\Microsoft\Windows\CurrentVersion\Run`.

## Build from source

```powershell
./packaging/windows/build.ps1 -Version 0.1.3
./packaging/windows/release.ps1 -Mode package -Version 0.1.3 -Tag v0.1.3
```

`build.ps1` writes the version-stamped binary; `release.ps1` writes the portable ZIP,
manifest, SBOM, notices, signing policy and `SHA256SUMS`. Release checks compare the tag,
the changelog section and the binary version; the Windows smoke job in CI runs the same
executable with a real PostgreSQL.

## Troubleshooting

| Symptom | Fix |
| --- | --- |
| `pg install` says the PostgreSQL binaries are missing | Install PostgreSQL 16 or set `ERRKB_PG_BIN` to its `bin` directory |
| `errkb db status` says no database | Pass `--credentials .\pg\credentials.json` or `--dsn`; the installed layout is resolved automatically when the file sits next to the EXE |
| The pipe is missing | Run `errkb ui --web`, `start-errkb.cmd`, or `errkb daemon`; the MCP server and the toolbox start it themselves |
| Port 5433 is busy | `errkb pg install --port <free-port>` (and `--data-dir` for a second instance) |
| Schema version mismatch | `errkb db migrate --credentials .\pg\credentials.json --migrations .\migrations` |