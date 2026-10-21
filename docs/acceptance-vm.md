# Clean-VM acceptance (EKB-052)

[简体中文](acceptance-vm.zh-CN.md)

> **Status: blocked, retest pending** — the acceptance harness and the installer are built and
> dry-run, but the clean-VM run itself **has not been executed**. No hypervisor, VM or snapshot
> tooling is available in the environment this card was implemented in (no `vmrun`, `qemu`,
> `VBoxManage`; Hyper-V queries are blocked for a non-administrator), so the checks that matter
> here — a fresh Windows install, a standard user, a reboot and an uninstall — cannot be
> performed. The harness below is the script the run must execute; the card stays `⚠ 阻塞` until
> the evidence directory exists.
>
> The two blocking findings below (a fresh install had no migrations, nothing restarted after a
> reboot) were implemented in **EKB-056**: `errkb db migrate`, the installer migration step, and the
> HKCU Run autostart (`packaging/windows/autostart.ps1`). The dev-host rehearsal of EKB-056 verified
> install, migrations, the Run entry, the loop, a reboot replay and both uninstall choices; the
> clean-VM evidence itself has not been produced yet, so this file still says `blocked`.

> **Artifact warning (2026-10-22).** This report names the locally rebuilt EKB-056 candidate
> (`B116E5BA…` / `F3E77D4C…`). The older files under `dist\release\errkb-0.1.0\` are a different
> revision and are not covered by this rehearsal. The Web toolbox and its launcher are also outside
> this evidence; they require EKB-057–060 and a new clean-VM run. See
> [`project-status.md`](project-status.md).

This file is the *report* side of §11.1: `packaging/windows/vm-acceptance.ps1` is the executable
half. The script exits non-zero when any automatic check fails, writes `results.json` plus one
evidence file per check, and marks the clicks it cannot automate as `MANUAL`.

## Artifacts under test

| Artifact | Value |
| --- | --- |
| installer | `dist\errkb-0.1.0-setup.exe`, 4,959,460 bytes |
| installer SHA256 | `F3E77D4CCAF0501129D092F71E3808ABB20E036BD3A10892DB0D0E55B640CC09` |
| installer toolchain | Inno Setup 6.7.3 (`ISCC.exe`, user-scope install) |
| binary | `dist\errkb.exe`, 10,936,320 bytes |
| binary SHA256 | `B116E5BA493EE5A5CF5AD070332F48159B289761DED89E16C366F25EBF2EAD67` |
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

## Findings recorded before the VM run

These were found while building the artifacts and are reported here rather than patched around,
as the card requires (`docs/` + the script are the only landed files).

1. ~~**A fresh install cannot record an error.**~~ **Addressed by EKB-056.** `packaging/windows/installer.iss`
   creates and starts the cluster, then runs `errkb db migrate --credentials …` before the success
   page; `cmd/errkb/db.go` exposes `db status` / `db migrate` over `database.Migrate` (same advisory
   lock, checksum validation and higher-ledger rejection). Local evidence: a freshly `initdb`-ed
   cluster received all eight migrations (`applied: [0001 … 0008]`), a second run printed
   `already up to date`, and the daemon started against it. The clean-VM first run remains the
   acceptance gate.
2. ~~**Nothing restarts after a reboot.**~~ **Addressed by EKB-056.** `packaging/windows/autostart.ps1`
   writes a per-user HKCU Run entry (Windows denies per-user Scheduled Task registration to a
   standard user, so the Run key is the compliant no-admin mechanism) that starts the cluster and
   the daemon after sign-in; the uninstaller removes it. On the implementation host the entry
   registered, the daemon started and `health.json` reached `status ok`, and re-running the Run
   command brought the pipe up in ~4 s. The sign-in/reboot assertion itself stays a clean-VM check.
3. **Silent install cannot answer the cluster question.** `installer.iss` asks create / reuse / other
   port interactively; `/VERYSILENT` takes the default (create a dedicated cluster on 5433). The
   automated run therefore exercises one branch only; the interactive standard-user click-through is
   a manual gate.

## How to run

```powershell
# 1. artifacts: verify the installer and binary hashes before touching the machine
pwsh -File packaging\windows\vm-acceptance.ps1 -Phase artifacts `
  -ExpectedSha256 F3E77D4CCAF0501129D092F71E3808ABB20E036BD3A10892DB0D0E55B640CC09

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
- Reboot → sign in → nothing manually started → `errkb doctor` has a running database and the pipe
  is reachable. EKB-056 implements this path; the clean-VM run must prove it.
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
| 1 | Fresh install runs and records | dev-host rehearsal passed (install → migrations → `errkb run` recorded); clean VM pending |
| 2 | Reuse inside the same install | harness ready (`loop`); migrations wired in EKB-056 |
| 3 | Standard user completes install and first run | manual gate; not run (no VM) |
| 4 | No manual step on a clean machine | installer runs `db migrate`; dev-host rehearsal passed; clean VM pending |
| 5 | Second run shows the fix with no model call | dev-host rehearsal passed (fix returned with trust 0.60); clean VM pending |
| 6 | Autostart after reboot | HKCU Run entry registered + daemon started (dev host); reboot replay passed; clean VM pending |
| 7 | Uninstall keeps data on request | keep-data run left `pg\`/`logs\`/`health.json`, removed exe + Run key; clean VM pending |
| 8 | Uninstall deletes everything | delete-data run removed the app directory, Run key and port; clean VM pending |
| 9 | Machine residue check | harness encodes it (service/port/process/files); not run (no VM) |
| 10 | `docs/acceptance-vm.md` records steps/results | this file |
| 11 | Scripted `vm-acceptance.ps1` with structured output | delivered |
| 12 | Scope = script + this document + plan rows | yes |
| 13 | Plan rows zh/en synced | yes (`⚠ 阻塞` with these findings) |

## Limitations

- The automated run cannot answer the interactive three-way cluster question; that path stays manual.
- The MCP half of the loop uses a one-shot `initialize` + `tools/call` session; a full client
  (Claude Code / Codex) is exercised by their own adapter cards, not here.
- The `errkb run -- cmd` first-argument parsing bug (`the command must follow --` even when `--`
  leads) was fixed in EKB-056 (`cmd/errkb/run.go`); the harness still passes
  `--connector/--session` first, so both paths are covered.
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

At this historical EKB-052 checkpoint, applying the eight migrations with a throwaway
`database.Migrate` probe (the `errkb db migrate` command did not exist yet) let the whole loop run
with the installed binary and a daemon on a named pipe. EKB-056 supersedes this workaround:

- `errkb run --connector cli --session ekb052 --project probe -- cmd /c "exit 9"` recorded the failure
  (`issue a2a91bd8-a30c-4c64-b353-020a8696a52e`, fingerprint `4202fb44db19`).
- `errkb search "command exited with status 9"` returned the issue (exact fingerprint before the second run,
  `jaccard_rerank` afterwards).
- MCP `err_record_solution` created solution version `16374da4-a2f1-4a23-8938-83661786bfe7`.
- The second `errkb run` of the same command printed the fix with `source cli`, `trust 0.50` and the version id.
- MCP `err_feedback success` recorded an outcome; `errkb stats` reported `events=2 issues=1 solutions=1 outcomes=1`.

### Blockers addressed by EKB-056 (2026-10-22)

The throwaway `database.Migrate` probe and the missing autostart described in the rehearsal above
are superseded by the EKB-056 deliverables:

1. **Migrations run as part of the install and through the CLI.** `errkb db status` prints the
   ledger position; `errkb db migrate --credentials <app>\pg\credentials.json --migrations <app>\migrations`
   applies every pending migration (`applied: [0001 … 0008]`), is idempotent (`already up to date`,
   exit 0), and maps failures to the documented exit codes (2 usage, 4 unavailable, 1 internal).
   The installer calls it after the cluster is up and before the success page; a failure aborts the
   install instead of leaving a half-installed cluster.

2. **No-admin autostart through HKCU Run.** `packaging/windows/autostart.ps1 -Action install` writes a
   per-user `Run` value that starts the cluster and the daemon at sign-in (idempotent `start`);
   `-Action uninstall` removes it and stops the daemon. On this host the entry registered and the
   daemon reached `status ok` with migrations 0008; the installer log shows `db migrate` exiting 0
   before `autostart.ps1 -Action install` exits 0. The reboot check on a clean VM is still required.

The artifact table above carries the EKB-056 rebuild (`errkb.exe` 10,936,320 bytes /
`B116E5BA…`, installer 4,959,460 bytes / `F3E77D4C…`). Re-run the phases below on a clean machine
to turn this report into evidence.

### Lifecycle unification rehearsal (2026-10-22, WF-007)

The PostgreSQL lifecycle now has exactly one implementation. `internal/service/pg_manage.go` is
reached through `errkb pg install|start|stop|uninstall|status|discover`, and
`packaging/windows/postgres.ps1` is a thin wrapper that maps the installer's arguments onto that
command (it no longer calls `initdb`/`pg_ctl` itself). Installer, uninstaller and the logon autostart
entry all go through the wrapper, so they all use the same code.

Rehearsal on this host:

- Wrapper install on a scratch layout (`-DataDir …\pg\data -Port 5442`) exited 0 and created the
  cluster, applied 0001–0008 and wrote `pg\credentials.json`; `status` reported `reachable: true`,
  `server: 16.15`, `schema: 0008`.
- `stop` and `start` are idempotent (second call succeeds without error); `uninstall -DeleteData`
  removed the data, credentials and the extra `logs\`/`health.json` and left port 5442 closed.
- A silent install of the rebuilt setup exited 0. The Inno log shows
  `postgres.ps1 install` exit 0 → `postgres.ps1 start` exit 0 → `errkb db migrate` exit 0
  (`already up to date`, because `pg install` already migrated) → `autostart.ps1 -Action install`
  exit 0. `health.json` reported `status ok` and the ledger 0008.
- Reboot replay: after stopping the daemon and cluster, re-running the HKCU `Run` command started
  the cluster and daemon in 3.4 s, `errkb search` answered, and `health.json` returned to `ok`.
- `/DELETE_DATA=1` uninstall exited 0 and removed `%LOCALAPPDATA%\errkb` completely (Run key gone,
  port closed, no processes).

Remaining for the clean-VM pass: the same sequence on a machine without the development toolchain.

