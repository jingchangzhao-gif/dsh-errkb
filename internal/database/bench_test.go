package database_test

// This file is the EKB-027 performance and crash gate:
//
//   - a deterministic one-hundred-thousand-issue dataset with a realistic
//     occurrence long tail (most issues appear once, a few appear hundreds of
//     times) and a realistic text distribution (English failures, Chinese
//     failures and mixed messages);
//   - the three retrieval paths of the plan (exact search, pg_trgm candidates,
//     full orchestration) measured as p50/p95/p99 against the committed gate
//     p95 < 150 ms;
//   - crash tests that kill a real process in the middle of a single write, a
//     batch of writes and a migration, then verify that committed data is
//     intact, that no half-written rows remain and that no lock survives.
//
// The gate is a gate, not a target: when p95 misses 150 ms the test fails so the
// owner sees it in CI runs that opt in; the threshold is never lowered and the
// measurement method is never replaced to make a run pass. The environment and
// the query plans of the recorded run live in docs/benchmarks.md.
//
// CI layering: pnpm/go CI has no database, so every database test skips there.
// The smoke test runs whenever ERRKB_TEST_DATABASE_URL is set (local runs, and
// any future database-backed job) and the full 100k gate is opt-in through
// ERRKB_PERF_BENCH=1, which keeps CI fast and makes the timed run reproducible.

import (
	"bufio"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/jingchangzhao-gif/dsh-errkb/internal/database"
)

const (
	perfGateIssues  = 100_000
	perfSmokeIssues = 4_000
	perfGateMaxP95  = 150 * time.Millisecond

	perfGateSamples = 64
	perfGateWarmup  = 10
	perfGateRounds  = 200

	perfSmokeSamples = 16
	perfSmokeWarmup  = 3
	perfSmokeRounds  = 40

	crashHelperApp     = "errkb-crash-helper"
	crashScenarioEnv   = "ERRKB_CRASH_SCENARIO"
	crashSchemaEnv     = "ERRKB_CRASH_SCHEMA"
	crashProjectEnv    = "ERRKB_CRASH_PROJECT"
	crashMigrationsEnv = "ERRKB_CRASH_MIGRATIONS"
)

// perfMessageSQL renders the text of issue number g: six to nine pronounceable
// pseudo-words plus a unique object id. The words come from the hash of (g, i),
// not from a stride or a small dictionary: a fixed vocabulary makes every
// message share trigrams with a large share of the table, which turns the
// pg_trgm prefilter into a full scan of rechecks. Real knowledge bases hold
// that many distinct failure texts too, because identical texts already share a
// fingerprint, and the unique object id stands in for the package path every
// real error carries.
const perfMessageSQL = `(
    select string_agg(
      translate(substr(md5(g::text || ':' || i::text), 1, 6), '0123456789abcdef', 'bcdfghjklmnpqrst')
      || case when g % 2 = 0 then translate(substr(md5(g::text || ':x' || i::text), 1, 4), '0123456789abcdef', 'bcdfghjklmnpqrst') else '' end,
      ' ')
    from generate_series(1, 6 + (g % 4)) i
  )
  || case when g % 9 = 0 then ' 错误' || chr(19968 + (g * 7919) % 20000) else '' end
  || ' obj-' || substr(md5(g::text || ':obj'), 1, 10) || '-' || substr(md5(g::text || ':obj2'), 1, 10)
  || ' e' || substr(md5(g::text), 1, 10)`

// perfIssuesSQL seeds N issues. The category split is 45% tool, 35%
// command-exit, 15% llm and 5% agent; project_key rotates over five projects.
var perfIssuesSQL = `insert into issues (
    fingerprint, project_key, title, category, status, trigger,
    first_seen, last_seen, hits, source, actor)
select
    lpad(to_hex(g), 12, '0'),
    'proj-' || (g % 5),
    left(workload.msg, 120),
    case when g % 100 < 45 then 'tool'
         when g % 100 < 80 then 'command-exit'
         when g % 100 < 95 then 'llm'
         else 'agent' end,
    'open',
    workload.msg,
    now() - make_interval(secs => (g % 2592000)),
    now() - make_interval(secs => (g % 2592000)),
    0,
    'perf', 'perf'
from (
    select g, ` + perfMessageSQL + ` as msg
    from generate_series(1, $1) g
) workload`

// perfEventsSQL seeds one event per occurrence slot. The occurrence count per
// issue is the long tail: 90% of issues appear once, 9.9% between 2 and 20
// times, 0.1% between 50 and 300 times (about 216k occurrences for 100k
// issues). Event ids are deterministic so a rerun over the same parameters
// produces the same distribution, and the event text is taken from the issue so
// the two tables cannot drift apart.
var perfEventsSQL = `insert into events (
    event_id, connector_id, session_id, observed_at, channel, category, severity, transient,
    headline, normalized_message, redacted_sample, error_code, command, exit_code, tool_name,
    os, shell, runtime_versions, project_key, fingerprint, schema_version, evidence, confidence,
    source, actor)
select
    ('00000000-0000-4000-8000-' || lpad(to_hex(g * 1000 + k), 12, '0'))::uuid,
    $2::uuid,
    null,
    now() - make_interval(secs => (g % 2592000) + k * 3600),
    (array['A','B','C','D','E'])[1 + (g % 5)],
    case when g % 100 < 45 then 'tool'
         when g % 100 < 80 then 'command-exit'
         when g % 100 < 95 then 'llm'
         else 'agent' end,
    'error',
    false,
    left(i.title, 200),
    i.title,
    left(i.title, 500),
    case when g % 6 = 0 then 'E' || (100 + g % 900) end,
    case when g % 3 = 0 then 'pnpm build --filter pkg-' || (g % 5000) end,
    null,
    case when g % 4 = 0 then 'pwsh' end,
    'windows',
    'powershell',
    '{"node":"24.16.0"}'::jsonb,
    i.project_key,
    i.fingerprint,
    'v1',
    'benchmark workload',
    0.9,
    'perf',
    'perf'
from generate_series(1, $1) g
join issues i
  on i.fingerprint = lpad(to_hex(g), 12, '0')
 and i.project_key = 'proj-' || (g % 5)
cross join lateral generate_series(1, case
    when ((g::bigint * 2654435761) % 1000) < 900 then 1
    when ((g::bigint * 2654435761) % 1000) < 999 then 2 + ((g * 97) % 19)
    else 50 + ((g * 31) % 250)
end) k`

// perfOccurrencesSQL links every event to its issue. The environment differs by
// capture channel so the retrieval reranker sees real environment differences.
var perfOccurrencesSQL = `insert into occurrences (
    issue_id, event_id, connector_id, project_key, environment, occurred_at, source, actor)
select
    i.id,
    e.event_id,
    e.connector_id,
    e.project_key,
    case when e.channel in ('C', 'D')
         then '{"os":"linux","shell":"bash","runtime_versions":{"node":"20.11.0"}}'::jsonb
         else '{"os":"windows","shell":"powershell","runtime_versions":{"node":"24.16.0"}}'::jsonb end,
    e.observed_at,
    'perf', 'perf'
from events e
join issues i on i.fingerprint = e.fingerprint and i.project_key = e.project_key`

// perfHitsSQL derives the issue aggregates from the occurrences so the hit
// counts are consistent with the tail.
var perfHitsSQL = `update issues i
set hits = c.n, first_seen = c.first, last_seen = c.last, updated_at = now()
from (
    select issue_id, count(*) as n, min(occurred_at) as first, max(occurred_at) as last
    from occurrences
    group by issue_id
) c
where c.issue_id = i.id`

// perfSchema creates and migrates a dedicated schema and switches the
// connection to it. The schema is dropped on cleanup; ERRKB_PERF_KEEP=1 keeps
// it so the recorded EXPLAIN ANALYZE run can be reproduced by hand.
func perfSchema(t *testing.T, conn *pgx.Conn) string {
	t.Helper()
	ctx := context.Background()
	schema := fmt.Sprintf("errkb_perf_%d", time.Now().UnixNano())
	if _, err := conn.Exec(ctx, "create schema "+schema); err != nil {
		t.Fatalf("create benchmark schema %s: %v", schema, err)
	}
	keep := os.Getenv("ERRKB_PERF_KEEP") == "1"
	t.Cleanup(func() {
		if keep {
			t.Logf("kept schema %s for the manual benchmark; drop it when done", schema)
			return
		}
		_, _ = conn.Exec(context.WithoutCancel(ctx), "drop schema if exists "+schema+" cascade")
	})
	if _, err := conn.Exec(ctx, "set search_path to "+schema+", public"); err != nil {
		t.Fatalf("set search_path to %s: %v", schema, err)
	}
	migrate(t, conn, migrationsDir())
	return schema
}

func seedPerformanceDataset(t *testing.T, conn *pgx.Conn, issues int) {
	t.Helper()
	ctx := context.Background()
	start := time.Now()
	var connectorID string
	if err := conn.QueryRow(ctx,
		`insert into connectors (name, channel, source, actor)
		 values ('perf-dataset', 'A', 'perf', 'perf') returning id`).Scan(&connectorID); err != nil {
		t.Fatalf("insert the benchmark connector: %v", err)
	}
	steps := []struct {
		name   string
		query  string
		params []any
	}{
		{"issues", perfIssuesSQL, []any{issues}},
		{"events", perfEventsSQL, []any{issues, connectorID}},
		{"occurrences", perfOccurrencesSQL, nil},
		{"hits", perfHitsSQL, nil},
	}
	for _, step := range steps {
		if _, err := conn.Exec(ctx, step.query, step.params...); err != nil {
			t.Fatalf("seed benchmark %s for %d issues: %v", step.name, issues, err)
		}
	}
	// VACUUM (not just ANALYZE) flushes the GIN fastupdate pending lists: the
	// seed inserts every event in one statement, so without it every query also
	// scans the pending list and the measurement describes index maintenance
	// rather than steady-state retrieval.
	if _, err := conn.Exec(ctx,
		"vacuum (analyze) connectors, events, issues, occurrences"); err != nil {
		t.Fatalf("vacuum the benchmark dataset: %v", err)
	}
	t.Logf("PERF-DATA seeded %d issues in %s", issues, time.Since(start).Round(time.Millisecond))
}

type performanceDistribution struct {
	Issues, Events, Occurrences int
	Single, Small, Heavy        int
	NonASCII                    int
	Categories                  map[string]int
	Projects                    map[string]int
}

func performanceDistributionOf(t *testing.T, conn *pgx.Conn) performanceDistribution {
	t.Helper()
	ctx := context.Background()
	dist := performanceDistribution{Categories: map[string]int{}, Projects: map[string]int{}}
	count := func(query string, dst *int) {
		if err := conn.QueryRow(ctx, query).Scan(dst); err != nil {
			t.Fatalf("count %q: %v", query, err)
		}
	}
	count(`select count(*) from issues`, &dist.Issues)
	count(`select count(*) from events`, &dist.Events)
	count(`select count(*) from occurrences`, &dist.Occurrences)
	count(`select count(*) from (select issue_id from occurrences group by issue_id having count(*) = 1) x`, &dist.Single)
	count(`select count(*) from (select issue_id from occurrences group by issue_id having count(*) between 2 and 49) x`, &dist.Small)
	count(`select count(*) from (select issue_id from occurrences group by issue_id having count(*) >= 50) x`, &dist.Heavy)
	count(`select count(*) from issues where title ~ '[^ -~]'`, &dist.NonASCII)
	categoryRows, err := conn.Query(ctx, `select category, count(*) from issues group by category order by category`)
	if err != nil {
		t.Fatalf("read category distribution: %v", err)
	}
	for categoryRows.Next() {
		var name string
		var n int
		if err := categoryRows.Scan(&name, &n); err != nil {
			categoryRows.Close()
			t.Fatalf("scan category distribution: %v", err)
		}
		dist.Categories[name] = n
	}
	categoryRows.Close()
	projectRows, err := conn.Query(ctx, `select project_key, count(*) from issues group by project_key order by project_key`)
	if err != nil {
		t.Fatalf("read project distribution: %v", err)
	}
	for projectRows.Next() {
		var name string
		var n int
		if err := projectRows.Scan(&name, &n); err != nil {
			projectRows.Close()
			t.Fatalf("scan project distribution: %v", err)
		}
		dist.Projects[name] = n
	}
	projectRows.Close()
	return dist
}

func reportPerformanceDistribution(t *testing.T, dist performanceDistribution) {
	t.Helper()
	if dist.Issues == 0 {
		t.Fatalf("the benchmark dataset is empty")
	}
	t.Logf("PERF-DIST issues=%d events=%d occurrences=%d single=%d small=%d heavy=%d non_ascii=%d",
		dist.Issues, dist.Events, dist.Occurrences, dist.Single, dist.Small, dist.Heavy, dist.NonASCII)
	for name, n := range dist.Categories {
		t.Logf("PERF-DIST category %s=%d (%.0f%%)", name, n, 100*float64(n)/float64(dist.Issues))
	}
	for name, n := range dist.Projects {
		t.Logf("PERF-DIST project %s=%d", name, n)
	}
}

func logPerformanceEnvironment(t *testing.T, conn *pgx.Conn) {
	t.Helper()
	ctx := context.Background()
	var version string
	if err := conn.QueryRow(ctx, `select version()`).Scan(&version); err != nil {
		t.Fatalf("read the PostgreSQL version: %v", err)
	}
	t.Logf("PERF-ENV go=%s platform=%s/%s postgres=%s", runtime.Version(), runtime.GOOS, runtime.GOARCH, version)
	for _, parameter := range []string{"shared_buffers", "work_mem", "random_page_cost", "max_parallel_workers_per_gather", "effective_cache_size"} {
		var value string
		if err := conn.QueryRow(ctx, "show "+parameter).Scan(&value); err != nil {
			t.Fatalf("show %s: %v", parameter, err)
		}
		t.Logf("PERF-ENV %s=%s", parameter, value)
	}
}

type performanceSample struct {
	IssueID     string
	Fingerprint string
	Message     string
}

func performanceSamples(t *testing.T, conn *pgx.Conn, n int) []performanceSample {
	t.Helper()
	rows, err := conn.Query(context.Background(),
		`select id::text, fingerprint, title
		   from issues
		  where fingerprint like '0%'
		  order by fingerprint
		  limit $1`, n)
	if err != nil {
		t.Fatalf("select benchmark samples: %v", err)
	}
	defer rows.Close()
	samples := make([]performanceSample, 0, n)
	for rows.Next() {
		var sample performanceSample
		if err := rows.Scan(&sample.IssueID, &sample.Fingerprint, &sample.Message); err != nil {
			t.Fatalf("scan benchmark sample: %v", err)
		}
		samples = append(samples, sample)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate benchmark samples: %v", err)
	}
	if len(samples) == 0 {
		t.Fatalf("the benchmark dataset has no issues to sample")
	}
	return samples
}

type performanceResult struct {
	Samples       int
	P50, P95, P99 time.Duration
	Min, Max      time.Duration
}

// summarizeDurations reports nearest-rank percentiles. The gate is defined on
// p95, so the measurement reports percentiles explicitly; go test -bench only
// reports means and is deliberately not used as the gate.
func summarizeDurations(samples []time.Duration) performanceResult {
	sorted := append([]time.Duration(nil), samples...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	at := func(quantile float64) time.Duration {
		index := int(math.Ceil(quantile*float64(len(sorted)))) - 1
		if index < 0 {
			index = 0
		}
		if index >= len(sorted) {
			index = len(sorted) - 1
		}
		return sorted[index]
	}
	return performanceResult{
		Samples: len(sorted),
		P50:     at(0.50),
		P95:     at(0.95),
		P99:     at(0.99),
		Min:     sorted[0],
		Max:     sorted[len(sorted)-1],
	}
}

func formatDuration(d time.Duration) string {
	return fmt.Sprintf("%.1fms", float64(d)/float64(time.Millisecond))
}

func runPerformanceClass(t *testing.T, name string, warmup, rounds int, call func(i int) error) performanceResult {
	t.Helper()
	for i := 0; i < warmup; i++ {
		if err := call(i); err != nil {
			t.Fatalf("%s warmup call %d: %v", name, i, err)
		}
	}
	samples := make([]time.Duration, 0, rounds)
	for i := 0; i < rounds; i++ {
		start := time.Now()
		if err := call(i); err != nil {
			t.Fatalf("%s call %d: %v", name, i, err)
		}
		samples = append(samples, time.Since(start))
	}
	result := summarizeDurations(samples)
	t.Logf("PERF %-9s n=%d p50=%s p95=%s p99=%s min=%s max=%s",
		name, result.Samples,
		formatDuration(result.P50), formatDuration(result.P95), formatDuration(result.P99),
		formatDuration(result.Min), formatDuration(result.Max))
	return result
}

func assertPerformanceGate(t *testing.T, name string, result performanceResult) {
	t.Helper()
	if result.P95 >= perfGateMaxP95 {
		t.Errorf("⚠ %s p95 %s misses the committed 150 ms gate; record the environment and the EXPLAIN ANALYZE plans in docs/benchmarks.md and optimize the path (the threshold and the measurement method must not change)",
			name, formatDuration(result.P95))
	}
}

func runPerformanceClasses(t *testing.T, conn *pgx.Conn, samples []performanceSample, warmup, rounds int) map[string]performanceResult {
	t.Helper()
	results := map[string]performanceResult{}
	results["exact"] = runPerformanceClass(t, "exact", warmup, rounds, func(i int) error {
		sample := samples[i%len(samples)]
		_, err := database.ExactSearch(context.Background(), conn, database.ExactSearchQuery{
			Dimension: database.SearchByFingerprint,
			Value:     sample.Fingerprint,
			Limit:     10,
		})
		return err
	})
	results["trigram"] = runPerformanceClass(t, "trigram", warmup, rounds, func(i int) error {
		sample := samples[i%len(samples)]
		_, err := database.FuzzyCandidates(context.Background(), conn, database.DefaultCandidateQuery(sample.Message))
		return err
	})
	results["retrieval"] = runPerformanceClass(t, "retrieval", warmup, rounds, func(i int) error {
		sample := samples[i%len(samples)]
		_, err := database.Retrieve(context.Background(), conn, database.RetrievalRequest{
			Category: "tool",
			Message:  sample.Message,
		})
		return err
	})
	for name, result := range results {
		assertPerformanceGate(t, name, result)
	}
	return results
}

// TestPerformanceSmoke is the cheap layer: a four-thousand-issue dataset and a
// short run that still checks the same p95 gate. It runs whenever a database is
// configured, including any future database-backed CI job.
func TestPerformanceSmoke(t *testing.T) {
	conn := connect(t)
	perfSchema(t, conn)
	logPerformanceEnvironment(t, conn)
	seedPerformanceDataset(t, conn, perfSmokeIssues)
	dist := performanceDistributionOf(t, conn)
	reportPerformanceDistribution(t, dist)
	if dist.Issues != perfSmokeIssues {
		t.Fatalf("smoke dataset has %d issues, want %d", dist.Issues, perfSmokeIssues)
	}
	if dist.Single < dist.Issues/2 {
		t.Fatalf("smoke dataset is not long tailed: only %d of %d issues appear once", dist.Single, dist.Issues)
	}
	samples := performanceSamples(t, conn, perfSmokeSamples)
	runPerformanceClasses(t, conn, samples, perfSmokeWarmup, perfSmokeRounds)
}

// TestPerformanceGate is the recorded gate: one hundred thousand issues, the
// three retrieval classes, p95 < 150 ms. It is opt-in because it seeds the
// dataset on every run and must not slow CI down.
func TestPerformanceGate(t *testing.T) {
	if os.Getenv("ERRKB_PERF_BENCH") != "1" {
		t.Skipf("set ERRKB_PERF_BENCH=1 and %s to run the 100k-issue gate; the full run is opt-in on purpose", testDatabaseEnv)
	}
	conn := connect(t)
	schema := perfSchema(t, conn)
	t.Logf("PERF-SCHEMA %s", schema)
	logPerformanceEnvironment(t, conn)
	seedPerformanceDataset(t, conn, perfGateIssues)
	dist := performanceDistributionOf(t, conn)
	reportPerformanceDistribution(t, dist)
	if dist.Issues != perfGateIssues || dist.Events != dist.Occurrences {
		t.Fatalf("gate dataset has %d issues, %d events and %d occurrences", dist.Issues, dist.Events, dist.Occurrences)
	}
	if dist.Heavy == 0 {
		t.Fatalf("gate dataset has no heavy issues, it is not a long tail")
	}
	if dist.Small == 0 || dist.Single < dist.Issues/2 {
		t.Fatalf("gate dataset distribution is unrealistic: single=%d small=%d heavy=%d", dist.Single, dist.Small, dist.Heavy)
	}
	if len(dist.Categories) != 4 {
		t.Fatalf("gate dataset categories = %v, want all four", dist.Categories)
	}
	if dist.NonASCII == 0 {
		t.Fatalf("gate dataset has no cross-language messages")
	}
	samples := performanceSamples(t, conn, perfGateSamples)
	runPerformanceClasses(t, conn, samples, perfGateWarmup, perfGateRounds)
}

// TestPerformanceGeneratorIsDeterministic proves the generator can be rerun:
// two fresh schemas seeded with the same parameters have the same fingerprint
// and hit distribution and the same row counts.
func TestPerformanceGeneratorIsDeterministic(t *testing.T) {
	conn := connect(t)
	signature := func() string {
		perfSchema(t, conn)
		seedPerformanceDataset(t, conn, 500)
		var fingerprintDigest string
		if err := conn.QueryRow(context.Background(),
			`select md5(string_agg(fingerprint || ':' || hits::text, ',' order by fingerprint)) from issues`).
			Scan(&fingerprintDigest); err != nil {
			t.Fatalf("hash the issue distribution: %v", err)
		}
		var events, occurrences int
		if err := conn.QueryRow(context.Background(),
			`select (select count(*) from events), (select count(*) from occurrences)`).Scan(&events, &occurrences); err != nil {
			t.Fatalf("count the seeded rows: %v", err)
		}
		return fmt.Sprintf("%s/events=%d/occurrences=%d", fingerprintDigest, events, occurrences)
	}
	first := signature()
	second := signature()
	if first != second {
		t.Fatalf("the generator is not deterministic: first=%s second=%s", first, second)
	}
}

// TestCrashHelper is the child process of TestCrashSafety. It performs one write
// pattern, reports readiness to the parent, then blocks until the parent kills
// it. It is skipped unless the parent sets a scenario.
func TestCrashHelper(t *testing.T) {
	scenario := os.Getenv(crashScenarioEnv)
	if scenario == "" {
		t.Skip("the crash helper only runs as a child process of TestCrashSafety")
	}
	ctx := context.Background()
	conn := connect(t)
	schema := os.Getenv(crashSchemaEnv)
	if schema == "" {
		t.Fatalf("%s is not set", crashSchemaEnv)
	}
	if _, err := conn.Exec(ctx, "set search_path to "+schema+", public"); err != nil {
		t.Fatalf("set search_path to %s: %v", schema, err)
	}
	if _, err := conn.Exec(ctx, "set application_name = '"+crashHelperApp+"'"); err != nil {
		t.Fatalf("set application_name: %v", err)
	}

	if scenario == "migration" {
		dir := os.Getenv(crashMigrationsEnv)
		if dir == "" {
			t.Fatalf("%s is not set", crashMigrationsEnv)
		}
		fmt.Println("HELPER READY")
		if _, err := database.Migrate(ctx, conn, dir); err != nil {
			fmt.Printf("HELPER MIGRATION ERROR %v\n", err)
		}
		blockUntilKilled()
	}

	project := os.Getenv(crashProjectEnv)
	connectorID := insertConnectorNamed(t, conn, "crash-connector-"+scenario)
	sessionID := insertTestSession(t, conn, connectorID)

	switch scenario {
	case "single-uncommitted":
		if _, err := conn.Exec(ctx, "begin"); err != nil {
			t.Fatalf("begin: %v", err)
		}
		event := recordFixture(connectorID, sessionID, eventUUID(900001), "uncommitted crash scenario", project)
		if _, err := database.RecordEvent(ctx, conn, event); err != nil {
			t.Fatalf("record inside the uncommitted transaction: %v", err)
		}
		fmt.Println("HELPER READY")
		blockUntilKilled()
	case "single-committed":
		event := recordFixture(connectorID, sessionID, eventUUID(900002), "committed crash scenario", project)
		if _, err := database.RecordEvent(ctx, conn, event); err != nil {
			t.Fatalf("record the committed event: %v", err)
		}
		fmt.Println("HELPER READY")
		blockUntilKilled()
	case "batch":
		fmt.Println("HELPER READY")
		for i := 0; ; i++ {
			event := recordFixture(connectorID, sessionID, crashEventID(), fmt.Sprintf("batch crash scenario %d", i), project)
			if _, err := database.RecordEvent(ctx, conn, event); err != nil {
				fmt.Printf("HELPER BATCH ERROR %v\n", err)
				break
			}
			if i%10 == 0 {
				fmt.Printf("HELPER PROGRESS %d\n", i)
			}
		}
		blockUntilKilled()
	default:
		t.Fatalf("unknown crash scenario %q", scenario)
	}
}

func blockUntilKilled() {
	for {
		time.Sleep(time.Hour)
	}
}

func crashEventID() string {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		panic(err)
	}
	bytes[6] = bytes[6]&0x0f | 0x40
	bytes[8] = bytes[8]&0x3f | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", bytes[0:4], bytes[4:6], bytes[6:8], bytes[8:10], bytes[10:16])
}

type crashHelper struct {
	cmd   *exec.Cmd
	lines chan string
	done  chan struct{}
}

func startCrashHelper(t *testing.T, scenario, schema, project, migrations string) *crashHelper {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("locate the test executable: %v", err)
	}
	cmd := exec.Command(executable, "-test.run=^TestCrashHelper$", "-test.v")
	cmd.Env = append(os.Environ(),
		crashScenarioEnv+"="+scenario,
		crashSchemaEnv+"="+schema,
		crashProjectEnv+"="+project,
	)
	if migrations != "" {
		cmd.Env = append(cmd.Env, crashMigrationsEnv+"="+migrations)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatalf("open the crash helper stdout: %v", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatalf("open the crash helper stderr: %v", err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start the crash helper: %v", err)
	}
	helper := &crashHelper{cmd: cmd, lines: make(chan string, 256), done: make(chan struct{})}
	collect := func(reader io.Reader) {
		scanner := bufio.NewScanner(reader)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			select {
			case helper.lines <- scanner.Text():
			default:
			}
		}
	}
	go collect(stdout)
	go collect(stderr)
	go func() {
		_ = cmd.Wait()
		close(helper.done)
	}()
	t.Cleanup(helper.stop)
	return helper
}

func (helper *crashHelper) stop() {
	if helper == nil || helper.cmd.Process == nil {
		return
	}
	select {
	case <-helper.done:
		return
	default:
	}
	_ = helper.cmd.Process.Kill()
	select {
	case <-helper.done:
	case <-time.After(10 * time.Second):
	}
}

func (helper *crashHelper) waitReady(t *testing.T, timeout time.Duration) {
	t.Helper()
	deadline := time.After(timeout)
	seen := make([]string, 0, 16)
	for {
		select {
		case line := <-helper.lines:
			seen = append(seen, line)
			if strings.Contains(line, "HELPER READY") {
				return
			}
		case <-helper.done:
			t.Fatalf("the crash helper exited before it was ready; captured output %q", strings.Join(seen, "\n"))
		case <-deadline:
			t.Fatalf("the crash helper was not ready after %s; captured output %q", timeout, strings.Join(seen, "\n"))
		}
	}
}

func (helper *crashHelper) kill(t *testing.T) {
	t.Helper()
	select {
	case <-helper.done:
		t.Fatalf("the crash helper had already exited before the kill")
	default:
	}
	if err := helper.cmd.Process.Kill(); err != nil {
		t.Fatalf("kill the crash helper: %v", err)
	}
	select {
	case <-helper.done:
	case <-time.After(20 * time.Second):
		t.Fatalf("the crash helper did not exit after the kill")
	}
}

func crashHelperState(t *testing.T, conn *pgx.Conn) string {
	t.Helper()
	var state string
	if err := conn.QueryRow(context.Background(),
		`select coalesce(max(state), '') from pg_stat_activity where application_name = $1`,
		crashHelperApp).Scan(&state); err != nil {
		t.Fatalf("read the crash helper state: %v", err)
	}
	return state
}

func waitForCrashHelperGone(t *testing.T, conn *pgx.Conn) {
	t.Helper()
	deadline := time.Now().Add(45 * time.Second)
	for {
		var active int
		if err := conn.QueryRow(context.Background(),
			`select count(*) from pg_stat_activity where application_name = $1`, crashHelperApp).Scan(&active); err != nil {
			t.Fatalf("count the crash helper backends: %v", err)
		}
		if active == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the crash helper backend did not exit within 45s")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func crashCounts(t *testing.T, conn *pgx.Conn) (events, occurrences, issues int) {
	t.Helper()
	if err := conn.QueryRow(context.Background(),
		`select (select count(*) from events),
		        (select count(*) from occurrences),
		        (select count(*) from issues)`).Scan(&events, &occurrences, &issues); err != nil {
		t.Fatalf("read the crash row counts: %v", err)
	}
	return events, occurrences, issues
}

func assertNoOrphanOccurrences(t *testing.T, conn *pgx.Conn) {
	t.Helper()
	var orphans int
	if err := conn.QueryRow(context.Background(),
		`select count(*) from occurrences o
		  where not exists (select 1 from issues i where i.id = o.issue_id)
		     or not exists (select 1 from events e where e.event_id = o.event_id)`).Scan(&orphans); err != nil {
		t.Fatalf("count orphan occurrences: %v", err)
	}
	if orphans != 0 {
		t.Fatalf("%d occurrences lost their issue or event", orphans)
	}
}

func assertNoAdvisoryLocks(t *testing.T, conn *pgx.Conn) {
	t.Helper()
	var locks int
	if err := conn.QueryRow(context.Background(),
		`select count(*) from pg_locks l
		   join pg_database d on d.oid = l.database
		  where l.locktype = 'advisory'
		    and d.datname = current_database()
		    and l.pid <> pg_backend_pid()`).Scan(&locks); err != nil {
		t.Fatalf("count advisory locks: %v", err)
	}
	if locks != 0 {
		t.Fatalf("%d advisory locks survived the crash", locks)
	}
}

func crashMigrationDir(t *testing.T) (string, string) {
	t.Helper()
	dir := t.TempDir()
	entries, err := os.ReadDir(migrationsDir())
	if err != nil {
		t.Fatalf("read the migrations directory: %v", err)
	}
	copied := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(migrationsDir(), entry.Name()))
		if err != nil {
			t.Fatalf("read migration %s: %v", entry.Name(), err)
		}
		if err := os.WriteFile(filepath.Join(dir, entry.Name()), data, 0o644); err != nil {
			t.Fatalf("copy migration %s: %v", entry.Name(), err)
		}
		copied++
	}
	if copied == 0 {
		t.Fatalf("no migrations copied into %s", dir)
	}
	committed, err := database.LoadMigrations(migrationsDir())
	if err != nil {
		t.Fatalf("load the committed migrations: %v", err)
	}
	last, err := strconv.Atoi(committed[len(committed)-1].Version)
	if err != nil {
		t.Fatalf("parse the last migration version %q: %v", committed[len(committed)-1].Version, err)
	}
	probeVersion := fmt.Sprintf("%04d", last+1)
	probe := `-- Crash probe: the sleep keeps this migration transaction open so
-- TestCrashSafety can kill the process inside it. The table and the ledger row
-- must both roll back. Windows disables client_connection_check_interval, so
-- the backend notices the dead client only when the statement returns; the
-- sleep stays short enough for the test to wait it out.
create table crash_probe (id integer);
select pg_sleep(8);
`
	if err := os.WriteFile(filepath.Join(dir, probeVersion+"_crash_probe.sql"), []byte(probe), 0o644); err != nil {
		t.Fatalf("write the crash probe migration: %v", err)
	}
	return dir, probeVersion
}

func rewriteCrashMigration(t *testing.T, dir, probeVersion string) {
	t.Helper()
	fixed := "create table crash_probe (id integer);\n"
	if err := os.WriteFile(filepath.Join(dir, probeVersion+"_crash_probe.sql"), []byte(fixed), 0o644); err != nil {
		t.Fatalf("rewrite the crash probe migration: %v", err)
	}
}

// TestCrashSafety kills a real writer process in four places and verifies that
// PostgreSQL's durability guarantees hold: the uncommitted write leaves nothing,
// the committed write survives, the interrupted batch keeps every committed
// occurrence, and the interrupted migration leaves neither the table nor a
// ledger row behind.
func TestCrashSafety(t *testing.T) {
	conn := connect(t)
	ctx := context.Background()
	schema := perfSchema(t, conn)
	project := "crash-scenario"

	beforeEvents, beforeOccurrences, beforeIssues := crashCounts(t, conn)

	// 1) A writer killed inside an open transaction must leave no rows.
	helper := startCrashHelper(t, "single-uncommitted", schema, project, "")
	helper.waitReady(t, 30*time.Second)
	if state := crashHelperState(t, conn); state != "idle in transaction" {
		t.Fatalf("the uncommitted crash helper is %q, want an open transaction", state)
	}
	helper.kill(t)
	waitForCrashHelperGone(t, conn)
	events, occurrences, issues := crashCounts(t, conn)
	if events != beforeEvents || occurrences != beforeOccurrences || issues != beforeIssues {
		t.Fatalf("the uncommitted transaction leaked rows: before events=%d occurrences=%d issues=%d, after events=%d occurrences=%d issues=%d",
			beforeEvents, beforeOccurrences, beforeIssues, events, occurrences, issues)
	}
	assertNoAdvisoryLocks(t, conn)

	// 2) A writer killed after commit: the committed event and its occurrence
	// must both be there.
	helper = startCrashHelper(t, "single-committed", schema, project, "")
	helper.waitReady(t, 30*time.Second)
	helper.kill(t)
	waitForCrashHelperGone(t, conn)
	events, occurrences, issues = crashCounts(t, conn)
	if events != beforeEvents+1 || occurrences != beforeOccurrences+1 || issues != beforeIssues+1 {
		t.Fatalf("the committed crash write is incomplete: before events=%d occurrences=%d issues=%d, after events=%d occurrences=%d issues=%d",
			beforeEvents, beforeOccurrences, beforeIssues, events, occurrences, issues)
	}
	assertNoOrphanOccurrences(t, conn)
	assertNoAdvisoryLocks(t, conn)

	// 3) Kill a writer in the middle of a batch: every committed event keeps its
	// occurrence, and no occurrence loses its issue or event.
	committedEvents := events
	committedOccurrences := occurrences
	helper = startCrashHelper(t, "batch", schema, project, "")
	helper.waitReady(t, 30*time.Second)
	time.Sleep(400 * time.Millisecond)
	helper.kill(t)
	waitForCrashHelperGone(t, conn)
	events, occurrences, _ = crashCounts(t, conn)
	if events <= committedEvents {
		t.Fatalf("the batch writer committed nothing before the kill (events=%d)", events)
	}
	if events != occurrences {
		t.Fatalf("committed events and occurrences diverge after the batch crash: events=%d occurrences=%d", events, occurrences)
	}
	if occurrences <= committedOccurrences {
		t.Fatalf("the batch writer committed no occurrence before the kill")
	}
	assertNoOrphanOccurrences(t, conn)
	assertNoAdvisoryLocks(t, conn)

	// 4) Kill a migration inside its transaction: the table and the ledger row
	// must both roll back, and the advisory lock must be released.
	committed, err := database.LoadMigrations(migrationsDir())
	if err != nil {
		t.Fatalf("load the committed migrations: %v", err)
	}
	wantLedger := len(committed)
	wantMax := committed[wantLedger-1].Version
	migrations, probeVersion := crashMigrationDir(t)
	helper = startCrashHelper(t, "migration", schema, project, migrations)
	helper.waitReady(t, 30*time.Second)
	time.Sleep(2 * time.Second)
	var sleeping int
	if err := conn.QueryRow(ctx,
		`select count(*) from pg_stat_activity where application_name = $1 and query like '%pg_sleep%'`,
		crashHelperApp).Scan(&sleeping); err != nil {
		t.Fatalf("find the migration sleep: %v", err)
	}
	if sleeping != 1 {
		t.Fatalf("the crash helper is not inside the migration transaction; the mid-migration kill would prove nothing")
	}
	helper.kill(t)
	waitForCrashHelperGone(t, conn)
	var ledger int
	var maxVersion *string
	if err := conn.QueryRow(ctx, `select count(*), max(version) from `+database.LedgerTable).Scan(&ledger, &maxVersion); err != nil {
		t.Fatalf("read the migration ledger: %v", err)
	}
	if ledger != wantLedger || maxVersion == nil || *maxVersion != wantMax {
		t.Fatalf("the killed migration left the ledger at %d rows (max %v), want the %d committed migrations (max %s)", ledger, maxVersion, wantLedger, wantMax)
	}
	var probe *string
	if err := conn.QueryRow(ctx, `select to_regclass('crash_probe')::text`).Scan(&probe); err != nil {
		t.Fatalf("look for the crash probe table: %v", err)
	}
	if probe != nil {
		t.Fatalf("the killed migration left table %q behind", *probe)
	}
	assertNoAdvisoryLocks(t, conn)

	// 5) Restart: the interrupted migration applies exactly once.
	rewriteCrashMigration(t, migrations, probeVersion)
	applied := migrate(t, conn, migrations)
	if len(applied) != 1 || applied[0] != probeVersion {
		t.Fatalf("the restart applied %v, want only %s", applied, probeVersion)
	}
	if err := conn.QueryRow(ctx, `select count(*) from `+database.LedgerTable).Scan(&ledger); err != nil {
		t.Fatalf("read the migration ledger after the restart: %v", err)
	}
	if ledger != wantLedger+1 {
		t.Fatalf("the ledger has %d rows after the restart, want %d", ledger, wantLedger+1)
	}
	if err := conn.QueryRow(ctx, `select to_regclass('crash_probe')::text`).Scan(&probe); err != nil {
		t.Fatalf("look for the crash probe table after the restart: %v", err)
	}
	if probe == nil || *probe != "crash_probe" {
		t.Fatalf("the restarted migration did not create crash_probe (%v)", probe)
	}
	assertNoAdvisoryLocks(t, conn)
}
