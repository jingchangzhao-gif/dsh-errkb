# CLI technical-error loop (EKB-035)

[简体中文](e2e-cli-loop.zh-CN.md)

Status: **passed** on 2026-10-19, Windows 11 (10.0.26200), PostgreSQL 16.15 on
`127.0.0.1:5432`, `errkb dev` built from this repository.

> This is historical acceptance evidence for the EKB-035 implementation point. It
> does not mean the current `dist`, installed executable and old release directory
> are mutually consistent. See [project-status.md](project-status.md).

This is the real end-to-end acceptance the plan calls for at the P4 gate: one
genuinely failing command, the production daemon, the production pipe client,
the real PostgreSQL store and the real retrieval path. Nothing was mocked and
nothing was hardcoded: the solution text below was written through the
product's own solution API and read back by the retriever.

The repeatable script is `tests/e2e/cli_loop_test.go`
(`go test ./tests/e2e/ -run TestCLIErrorLoop -v`). It skips unless
`ERRKB_TEST_DATABASE_URL` points at a database it may create a schema in. The
transcript below is the artifact dump of one run
(`ERRKB_E2E_ARTIFACT_DIR=<dir>`); the script asserts every fact quoted here.

## Environment

| Fact | Value |
| --- | --- |
| OS | Windows 11 Pro for Workstations 10.0.26200 |
| Database | PostgreSQL 16.15, `pg_trgm` 1.6 |
| Binary | `errkb dev` (go1.26.4, windows/amd64) |
| Transport | Named Pipe (`\\.\pipe\errkb-e2e-…`) only; no TCP listener |
| Model calls | **none** |
| Network | localhost PostgreSQL only; no outbound connection |

Doctor's own pre-check was all green before the loop (eight checks), which the
script enforces: a degraded environment skips with the failing check named.

## No model dependency

The P4 gate says "no model dependency", and this run demonstrates it:

- `go.mod` requires only `pgx`, `go-winio`, `golang.org/x/sys` and `yaml.v3`;
  `go list -deps ./cmd/errkb` contains no model SDK.
- No first-party file imports `net/http`; the CLI's transport is the local
  Named Pipe, the daemon's outward edge is the PostgreSQL connection.
- Nothing in the transcript is generated text: the answer below is a row from
  `solution_versions`, selected by SQL.

## Scenario one — the first failure creates an issue and returns no fix

```text
$ errkb run --connector <connector-id> --session <session-id> --project e2e --json -- cmd /c type <missing-path>
```

The wrapped command fails (`type` of a path that does not exist), the CLI passes
its exit code through, and the failure is recorded:

```json
{
  "exit_code": 1,
  "record": {
    "schema_version": "v1",
    "event_id": "a97fde40-c008-4661-ab05-2bb2803e8ac9",
    "occurrence_id": "5be69cb1-a5c2-42a4-9bee-6b5ff1fc361f",
    "issue_id": "c73c64e2-41bf-4c29-8df0-a579fc25e154",
    "fingerprint": "f713a9cf9af8",
    "created": true
  },
  "lookup": {
    "schema_version": "v1",
    "candidates": [
      {
        "issue_id": "c73c64e2-41bf-4c29-8df0-a579fc25e154",
        "fingerprint": "f713a9cf9af8",
        "title": "The system cannot find the file specified.",
        "match_reason": "exact_fingerprint",
        "match_detail": "score 1.00; matched: fingerprint",
        "approximate": false,
        "confidence": 1
      }
    ]
  }
}
```

`created: true`, one issue in the store, one `retrievals` row naming the
fingerprint reason — and **no `solution` object anywhere**, because this issue
had no fix yet. Had a fix been returned here, the card requires marking the run
`⚠ blocked`; it was not.

## Scenario two — the same failure returns the known fix

A solution was then recorded through the product API (`CreateSolution` →
`AddSolutionVersion` → `TransitionSolution(verified)`), not written into the
test output:

> Open the missing path in Explorer and check that the drive and the folder
> exist, then rerun the command.

Rerunning the same failing command:

```text
$ errkb run --connector <connector-id> --session <session-id> --project e2e -- cmd /c type <missing-path>

errkb: failure recorded as issue c73c64e2-41bf-4c29-8df0-a579fc25e154 (f713a9cf9af8)
errkb: candidates from the knowledge base
1. The system cannot find the file specified.
   issue c73c64e2-41bf-4c29-8df0-a579fc25e154  fingerprint f713a9cf9af8
   confidence 1.00
   reason exact_fingerprint (exact match)
   detail score 1.00; matched: fingerprint
   fix Open the missing path in Explorer and check that the drive and the folder exist, then rerun the command.
   source e2e  trust 0.50
   applies to os=windows
   version 1c798104-50c4-4f26-afab-fbaa7834ea3a
```

Same issue id as the first run, the fix text matches the stored row exactly, and
the answer is traced: `source e2e`, `applies to os=windows`, trust computed by
`errkb_trust()` in SQL (`0.50` = (0+2)/(0+0+4)).

## Scenario three — the outcome is recorded

```text
$ errkb feedback c73c64e2-41bf-4c29-8df0-a579fc25e154 1c798104-50c4-4f26-afab-fbaa7834ea3a --outcome success --json
```

```json
{ "schema_version": "v1", "outcome_id": "7cc1af5e-e260-40ca-a693-50d9c76f7811", "recorded": true }
```

## Scenario four — a near duplicate is a candidate, never an exact hit

A prior observation with the same word bag but a different normalized form
(`The system cannot find the specified file.`) was seeded; searching for the
original message returns both as **approximate** candidates:

```json
{
  "candidates": [
    { "issue_id": "57017232-…", "match_reason": "jaccard_rerank", "approximate": true, "confidence": 0.55 },
    {
      "issue_id": "c73c64e2-…",
      "match_reason": "jaccard_rerank",
      "approximate": true,
      "confidence": 0.55,
      "solution": {
        "solution_version_id": "1c798104-…",
        "body": "Open the missing path in Explorer and check that the drive and the folder exist, then rerun the command.",
        "source": "e2e",
        "applies_to": "os=windows",
        "trust": 0.6
      }
    }
  ]
}
```

No candidate claims `exact_fingerprint`, and the human rendering marks both:

```text
reason jaccard_rerank (APPROXIMATE - confirm before reuse)
```

Note the trust differs from scenario two (0.60 vs 0.50): the success outcome was
recorded in between and `errkb_trust()` is a query-time computation, exactly as
EKB-021 specifies.

## What the store contains afterwards

`errkb stats --json`:

```json
{ "totals": { "events": 3, "issues": 2, "solutions": 1, "outcomes": 1 } }
```

The rows behind those numbers (artifact `08-database.txt`):

```text
== issues ==
id | title | status | hits | fingerprint
c73c64e2-41bf-4c29-8df0-a579fc25e154 | The system cannot find the file specified. | open | 2 | f713a9cf9af8
57017232-f5c0-4f22-a543-fb332ceee84e | The system cannot find the specified file. | open | 1 | 2572d7f81b8d

== events ==
event_id | headline | error_code | fingerprint
a97fde40-c008-4661-ab05-2bb2803e8ac9 | The system cannot find the file specified. | <nil> | f713a9cf9af8
19aba844-673d-4ff4-8d4c-a4c4c823ad38 | The system cannot find the file specified. | <nil> | f713a9cf9af8
f00da86f-8693-402a-a500-d0ed183469e9 | The system cannot find the specified file. | <nil> | 2572d7f81b8d

== retrievals ==
candidate_rank | candidate_issue_id | match_fields | score | approximate
1 | c73c64e2-41bf-4c29-8df0-a579fc25e154 | {fingerprint} | 1 | false
1 | c73c64e2-41bf-4c29-8df0-a579fc25e154 | {fingerprint} | 1 | false
1 | 57017232-f5c0-4f22-a543-fb332ceee84e | {token_similarity} | 0.55 | true
2 | c73c64e2-41bf-4c29-8df0-a579fc25e154 | {token_similarity} | 0.55 | true
1 | 57017232-f5c0-4f22-a543-fb332ceee84e | {token_similarity} | 0.55 | true
2 | c73c64e2-41bf-4c29-8df0-a579fc25e154 | {token_similarity} | 0.55 | true

== solutions ==
id | status | version | body | source | applies_to
a5b34a74-28cc-49f6-82b8-26d908f1b808 | verified | 1 | Open the missing path in Explorer and check that the drive and the folder exist, then rerun the command. | e2e | {"os": "windows"}

== outcomes ==
outcome | issue_id | solution_version_id
success | c73c64e2-41bf-4c29-8df0-a579fc25e154 | 1c798104-50c4-4f26-afab-fbaa7834ea3a
```

## P4 gate evidence map

| Gate requirement | Evidence |
| --- | --- |
| real scenario, not a simulation | The test builds `./cmd/errkb`, starts `errkb daemon`, runs `cmd /c type <missing>` twice, and asserts on the printed output and on SQL counts. |
| first run creates an issue | `record.created = true`, issue `c73c64e2-…`, `issues = 1` after the first run. |
| first run returns no fix | The envelope's `lookup.candidates[]` carries no `solution` object (asserted). |
| second run returns the known fix from the database | Human output prints the stored body; the body was inserted by `AddSolutionVersion`, not by the test's expectation. |
| retrieval recorded with reason | Six `retrievals` rows, `match_fields` = `{fingerprint}` for exact hits and `{token_similarity}` for the approximate ones. |
| outcome recorded | `outcomes` row `success` for the exact retrieval, and trust moved 0.50 → 0.60. |
| approximate needs confirmation | `approximate: true` plus `APPROXIMATE - confirm before reuse` in the human rendering, with zero `exact_fingerprint` in the near-duplicate search. |
| no model, no network | `go.mod` and `go list -deps ./cmd/errkb` contain no model SDK; no first-party `net/http`; the only peers are the local pipe and `127.0.0.1:5432`. |
| repeatable script | `go test ./tests/e2e/ -run TestCLIErrorLoop -v` recreates the schema, binary, daemon and all assertions from scratch. |

## Known boundaries

- This record describes the EKB-035 implementation point, when the test migrated
  its throwaway schema through `database.Migrate`. EKB-056 later added
  `errkb db migrate`; current development and installation flows should use that
  command. The explicit DSN remains intentional test isolation.
- The scenario runs the real Windows `cmd /c type`, so this test is
  Windows-only, like the daemon and the CLI; the Linux CI `go` job skips it.
- At the time of this record, `errkb run` required explicit `--connector` and
  `--session`. EKB-056 later added the `cli` defaults and fixed a leading `--`.
- The solution was recorded through the Go API rather than a CLI verb because
  no solution-entry command exists; the card explicitly allows this for the P4
  demo as long as the fix comes from the database, which it does.
