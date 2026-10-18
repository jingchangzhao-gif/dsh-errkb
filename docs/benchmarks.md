# Retrieval performance and crash-safety gates

EKB-027 fixes the performance and crash-safety gates for the P3 knowledge base. The
committed gate is:

- retrieval at 100,000 issues keeps **p95 below 150 ms**;
- committed data survives a real process kill in the middle of a write, a batch
  write and a migration.

The 150 ms number is a gate, not a target. This document records the environment,
the measured numbers, the query plans, the itemized bottlenecks and the optimization
directions; it does not change the threshold and it does not change the measurement
method. Measurements are reproducible with `internal/database/bench_test.go`.

## Status

| Gate | Result |
| --- | --- |
| Exact lookup (fingerprint) p95 | pass (0.6 ms) |
| `pg_trgm` candidates p95 | **miss** (155.3 ms) |
| Full retrieval p95 | **miss** (226.0 ms; a fixable missing index accounts for most of the gap) |
| Crash safety (four kill scenarios) | pass |
| Status of EKB-027 | **⚠ blocked**: the threshold is fixed and the retrieval path needs two follow-up fixes before the gate can pass (see [Findings](#findings-and-optimization-directions)) |

## Environment

| Item | Value |
| --- | --- |
| Date | 2026-10-19 |
| OS | Windows 11 Pro for Workstations 10.0.26200 |
| CPU | Intel Core i9-14900HX (17 cores / 18 logical processors) |
| RAM | 31.8 GB |
| Disk | KIOXIA EXCERIA G2 SSD |
| Go | go1.26.4 windows/amd64 |
| PostgreSQL | 16.15 (EDB installer, local instance) |
| `shared_buffers` | 128 MB |
| `work_mem` | 4 MB |
| `random_page_cost` | 4 |
| `max_parallel_workers_per_gather` | 2 |
| `effective_cache_size` | 4 GB |

The PostgreSQL instance is the default local development configuration from the
EKB-002 toolchain freeze; no tuning was applied for this measurement.

## Dataset

`seedPerformanceDataset` in `internal/database/bench_test.go` generates the dataset
with deterministic SQL, so the same parameters always produce the same distribution.
At the gate size it produces:

| Property | Value |
| --- | --- |
| Issues | 100,000 |
| Events | 221,803 |
| Occurrences | 221,803 |
| Issues with one occurrence | 90,000 |
| Issues with a small tail (2–20 occurrences) | 9,900 |
| Issues with a heavy tail (50–299 occurrences) | 100 |
| Non-ASCII issues (Chinese characters inside the message) | 11,111 |
| Categories | tool 45%, command-exit 35%, llm 15%, agent 5% |
| Projects | five project keys, 20,000 issues each |

Design notes:

- The message generator builds 6–9 pronounceable pseudo-words per issue, appends a
  unique object identifier (`obj-<10 hex>-<10 hex>`) and, for one issue in nine, a
  Chinese character. A real 100k-issue knowledge base holds 100k *distinct* failure
  texts, because identical texts share a fingerprint and merge into one issue; the
  unique object id stands in for the package path that every real error carries. An
  earlier version used a fixed vocabulary and ten templates, which made a large
  share of rows share trigrams and turned the `pg_trgm` prefilter into a mass of
  rechecks; that version measured 198 ms p95 and was replaced before the gate run.
- Events inherit the issue title, project and fingerprint, so the tables cannot
  drift apart, and channel/environment columns vary by row (Windows/pwsh/node 24.16
  and Linux/bash/node 20.11).
- The channel accepts A–E by `g % 5`, error codes, commands and tool names appear
  with fixed deterministic ratios, and `vacuum (analyze)` runs after seeding so GIN
  fastupdate pending lists are flushed and the measurement describes steady-state
  retrieval rather than index maintenance.
- Reproducibility is itself a test: `TestPerformanceGeneratorIsDeterministic` seeds
  two small schemas (500 issues each) and compares an md5 signature over the ordered
  `fingerprint:hits` pairs and the counts.

## Workload and method

Three classes, measured on the same connection after warmup:

| Class | Call |
| --- | --- |
| exact | `database.ExactSearch` by 12-hex fingerprint |
| trigram | `database.FuzzyCandidates` with the issue title |
| retrieval | `database.Retrieve` with the issue title and category `tool` |

- Samples: the first 64 issues by fingerprint order (`fingerprint like '0%'`,
  `order by fingerprint limit 64`). This is a deterministic sample of existing
  titles, **not** a uniform random sample; the limitation is listed below.
- Warmup: 10 rounds per class; measured: 200 rounds per class; each round issues
  one call and records its wall-clock duration (including row materialization and
  the Go wrapper).
- Percentiles are nearest-rank (`summarizeDurations`). Go's `testing.B` only
  reports means, so the gate tests measure percentiles explicitly; no `Benchmark*`
  functions are added.
- Thresholds and weights are the committed defaults: similarity threshold 0.30,
  candidate limit 20, weights 1.00 / 0.40 / 0.20.

## Results at 100,000 issues

| Class | n | p50 | p95 | p99 | Gate (p95 < 150 ms) |
| --- | --- | --- | --- | --- | --- |
| exact | 200 | 0.0 ms | 0.6 ms | 0.7 ms | pass |
| trigram | 200 | 122.1 ms | 155.3 ms | 158.6 ms | **miss** |
| retrieval | 200 | 194.1 ms | 226.0 ms | 230.2 ms | **miss** |

Seeding the 100k dataset and the run itself took 138.6 s (single local instance,
warm cache after seeding).

Smoke size (4,000 issues, run whenever a database is configured) is green:

| Class | p50 | p95 | p99 |
| --- | --- | --- | --- |
| exact | 0.0 ms | 0.6 ms | 0.9 ms |
| trigram | 8.6 ms | 9.4 ms | 11.0 ms |
| retrieval | 10.0 ms | 12.2 ms | — |

## Query plans

Captured with `auto_explain` on the 100k schema (one sampled title, 100 characters).

Trigram candidate generation (`errkb_candidates`), inner statement 82.8 ms:

```text
Limit -> Sort -> Subquery Scan -> Unique -> Sort -> Nested Loop
  -> Nested Loop
       -> Limit -> Sort -> Subquery Scan on s1
            -> Bitmap Heap Scan on events e (actual rows=1)
                 -> BitmapOr
                      -> Bitmap Index Scan on events_normalized_message_trgm_idx
                           (actual time=82.394 ms, rows=1)
                      -> Bitmap Index Scan on events_command_trgm_idx
                           (actual time=0.129 ms, rows=0)
                      -> Bitmap Index Scan on events_tool_name_trgm_idx
                           (actual time=0.042 ms, rows=0)
       -> Index Scan using occurrences_event_idx on occurrences o (0.087 ms)
  -> Index Scan using issues_pkey on issues i (0.010 ms)
```

The whole cost is the GIN bitmap scan on `events_normalized_message_trgm_idx`. It
returns a single row, so the rechecks and the row materialization afterwards are
irrelevant; the scan pays for OR-ing the posting lists of the query's trigrams.

Cost scales with query length (same schema, `set enable_seqscan = off`, function
call with the title truncated):

| Query length | `errkb_candidates` |
| --- | --- |
| 100 chars | 74–89 ms |
| 64 chars | 47–49 ms |
| 48 chars | 31–34 ms |
| 32 chars | 20–21 ms |

Exact lookup (`errkb_search` by fingerprint): the `issues_fingerprint_project_key_key`
index scan is 0.011–0.039 ms and the first cold call of the wrapper is 4.3 ms; warm
calls are 0.1–0.2 ms.

Latest-occurrence lookup used by the retrieval class
(`internal/database/rerank.go`, `loadRetrievalCandidates`):

```text
Limit -> Limit -> Index Only Scan using occurrences_issue_latest_idx
                              (actual time=0.037 ms, rows=1)
```

Without that index (the state EKB-027 found) the same lookup is
`206.8 ms / 236.9 ms` because `occurrences` has no index on `issue_id`
(`occurrences_pkey` is on `id`, `occurrences_event_idx` on `event_id`,
`occurrences_occurred_at_idx` on `occurred_at`). EKB-020 dropped the
`(issue_id, event_id)` unique constraint to allow replays and with it the only
index that covered this access path.

## Findings and optimization directions

Both gate misses have measured causes; none of them justifies lowering the
threshold or changing the method.

1. **Missing `occurrences (issue_id, …)` index** (retrieval class, ~70–200 ms of the
   gap). Adding `create index occurrences_issue_latest_idx on occurrences
   (issue_id, occurred_at desc, id desc);` inside the kept schema changed the
   latest-occurrence lookup from 206.8–236.9 ms to 0.037–0.28 ms. This is a new
   migration; it is outside EKB-027's strict landing (`bench_test.go`,
   `docs/benchmarks.md`, plan row only) and must be authorized as a follow-up card.
2. **GIN trigram scan cost on long queries** (trigram class, 155.3 ms p95, ~3.5%
   over the gate). The scan cost is dominated by the number of query trigrams.
   Measured options:
   - capping the fuzzy candidate input at 64 characters cuts the function cost to
     47–49 ms (32 characters: 20–21 ms), but it changes candidate generation and
     must be re-validated against the `distinct-lookalike` zero-false-merge gate
     and the `same-error` recall record (EKB-024), because the unique object suffix
     would be cut;
   - GiST is **not** the answer: with `gist_trgm_ops` the same function measured
     444–593 ms on this schema (build 8.79 s for the message index alone), six to
     seven times worse than GIN. EKB-023's plan-mandated GIN choice still holds at
     100k; the earlier 3000-row GiST/GIN comparison did not generalize;
   - a two-phase scan (per-field `LATERAL` scan with a per-field cap) could bound
     the OR-ed posting lists, but it changes `errkb_candidates` and needs its own
     card.

The retrieval class also pays the trigram class cost; with the occurrence index in
place and the current trigram path its p95 is expected to sit at roughly
trigram p95 + 1–2 ms, i.e. still above 150 ms until the trigram path is addressed.
Both follow-ups must be re-measured with the same generator, samples and method.

## Crash safety

`TestCrashSafety` kills a real child process (`os.Executable()` running
`TestCrashHelper`) with `Process.Kill` and then waits until the server-side backend
disappears from `pg_stat_activity` before inspecting the database.

| Scenario | Assertion |
| --- | --- |
| single write, uncommitted (kill while the transaction is open) | counts unchanged, no advisory locks left |
| single write, committed (kill after the commit) | event, occurrence and issue are present exactly once |
| batch write (kill around 400 ms into an infinite `RecordEvent` loop) | `events = occurrences`, no orphan occurrence, no partial row |
| migration (kill while `0007_crash_probe.sql` is mid-statement) | ledger still 6 rows with `0006` as the highest version, `crash_probe` table absent, no advisory locks; rewriting the probe without the sleep and rerunning applies only `0007` (ledger 7, table present) |

All four scenarios pass. On Windows the server cannot notice a dead client during
a long statement (`client_connection_check_interval` must be 0 on this platform), so
the migration probe sleeps 8 seconds and the helper cleanup allows 45 seconds; the
kill itself is still a real process kill, not a mock.

## CI layering

The gate must not slow CI down:

- `TestPerformanceSmoke` and `TestCrashSafety` run whenever `ERRKB_TEST_DATABASE_URL`
  is set; with 4,000 issues they take a few seconds.
- `TestPerformanceGate` (100,000 issues, 200 rounds per class) is opt-in with
  `ERRKB_PERF_BENCH=1` and is meant for a manual run on a workstation before
  accepting a retrieval change.
- `ERRKB_PERF_KEEP=1` keeps the generated schema (`errkb_perf_<nanoseconds>`) for
  plan inspection; the schema must be dropped afterwards.
- No CI job runs Go database tests today: the `go` job has no PostgreSQL service and
  skips, while the `database` job runs SQL migrations and a 50-session smoke. When a
  database-backed Go job is added, `TestPerformanceSmoke` and `TestCrashSafety` are
  the intended smoke set; the 100k gate stays manual.

Reproduce:

```text
set ERRKB_TEST_DATABASE_URL=postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable
go test ./internal/database/ -run 'TestPerformanceSmoke|TestCrashSafety|TestPerformanceGenerator' -v -count=1
set ERRKB_PERF_BENCH=1
set ERRKB_PERF_KEEP=1
go test ./internal/database/ -run TestPerformanceGate -v -count=1
```

## Limitations

- One machine, one run: no variance study, no concurrent workload, warm OS cache
  after seeding.
- The 64 sampled titles are deterministic (first by fingerprint order), not a
  uniform random sample; they do cover all message shapes because fingerprints are
  assigned before messages.
- Local PostgreSQL with default parameters; a production instance with more memory
  would change the GIN scan cost, but the gate is defined for this recorded
  environment.
- The trigram threshold is the committed default (0.30); raising it would reduce
  candidate rows, not the posting-list scan that dominates.
