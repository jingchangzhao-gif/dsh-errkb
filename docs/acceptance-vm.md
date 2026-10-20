# Clean-VM acceptance (EKB-052)

> **Status: blocked** — the acceptance harness and the installer are built and dry-run, but the
> clean-VM run itself **has not been executed**. No hypervisor, VM or snapshot tooling is available
> in the environment this card was implemented in (no `vmrun`, `qemu`, `VBoxManage`; Hyper-V queries
> are blocked for a non-administrator), so the checks that matter here — a fresh Windows install, a
> standard user, a reboot and an uninstall — cannot be performed. The harness below is the script the
> run must execute; the card stays `⚠ 阻塞` until the evidence directory exists.

This file is the *report* side of §11.1: `packaging/windows/vm-acceptance.ps1` is the executable
half. The script exits non-zero when any automatic check fails, writes `results.json` plus one
evidence file per check, and marks the clicks it cannot automate as `MANUAL`.

## Artifacts under test

| Artifact | Value |
| --- | --- |
| installer | `dist\errkb-0.1.0-setup.exe`, 4,950,820 bytes |
| installer SHA256 | `703B535C540BD99D0B3C2ED1FEB6E706404C130DEF6CB2970E0818196077717F` |
| installer toolchain | Inno Setup 6.7.3 (`ISCC.exe`, user-scope install) |
| binary | `dist\errkb.exe`, 10,897,408 bytes |
| binary SHA256 | `f9942ff0cedb3352a59f57dff8633d8fd89fc0832c93313032fce5eb620d002d` |
| binary version | `errkb 0.1.0`, `commit local`, `built 2026-10-21T00:00:00Z`, `go go1.26.4 windows/amd64` |
| database | PostgreSQL 16.15, dedicated cluster on the chosen port (default 5433) |

The installer and binary hashes are the inputs the script verifies (`-ExpectedSha256` for the
installer; the binary hash is printed in `results.json` for cross-checking against this table).
Rebuild both before a real run and update this table in the same commit.

## Required machine

- Windows 11 x64 with a clean snapshot: no Go, Node.js, pnpm or PostgreSQL *service*, no errkb
  install, no development tools on `PATH`.
- The PostgreSQL 16.15 installer (EDB) available on the machine — errkb's release depends on
  PostgreSQL (EKB-004) and does not bundle a server.
- A standard (non-administrator) user account; the installer elevates itself through UAC.
- Two snapshots available: one "clean", one after reboot.

## Findings that block the acceptance even before the VM run

These were found while building the artifacts and are reported here rather than patched around,
as the card requires (`docs/` + the script are the only landed files).

1. **A fresh install cannot record an error.** `packaging/windows/installer.iss:53-57` creates and
   starts the cluster and offers `errkb version`, but nothing applies the schema migrations, and
   `cmd/errkb/main.go` has no `db migrate` subcommand (dispatch: `help/version/daemon/ui/doctor/run/mcp`).
   The daemon refuses to start when the ledger is empty
   (`errkb daemon: schema migration check failed: applied version "", expected "0008" (run `errkb db
   migrate` first)`). Consequence: on a real fresh install, acceptance steps 3–5 (record → reuse fix →
   trust 0.6) fail at the first `errkb run`. Fix belongs to a follow-up card (either `db migrate` in
   the CLI or a migration step in the installer).
2. **Nothing restarts after a reboot.** The installer never registers a Windows service for the
   cluster (`PGManager.RegisterService` exists but is unused) and there is no daemon/tray autostart.
   After reboot the port is closed and `errkb doctor` reports `database: connection refused`.
   Acceptance step 6 fails as-is; this is an EKB-051 follow-up.
3. **Silent install cannot answer the cluster question.** `installer.iss` asks create / reuse / other
   port interactively; `/VERYSILENT` takes the default (create a dedicated cluster on 5433). The
   automated run therefore exercises one branch only; the interactive standard-user click-through is
   a manual gate.

## How to run

```powershell
# 1. artifacts: verify the installer and binary hashes before touching the machine
pwsh -File packaging\windows\vm-acceptance.ps1 -Phase artifacts `
  -ExpectedSha256 703B535C540BD99D0B3C2ED1FEB6E706404C130DEF6CB2970E0818196077717F

# 2. interactive install as a standard user (manual gate)
#    double-click the setup, accept UAC, choose create-reusable cluster, keep default port
pwsh -File packaging\windows\vm-acceptance.ps1 -Phase install -Port 5433

# 3. first run: record once, add a fix, run again — must show the fix and no model involvement
pwsh -File packaging\windows\vm-acceptance.ps1 -Phase firstrun -Port 5433

# 4. loop: run twice with MCP record/feedback and check the second run returns the fix
pwsh -File packaging\windows\vm-acceptance.ps1 -Phase loop -Port 5433

# 5. reboot through the Start menu, sign in as the same standard user, then:
pwsh -File packaging\windows\vm-acceptance.ps1 -Phase afterreboot -Port 5433

# 6. uninstall keeping the data (choose the data-retention answer in the wizard), then
#    reinstall once and confirm the retained cluster is detected:
pwsh -File packaging\windows\vm-acceptance.ps1 -Phase uninstall -KeepData -Port 5433

# 7. uninstall deleting everything:
pwsh -File packaging\windows\vm-acceptance.ps1 -Phase uninstall -Port 5433
```

`-Phase all` runs artifacts → preflight → (install is manual) → firstrun → loop → rebootprepare and
stops before `afterreboot`; `rebootprepare` prints what to do. Every phase exits `0` on pass, `1` on
any failed check, and writes `<temp>\errkb-vm-acceptance-<timestamp>\` with `results.json` and one
file per check.

Dry runs on this host (dirty, administrator):

| Phase | Result |
| --- | --- |
| `artifacts` | PASS — installer present, 4.9 MB, checksum matches |
| `preflight -AllowAdmin -AllowDirtyHost` | PASS — PostgreSQL 16 present, port 5433 free, not installed |

On a real clean VM, `preflight` must run **without** those overrides so the "no development tools"
and "standard user" checks are real.

## Manual gates

- Create cluster without touching the existing instance; the wizard explains the three choices.
- Standard-user UAC prompt and successful install into the per-user application directory.
- First run in a fresh terminal: `errkb run -- cmd /c "exit 9"` records; `errkb search` finds the fix
  after it was recorded through the UI; no model or network call produces the fix.
- Reboot → sign in → nothing manually started → `errkb doctor` has a running database and the tray
  shows `running` (currently expected to fail; see finding 2).
- Uninstall, choose **keep data**, reinstall, confirm reuse of the retained cluster.
- Uninstall, choose **delete data**, confirm no installer/service/port/data residue.

## Evidence layout

```
<temp>\errkb-vm-acceptance-<timestamp>\
  results.json            # one row per check: name, expected, ok, actual, manual, evidence
  <phase>.<file>.txt      # stdout/stderr or JSON dumps per check
  install.exit.txt        # installer exit code
  daemon-health.json      # health document written by the running daemon
```

## Acceptance mapping

| # | Check | Status here |
| --- | --- | --- |
| 1 | Fresh install runs and records | harness ready; blocked by finding 1 |
| 2 | Reuse inside the same install | harness ready (`loop`), blocked by finding 1 |
| 3 | Standard user completes install and first run | manual gate; not run (no VM) |
| 4 | No manual step on a clean machine | blocked by finding 1 (migrations) |
| 5 | Second run shows the fix with no model call | blocked by finding 1 |
| 6 | Autostart after reboot | **fails as-is** (finding 2) |
| 7 | Uninstall keeps data on request | harness ready (`uninstall -KeepData`); not run (no VM) |
| 8 | Uninstall deletes everything | harness ready; not run (no VM) |
| 9 | Machine residue check | harness encodes it (service/port/process/files); not run (no VM) |
| 10 | `docs/acceptance-vm.md` records steps/results | this file |
| 11 | Scripted `vm-acceptance.ps1` with structured output | delivered |
| 12 | Scope = script + this document + plan rows | yes |
| 13 | Plan rows zh/en synced | yes (`⚠ 阻塞` with these findings) |

## Limitations

- The automated run cannot answer the interactive three-way cluster question; that path stays manual.
- The MCP half of the loop uses a one-shot `initialize` + `tools/call` session; a full client
  (Claude Code / Codex) is exercised by their own adapter cards, not here.
- `errkb run -- cmd` without flags before `--` hits the known EKB-031 parsing bug; the harness always
  passes `--connector/--session` first.
- The installer is currently unsigned; a SmartScreen warning is expected and must be documented in
  the release card (EKB-054) rather than worked around here.

## Rehearsal on the development host (2026-10-21)

The clean-VM run itself cannot happen here (no hypervisor, not a clean machine, shell not
elevated), so this section records what could be rehearsed and the defects that surfaced.

### Installer defects found by the rehearsal

1. `packaging/windows/installer.iss` created its cluster page with `CreateInputOptionPage(wpInfoBefore, ...)`.
   In silent mode Setup advances the wizard before the directory page has initialised `{app}`, and
   `NextButtonClick` on that page calls `ClusterDataPresent()`, which expands `{app}`:
   `Runtime error (at 7:84): Internal error: An attempt was made to expand the "app" constant before it was initialized.`
   Fixed by creating the page after the directory page (`wpSelectDir`), which is where `{app}` first exists.
2. `packaging/windows/postgres.ps1` generated the cluster password with
   `[System.Security.Cryptography.RandomNumberGenerator]::Fill(...)` — a .NET 6+ API. The installer runs
   the script under Windows PowerShell 5.1, where it throws
   `does not contain a method named 'Fill'`, so the `[Run]` install step exited 1 and no cluster was created.
   Fixed with the classic `RandomNumberGenerator.Create()` + `GetBytes()` form.

After both fixes the silent install finishes with exit code 0: `errkb.exe`, `postgres.ps1`, `README.md`
and the uninstaller land in `%LOCALAPPDATA%\errkb`, the dedicated cluster listens on 5433, and
`pg\credentials.json` exists. This is the first time the EKB-051 installer was actually compiled and
executed (EKB-051 left it to this card).

### What the rehearsal proved once the schema exists

On the same cluster, applying the eight migrations with a throwaway `database.Migrate` probe (there is no
`errkb db migrate` command) let the whole loop run with the installed binary and a daemon on a named pipe:

- `errkb run --connector cli --session ekb052 --project probe -- cmd /c "exit 9"` recorded the failure
  (`issue a2a91bd8-a30c-4c64-b353-020a8696a52e`, fingerprint `4202fb44db19`).
- `errkb search "command exited with status 9"` returned the issue (exact fingerprint before the second run,
  `jaccard_rerank` afterwards).
- MCP `err_record_solution` created solution version `16374da4-a2f1-4a23-8938-83661786bfe7`.
- The second `errkb run` of the same command printed the fix with `source cli`, `trust 0.50` and the version id.
- MCP `err_feedback success` recorded an outcome; `errkb stats` reported `events=2 issues=1 solutions=1 outcomes=1`.

### Remaining blockers (unchanged)

1. **No schema on a fresh install**: the installer only creates and starts the cluster; `errkb` has no
   `db migrate` subcommand, and the daemon refuses to start without the ledger
   (`schema migration check failed: ... relation "schema_migrations" does not exist`). First run therefore
   fails until migrations are applied by hand.
2. **No autostart**: neither the cluster nor the daemon is registered to start after a reboot.

Both need their own cards before a clean-VM run can pass; this card stays `⚠ blocked`. The two installer
fixes are recorded here because the rehearsal cannot get past `install` without them.
