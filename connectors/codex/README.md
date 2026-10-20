# Codex connector (EKB-040)

The Codex CLI adapter is two pieces with one knowledge base:

1. **Capture — channel B, CLI wrapper.** Wrap any `codex exec` invocation with
   `errkb run --`. The wrapper mirrors the process output, keeps the exit code,
   records a non-transient failure through the daemon and prints the known fix
   when the knowledge base has one.
2. **Query and record — MCP.** Register the errkb MCP server so the host can call
   `err_lookup`, `err_search`, `err_record`, `err_feedback`, `err_list`,
   `err_stats`, `err_record_solution` and `err_set_status`.

The adapter adds no judgement: classification, redaction, fingerprinting,
matching and trust stay in the core (see `probe.md` for the measured capabilities
and the live closure record).

## Install

```powershell
# 1. a daemon must be running on the default pipe (or set ERRKB_PIPE)
errkb daemon --dsn "postgres://…" --migrations .\db\migrations

# 2. build and register the MCP server with Codex
go build -o bin\errkb.exe .\cmd\errkb
codex mcp add errkb -- "$PWD\bin\errkb.exe" mcp

# 3. optional: use the wrapper helper instead of calling errkb run by hand
.\connectors\codex\errkb-codex.ps1 exec --skip-git-repo-check "Say hi."
```

`errkb mcp` prints its log to stderr and speaks JSON-RPC on stdout, so an MCP
host can keep it attached to a session; the wrapper (`errkb run`) is the
process-level capture path.

## Configuration

| Setting | Meaning |
| ------- | ------- |
| `ERRKB_PIPE` | Named Pipe name, default `\\.\pipe\errkb` |
| `ERRKB_PROJECT` | Project key the wrapper scopes issues to, default `default` |
| `--connector` | Connector identity the core records on first use (`codex-cli` here) |
| `--session` | Session identity; one value per CLI session is enough |

Hooks are supported by the host (`hooks.toml` is a measured sample for the `Stop`
event) but are **not** the capture path: the probe found no hook that carries the
LLM failure code/message, so hook-based capture is documented as the fallback C
in `probe.md`, not claimed as implemented.

## Verified behaviour

- A real Codex failure (exhausted quota) was captured once per run; a second run
  returned the known fix from PostgreSQL (exact fingerprint, trust 0.60, source
  and applicability shown), and the outcome was recorded through the MCP tool.
- The database side is local only: PostgreSQL behind the Named Pipe; capture and
  lookup do not contact the network.
- `errkb run` passes the child's exit code through, so wrapping does not change
  what a script sees.
