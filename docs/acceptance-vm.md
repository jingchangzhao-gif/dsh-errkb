# Clean-machine acceptance (historical)

This page is kept as a historical record. The installer route was removed on
2026-10-25; the supported distribution is the portable `errkb-toolbox.zip`, and the
live gates are the Windows smoke job in CI and the release checks in
`packaging/windows/release.ps1`. See [`project-status.md`](project-status.md) and
[`install.md`](install.md).

The earlier installer acceptance work produced two useful findings that the portable
layout keeps:

- The first run must apply schema migrations before the daemon starts; the current
  `errkb pg install` applies `migrations\` and `errkb db migrate` covers upgrades.
- Logon recovery must not need administrator rights; the current `autostart.ps1`
  writes a per-user HKCU Run value.

The old `vm-acceptance.ps1`, `ci-acceptance.ps1` and `recovery-acceptance.ps1` scripts
were deleted with the installer. Physical reboot and interactive visual review were
never automated; the current tests restart the daemon and the PostgreSQL instance and
replay the logon command deterministically.