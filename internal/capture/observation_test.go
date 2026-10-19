package capture_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jingchangzhao-gif/dsh-errkb/internal/capture"
	"github.com/jingchangzhao-gif/dsh-errkb/internal/contract"
)

// captureRequest is the shape the DSH adapter sends: the adapter names itself
// and its session, and hands over the raw failure. Everything else is the
// core's job.
func captureRequest(t *testing.T, observationID, message string) contract.CaptureRequest {
	t.Helper()
	exit := 1
	return contract.CaptureRequest{
		SchemaVersion: contract.ConnectorSDKVersion,
		ConnectorID:   "capture-adapter",
		SessionID:     "session-1",
		ObservationID: observationID,
		Observation: contract.ConnectorObservation{
			ObservedAt: time.Now().UTC().Format(time.RFC3339),
			Source:     "tool",
			Message:    message,
			ErrorCode:  "EPERM",
			Command:    "pnpm build",
			ExitCode:   &exit,
			ToolName:   "bash",
			OS:         "windows",
			Shell:      "powershell",
			ProjectKey: "proj-a",
			Evidence:   "hook: tool call",
		},
	}
}

func countCaptureRows(t *testing.T, db *captureDatabase, table string) int {
	t.Helper()
	var count int
	if err := db.conn.QueryRow(context.Background(), "select count(*) from "+table).Scan(&count); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return count
}

func TestCaptureStoresObservationAndReplaysIdempotently(t *testing.T) {
	db := newCaptureDatabase(t)
	ctx := context.Background()
	request := captureRequest(t, "obs-1", "open <path> failed with EPERM")

	first, err := capture.CaptureObservation(ctx, db.conn, request)
	if err != nil {
		t.Fatalf("first capture: %v", err)
	}
	if !first.Created {
		t.Fatalf("first capture created = false, want true")
	}
	if first.EventID == "" || first.IssueID == "" || first.Fingerprint == "" {
		t.Fatalf("first capture identity incomplete: %+v", first)
	}

	// The same observation id is a replay: the identity must not change and the
	// observation must not be counted twice.
	second, err := capture.CaptureObservation(ctx, db.conn, request)
	if err != nil {
		t.Fatalf("replay capture: %v", err)
	}
	if second.Created {
		t.Fatalf("replay created = true, want false")
	}
	if second.EventID != first.EventID || second.OccurrenceID != first.OccurrenceID || second.IssueID != first.IssueID {
		t.Fatalf("replay identity changed: %+v vs %+v", second, first)
	}
	for table, want := range map[string]int{"events": 1, "occurrences": 1, "issues": 1} {
		if got := countCaptureRows(t, db, table); got != want {
			t.Fatalf("%s = %d, want %d", table, got, want)
		}
	}

	var category, channel, headline string
	if err := db.conn.QueryRow(ctx,
		`select category, channel, headline from events where event_id = $1::uuid`, first.EventID,
	).Scan(&category, &channel, &headline); err != nil {
		t.Fatalf("read stored event: %v", err)
	}
	if category != "tool" {
		t.Fatalf("stored category = %q, want the capture source tool", category)
	}
	if channel != "A" {
		t.Fatalf("stored channel = %q, want the default A", channel)
	}
	if !strings.Contains(headline, "EPERM") {
		t.Fatalf("stored headline = %q, want the EPERM headline", headline)
	}
}

func TestCaptureRedactsRawTextBeforeStorage(t *testing.T) {
	db := newCaptureDatabase(t)
	ctx := context.Background()
	secret := "sk-" + "abcdefghijklmnopqrstuvwx"
	message := "auth failed for " + secret

	response, err := capture.CaptureObservation(ctx, db.conn, captureRequest(t, "obs-secret", message))
	if err != nil {
		t.Fatalf("capture: %v", err)
	}
	var headline, normalized, sample string
	if err := db.conn.QueryRow(ctx,
		`select headline, normalized_message, redacted_sample from events where event_id = $1::uuid`, response.EventID,
	).Scan(&headline, &normalized, &sample); err != nil {
		t.Fatalf("read stored event: %v", err)
	}
	for name, text := range map[string]string{"headline": headline, "normalized": normalized, "sample": sample} {
		if strings.Contains(text, secret) {
			t.Fatalf("stored %s still contains the raw secret: %q", name, text)
		}
	}
	if !strings.Contains(sample, "<secret>") {
		t.Fatalf("stored sample = %q, want the redacted marker", sample)
	}
}

func TestCaptureRejectsAnInvalidEnvelope(t *testing.T) {
	db := newCaptureDatabase(t)
	request := captureRequest(t, "", "open <path> failed")
	request.SessionID = ""
	if _, err := capture.CaptureObservation(context.Background(), db.conn, request); err == nil {
		t.Fatalf("capture without a session was accepted")
	}
	if countCaptureRows(t, db, "events") != 0 {
		t.Fatalf("an invalid capture wrote an event")
	}
}

// The adapter identity is created on first use; the same connector keeps a
// single row across sessions so counts are not split by reconnects.
func TestCaptureCreatesTheAdapterIdentityOnce(t *testing.T) {
	db := newCaptureDatabase(t)
	ctx := context.Background()
	if _, err := capture.CaptureObservation(ctx, db.conn, captureRequest(t, "obs-2", "boom one")); err != nil {
		t.Fatalf("first capture: %v", err)
	}
	second := captureRequest(t, "obs-3", "boom two")
	second.SessionID = "session-2"
	if _, err := capture.CaptureObservation(ctx, db.conn, second); err != nil {
		t.Fatalf("second capture: %v", err)
	}
	var connectors, sessions int
	if err := db.conn.QueryRow(ctx, `select count(*) from connectors where name = 'capture-adapter'`).Scan(&connectors); err != nil {
		t.Fatalf("count connectors: %v", err)
	}
	if err := db.conn.QueryRow(ctx, `select count(*) from sessions where external_id in ('session-1', 'session-2')`).Scan(&sessions); err != nil {
		t.Fatalf("count sessions: %v", err)
	}
	if connectors != 1 {
		t.Fatalf("connectors = %d, want 1", connectors)
	}
	if sessions != 2 {
		t.Fatalf("sessions = %d, want 2", sessions)
	}
}
