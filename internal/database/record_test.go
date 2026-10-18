package database_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jingchangzhao-gif/dsh-errkb/internal/contract"
	"github.com/jingchangzhao-gif/dsh-errkb/internal/core"
	"github.com/jingchangzhao-gif/dsh-errkb/internal/database"
	"github.com/jingchangzhao-gif/dsh-errkb/internal/redact"
)

const recordFixtureMessage = "eperm: operation not permitted, rename '<path>/pkg'"

// setupRecordSchema gives a migrated schema with one connector and one session.
// The schema name is needed by the pool in the concurrency test, because every
// pooled connection must see the same schema.
func setupRecordSchema(t *testing.T) (conn *pgx.Conn, schema, connectorID, sessionID string) {
	t.Helper()
	conn = connect(t)
	schema = freshSchema(t, conn)
	migrate(t, conn, migrationsDir())
	connectorID = insertTestConnector(t, conn)
	sessionID = insertTestSession(t, conn, connectorID)
	return conn, schema, connectorID, sessionID
}

func insertTestConnector(t *testing.T, conn *pgx.Conn) string {
	t.Helper()
	var id string
	err := conn.QueryRow(context.Background(), `
		insert into connectors (name, channel, source, actor)
		values ('record-test', 'A', 'test', 'test')
		returning id
	`).Scan(&id)
	if err != nil {
		t.Fatalf("insert connector: %v", err)
	}
	return id
}

func insertTestSession(t *testing.T, conn *pgx.Conn, connectorID string) string {
	t.Helper()
	var id string
	err := conn.QueryRow(context.Background(), `
		insert into sessions (connector_id, external_id, started_at, source, actor)
		values ($1, 'record-session', now(), 'test', 'test')
		returning id
	`, connectorID).Scan(&id)
	if err != nil {
		t.Fatalf("insert session: %v", err)
	}
	return id
}

// eventUUID builds a valid UUID from an integer, so each synthetic event gets a
// distinct event_id without pulling in a UUID dependency.
func eventUUID(n int) string {
	return fmt.Sprintf("00000000-0000-4000-8000-%012d", n)
}

// unregisteredConnectorID is a valid UUID that was never inserted.
func unregisteredConnectorID() string {
	return "11111111-1111-4111-8111-111111111111"
}

func recordFixture(connectorID, sessionID, eventID, message, projectKey string) contract.ErrorEvent {
	normalized := core.Normalize(message)
	category := "tool"
	transient := false
	confidence := 0.9
	return contract.ErrorEvent{
		SchemaVersion:     "v1",
		EventID:           eventID,
		ConnectorID:       connectorID,
		SessionID:         sessionID,
		ObservedAt:        time.Now().UTC().Format(time.RFC3339),
		Channel:           "A",
		Category:          category,
		Severity:          "error",
		Transient:         &transient,
		Headline:          "EPERM renaming the package",
		NormalizedMessage: normalized,
		RedactedSample:    redact.Sample(message, redact.Public),
		OS:                "windows",
		Shell:             "powershell",
		RuntimeVersions:   map[string]string{"node": "24.16.0"},
		ProjectKey:        projectKey,
		Fingerprint:       core.Signature(category, normalized),
		Evidence:          "tool output",
		Confidence:        &confidence,
	}
}

func rowCount(t *testing.T, conn *pgx.Conn, table string) int {
	t.Helper()
	var count int
	if err := conn.QueryRow(context.Background(), "select count(*) from "+table).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}

func storedHeadline(t *testing.T, conn *pgx.Conn, eventID string) string {
	t.Helper()
	var headline string
	if err := conn.QueryRow(context.Background(), "select headline from events where event_id = $1", eventID).Scan(&headline); err != nil {
		t.Fatalf("read the stored event: %v", err)
	}
	return headline
}

// TestRecordEventReplayIsIdempotent replays one event ten times: the event row
// and the issue are created once, occurrences are appended each time, and the
// stored event is never rewritten.
func TestRecordEventReplayIsIdempotent(t *testing.T) {
	ctx := context.Background()
	conn, _, connector, session := setupRecordSchema(t)
	event := recordFixture(connector, session, eventUUID(1), recordFixtureMessage, "proj")

	first, err := database.RecordEvent(ctx, conn, event)
	if err != nil {
		t.Fatalf("first record: %v", err)
	}
	if !first.Created {
		t.Fatal("the first record reported created=false, want true")
	}
	if first.Fingerprint != event.Fingerprint {
		t.Fatalf("fingerprint = %s, want %s", first.Fingerprint, event.Fingerprint)
	}
	if first.IssueID == "" || first.OccurrenceID == "" {
		t.Fatalf("result is missing identifiers: %+v", first)
	}

	for i := 0; i < 9; i++ {
		replay := event
		replay.Headline = "a different headline on replay"
		result, err := database.RecordEvent(ctx, conn, replay)
		if err != nil {
			t.Fatalf("replay %d: %v", i, err)
		}
		if result.Created {
			t.Fatalf("replay %d reported created=true, want false", i)
		}
		if result.IssueID != first.IssueID {
			t.Fatalf("replay %d issue = %s, want %s", i, result.IssueID, first.IssueID)
		}
	}

	if got := rowCount(t, conn, "events"); got != 1 {
		t.Errorf("events = %d, want 1", got)
	}
	if got := rowCount(t, conn, "issues"); got != 1 {
		t.Errorf("issues = %d, want 1", got)
	}
	if got := rowCount(t, conn, "occurrences"); got != 10 {
		t.Errorf("occurrences = %d, want 10", got)
	}

	var hits int
	if err := conn.QueryRow(ctx, "select hits from issues").Scan(&hits); err != nil {
		t.Fatalf("read the issue: %v", err)
	}
	if hits != 10 {
		t.Errorf("issue hits = %d, want 10", hits)
	}
	if got := storedHeadline(t, conn, event.EventID); got != event.Headline {
		t.Errorf("stored headline = %q, want the first value %q", got, event.Headline)
	}
}

// TestRecordEventConcurrentExactFingerprint is the card's central test: fifty
// goroutines on a pool, each its own connection, fifty distinct events that
// share one exact fingerprint. One issue and fifty occurrences must appear.
func TestRecordEventConcurrentExactFingerprint(t *testing.T) {
	ctx := context.Background()
	conn, schema, connector, session := setupRecordSchema(t)

	config, err := pgxpool.ParseConfig(os.Getenv(testDatabaseEnv))
	if err != nil {
		t.Fatalf("parse the pool config: %v", err)
	}
	config.MaxConns = 60
	config.AfterConnect = func(ctx context.Context, pooled *pgx.Conn) error {
		_, err := pooled.Exec(ctx, "set search_path to "+schema+", public")
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatalf("create the pool: %v", err)
	}
	t.Cleanup(pool.Close)

	const total = 50
	var wg sync.WaitGroup
	errs := make(chan error, total)
	issueIDs := make(chan string, total)
	start := make(chan struct{})

	for i := 0; i < total; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start

			event := recordFixture(connector, session, eventUUID(1000+i), recordFixtureMessage, "proj")
			acquired, err := pool.Acquire(ctx)
			if err != nil {
				errs <- fmt.Errorf("acquire connection %d: %w", i, err)
				return
			}
			defer acquired.Release()

			result, err := database.RecordEvent(ctx, acquired.Conn(), event)
			if err != nil {
				errs <- fmt.Errorf("record event %d: %w", i, err)
				return
			}
			if !result.Created {
				errs <- fmt.Errorf("event %d was reported as a replay", i)
				return
			}
			issueIDs <- result.IssueID
		}(i)
	}
	close(start)
	wg.Wait()
	close(errs)
	close(issueIDs)

	for err := range errs {
		t.Fatalf("concurrent record: %v", err)
	}

	seen := make(map[string]bool)
	for id := range issueIDs {
		seen[id] = true
	}
	if len(seen) != 1 {
		t.Fatalf("the 50 events produced %d issues, want exactly 1: %v", len(seen), seen)
	}

	if got := rowCount(t, conn, "events"); got != total {
		t.Errorf("events = %d, want %d", got, total)
	}
	if got := rowCount(t, conn, "issues"); got != 1 {
		t.Errorf("issues = %d, want 1", got)
	}
	if got := rowCount(t, conn, "occurrences"); got != total {
		t.Errorf("occurrences = %d, want %d", got, total)
	}

	var hits int
	if err := conn.QueryRow(ctx, "select hits from issues").Scan(&hits); err != nil {
		t.Fatalf("read the issue: %v", err)
	}
	if hits != total {
		t.Errorf("issue hits = %d, want %d", hits, total)
	}
}

// TestRecordEventReplayWithDifferentFingerprintFails proves the stored event is
// not silently merged when a replay claims another fingerprint.
func TestRecordEventReplayWithDifferentFingerprintFails(t *testing.T) {
	ctx := context.Background()
	conn, _, connector, session := setupRecordSchema(t)
	event := recordFixture(connector, session, eventUUID(2), recordFixtureMessage, "proj")
	if _, err := database.RecordEvent(ctx, conn, event); err != nil {
		t.Fatalf("record: %v", err)
	}

	changed := recordFixture(connector, session, event.EventID, "enospc: no space left on device", "proj")
	_, err := database.RecordEvent(ctx, conn, changed)
	if err == nil || !strings.Contains(err.Error(), "already recorded with fingerprint") {
		t.Fatalf("replay with a different fingerprint error = %v, want the recorded-fingerprint error", err)
	}
	if got := rowCount(t, conn, "occurrences"); got != 1 {
		t.Errorf("occurrences = %d after the rejected replay, want 1", got)
	}
}

// TestRecordEventAggregatesByExactFingerprintOnly records two different
// messages and expects two issues: no similarity path exists in the function.
func TestRecordEventAggregatesByExactFingerprintOnly(t *testing.T) {
	ctx := context.Background()
	conn, _, connector, session := setupRecordSchema(t)

	first := recordFixture(connector, session, eventUUID(3), recordFixtureMessage, "proj")
	second := recordFixture(connector, session, eventUUID(4), "enospc: no space left on device", "proj")
	if first.Fingerprint == second.Fingerprint {
		t.Fatalf("the two fixtures share fingerprint %s; the test cannot prove exact matching", first.Fingerprint)
	}

	firstResult, err := database.RecordEvent(ctx, conn, first)
	if err != nil {
		t.Fatalf("record first: %v", err)
	}
	secondResult, err := database.RecordEvent(ctx, conn, second)
	if err != nil {
		t.Fatalf("record second: %v", err)
	}
	if firstResult.IssueID == secondResult.IssueID {
		t.Fatalf("different fingerprints produced the same issue %s", firstResult.IssueID)
	}
	if got := rowCount(t, conn, "issues"); got != 2 {
		t.Errorf("issues = %d, want 2", got)
	}
}

// TestRecordEventRollsBackAsOneUnit runs the function inside an explicit
// transaction and fails the transaction afterwards: the event, the occurrence
// and the issue must all disappear together.
func TestRecordEventRollsBackAsOneUnit(t *testing.T) {
	ctx := context.Background()
	conn, _, connector, session := setupRecordSchema(t)
	event := recordFixture(connector, session, eventUUID(5), recordFixtureMessage, "proj")

	payload, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("encode the event: %v", err)
	}

	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	if _, err := tx.Exec(ctx, "select * from errkb_record_event($1::jsonb, 'test', 'test')", payload); err != nil {
		t.Fatalf("record inside the transaction: %v", err)
	}
	if _, err := tx.Exec(ctx, "select 1 / 0"); err == nil {
		t.Fatal("the forced failure did not happen")
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatalf("rollback: %v", err)
	}

	for _, table := range []string{"events", "issues", "occurrences"} {
		if got := rowCount(t, conn, table); got != 0 {
			t.Errorf("%s = %d after the rollback, want 0", table, got)
		}
	}
}

// TestRecordEventRequiresTheConnector checks the error translation for a
// missing foreign key.
func TestRecordEventRequiresTheConnector(t *testing.T) {
	ctx := context.Background()
	conn, _, _, _ := setupRecordSchema(t)
	event := recordFixture(unregisteredConnectorID(), eventUUID(9), eventUUID(6), recordFixtureMessage, "proj")

	_, err := database.RecordEvent(ctx, conn, event)
	if err == nil || !strings.Contains(err.Error(), "does not exist") {
		t.Fatalf("record with an unknown connector error = %v, want the missing-connector message", err)
	}
	if got := rowCount(t, conn, "events"); got != 0 {
		t.Errorf("events = %d, want 0", got)
	}
}
