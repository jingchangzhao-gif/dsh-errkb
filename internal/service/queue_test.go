package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/jingchangzhao-gif/dsh-errkb/internal/contract"
	"github.com/jingchangzhao-gif/dsh-errkb/internal/core"
	"github.com/jingchangzhao-gif/dsh-errkb/internal/database"
	"github.com/jingchangzhao-gif/dsh-errkb/internal/redact"
)

func queueClock(start time.Time) (func() time.Time, func(time.Duration)) {
	current := start
	now := func() time.Time { return current }
	advance := func(by time.Duration) { current = current.Add(by) }
	return now, advance
}

func queueEvent(t *testing.T, eventID, connectorID, sessionID, message string) contract.ErrorEvent {
	t.Helper()
	normalized := core.Normalize(message)
	headline := core.ExtractHeadline(message)
	transient := false
	confidence := 0.5
	return contract.ErrorEvent{
		SchemaVersion:     contract.SchemaVersion,
		EventID:           eventID,
		ConnectorID:       connectorID,
		SessionID:         sessionID,
		ObservedAt:        time.Now().UTC().Format(time.RFC3339),
		Channel:           "B",
		Category:          "tool",
		Severity:          "error",
		Transient:         &transient,
		Headline:          redact.Text(headline.Line, redact.Public),
		NormalizedMessage: normalized,
		RedactedSample:    redact.Sample(message, redact.Public),
		OS:                "windows",
		ProjectKey:        "queue-test",
		Fingerprint:       core.Signature("tool", normalized),
		Evidence:          "errkb queue test",
		Confidence:        &confidence,
	}
}

func openTestQueue(t *testing.T, options QueueOptions) *Queue {
	t.Helper()
	options.Dir = t.TempDir()
	queue, err := OpenQueue(options)
	if err != nil {
		t.Fatalf("OpenQueue: %v", err)
	}
	return queue
}

func queueFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read spool directory: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), queueFileSuffix) {
			names = append(names, entry.Name())
		}
	}
	return names
}

func readQueuedFile(t *testing.T, dir string) (queuedEvent, []byte) {
	t.Helper()
	names := queueFiles(t, dir)
	if len(names) != 1 {
		t.Fatalf("queued files = %d, want 1", len(names))
	}
	payload, err := os.ReadFile(filepath.Join(dir, names[0]))
	if err != nil {
		t.Fatalf("read queued file: %v", err)
	}
	var queued queuedEvent
	if err := json.Unmarshal(payload, &queued); err != nil {
		t.Fatalf("decode queued file: %v", err)
	}
	return queued, payload
}

// newQueueDatabase creates an isolated schema on the test database. The queue
// tests that prove replay idempotency need the real errkb_record_event.
func newQueueDatabase(t *testing.T) (*pgx.Conn, string, string) {
	t.Helper()
	dsn := os.Getenv("ERRKB_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set ERRKB_TEST_DATABASE_URL to run the database-backed queue tests")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	schema := fmt.Sprintf("errkb_queue_%d", time.Now().UnixNano())
	if _, err := conn.Exec(ctx, "create schema "+schema); err != nil {
		t.Fatalf("create schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = conn.Exec(context.Background(), "drop schema "+schema+" cascade")
		_ = conn.Close(context.Background())
	})
	if _, err := conn.Exec(ctx, "set search_path to "+schema+", public"); err != nil {
		t.Fatalf("set search_path: %v", err)
	}
	if _, err := database.Migrate(ctx, conn, "../../db/migrations"); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	var connectorID, sessionID string
	name := fmt.Sprintf("queue-test-%d", time.Now().UnixNano())
	if err := conn.QueryRow(ctx,
		`insert into connectors (name, channel, source, actor) values ($1, 'B', 'queue', 'test') returning id`,
		name,
	).Scan(&connectorID); err != nil {
		t.Fatalf("insert connector: %v", err)
	}
	if err := conn.QueryRow(ctx,
		`insert into sessions (connector_id, external_id, started_at, source, actor)
		 values ($1, 'queue-session', now(), 'queue', 'test') returning id`,
		connectorID,
	).Scan(&sessionID); err != nil {
		t.Fatalf("insert session: %v", err)
	}
	return conn, connectorID, sessionID
}

func countRows(t *testing.T, conn *pgx.Conn, table string) int {
	t.Helper()
	var count int
	if err := conn.QueryRow(context.Background(), "select count(*) from "+table).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}

func TestQueueStoresAlreadyRedactedEvents(t *testing.T) {
	dir := t.TempDir()
	queue, err := OpenQueue(QueueOptions{Dir: dir})
	if err != nil {
		t.Fatalf("OpenQueue: %v", err)
	}
	token := "sk-" + "abcdefghijklmnopqrstuvwx"
	event := queueEvent(t, "00000000-0000-4000-8000-000000000001", "connector", "session",
		"open "+token+" failed")

	added, err := queue.Enqueue(event)
	if err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if !added {
		t.Fatal("Enqueue reported the event as a duplicate")
	}
	queued, payload := readQueuedFile(t, dir)
	if string(payload) == "" || strings.Contains(string(payload), token) {
		t.Fatalf("queued payload still contains the secret: %s", payload)
	}
	for name, field := range map[string]string{
		"headline":           queued.Event.Headline,
		"normalized_message": queued.Event.NormalizedMessage,
		"redacted_sample":    queued.Event.RedactedSample,
	} {
		if strings.Contains(field, token) {
			t.Errorf("%s still contains the secret", name)
		}
	}
	if !strings.Contains(queued.Event.RedactedSample, "<secret>") {
		t.Errorf("redacted sample = %q, want a redaction marker", queued.Event.RedactedSample)
	}
	if err := queued.Event.Validate(); err != nil {
		t.Errorf("queued event no longer validates: %v", err)
	}
	stats := queue.Stats()
	if stats.Enqueued != 1 || stats.Pending != 1 || stats.Bytes == 0 {
		t.Fatalf("stats = %+v, want one pending event with bytes", stats)
	}
}

func TestQueueRejectsAnEventThatCannotBeStored(t *testing.T) {
	dir := t.TempDir()
	queue, err := OpenQueue(QueueOptions{Dir: dir})
	if err != nil {
		t.Fatalf("OpenQueue: %v", err)
	}
	event := queueEvent(t, "00000000-0000-4000-8000-000000000002", "connector", "session", "boom")
	event.ObservedAt = ""
	if _, err := queue.Enqueue(event); err == nil {
		t.Fatal("Enqueue accepted an event without observed_at")
	}
	if names := queueFiles(t, dir); len(names) != 0 {
		t.Fatalf("queued files = %d, want none", len(names))
	}
}

func TestQueueIgnoresRepeatedEventIDs(t *testing.T) {
	queue := openTestQueue(t, QueueOptions{})
	event := queueEvent(t, "00000000-0000-4000-8000-000000000003", "connector", "session", "boom")

	first, err := queue.Enqueue(event)
	if err != nil || !first {
		t.Fatalf("first Enqueue = %v, %v; want true, nil", first, err)
	}
	second, err := queue.Enqueue(event)
	if err != nil {
		t.Fatalf("second Enqueue: %v", err)
	}
	if second {
		t.Fatal("second Enqueue stored the same event_id twice")
	}
	if stats := queue.Stats(); stats.Enqueued != 1 || stats.Pending != 1 {
		t.Fatalf("stats = %+v, want one event", stats)
	}
}

func TestQueueDropsOldestWhenOverTheCountCap(t *testing.T) {
	queue := openTestQueue(t, QueueOptions{MaxEvents: 2})
	for i := 0; i < 3; i++ {
		event := queueEvent(t, fmt.Sprintf("00000000-0000-4000-8000-00000000001%d", i), "connector", "session", fmt.Sprintf("boom %d", i))
		if _, err := queue.Enqueue(event); err != nil {
			t.Fatalf("Enqueue %d: %v", i, err)
		}
	}
	stats := queue.Stats()
	if stats.Pending != 2 {
		t.Fatalf("pending = %d, want 2", stats.Pending)
	}
	if stats.Dropped != 1 {
		t.Fatalf("dropped = %d, want 1", stats.Dropped)
	}
	names := queueFiles(t, queue.Dir())
	if strings.Contains(names[0], "000000000010") {
		t.Fatalf("oldest event survived: %v", names)
	}
}

func TestQueueDropsOldestWhenOverTheByteCap(t *testing.T) {
	queue := openTestQueue(t, QueueOptions{MaxBytes: 2000})
	body := strings.Repeat("build failed on a very long line of output ", 40)
	for i := 0; i < 3; i++ {
		event := queueEvent(t, fmt.Sprintf("00000000-0000-4000-8000-00000000002%d", i), "connector", "session", body+fmt.Sprintf(" %d", i))
		if _, err := queue.Enqueue(event); err != nil {
			t.Fatalf("Enqueue %d: %v", i, err)
		}
	}
	stats := queue.Stats()
	if stats.Dropped == 0 {
		t.Fatalf("stats = %+v, want at least one drop under the byte cap", stats)
	}
	// The newest entry is always kept, even when it alone is larger than the
	// cap: losing the failure that just happened would be worse than exceeding
	// the byte budget until the next replay.
	if stats.Pending != 1 || stats.Dropped != 2 {
		t.Fatalf("stats = %+v, want one kept entry and two drops", stats)
	}
}

func TestQueueExpiresOldEntries(t *testing.T) {
	start := time.Date(2026, 10, 19, 9, 0, 0, 0, time.UTC)
	now, advance := queueClock(start)
	queue := openTestQueue(t, QueueOptions{MaxAge: time.Hour, Now: now})

	old := queueEvent(t, "00000000-0000-4000-8000-000000000030", "connector", "session", "old failure")
	if _, err := queue.Enqueue(old); err != nil {
		t.Fatalf("Enqueue old: %v", err)
	}
	advance(25 * time.Hour)
	fresh := queueEvent(t, "00000000-0000-4000-8000-000000000031", "connector", "session", "new failure")
	if _, err := queue.Enqueue(fresh); err != nil {
		t.Fatalf("Enqueue fresh: %v", err)
	}
	stats := queue.Stats()
	if stats.Expired != 1 {
		t.Fatalf("expired = %d, want 1", stats.Expired)
	}
	if stats.Pending != 1 {
		t.Fatalf("pending = %d, want 1", stats.Pending)
	}
}

func TestQueueEnqueueFailsFastWhenTheDirectoryIsGone(t *testing.T) {
	dir := t.TempDir()
	queue, err := OpenQueue(QueueOptions{Dir: dir})
	if err != nil {
		t.Fatalf("OpenQueue: %v", err)
	}
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("remove spool directory: %v", err)
	}
	started := time.Now()
	_, err = queue.Enqueue(queueEvent(t, "00000000-0000-4000-8000-000000000040", "connector", "session", "boom"))
	if err == nil {
		t.Fatal("Enqueue accepted an event with nowhere to write it")
	}
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("Enqueue blocked for %s on a missing directory", elapsed)
	}
}

func TestQueueReplayRecordsEachEventOnce(t *testing.T) {
	conn, connectorID, sessionID := newQueueDatabase(t)
	queue := openTestQueue(t, QueueOptions{})
	for i := 0; i < 2; i++ {
		event := queueEvent(t, fmt.Sprintf("00000000-0000-4000-8000-00000000004%d", i), connectorID, sessionID, fmt.Sprintf("failure %d", i))
		if _, err := queue.Enqueue(event); err != nil {
			t.Fatalf("Enqueue %d: %v", i, err)
		}
	}

	report, err := queue.Replay(context.Background(), func(ctx context.Context, event contract.ErrorEvent) (database.RecordResult, error) {
		return database.RecordEvent(ctx, conn, event)
	})
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if report.Replayed != 2 || report.Remaining != 0 || report.Errors != 0 {
		t.Fatalf("report = %+v, want 2 replayed and nothing left", report)
	}
	if got := countRows(t, conn, "events"); got != 2 {
		t.Fatalf("events = %d, want 2", got)
	}
	if stats := queue.Stats(); stats.Replayed != 2 || stats.Pending != 0 {
		t.Fatalf("stats = %+v", stats)
	}
}

func TestQueueReplayIsIdempotentAfterACrash(t *testing.T) {
	conn, connectorID, sessionID := newQueueDatabase(t)
	queue := openTestQueue(t, QueueOptions{})
	event := queueEvent(t, "00000000-0000-4000-8000-000000000050", connectorID, sessionID, "EPERM: rename failed")
	if _, err := queue.Enqueue(event); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	// Simulate a crash after the database accepted the event but before the
	// queue file was deleted.
	if _, err := database.RecordEvent(context.Background(), conn, event); err != nil {
		t.Fatalf("record before replay: %v", err)
	}
	report, err := queue.Replay(context.Background(), func(ctx context.Context, queued contract.ErrorEvent) (database.RecordResult, error) {
		return database.RecordEvent(ctx, conn, queued)
	})
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if report.Replayed != 1 || report.Remaining != 0 {
		t.Fatalf("report = %+v, want the file replayed and removed", report)
	}
	if got := countRows(t, conn, "events"); got != 1 {
		t.Fatalf("events = %d, want 1 (event_id stays unique)", got)
	}
}

func TestQueueReplayKeepsEntriesWhenTheDatabaseIsStillDown(t *testing.T) {
	queue := openTestQueue(t, QueueOptions{})
	for i := 0; i < 2; i++ {
		event := queueEvent(t, fmt.Sprintf("00000000-0000-4000-8000-00000000006%d", i), "connector", "session", fmt.Sprintf("failure %d", i))
		if _, err := queue.Enqueue(event); err != nil {
			t.Fatalf("Enqueue %d: %v", i, err)
		}
	}
	report, err := queue.Replay(context.Background(), func(context.Context, contract.ErrorEvent) (database.RecordResult, error) {
		return database.RecordResult{}, fmt.Errorf("connection refused")
	})
	if err != nil {
		t.Fatalf("Replay: %v", err)
	}
	if report.Replayed != 0 || report.Errors != 1 || report.Remaining != 2 {
		t.Fatalf("report = %+v, want one error and both files kept", report)
	}
	stats := queue.Stats()
	if stats.ReplayErrors != 1 || !strings.Contains(stats.LastError, "connection refused") {
		t.Fatalf("stats = %+v, want the replay error recorded", stats)
	}
}

func TestQueueRunReplaysWhenTheDatabaseComesBack(t *testing.T) {
	dir := t.TempDir()
	queue, err := OpenQueue(QueueOptions{Dir: dir})
	if err != nil {
		t.Fatalf("OpenQueue: %v", err)
	}
	event := queueEvent(t, "00000000-0000-4000-8000-000000000070", "connector", "session", "boom")
	if _, err := queue.Enqueue(event); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}

	var probes int
	recorded := make(chan struct{}, 1)
	run := func(ctx context.Context) error {
		return queue.Run(ctx, QueueRunOptions{
			Available: func(context.Context) error {
				probes++
				if probes < 3 {
					return fmt.Errorf("database unavailable")
				}
				return nil
			},
			Recorder: func(context.Context, contract.ErrorEvent) (database.RecordResult, error) {
				select {
				case recorded <- struct{}{}:
				default:
				}
				return database.RecordResult{}, nil
			},
			PollInterval: 10 * time.Millisecond,
			MaxBackoff:   20 * time.Millisecond,
		})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- run(ctx) }()

	select {
	case <-recorded:
	case <-ctx.Done():
		t.Fatal("the queue never replayed after the probe succeeded")
	}
	waitFor(t, func() bool { return queue.Stats().Pending == 0 }, 2*time.Second)
	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
	if probes < 3 {
		t.Fatalf("probes = %d, want at least 3", probes)
	}
	if stats := queue.Stats(); stats.Replayed != 1 {
		t.Fatalf("stats = %+v, want one replay", stats)
	}
}

func waitFor(t *testing.T, condition func() bool, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("condition did not become true before the deadline")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
