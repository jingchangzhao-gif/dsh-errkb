# Toolbox UI design and delivery status (local web shell)

[简体中文](toolbox-ui.zh-CN.md)

Status: **implemented, automated-test covered and released in v0.1.1**. The
current source carries the EKB-057 search shell, the EKB-058 detail and editing screens, and
the EKB-059 statistics, audit and settings screens, all served by `errkb.exe ui --web`;
`packaging/windows/release.ps1` writes `errkb-toolbox.zip`; the ZIP ships the
launcher, autostart script and migrations. The Windows smoke job covers build and
lifecycle basics; a
visual interactive review remains optional. The target remains an interactive UI embedded in `errkb.exe`,
without Chromium, Electron or NW.js.

## Current state

| Item | State | Evidence / gap |
| --- | --- | --- |
| Embedded HTML/CSS/JS and loopback server | Implemented | `internal/webshell/` with a token-protected HTTP surface |
| Token, CSP, loopback bind and APIs | Implemented and tested | search, issue detail, issue/solution writes, stats, audit, settings, deletion preview/confirm |
| `ui --web` command dispatch | Implemented | `cmd/errkb/ui.go` hands `--web` to `cmd/errkb/web.go` |
| Start a missing daemon | Implemented | resolves `pg/credentials.json` and migrations, spawns the daemon and waits for the pipe |
| Detail/edit pages | Implemented | `/issues/{id}` page; appending a version, issue status, solution status and confirmed deletion are audited in the daemon transaction |
| Statistics/audit/settings pages | Implemented | `/stats` and `/settings` pages over `/api/stats`, `/api/audit`, `/api/settings` |
| Launcher and toolbox ZIP | Implemented | `start-errkb.cmd`, `autostart.ps1`, `errkb-toolbox.zip` in `release.ps1` |
| Interactive visual review | Optional | A human may inspect Edge app mode, copy behavior, SmartScreen/UAC and a physical reboot on a clean machine |

## Decisions

1. **The UI is embedded in `errkb.exe`.** HTML/CSS/JS live under `internal/webshell/assets/`
   and are compiled with `//go:embed`. The release binary stays a single file; the
   the portable zip and the SBOM keep the same shape.
2. **The window is the system browser in app mode.** `errkb ui --web` starts a loopback HTTP
   server and opens `msedge --app=<url>` (fallback: the default browser). This is only a
   window host; the product does not depend on a specific browser engine.
3. **The web shell is a presentation layer.** Every operation calls the existing Go
   services over the local pipe (`service.Client` -> daemon -> PostgreSQL). The shell
   contains no retrieval, trust, redaction or storage logic, and never talks to
   PostgreSQL directly.
4. **Loopback + per-launch token.** The server binds `127.0.0.1` on an ephemeral port,
   generates a 32-byte token per launch, serves only same-origin requests, and sets a
   strict CSP with no external resources. The daemon's capture HTTP endpoint stays off
   and unchanged.

## Process model

```
start-errkb.cmd
  -> errkb.exe ui --web
       -> ensure daemon (start it if the pipe is not reachable)
       -> HTTP server on 127.0.0.1:<ephemeral port> (token)
       -> open Edge app window -> http://127.0.0.1:<port>/?token=<token>
       -> window close leaves the daemon and the tray running
```

Flags: `--no-browser` (tests, headless), `--port` (fixed port for debugging),
`--dsn`/`--migrations` only when `ui` has to start the daemon itself.

## Screens

| Screen | Content | Go source |
| --- | --- | --- |
| Search | address-bar input, top-3 hits, exact/approximate badge, match reason, empty state that names the next step | `ui.OmniboxState`, `service.SearchService` |
| Issue detail | summary, occurrences with environment, solution versions (newest first, source/risks/applies-to), outcomes, trust read-only, copy fix | `ui.DetailPage`, `service.DetailService` |
| Stats and audit | false merges (target 0), exact/approximate hit rate, measured p50/p95, measured vs estimated savings with the assumption text, audit filter (time/action/entity) | `ui.StatsPage`, `audit.Service` |
| Connector and privacy | connector list with enabled toggle, project pause, capture pause, retention 7/90/365, HTTP state, delete preview then confirm, retention run with confirmation | `ui.SettingsPage`, `database.PreviewDelete/DeleteScoped/ApplyRetention` |

Read-only surfaces keep their existing guarantees: approximate results are always badged,
false merges are always displayed, trust is never editable, deletions require a preview
and a second confirmation, and every mutation writes an audit row.

## HTTP surface

All `/api/*` requests require `X-Errkb-Token`; the value is injected into the page by the
server, never stored in a file. Payloads reuse the contract structs.

| Route | State | Owner |
| --- | --- | --- |
| `GET /`, `GET /assets/*` | Implemented locally | EKB-057 |
| `POST /api/search` | Implemented locally | EKB-057 |
| `GET /api/issues/{id}` and issue/solution mutation routes | Implemented locally | EKB-058 |
| `GET /api/stats`, `GET /api/audit`, settings and deletion routes | Implemented locally | EKB-059 |

Every handler validates its payload, maps service errors to `{code, message}` and never
returns raw DSNs, passwords or unredacted samples.

## Packaging and release

The EKB-060 packaging implementation now does the following:

- `packaging/windows/build.ps1` embeds the assets in the binary.
- `release.ps1` produces `errkb-toolbox.zip` from a strict file allowlist.
- `start-errkb.cmd` is the launcher that runs
  `errkb.exe ui --web`; the UI itself is not opened automatically at logon.
- The ZIP and the standalone EXE identify one tag/commit and share release hashes.

EKB-060 and WF-012 are complete. The v0.1.1 tagged release contains the coherent
toolbox ZIP and standalone EXE; its identity and hashes were verified after
download. Local ignored `dist/` files are not release authority.

## Testing

- Go unit tests for token rejection, route validation, CSP headers, static asset serving,
  and no-DB-start behaviour; table tests keep the JSON shapes identical to the contract.
- Integration tests start `errkb ui --no-browser` against a real daemon and drive search,
  detail, stats, and a delete preview/confirm cycle; a fake pipe client is used for the
  unit layer.
- A headless smoke script (`tests/e2e/ui_smoke_test.go`) starts the shell, fetches the
  index, posts one search and one settings read, and asserts no external asset URL appears
  in the HTML.
- Optional manual evidence: the app-mode window opens, the four screens render, copy works,
  and the release zip contains the shell.

## Card split

1. `EKB-057` web shell foundation: **implemented locally** — server, embedded assets,
   token, search page, detail-page navigation, credential-aware daemon startup, e2e
   coverage and the toolbox ZIP in the release script; remote merge and owner review
   remain.
2. `EKB-058` detail and editor screens: **implemented locally** — the detail read,
   version append, issue-status and solution-status writes, and confirmation-gated
   deletion; every edit leaves an audit row in the same daemon transaction, and the e2e
   test exercises the write path.
3. `EKB-059` stats, audit and settings screens: **implemented locally** — measured
   statistics, filtered audit page, and the settings page with a deletion preview and
   cancel; the e2e test checks the preview leaves the rows untouched.
4. `EKB-060` packaging integration: **superseded** by the portable distribution — launcher and
   toolbox ZIP ship in v0.1.3; the Windows smoke job and the release checks
   verify the tagged release.

All land in the existing release pipeline; no new runtime is introduced.
