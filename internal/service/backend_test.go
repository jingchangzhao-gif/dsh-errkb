package service

import (
	"context"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/contract"
)

// captureTestEvent builds a valid ErrorEvent for the gate/queue tests without
// touching PostgreSQL.
func captureTestEvent(eventID string) contract.ErrorEvent {
	transient := false
	confidence := 0.5
	return contract.ErrorEvent{
		SchemaVersion:     "v1",
		EventID:           eventID,
		ConnectorID:       "codex-cli",
		SessionID:         "session-1",
		ObservedAt:        "2026-10-22T10:00:00Z",
		Channel:           "B",
		Category:          "command-exit",
		Severity:          "error",
		Transient:         &transient,
		Headline:          "command exited with status 3",
		NormalizedMessage: "command exited with status 3",
		RedactedSample:    "command exited with status 3",
		OS:                "windows",
		ProjectKey:        "probe",
		Fingerprint:       "0123456789ab",
		Evidence:          "errkb run -- cmd /c exit 3",
		Confidence:        &confidence,
	}
}

// TestCaptureGateStopsPausedWrites locks the rule the settings page and the
// daemon share: the global switch, paused connectors and paused projects each
// stop a write, and empty keys match nothing.
func TestCaptureGateStopsPausedWrites(t *testing.T) {
	enabled := true
	disabled := false
	pausedConfig := contract.Config{}
	pausedConfig.Capture.Enabled = &disabled
	pausedConfig.Capture.PausedConnectors = []string{"codex-cli"}
	pausedConfig.Capture.PausedProjects = []string{"probe"}

	globalGate := CaptureGate(validDefaultConfig())
	if err := globalGate(context.Background(), "codex-cli", "probe"); err != nil {
		t.Fatalf("a default config must allow capture: %v", err)
	}
	partial := pausedConfig
	partial.Capture.Enabled = &enabled
	if err := CaptureGate(partial)(context.Background(), "codex-cli", "other"); err == nil {
		t.Fatal("paused connector accepted a write")
	} else if !strings.Contains(err.Error(), "codex-cli") {
		t.Fatalf("error does not name the connector: %v", err)
	}
	if err := CaptureGate(partial)(context.Background(), "claude-code", "probe"); err == nil {
		t.Fatal("paused project accepted a write")
	} else if !strings.Contains(err.Error(), "probe") {
		t.Fatalf("error does not name the project: %v", err)
	}
	disabledConfig := pausedConfig
	disabledConfig.Capture.Enabled = &disabled
	if err := CaptureGate(disabledConfig)(context.Background(), "anything", "anything"); err == nil {
		t.Fatal("global pause accepted a write")
	}
	if err := CaptureGate(partial)(context.Background(), "claude-code", ""); err != nil {
		t.Fatalf("an empty project must not match a paused key: %v", err)
	}
}

func validDefaultConfig() contract.Config {
	enabled := true
	config := contract.Config{}
	config.Capture.Enabled = &enabled
	return config
}

// TestRecordQueuesWhenTheDatabaseIsUnreachable covers the offline path: a
// database outage spools the already-redacted event and the response says
// queued instead of pretending the write landed.
func TestRecordQueuesWhenTheDatabaseIsUnreachable(t *testing.T) {
	queue, err := OpenQueue(QueueOptions{Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("open the queue: %v", err)
	}
	// Port 1 is never a PostgreSQL listener, so the acquire fails fast.
	pool, err := pgxpool.New(context.Background(), "postgres://postgres:postgres@127.0.0.1:1/postgres?sslmode=disable&connect_timeout=1")
	if err != nil {
		t.Fatalf("build the pool: %v", err)
	}
	defer pool.Close()
	backend := NewSQLBackend(pool).WithQueue(queue)

	event := captureTestEvent("11111111-1111-4111-8111-111111111111")
	response, err := backend.Record(context.Background(), contract.RecordRequest{SchemaVersion: "v1", Event: event})
	if err != nil {
		t.Fatalf("a queued write must not fail: %v", err)
	}
	if !response.Queued || response.EventID != event.EventID {
		t.Fatalf("queued response is wrong: %+v", response)
	}
	if pending := queue.Stats().Pending; pending != 1 {
		t.Fatalf("the queue holds %d event(s), want 1", pending)
	}
}

// TestRecordRefusesPausedCapture keeps the gate ahead of the database call: a
// paused write never reaches the queue either.
func TestRecordRefusesPausedCapture(t *testing.T) {
	queue, err := OpenQueue(QueueOptions{Dir: t.TempDir()})
	if err != nil {
		t.Fatalf("open the queue: %v", err)
	}
	disabled := false
	config := contract.Config{}
	config.Capture.Enabled = &disabled
	pool, err := pgxpool.New(context.Background(), "postgres://postgres:postgres@127.0.0.1:1/postgres?sslmode=disable&connect_timeout=1")
	if err != nil {
		t.Fatalf("build the pool: %v", err)
	}
	defer pool.Close()
	backend := NewSQLBackend(pool).WithCaptureGate(CaptureGate(config)).WithQueue(queue)

	_, recordErr := backend.Record(context.Background(), contract.RecordRequest{
		SchemaVersion: "v1",
		Event:         captureTestEvent("22222222-2222-4222-8222-222222222222"),
	})
	if recordErr == nil {
		t.Fatal("a paused capture was accepted")
	}
	var apiError *APIError
	if !asAPIError(recordErr, &apiError) || apiError.Code != CodeConflict {
		t.Fatalf("paused write returned %v, want a conflict APIError", recordErr)
	}
	if pending := queue.Stats().Pending; pending != 0 {
		t.Fatalf("a paused write reached the queue (%d pending)", pending)
	}
}

// asAPIError is a local helper to keep the test readable.
func asAPIError(err error, target **APIError) bool {
	for err != nil {
		if apiErr, ok := err.(*APIError); ok {
			*target = apiErr
			return true
		}
		unwrapped, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = unwrapped.Unwrap()
	}
	return false
}
