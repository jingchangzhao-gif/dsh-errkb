# Retrieval performance and crash-safety gates

[简体中文](benchmarks.zh-CN.md)

EKB-027 fixes the performance and crash-safety gates for the P3 knowledge base. The
committed gate is:

- retrieval at 100,000 issues keeps **p95 below 150 ms**;
- committed data survives a real process kill in the middle of a write, a batch
  write and a migration.

The 150 ms number is a gate, not a target. This document records the environment,
the measured numbers, the query plans, the fixes the first blocked run produced and
the optimization directions that were measured and rejected. Measurements are
reproducible with `internal/database/bench_test.go`.

> This is historical evidence for the source revision tested on 2026-10-19. It does
> not mean the current `dist`, installed executable and old release bundle form one
> coherent release. See [project-status.md](project-status.md).

## Status

| Gate | Result |
| --- | --- |
| Exact lookup (fingerprint) p95 | pass (0.6 ms) |
| `pg_trgm` candidates p95 | pass (81.7 ms) |
| Full retrieval p95 | pass (84.5 ms) |
| Crash safety (four kill scenarios) | pass |
| Parallel live-database gate (WF-013) | pass, 3/3 fresh full runs |
| Status of EKB-027 | **accepted** |

The first 100k run (same generator, same method) produced p95 **155.3 ms** for the
`pg_trgm` class and **226.0 ms** for full retrieval, so EKB-027 was recorded as
`⚠ blocked`. The two causes it named are fixed by this change set (see
[Fixes applied after the blocked run](#fixes-applied-after-the-blocked-run)); the
threshold and the measurement method were not changed in either run.

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
| Issues with a small tail (2–19 occurrences) | 9,900 |
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
  rechecks; that version measured 198 ms p95 and was replaced before the first gate
  run.
- Events inherit the issue title, project and fingerprint, so the tables cannot
  drift apart, and channel/environment columns vary by row (Windows/pwsh/node 24.16
  and Linux/bash/node 20.11).
- `vacuum (analyze)` runs after seeding so GIN fastupdate pending lists are flushed
  and the measurement describes steady-state retrieval rather than index
  maintenance. Seeding plus VACUUM took 78.7 s in the accepted run.
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

Accepted run (`TestPerformanceGate`, 109.3 s total):

| Class | n | p50 | p95 | p99 | Gate (p95 < 150 ms) |
| --- | --- | --- | --- | --- | --- |
| exact | 200 | 0.0 ms | 0.6 ms | 0.6 ms | pass |
| trigram | 200 | 65.5 ms | 81.7 ms | 84.7 ms | pass |
| retrieval | 200 | 68.7 ms | 84.5 ms | 87.9 ms | pass |

For comparison, the first blocked run measured trigram p50 122.1 / p95 155.3 /
p99 158.6 ms and retrieval p50 194.1 / p95 226.0 / p99 230.2 ms, with the exact
class unchanged.

Smoke size (4,000 issues, runs whenever a database is configured) is green:

| Class | p50 | p95 | p99 |
| --- | --- | --- | --- |
| exact | 0.0 ms | 0.6 ms | 0.9 ms |
| trigram | 8.6 ms | 9.4 ms | 11.0 ms |
| retrieval | 10.0 ms | 12.2 ms | — |

## Query plans

Captured with `auto_explain` on the accepted 100k schema (one sampled title, capped
to the 64-rune trigram input).

Trigram candidate generation (`errkb_candidates`), inner statement 58.8 ms:

```text
Limit -> Sort -> Subquery Scan -> Unique -> Sort -> Nested Loop
  -> Nested Loop
       -> Limit -> Sort -> Subquery Scan on s1
            -> Bitmap Heap Scan on events e (actual rows=1)
                 -> BitmapOr
                      -> Bitmap Index Scan on events_normalized_message_trgm_idx
                           (actual time=58.538 ms, rows=1)
                      -> Bitmap Index Scan on events_command_trgm_idx
                           (actual time=0.078 ms, rows=0)
                      -> Bitmap Index Scan on events_tool_name_trgm_idx
                           (actual time=0.026 ms, rows=0)
       -> Index Scan using occurrences_event_idx on occurrences o (0.014 ms)
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
| 64 chars | 47–58 ms |
| 48 chars | 31–34 ms |
| 32 chars | 20–21 ms |

Exact lookup (`errkb_search` by fingerprint): the `issues_fingerprint_project_key_key`
index scan is 0.017–0.068 ms and the first cold call of the wrapper is 5.0 ms; warm
calls are 0.1–0.2 ms.

Latest-occurrence lookup used by the retrieval class
(`internal/database/rerank.go`, `loadRetrievalCandidates`):

```text
Limit -> Limit -> Index Only Scan using occurrences_issue_latest_idx
                              (actual time=0.025 ms, rows=1)
```

## Fixes applied after the blocked run

The first run named two causes. Both fixes are in this change set and were
re-measured with the same generator, samples and method.

1. **`0007_occurrence_lookup.sql`** adds
   `occurrences_issue_latest_idx on occurrences (issue_id, occurred_at desc, id desc)`.
   EKB-020 dropped the `(issue_id, event_id)` unique constraint so replays could
   append a second occurrence, and the only index covering the latest-occurrence
   lookup disappeared with it: one lateral cost 206.8–236.9 ms because it scanned
   all 221,803 occurrences. With the new index it is an `Index Only Scan` at
   0.025–0.28 ms, which removes the retrieval class's extra ~140–200 ms.
2. **`database.TrigramQueryMaxChars = 64`** caps the text handed to the pg_trgm
   stage in `internal/database/candidates.go` (the cap is counted in runes and is
   applied in `normalizeCandidateQuery`). The GIN scan cost grows with the number
   of query trigrams, and the fuzzy stage only produces candidates for the Go
   reranker, which still compares the full message (EKB-024): capping the candidate
   query cut the `pg_trgm` class from p50 122.1 / p95 155.3 ms to p50 65.5 /
   p95 81.7 ms. Candidate recall is unchanged on the labelled corpus: the
   EKB-024 tests still show zero false automatic merges on all 12
   `distinct-lookalike` pairs and the same `same-error` recall record (10/10, with
   `same-error-009` still recorded as a gap). The new
   `TestFuzzyCandidatesCapTheQueryText` locks the cap in: two queries that differ
   only after the cap produce the same candidates.

Rejected direction: **GiST instead of GIN**. With `gist_trgm_ops` the same function
measured 444–593 ms on a 100k schema (message index build 8.79 s), six to seven
times worse than GIN; EKB-023's plan-mandated GIN choice still holds at 100k, and
the earlier 3000-row GiST/GIN comparison did not generalize.

## Crash safety

`TestCrashSafety` kills a real child process (`os.Executable()` running
`TestCrashHelper`) with `Process.Kill` and then waits until the server-side backend
disappears from `pg_stat_activity` before inspecting the database.

| Scenario | Assertion |
| --- | --- |
| single write, uncommitted (kill while the transaction is open) | counts unchanged, no advisory locks left |
| single write, committed (kill after the commit) | event, occurrence and issue are present exactly once |
| batch write (kill around 400 ms into an infinite `RecordEvent` loop) | `events = occurrences`, no orphan occurrence, no partial row |
| migration (kill while the crash probe is mid-statement) | the ledger keeps every committed migration (7 rows, highest `0007`), the `crash_probe` table is absent, no advisory locks; rewriting the probe without the sleep and rerunning applies only the probe version (ledger 8, table present) |

All four scenarios pass. The probe migration version is derived from the committed
migrations (`LoadMigrations`), so the crash suite keeps working as migrations are
added. On Windows the server cannot notice a dead client during a long statement
(`client_connection_check_interval` must be 0 on this platform), so the migration
probe sleeps 8 seconds and the helper cleanup allows 45 seconds; the kill itself is
still a real process kill, not a mock.

WF-013 removed a test-only source of nondeterminism found during the 2026-10-22
release review. The lock assertion now inspects advisory locks owned by the killed
`errkb-crash-helper` backend instead of counting every advisory lock in the shared
database. `TestCrashLockAssertionIgnoresUnrelatedAdvisoryLocks` holds a lock on an
unrelated backend, proves that it is ignored, then identifies that backend as the
crash helper and proves that the scoped check detects it. The focused crash pair
passed three consecutive runs, followed by three consecutive fresh parallel
`ERRKB_TEST_DATABASE_URL=<redacted> go test ./... -count=1` runs. No `-p 1`
serialization was used.

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
- The 64-rune candidate cap is a measured trade-off: the reranker still ranks with
  the full text and the labelled-corpus gates are unchanged, but a discriminating
  detail that appears only after the first 64 runes of a query cannot create a
  candidate through the trigram stage. The exact and deterministic-filter stages
  are unaffected.
