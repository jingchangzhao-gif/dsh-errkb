package audit_test

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jingchangzhao-gif/dsh-errkb/internal/audit"
	"github.com/jingchangzhao-gif/dsh-errkb/internal/contract"
	"github.com/jingchangzhao-gif/dsh-errkb/internal/core"
	"github.com/jingchangzhao-gif/dsh-errkb/internal/database"
)

func testDatabaseURL(t *testing.T) string {
	t.Helper()
	url := strings.TrimSpace(os.Getenv("ERRKB_TEST_DATABASE_URL"))
	if url == "" {
		t.Skip("set ERRKB_TEST_DATABASE_URL to run the audit tests")
	}
	return url
}

func auditTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	url := testDatabaseURL(t)
	schema := fmt.Sprintf("errkb_audit_%d", time.Now().UnixNano())

	admin, err := pgx.Connect(context.Background(), url)
	if err != nil {
		t.Fatalf("connect for setup: %v", err)
	}
	if _, err := admin.Exec(context.Background(), "create schema "+schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	if err := admin.Close(context.Background()); err != nil {
		t.Fatalf("close setup connection: %v", err)
	}

	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		t.Fatalf("parse config: %v", err)
	}
	config.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "set search_path to "+schema+", public")
		return err
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("create pool: %v", err)
	}
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), "drop schema "+schema+" cascade"); err != nil {
			t.Logf("drop schema: %v", err)
		}
		pool.Close()
	})

	pooled, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire for migrate: %v", err)
	}
	defer pooled.Release()
	if app, err := database.Migrate(context.Background(), pooled.Conn(), migrationsDir(t)); err != nil {
		t.Fatalf("migrate: %v (%v)", err, app)
	}
	return pool
}

func migrationsDir(t *testing.T) string {
	t.Helper()
	return "../../db/migrations"
}

func auditConn(t *testing.T, pool *pgxpool.Pool) (*pgx.Conn, func()) {
	t.Helper()
	pooled, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire: %v", err)
	}
	return pooled.Conn(), pooled.Release
}

func newEventID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		panic(fmt.Sprintf("random event id: %v", err))
	}
	buf[6] = (buf[6] & 0x0f) | 0x40
	buf[8] = (buf[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(buf)
	return encoded[0:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:32]
}

func testEvent(connectorID, sessionID string, sequence int, message string) contract.ErrorEvent {
	normalized := core.Normalize(message)
	headline := core.ExtractHeadline(message)
	confidence := 0.5
	transient := false
	return contract.ErrorEvent{
		SchemaVersion:     contract.SchemaVersion,
		EventID:           newEventID(),
		ConnectorID:       connectorID,
		SessionID:         sessionID,
		ObservedAt:        time.Now().UTC().Format(time.RFC3339),
		Channel:           "B",
		Category:          "tool",
		Severity:          "error",
		Transient:         &transient,
		Headline:          headline.Line,
		NormalizedMessage: normalized,
		RedactedSample:    message,
		OS:                "windows",
		ProjectKey:        "audit-test",
		Fingerprint:       core.Signature("tool", normalized),
		Evidence:          fmt.Sprintf("audit test event %d", sequence),
		Confidence:        &confidence,
	}
}

func TestRecordAndQueryTheTrail(t *testing.T) {
	pool := auditTestPool(t)
	ctx := context.Background()
	conn, release := auditConn(t, pool)
	defer release()

	for _, entry := range []audit.Entry{
		{Action: "solution.version.added", Entity: "solution", EntityID: "s-1", Operator: "owner", Source: "ui",
			Detail: map[string]any{"version": 2}},
		{Action: "fault.daemon", Entity: "daemon", EntityID: "d-1", Operator: "system", Source: "daemon"},
	} {
		if err := audit.Record(ctx, conn, entry); err != nil {
			t.Fatalf("record %q: %v", entry.Action, err)
		}
	}

	page, err := audit.Query(ctx, conn, audit.Filter{Action: "solution.version.added"})
	if err != nil {
		t.Fatalf("query by action: %v", err)
	}
	if page.Total != 1 || len(page.Records) != 1 {
		t.Fatalf("query by action = %d rows (total %d), want 1", len(page.Records), page.Total)
	}
	if page.Records[0].EntityID != "s-1" || string(page.Records[0].Detail) != `{"version": 2}` {
		t.Fatalf("row = %+v detail %s", page.Records[0], page.Records[0].Detail)
	}

	page, err = audit.Query(ctx, conn, audit.Filter{Action: "no.such.action"})
	if err != nil {
		t.Fatalf("query unknown action: %v", err)
	}
	if page.Total != 0 || len(page.Records) != 0 {
		t.Fatalf("unknown action returned %d rows", len(page.Records))
	}

	actions, err := audit.Actions(ctx, conn)
	if err != nil {
		t.Fatalf("list actions: %v", err)
	}
	if strings.Join(actions, ",") != "fault.daemon,solution.version.added" {
		t.Fatalf("actions = %v", actions)
	}

	// Paging is clamped so the view cannot ask for an unbounded page.
	page, err = audit.Query(ctx, conn, audit.Filter{Limit: 1000})
	if err != nil {
		t.Fatalf("paged query: %v", err)
	}
	if page.Limit != audit.MaxQueryLimit {
		t.Fatalf("limit = %d, want %d", page.Limit, audit.MaxQueryLimit)
	}
}

func TestRecordRejectsIncompleteEntries(t *testing.T) {
	for name, entry := range map[string]audit.Entry{
		"missing action":   {Entity: "solution", Operator: "owner", Source: "ui"},
		"missing entity":   {Action: "solution.deleted", Operator: "owner", Source: "ui"},
		"missing operator": {Action: "solution.deleted", Entity: "solution", Source: "ui"},
		"missing source":   {Action: "solution.deleted", Entity: "solution", Operator: "owner"},
	} {
		if err := entry.Validate(); err == nil {
			t.Fatalf("%s: accepted an incomplete entry", name)
		}
	}
}

func TestCollectMeasuresTheMetrics(t *testing.T) {
	pool := auditTestPool(t)
	ctx := context.Background()
	conn, release := auditConn(t, pool)
	defer release()

	connectorID, err := database.EnsureConnector(ctx, conn, "audit-test", "B", "test")
	if err != nil {
		t.Fatalf("ensure connector: %v", err)
	}
	sessionID, err := database.EnsureSession(ctx, conn, connectorID, "audit-session", "audit-test")
	if err != nil {
		t.Fatalf("ensure session: %v", err)
	}

	first, err := database.RecordEvent(ctx, conn, testEvent(connectorID, sessionID, 1, "permission denied writing the file"))
	if err != nil {
		t.Fatalf("record the first event: %v", err)
	}
	if _, err := database.RecordEvent(ctx, conn, testEvent(connectorID, sessionID, 2, "permission denied writing the file")); err != nil {
		t.Fatalf("record the repeated event: %v", err)
	}
	second, err := database.RecordEvent(ctx, conn, testEvent(connectorID, sessionID, 3, "no such file or directory"))
	if err != nil {
		t.Fatalf("record the second issue: %v", err)
	}

	solutionID, err := database.CreateSolution(ctx, conn, first.IssueID, "audit-test", "owner")
	if err != nil {
		t.Fatalf("create solution: %v", err)
	}
	version, err := database.AddSolutionVersion(ctx, conn, solutionID, "chmod the file", "", "", database.Environment{}, "audit-test", "owner")
	if err != nil {
		t.Fatalf("add version: %v", err)
	}
	if err := database.TransitionSolution(ctx, conn, solutionID, database.SolutionVerified); err != nil {
		t.Fatalf("verify solution: %v", err)
	}

	secondSolutionID, err := database.CreateSolution(ctx, conn, second.IssueID, "audit-test", "owner")
	if err != nil {
		t.Fatalf("create second solution: %v", err)
	}
	secondVersion, err := database.AddSolutionVersion(ctx, conn, secondSolutionID, "create the file first", "", "", database.Environment{}, "audit-test", "owner")
	if err != nil {
		t.Fatalf("add second version: %v", err)
	}

	if _, err := conn.Exec(ctx, `
		insert into retrievals (session_id, query, candidate_issue_id, candidate_rank,
		                        score, match_fields, approximate, environment_diff, duration_ms, created_at)
		values
		  (null, 'q-exact', $1, 1, 0.9, array['fingerprint'], false, '[]'::jsonb, 10, now() - interval '2 minutes'),
		  (null, 'q-approximate', $2, 1, 0.7, array['trigram_candidate'], true, '[]'::jsonb, 30, now() - interval '1 minute')
	`, first.IssueID, second.IssueID); err != nil {
		t.Fatalf("insert retrievals: %v", err)
	}

	if _, err := conn.Exec(ctx, `
		insert into outcomes (issue_id, solution_version_id, occurrence_id, outcome, environment, source, actor)
		values ($1, $2, null, 'success', '{}'::jsonb, 'audit-test', 'owner')
	`, second.IssueID, secondVersion.ID); err != nil {
		t.Fatalf("insert outcome: %v", err)
	}

	if _, err := conn.Exec(ctx, `
		insert into notices (session_id, issue_id, kind, body, estimated_tokens)
		values (null, $1, 'hit', 'fix text', 2)
	`, second.IssueID); err != nil {
		t.Fatalf("insert notice: %v", err)
	}

	for _, entry := range []audit.Entry{
		{Action: "solution.version.added", Entity: "solution", EntityID: solutionID, Operator: "owner", Source: "ui"},
		{Action: "fault.daemon", Entity: "daemon", EntityID: "f-1", Operator: "system", Source: "daemon"},
	} {
		if err := audit.Record(ctx, conn, entry); err != nil {
			t.Fatalf("record audit row: %v", err)
		}
	}
	// The version variable above also proves AddSolutionVersion returned usable data.
	_ = version.ID

	stats, err := audit.NewService(pool).Collect(ctx)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}

	if stats.Retrieval.Rounds != 2 || stats.Retrieval.ExactTopOne != 1 || stats.Retrieval.ApproximateTopOne != 1 {
		t.Fatalf("rounds = %+v, want rounds 2, exact 1, approximate 1", stats.Retrieval)
	}
	if stats.Retrieval.FalseMerges != 0 {
		t.Fatalf("false merges = %d, want 0", stats.Retrieval.FalseMerges)
	}
	if stats.Retrieval.ApproximateTopOneRuns != 1 || stats.Retrieval.ApproximateTopOneHits != 1 {
		t.Fatalf("approximate top-1 = %d/%d, want 1/1",
			stats.Retrieval.ApproximateTopOneHits, stats.Retrieval.ApproximateTopOneRuns)
	}
	if stats.Retrieval.P50Milliseconds < 19 || stats.Retrieval.P50Milliseconds > 21 {
		t.Fatalf("p50 = %.2f ms, want about 20", stats.Retrieval.P50Milliseconds)
	}
	if stats.Retrieval.LatencySamples != 2 {
		t.Fatalf("latency samples = %d, want 2", stats.Retrieval.LatencySamples)
	}
	if stats.Solutions.Success != 1 || stats.Solutions.SolutionSuccessRate() != 1 {
		t.Fatalf("solutions = %+v", stats.Solutions)
	}
	if stats.Injections.Notices != 1 || stats.Injections.Characters != int64(len("fix text")) || stats.Injections.EstimatedTokens != 2 {
		t.Fatalf("injections = %+v", stats.Injections)
	}
	if stats.Repeats.RepeatedIssues != 1 || stats.Repeats.RepeatedHits != 1 {
		t.Fatalf("repeats = %+v", stats.Repeats)
	}
	if stats.Corrections.Total != 1 || stats.Faults.Total != 1 {
		t.Fatalf("corrections = %+v faults = %+v", stats.Corrections, stats.Faults)
	}
	// One repeated hit on an issue with a verified solution becomes an estimated
	// avoided diagnosis.
	if stats.Savings.AvoidedDiagnoses != 1 || stats.Savings.AvoidedTokens != audit.AssumedTokensPerDiagnosis {
		t.Fatalf("savings = %+v", stats.Savings)
	}
	if stats.Savings.InjectedCharacters != int64(len("fix text")) || stats.Savings.InjectedTokens != 2 {
		t.Fatalf("savings split = %+v", stats.Savings)
	}
	if !strings.Contains(stats.Savings.Assumption, "assumption") {
		t.Fatalf("assumption = %q", stats.Savings.Assumption)
	}
}

func TestCollectNeedsAPool(t *testing.T) {
	if _, err := audit.NewService(nil).Collect(context.Background()); err == nil {
		t.Fatal("collect without a pool returned no error")
	}
}
