# Windows acceptance

[简体中文](acceptance-vm.zh-CN.md)

This document defines the release-blocking Windows checks and the smaller set of optional manual checks. A local virtual machine is no longer required to move the project forward.

## Acceptance policy

| Gate | Required for merge/release | What it proves |
| --- | --- | --- |
| WF-011A — unattended lifecycle | Yes | The real setup executable installs the app, creates and migrates the managed PostgreSQL cluster, starts the daemon, records and reuses an error, keeps data through uninstall/reinstall, and removes all data on the delete path. |
| WF-011B — deterministic recovery | Yes | The installed logon script is configured correctly; repeated starts keep one daemon; daemon-only and daemon-plus-database failures recover; the Named Pipe, port, health and saved knowledge return. |
| Interactive clean-machine review | No | A human can additionally inspect the setup pages, Start-menu shortcut, SmartScreen/UAC presentation and behavior after a physical Windows reboot. |

WF-011A and WF-011B run in the `windows-acceptance` job on a fresh, ephemeral GitHub-hosted Windows runner for every pull request. The tag workflow repeats the same acceptance before packaging or publishing. A successful manual VM run may add confidence, but it cannot replace or override a failed automated gate.

## Required sequence

`packaging/windows/ci-acceptance.ps1` executes these stages in order:

1. Verify the installer exists, has a plausible size and matches the measured SHA-256.
2. Confirm PostgreSQL 16 is available, port 5433 is free and errkb is not installed.
3. Silently install the real Inno Setup package.
4. Verify the installed version, credential ACL, schema, health, Named Pipe and absence of an HTTP listener.
5. Record a failing command, write a solution through MCP, rerun the command, receive the known solution, record feedback, search and read statistics.
6. Run WF-011B recovery checks.
7. Uninstall while keeping data.
8. Reinstall, verify the retained connector, issue and solution, then reuse the solution again.
9. Uninstall with data deletion and verify that the port, processes, Run entry and application directory are gone.

Every failed assertion exits non-zero. Human-only checks appear as `SKIP` in automated evidence; they are never reported as `PASS`.

## WF-011B recovery model

`packaging/windows/recovery-acceptance.ps1` does not claim to reboot Windows. It deterministically exercises the product state that must recover at sign-in:

- inspect the per-user `errkb-autostart` Run entry;
- start the installed product twice and verify exactly one installed daemon;
- terminate only the daemon, replay the installed startup script and verify recovery;
- stop both the daemon and managed PostgreSQL cluster, replay the same startup script and verify recovery;
- require port 5433, the Named Pipe and a current health document;
- search for the issue recorded before the failure to prove persistence.

This gives reproducible failure evidence in CI. A real reboot remains useful as an optional manual smoke test because GitHub-hosted runners cannot reboot and resume the same job reliably.

## CI and release integration

- `.github/workflows/ci.yml` exposes the check name `windows-acceptance` on pull requests.
- `.github/workflows/release.yml` runs the same lifecycle and recovery sequence before the release job.
- The release job receives `WINDOWS_ACCEPTANCE_VERSION` only from the successful acceptance job.
- `packaging/windows/release.ps1` rejects a missing or mismatched accepted version.
- `packaging/windows/release-test.ps1` statically guards the lifecycle order, recovery assertions and release gate.

Repository administrators should make `windows-acceptance` a required status check for the protected default branch.

## Evidence

Each run uploads an artifact named `wf-011-windows-acceptance-<run-id>`. It contains:

```text
windows-acceptance/
  results.json
  01-artifacts/
  02-preflight/
  03-install/
  04-first-run/
  05-error-reuse-loop/
  06-recovery/
  07-uninstall-keep/
  08-reinstall-retained/
  09-retained-first-run/
  10-retained-data/
  11-uninstall-delete/
```

Each stage stores structured results plus command, installer, daemon, MCP and search logs. Credentials and database passwords must never be copied into the artifact.

## Local reproduction

The local host needs PowerShell 7, Go, go-winres, Inno Setup 6 and PostgreSQL 16:

```powershell
pwsh -File packaging\windows\build.ps1 -Version 0.1.0
& "<ISCC.exe>" packaging\windows\installer.iss
pwsh -File packaging\windows\ci-acceptance.ps1 `
  -ExpectedVersion 0.1.0 `
  -EvidenceDir artifacts\windows-acceptance
```

This command changes the current user's installed errkb state and deletes its errkb data at the end. Run it only on a disposable test account or ephemeral runner.

## Optional manual review

When a suitable physical or virtual Windows 11 machine is available, a human may additionally check:

- standard-user installation and all interactive PostgreSQL choices;
- Start-menu launch and the visible toolbox pages;
- behavior after a physical restart and sign-in;
- SmartScreen/UAC messaging for the unsigned installer;
- the interactive keep/delete prompts.

Record those observations separately. They are advisory until the project has stable infrastructure for a repeatable real-reboot test.

## Honest boundary

The automated gate proves the installed lifecycle and restart-equivalent recovery on a fresh hosted runner. It does not prove a physical reboot, visual quality of interactive windows, code signing, or behavior on every Windows edition. Documentation and release notes must preserve that distinction.
