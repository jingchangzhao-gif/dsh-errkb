package database_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/core"
	"github.com/jingchangzhao-gif/error-knowledge-base/internal/database"
)

// candidateSeed is one synthetic event with an optional command. Each seed
// creates an issue, an event and an occurrence, so a fuzzy candidate has a real
// issue to point at.
type candidateSeed struct {
	message string
	command string
	project string
}

func insertCandidateSeed(t *testing.T, conn *pgx.Conn, connectorID, sessionID string, n int, seed candidateSeed) string {
	t.Helper()
	ctx := context.Background()
	project := seed.project
	if project == "" {
		project = "proj"
	}
	fingerprint := core.Signature("tool", core.Normalize(seed.message))
	var issueID string
	if err := conn.QueryRow(ctx, `
		insert into issues (fingerprint, project_key, title, category, status, trigger, hits, source, actor)
		values ($1, $2, $3, 'tool', 'open', '', 1, 'test', 'test')
		returning id`,
		fingerprint, project, seed.message).Scan(&issueID); err != nil {
		t.Fatalf("insert issue: %v", err)
	}
	var command *string
	if seed.command != "" {
		command = &seed.command
	}
	eventID := eventUUID(n)
	if _, err := conn.Exec(ctx, `
		insert into events (event_id, connector_id, session_id, observed_at, channel,
			category, severity, transient, headline, normalized_message, redacted_sample,
			command, os, project_key, fingerprint, evidence, confidence, source, actor)
		values ($1, $2, $3, now(), 'A', 'tool', 'error', false, $4, $5, $5, $6, 'windows', $7, $8, 'probe', 0.9, 'test', 'test')`,
		eventID, connectorID, sessionID, seed.message, core.Normalize(seed.message),
		command, project, fingerprint); err != nil {
		t.Fatalf("insert event: %v", err)
	}
	if _, err := conn.Exec(ctx, `
		insert into occurrences (issue_id, event_id, connector_id, project_key, environment, occurred_at, source, actor)
		values ($1, $2, $3, $4, '{}'::jsonb, now(), 'test', 'test')`,
		issueID, eventID, connectorID, project); err != nil {
		t.Fatalf("insert occurrence: %v", err)
	}
	return issueID
}

// seedCandidateBulk inserts many unrelated events with deterministic
// pseudo-random messages. The rows exist so the planner sees a realistic
// distribution when the GIN plan is asserted.
func seedCandidateBulk(t *testing.T, conn *pgx.Conn, connectorID, sessionID string, rows int) (issueCount int) {
	t.Helper()
	ctx := context.Background()
	if _, err := conn.Exec(ctx, `
		with seeded as (
			select g, (
				select string_agg(substr(md5(((g * 31 + w)::text)), 1, 6), ' ')
				from generate_series(1, 5) w
			) as message
			from generate_series(1, $1) g
		)
		insert into issues (fingerprint, project_key, title, category, status, trigger, hits, source, actor)
		select lpad(to_hex(g), 12, '0'), 'proj', 'issue ' || g, 'tool', 'open', '', 1, 'test', 'test'
		from seeded`, rows); err != nil {
		t.Fatalf("seed issues: %v", err)
	}
	if _, err := conn.Exec(ctx, `
		with seeded as (
			select g, (
				select string_agg(substr(md5(((g * 31 + w)::text)), 1, 6), ' ')
				from generate_series(1, 5) w
			) as message
			from generate_series(1, $1) g
		)
		insert into events (event_id, connector_id, session_id, observed_at, channel,
			category, severity, transient, headline, normalized_message, redacted_sample,
			project_key, os, fingerprint, evidence, confidence, source, actor)
		select ('00000000-0000-4000-8000-' || lpad(to_hex(g), 12, '0'))::uuid,
			$2, $3, now(), 'A', 'tool', 'error', false, 'bulk', message, message,
			'proj', 'windows', lpad(to_hex(g), 12, '0'), 'probe', 0.9, 'test', 'test'
		from seeded`, rows, connectorID, sessionID); err != nil {
		t.Fatalf("seed events: %v", err)
	}
	if _, err := conn.Exec(ctx, `
		insert into occurrences (issue_id, event_id, connector_id, project_key, environment, occurred_at, source, actor)
		select i.id, e.event_id, $1, 'proj', '{}'::jsonb, now(), 'test', 'test'
		from events e
		join issues i on i.fingerprint = e.fingerprint
		where e.headline = 'bulk'`, connectorID); err != nil {
		t.Fatalf("seed occurrences: %v", err)
	}
	return rows
}

func candidateIssues(set database.CandidateSet) []string {
	ids := make([]string, 0, len(set.Candidates))
	for _, candidate := range set.Candidates {
		ids = append(ids, candidate.IssueID)
	}
	return ids
}

func containsAll(haystack []string, needles ...string) bool {
	seen := map[string]bool{}
	for _, value := range haystack {
		seen[value] = true
	}
	for _, needle := range needles {
		if !seen[needle] {
			return false
		}
	}
	return true
}

func TestFuzzyCandidatesFindRelatedIssues(t *testing.T) {
	conn, _, connectorID, sessionID := setupRecordSchema(t)
	ctx := context.Background()

	first := insertCandidateSeed(t, conn, connectorID, sessionID, 1, candidateSeed{
		message: "EPERM operation not permitted, rename the package directory",
		command: "npm install",
	})
	second := insertCandidateSeed(t, conn, connectorID, sessionID, 2, candidateSeed{
		message: "EPERM operation not permitted, rename the package directory (retry)",
		command: "npm install",
	})
	unrelated := insertCandidateSeed(t, conn, connectorID, sessionID, 3, candidateSeed{
		message: "error TS2345: argument of type string is not assignable",
	})

	query := database.DefaultCandidateQuery("eprem operation not permitted rename the package directory")
	query.Limit = 10
	set, err := database.FuzzyCandidates(ctx, conn, query)
	if err != nil {
		t.Fatalf("fuzzy candidates: %v", err)
	}
	if set.Degraded {
		t.Fatalf("expected the trigram path, got the degraded fallback")
	}
	if len(set.Candidates) < 2 {
		t.Fatalf("expected the two related issues, got %v", candidateIssues(set))
	}
	if !containsAll(candidateIssues(set), first, second) {
		t.Fatalf("missing a related issue: %v", candidateIssues(set))
	}
	for _, id := range candidateIssues(set) {
		if id == unrelated {
			t.Fatalf("the unrelated issue became a candidate")
		}
	}
	for _, candidate := range set.Candidates {
		if !candidate.Approximate {
			t.Fatalf("candidate %s must be approximate", candidate.IssueID)
		}
		if candidate.Similarity == nil || candidate.Score == nil {
			t.Fatalf("candidate %s must carry similarity and score", candidate.IssueID)
		}
		if !containsAll(candidate.MatchFields, "normalized_message") {
			t.Fatalf("candidate %s must report the matched field, got %v", candidate.IssueID, candidate.MatchFields)
		}
	}
}

func TestFuzzyCandidatesReportNoMergeVerdict(t *testing.T) {
	candidateType := reflect.TypeOf(database.Candidate{})
	forbidden := []string{"merge", "same", "duplicate", "is_match"}
	for index := 0; index < candidateType.NumField(); index++ {
		name := strings.ToLower(candidateType.Field(index).Name)
		for _, word := range forbidden {
			if strings.Contains(name, word) {
				t.Fatalf("Candidate.%s looks like a merge verdict", candidateType.Field(index).Name)
			}
		}
	}
	setType := reflect.TypeOf(database.CandidateSet{})
	if setType.NumField() != 2 {
		t.Fatalf("CandidateSet must be candidates plus the degraded flag, got %d fields", setType.NumField())
	}
}

func TestFuzzyCandidatesWeightsAreExternal(t *testing.T) {
	conn, _, connectorID, sessionID := setupRecordSchema(t)
	ctx := context.Background()

	commandOnly := insertCandidateSeed(t, conn, connectorID, sessionID, 1, candidateSeed{
		message: "the build failed",
		command: "pnpm build --filter app",
	})
	messageMatch := insertCandidateSeed(t, conn, connectorID, sessionID, 2, candidateSeed{
		message: "pnpm build --filter other failed with exit code 2",
	})

	query := database.DefaultCandidateQuery("pnpm build --filter app")
	query.Limit = 10
	withDefaults, err := database.FuzzyCandidates(ctx, conn, query)
	if err != nil {
		t.Fatalf("fuzzy candidates: %v", err)
	}
	if !containsAll(candidateIssues(withDefaults), commandOnly) {
		t.Fatalf("the command match must be a candidate, got %v", candidateIssues(withDefaults))
	}
	defaultScore := candidateScore(t, withDefaults.Candidates, commandOnly)

	messageOnly := database.DefaultCandidateQuery("pnpm build --filter app")
	messageOnly.Limit = 10
	messageOnly.CommandWeight = 0
	messageOnly.ToolNameWeight = 0
	messageOnly.MessageWeight = 1
	withoutCommand, err := database.FuzzyCandidates(ctx, conn, messageOnly)
	if err != nil {
		t.Fatalf("fuzzy candidates with command weight 0: %v", err)
	}
	for _, id := range candidateIssues(withoutCommand) {
		if id == commandOnly {
			t.Fatalf("a zero command weight still produced the command-only candidate")
		}
	}
	for _, candidate := range withoutCommand.Candidates {
		if candidate.IssueID == messageMatch && candidate.Score == nil {
			t.Fatalf("the message match must keep its score")
		}
	}
	if defaultScore <= 0 {
		t.Fatalf("expected a positive weighted score, got %v", defaultScore)
	}
}

func candidateScore(t *testing.T, candidates []database.Candidate, issueID string) float64 {
	t.Helper()
	for _, candidate := range candidates {
		if candidate.IssueID == issueID && candidate.Score != nil {
			return *candidate.Score
		}
	}
	t.Fatalf("issue %s is not a scored candidate", issueID)
	return 0
}

func TestFuzzyCandidatesRespectTheLimit(t *testing.T) {
	conn, _, connectorID, sessionID := setupRecordSchema(t)
	ctx := context.Background()

	for index := 1; index <= 3; index++ {
		insertCandidateSeed(t, conn, connectorID, sessionID, index, candidateSeed{
			message: fmt.Sprintf("eperm rename the package directory variant %d", index),
		})
	}
	query := database.DefaultCandidateQuery("eperm rename the package directory")
	query.Limit = 2
	set, err := database.FuzzyCandidates(ctx, conn, query)
	if err != nil {
		t.Fatalf("fuzzy candidates: %v", err)
	}
	if len(set.Candidates) != 2 {
		t.Fatalf("limit 2 returned %d candidates", len(set.Candidates))
	}
}

// TestFuzzyCandidatesCapTheQueryText locks in the EKB-027 follow-up: only the
// first TrigramQueryMaxChars runes reach the pg_trgm stage, so two queries that
// differ after the cap produce the same candidates. The reranker still reads the
// full message (EKB-024); only the candidate lookup is capped.
func TestFuzzyCandidatesCapTheQueryText(t *testing.T) {
	conn, _, connectorID, sessionID := setupRecordSchema(t)
	ctx := context.Background()

	message := strings.Repeat("network timeout while resolving the dependency ", 3)
	insertCandidateSeed(t, conn, connectorID, sessionID, 1, candidateSeed{message: message})

	prefix := string([]rune(message)[:database.TrigramQueryMaxChars])
	first, err := database.FuzzyCandidates(ctx, conn, database.DefaultCandidateQuery(prefix+" alpha suffix"))
	if err != nil {
		t.Fatalf("fuzzy candidates (alpha): %v", err)
	}
	second, err := database.FuzzyCandidates(ctx, conn, database.DefaultCandidateQuery(prefix+" beta suffix"))
	if err != nil {
		t.Fatalf("fuzzy candidates (beta): %v", err)
	}
	if len(first.Candidates) == 0 {
		t.Fatal("the capped query found no candidates")
	}
	if len(first.Candidates) != len(second.Candidates) {
		t.Fatalf("candidates differ after the cap: %d vs %d", len(first.Candidates), len(second.Candidates))
	}
	for index := range first.Candidates {
		if first.Candidates[index].IssueID != second.Candidates[index].IssueID {
			t.Fatalf("candidate %d differs after the cap: %s vs %s", index, first.Candidates[index].IssueID, second.Candidates[index].IssueID)
		}
	}
}

func TestFuzzyCandidatesRejectMalformedQueries(t *testing.T) {
	conn := connect(t)
	ctx := context.Background()

	base := database.DefaultCandidateQuery("eperm rename")
	cases := []struct {
		name    string
		mutate  func(*database.CandidateQuery)
		message string
	}{
		{"empty query", func(query *database.CandidateQuery) { query.Query = "  " }, "a non-empty query is required"},
		{"zero threshold", func(query *database.CandidateQuery) { query.Threshold = 0 }, "threshold must be greater than 0"},
		{"threshold above one", func(query *database.CandidateQuery) { query.Threshold = 1.5 }, "threshold must be greater than 0"},
		{"zero limit", func(query *database.CandidateQuery) { query.Limit = 0 }, "limit must be between 1 and 100"},
		{"limit above max", func(query *database.CandidateQuery) { query.Limit = 101 }, "limit must be between 1 and 100"},
		{"negative weight", func(query *database.CandidateQuery) { query.CommandWeight = -1 }, "weights must be non-negative"},
		{"all weights zero", func(query *database.CandidateQuery) {
			query.MessageWeight = 0
			query.CommandWeight = 0
			query.ToolNameWeight = 0
		}, "at least one weight must be positive"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			query := base
			testCase.mutate(&query)
			if _, err := database.FuzzyCandidates(ctx, conn, query); err == nil ||
				!strings.Contains(err.Error(), testCase.message) {
				t.Fatalf("expected %q, got %v", testCase.message, err)
			}
		})
	}
}

func TestFuzzyCandidatesDegradeWithoutTrigram(t *testing.T) {
	conn, _, connectorID, sessionID := setupRecordSchema(t)
	ctx := context.Background()

	insertCandidateSeed(t, conn, connectorID, sessionID, 1, candidateSeed{
		message: "EPERM rename the package directory",
	})

	if _, err := conn.Exec(ctx, "begin"); err != nil {
		t.Fatalf("begin: %v", err)
	}
	// The transaction must always close, even when an assertion below fails:
	// a leaked transaction would hide the dropped extension from later tests.
	defer func() { _, _ = conn.Exec(ctx, "rollback") }()
	if _, err := conn.Exec(ctx, "drop extension pg_trgm cascade"); err != nil {
		t.Fatalf("drop pg_trgm: %v", err)
	}
	// The exact fallback compares raw values, so the query must already be in
	// stored (normalized) form, exactly as the exact-search layer expects.
	query := database.DefaultCandidateQuery(core.Normalize("EPERM rename the package directory"))
	set, err := database.FuzzyCandidates(ctx, conn, query)
	if err != nil {
		_, _ = conn.Exec(ctx, "rollback")
		t.Fatalf("degraded fuzzy candidates: %v", err)
	}
	if !set.Degraded {
		t.Fatalf("expected the degraded flag without pg_trgm")
	}
	if len(set.Candidates) == 0 {
		t.Fatalf("the bounded exact fallback must still return the exact match")
	}
	for _, candidate := range set.Candidates {
		if !candidate.Degraded {
			t.Fatalf("candidate %s must be marked degraded", candidate.IssueID)
		}
		if candidate.Similarity != nil || candidate.Score != nil {
			t.Fatalf("degraded candidate %s must not carry similarity or score", candidate.IssueID)
		}
	}
	if _, err := conn.Exec(ctx, "rollback"); err != nil {
		t.Fatalf("rollback: %v", err)
	}
	available, err := database.TrigramAvailable(ctx, conn)
	if err != nil || !available {
		t.Fatalf("pg_trgm must be available again after the rollback: %v", err)
	}
}

func TestFuzzyCandidatesUseTheTrigramIndexes(t *testing.T) {
	conn, _, connectorID, sessionID := setupRecordSchema(t)
	ctx := context.Background()

	bulk := seedCandidateBulk(t, conn, connectorID, sessionID, 6000)
	insertCandidateSeed(t, conn, connectorID, sessionID, 999999, candidateSeed{
		message: "eperm operation not permitted rename the package directory",
	})
	if bulk != 6000 {
		t.Fatalf("seeded %d bulk rows", bulk)
	}
	if _, err := conn.Exec(ctx, "analyze"); err != nil {
		t.Fatalf("analyze: %v", err)
	}

	// The function pins enable_seqscan=off for its own scope; reproduce that
	// setting here so the plan reflects the function's planner context.
	if _, err := conn.Exec(ctx, "set enable_seqscan = off"); err != nil {
		t.Fatalf("set enable_seqscan: %v", err)
	}
	defer func() { _, _ = conn.Exec(ctx, "reset enable_seqscan") }()

	plan := explainJSON(t, conn, "eperm operation not permitted rename the package directory")
	if !strings.Contains(plan, "Bitmap Index Scan") || !strings.Contains(plan, "_trgm_idx") {
		t.Fatalf("expected a trigram bitmap index plan, got:\n%s", plan)
	}
}

// explainJSON returns the JSON plan for the candidate predicate, with the
// query inlined because EXPLAIN cannot take bind parameters.
func explainJSON(t *testing.T, conn *pgx.Conn, query string) string {
	t.Helper()
	ctx := context.Background()
	literal := strings.ReplaceAll(query, "'", "''")
	sql := fmt.Sprintf(`
		explain (format json)
		select event_id from events
		where normalized_message %% '%s'
		   or command %% '%s'
		   or tool_name %% '%s'`, literal, literal, literal)
	var raw []byte
	if err := conn.QueryRow(ctx, sql).Scan(&raw); err != nil {
		t.Fatalf("explain: %v", err)
	}
	var document any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("decode plan: %v", err)
	}
	return string(raw)
}

// BenchmarkFuzzyCandidatesSelective measures a query that matches one seeded
// issue out of 6000 unrelated events.
func BenchmarkFuzzyCandidatesSelective(b *testing.B) {
	conn := benchmarkDatabase(b)
	connectorID := benchmarkConnector(b, conn, "bench-connector")
	sessionID := benchmarkSession(b, conn, connectorID)
	seedBenchmarkBulk(b, conn, connectorID, sessionID, 6000)
	defer conn.Close(context.Background())
	if _, err := conn.Exec(context.Background(), "analyze"); err != nil {
		b.Fatalf("analyze: %v", err)
	}

	query := database.DefaultCandidateQuery("eperm operation not permitted rename the package directory")
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		if _, err := database.FuzzyCandidates(context.Background(), conn, query); err != nil {
			b.Fatalf("fuzzy candidates: %v", err)
		}
	}
}

// BenchmarkFuzzyCandidatesBroad measures the worst case, where thousands of
// candidate rows share the query vocabulary.
func BenchmarkFuzzyCandidatesBroad(b *testing.B) {
	conn := benchmarkDatabase(b)
	connectorID := benchmarkConnector(b, conn, "bench-connector")
	sessionID := benchmarkSession(b, conn, connectorID)
	if _, err := conn.Exec(context.Background(), `
		insert into issues (fingerprint, project_key, title, category, status, trigger, hits, source, actor)
		select lpad(to_hex(g), 12, '0'), 'proj', 'broad', 'tool', 'open', '', 1, 'test', 'test'
		from generate_series(1, 3000) g`); err != nil {
		b.Fatalf("seed broad issues: %v", err)
	}
	if _, err := conn.Exec(context.Background(), `
		insert into events (event_id, connector_id, session_id, observed_at, channel,
			category, severity, transient, headline, normalized_message, redacted_sample,
			command, os, project_key, fingerprint, evidence, confidence, source, actor)
		select ('00000000-0000-4000-8000-' || lpad(to_hex(g), 12, '0'))::uuid,
			$1, $2, now(), 'A', 'tool', 'error', false, 'broad',
			'pnpm build --filter pkg' || g || ' failed', 'pnpm build failed',
			'pnpm build --filter pkg' || g, 'windows', 'proj', lpad(to_hex(g), 12, '0'),
			'probe', 0.9, 'test', 'test'
		from generate_series(1, 3000) g`, connectorID, sessionID); err != nil {
		b.Fatalf("seed broad events: %v", err)
	}
	if _, err := conn.Exec(context.Background(), `
		insert into occurrences (issue_id, event_id, connector_id, project_key, environment, occurred_at, source, actor)
		select i.id, e.event_id, $1, 'proj', '{}'::jsonb, now(), 'test', 'test'
		from events e join issues i on i.fingerprint = e.fingerprint`, connectorID); err != nil {
		b.Fatalf("seed broad occurrences: %v", err)
	}
	defer conn.Close(context.Background())
	if _, err := conn.Exec(context.Background(), "analyze"); err != nil {
		b.Fatalf("analyze: %v", err)
	}

	query := database.DefaultCandidateQuery("pnpm build --filter pkg13")
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		if _, err := database.FuzzyCandidates(context.Background(), conn, query); err != nil {
			b.Fatalf("fuzzy candidates: %v", err)
		}
	}
}

func benchmarkDatabase(b *testing.B) *pgx.Conn {
	b.Helper()
	url := os.Getenv("ERRKB_TEST_DATABASE_URL")
	if url == "" {
		b.Skip("ERRKB_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	if err != nil {
		b.Fatalf("connect: %v", err)
	}
	schema := fmt.Sprintf("errkb_bench_%d", time.Now().UnixNano())
	if _, err := conn.Exec(ctx, "create schema "+schema); err != nil {
		b.Fatalf("create schema: %v", err)
	}
	if _, err := conn.Exec(ctx, "set search_path to "+schema+", public"); err != nil {
		b.Fatalf("set search_path: %v", err)
	}
	if _, err := database.Migrate(ctx, conn, migrationsDir()); err != nil {
		b.Fatalf("migrate: %v", err)
	}
	b.Cleanup(func() {
		_, _ = conn.Exec(context.Background(), "set search_path to public")
		_, _ = conn.Exec(context.Background(), "drop schema if exists "+schema+" cascade")
	})
	return conn
}

func benchmarkConnector(b *testing.B, conn *pgx.Conn, name string) string {
	b.Helper()
	var id string
	if err := conn.QueryRow(context.Background(), `
		insert into connectors (name, channel, source, actor)
		values ($1, 'A', 'test', 'test') returning id`, name).Scan(&id); err != nil {
		b.Fatalf("insert connector: %v", err)
	}
	return id
}

func benchmarkSession(b *testing.B, conn *pgx.Conn, connectorID string) string {
	b.Helper()
	var id string
	if err := conn.QueryRow(context.Background(), `
		insert into sessions (connector_id, external_id, started_at, source, actor)
		values ($1, 'bench-session', now(), 'test', 'test') returning id`, connectorID).Scan(&id); err != nil {
		b.Fatalf("insert session: %v", err)
	}
	return id
}

func seedBenchmarkBulk(b *testing.B, conn *pgx.Conn, connectorID, sessionID string, rows int) {
	b.Helper()
	ctx := context.Background()
	if _, err := conn.Exec(ctx, `
		with seeded as (
			select g, (
				select string_agg(substr(md5(((g * 31 + w)::text)), 1, 6), ' ')
				from generate_series(1, 5) w
			) as message
			from generate_series(1, $1) g
		)
		insert into issues (fingerprint, project_key, title, category, status, trigger, hits, source, actor)
		select lpad(to_hex(g), 12, '0'), 'proj', 'issue ' || g, 'tool', 'open', '', 1, 'test', 'test'
		from seeded`, rows); err != nil {
		b.Fatalf("seed issues: %v", err)
	}
	if _, err := conn.Exec(ctx, `
		with seeded as (
			select g, (
				select string_agg(substr(md5(((g * 31 + w)::text)), 1, 6), ' ')
				from generate_series(1, 5) w
			) as message
			from generate_series(1, $1) g
		)
		insert into events (event_id, connector_id, session_id, observed_at, channel,
			category, severity, transient, headline, normalized_message, redacted_sample,
			project_key, os, fingerprint, evidence, confidence, source, actor)
		select ('00000000-0000-4000-8000-' || lpad(to_hex(g), 12, '0'))::uuid,
			$2, $3, now(), 'A', 'tool', 'error', false, 'bulk', message, message,
			'proj', 'windows', lpad(to_hex(g), 12, '0'), 'probe', 0.9, 'test', 'test'
		from seeded`, rows, connectorID, sessionID); err != nil {
		b.Fatalf("seed events: %v", err)
	}
	if _, err := conn.Exec(ctx, `
		insert into occurrences (issue_id, event_id, connector_id, project_key, environment, occurred_at, source, actor)
		select i.id, e.event_id, $1, 'proj', '{}'::jsonb, now(), 'test', 'test'
		from events e join issues i on i.fingerprint = e.fingerprint`, connectorID); err != nil {
		b.Fatalf("seed occurrences: %v", err)
	}
	if _, err := conn.Exec(ctx, "analyze"); err != nil {
		b.Fatalf("analyze: %v", err)
	}
}
