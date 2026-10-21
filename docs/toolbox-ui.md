# Toolbox UI design and delivery status (local web shell)

[简体中文](toolbox-ui.zh-CN.md)

Status: **implementation in progress; not included in a releasable artifact**. The local
working copy contains the EKB-057 search shell, but the current GitHub `main`, installed
binary and release bundle do not expose its `ui --web` route. EKB-058–060 remain open.
The target is still an interactive UI embedded in `errkb.exe`, without Chromium,
Electron or NW.js.

## Current state

| Item | State | Evidence / gap |
| --- | --- | --- |
| Embedded HTML/CSS/JS and loopback server | Implemented locally | `internal/webshell/`; not present in the current remote revision |
| Token, CSP, loopback bind and search API | Implemented locally and unit-tested | Only `POST /api/search` exists |
| `ui --web` command dispatch | Implemented locally | Existing `dist` and GitHub `main` reject `--web` |
| Start a missing daemon | Incomplete | Does not read installed credentials; fails after the 20-second wait when DSN is absent |
| Detail/edit pages | Not started | EKB-058 |
| Statistics/audit/settings pages | Not started | EKB-059 |
| Launcher, Start-menu shortcut and toolbox ZIP | Not started | EKB-060 |
| Clean-VM UI acceptance | Blocked | Requires the completed EKB-057–060 artifact |

## Decisions

1. **The UI is embedded in `errkb.exe`.** HTML/CSS/JS live under `internal/webshell/assets/`
   and are compiled with `//go:embed`. The release binary stays a single file; the
   installer, the portable zip and the SBOM keep the same shape.
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
start-errkb.cmd / Start-menu shortcut
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
| Issue detail and solution mutation routes | Planned | EKB-058 |
| Statistics, audit, settings and deletion routes | Planned | EKB-059 |

Every handler validates its payload, maps service errors to `{code, message}` and never
returns raw DSNs, passwords or unredacted samples.

## Packaging and release

The following is the **EKB-060 target**, not current behavior:

- `packaging/windows/build.ps1` embeds the assets in the binary.
- `release.ps1` produces `errkb-toolbox.zip` from a strict file allowlist.
- `installer.iss` installs the launcher and creates a Start-menu shortcut that runs
  `errkb.exe ui --web`; the UI itself is not opened automatically at logon.
- The ZIP, installer and installed EXE identify one tag/commit and share release hashes.

Current behavior: no toolbox ZIP exists, `start-errkb.cmd` is not in the release bundle,
the installer has no `[Icons]` section, and existing binaries reject `ui --web`.

## Testing

- Go unit tests for token rejection, route validation, CSP headers, static asset serving,
  and no-DB-start behaviour; table tests keep the JSON shapes identical to the contract.
- Integration tests start `errkb ui --no-browser` against a real daemon and drive search,
  detail, stats, and a delete preview/confirm cycle; a fake pipe client is used for the
  unit layer.
- A headless smoke script (`tests/e2e/ui_smoke_test.go`) starts the shell, fetches the
  index, posts one search and one settings read, and asserts no external asset URL appears
  in the HTML.
- Manual evidence on the clean VM: window opens, the four screens render, copy works, and
  the release zip contains the shell.

## Card split

1. `EKB-057` web shell foundation: **in progress locally** — server, embedded assets,
   token and search page exist; remote integration, credential-aware daemon startup,
   e2e coverage and release packaging remain.
2. `EKB-058` detail and editor screens (reuses `DetailService`).
3. `EKB-059` stats, audit and settings screens.
4. `EKB-060` installer/Start-menu integration and clean-VM UI evidence.

All land in the existing release pipeline; no new runtime is introduced.
