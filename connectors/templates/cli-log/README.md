# Generic host template: CLI, log and manual channels

This directory is a **template, not a named adapter**. It exists for hosts with
no stable, testable interface (DeepSeek, Kimi, Qianwen and everything else like
them): connect them through the generic channels described in plan §3.2, and do
not claim official support for a host. A window title is not an interface
contract; a generic route with honest limits is the correct answer until a
stable interface exists.

| Channel | Level (§3.1) | What it gives you | What it cannot give you |
| ------- | ------------ | ----------------- | ---------------------- |
| CLI wrapper | **B** | stdout/stderr, command line, exit code, command category | tool names, structured host events, the host's own retry outcome |
| Log tail | **C** | failure text from a log file | exit codes, structured fields; fingerprints are weaker because text differences (timestamps, paths, order) leak in |
| Manual entry | **E** | a human-selected failure message | anything automatic; volume and classification depend on the person |

The three keys live under `templates:` in [config.yaml](config.yaml). The whole
file is validated by the real configuration contract (`spec/config.cue` plus the
Go loader), so it is a working file to copy, not a sketch. Change the marked
`<...>` values and run the channel you need.

## Level B: wrap the host command

```powershell
errkb run --connector generic-cli --session my-session --project my-project -- `
    <host-cli> <arguments>
```

- The exit code is passed through unchanged, both streams are shown live and
  captured (head+tail capped), and only failures are recorded.
- A first failure creates an issue; the next identical failure prints the known
  fix (marked `APPROXIMATE - confirm before reuse` when the match is fuzzy).
- `capture_exit_codes: true` in the template is the default; non-zero exit codes
  are what turn a run into a failure record.
- The connector/project names you pass are the ones that appear in `doctor`.

## Level C: log tail (status and limits)

The daemon-side tailer exists in the core (EKB-032: UTF-8/UTF-16 detection,
rotation/truncation by file identity, bounded duplicate bursts, backoff), but it
is **not yet exposed as an `errkb` subcommand**. The `templates.log` block
records exactly the settings the tailer will use, so the template is ready when
that wiring lands; do not pretend otherwise.

Until then, two honest routes:

1. **Preferred:** wrap the host itself (level B) whenever you can start it from
   a shell. That gives a real exit code and a stable fingerprint.
2. **Interim scan:** when only a log exists, take one candidate line and send it
   through the wrapper. This is B-level capture of a line you selected, not a
   log channel:

   ```powershell
   $line = (Get-Content <host-log> -Tail 200 |
       Select-String -Pattern '(?i)\b(error|fatal|failed|exception)\b' |
       Select-Object -Last 1).Line
   errkb run --connector generic-log --project my-project -- powershell -Command "Write-Output `"$line`"; exit 1"
   ```

   Expect weaker fingerprints here: the same underlying failure with a different
   timestamp or temp path may not merge. That is the documented cost of C, not a
   bug to work around with a host-specific parser.

Do not write a per-host parser. A regular expression tuned to one host's log is
an adapter without a probe and without acceptance testing — exactly what §3.2
forbids.

## Level E: manual entry

Nothing can wrap or tail a failure you only saw on screen. Hand the text to the
generic capture path:

```powershell
errkb run --connector manual --project my-project -- powershell -Command `
    "Write-Output '<paste one representative error line here>'; exit 1"
```

- The core still owns the classification, redaction and fingerprint; you only
  supply the text.
- The record is stored on the CLI channel (B mechanics) with a `manual`
  connector name. A first-class manual-entry UI is P6 work, not this template.
- For scripts that already have a finished contract event, `errkb record --event
  file.json` accepts one complete `ErrorEvent v1` JSON document (validation is
  strict; the CLI never invents a fingerprint).

## Validate the template

```powershell
# 1. The configuration contract accepts the shipped file (Go test).
go test ./internal/contract -run 'LoadConfigAcceptsGenericHostTemplates|ShippedGenericTemplateIsValid'

# 2. Load it into the real CLI (database checks may fail if no server is up,
#    but a configuration error would fail before any check runs).
errkb doctor --config connectors/templates/cli-log/config.yaml --json
```

After the first wrapped failure, the connector exists in the knowledge base and
`errkb doctor` lists it under its connectors check (name, channel and version
come from the connectors table). That is the same evidence the named adapters
produce; only the amount of structure is smaller.

## Limitations, stated once

- No official support is claimed for any host named in this directory. Use the
  generic channels; a named adapter requires a stable, testable interface and a
  recorded probe (see `connectors/codex/` and `connectors/claude/`).
- Level C cannot see exit codes or structured fields, so its deduplication and
  fingerprint stability are weaker than B.
- The `templates.log` block is validated but not consumed by the daemon yet; the
  README says so plainly instead of implying a working tailer command.
- Anything that depends on a host's internal retry or on UI text is out of
  scope; if you need it, the honest path is a probe and a new plan card.
