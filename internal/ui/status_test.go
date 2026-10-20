//go:build windows

package ui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/service"
)

// fakeService is a Service whose health document the test controls.
type fakeService struct {
	health      service.Health
	shutdownErr error
	shutdowns   int
}

func (f *fakeService) Health(context.Context) service.Health { return f.health }

func (f *fakeService) Shutdown(context.Context) error {
	f.shutdowns++
	return f.shutdownErr
}

func healthyDocument() service.Health {
	return service.Health{
		Version:   service.HealthVersion,
		Status:    "ok",
		CheckedAt: time.Now().UTC(),
		Database:  service.DatabaseHealth{Connected: true},
		Migrations: service.MigrationHealth{
			Current:  "0007",
			Expected: "0007",
			OK:       true,
		},
		Connections: service.ConnectionHealth{Total: 2, Idle: 1},
	}
}

// startedHost returns a host whose capture service is a fresh fake.
func startedHost(t *testing.T, health service.Health) (*Host, *fakeService) {
	t.Helper()
	svc := &fakeService{health: health}
	host := NewHost(HostOptions{Start: func(context.Context) (Service, error) {
		return svc, nil
	}})
	if err := host.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	return host, svc
}

func TestSnapshotRunning(t *testing.T) {
	host, _ := startedHost(t, healthyDocument())
	snapshot := host.Snapshot(context.Background())
	if snapshot.State != StateRunning {
		t.Fatalf("state = %q, want %q", snapshot.State, StateRunning)
	}
	if snapshot.Title != "errkb: running" {
		t.Fatalf("title = %q", snapshot.Title)
	}
	if !strings.Contains(snapshot.Detail, "queue 0") {
		t.Fatalf("detail = %q, want the queue count", snapshot.Detail)
	}
	if snapshot.CheckedAt.IsZero() {
		t.Fatal("checked_at is zero")
	}
}

func TestSnapshotPausedStopsTheService(t *testing.T) {
	host, svc := startedHost(t, healthyDocument())
	if err := host.Pause(context.Background()); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if svc.shutdowns != 1 {
		t.Fatalf("shutdowns = %d, want 1", svc.shutdowns)
	}
	if !host.Paused() {
		t.Fatal("paused = false, want true")
	}
	snapshot := host.Snapshot(context.Background())
	if snapshot.State != StatePaused {
		t.Fatalf("state = %q, want %q", snapshot.State, StatePaused)
	}
	if !strings.Contains(snapshot.Detail, "nothing is written or queued") {
		t.Fatalf("detail = %q, want the pause explanation", snapshot.Detail)
	}
}

func TestSnapshotResumeRestarts(t *testing.T) {
	host, _ := startedHost(t, healthyDocument())
	if err := host.Pause(context.Background()); err != nil {
		t.Fatalf("pause: %v", err)
	}
	if err := host.Resume(context.Background()); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if host.Paused() {
		t.Fatal("paused = true after resume")
	}
	if state := host.Snapshot(context.Background()).State; state != StateRunning {
		t.Fatalf("state = %q after resume, want running", state)
	}
}

func TestSnapshotDatabaseFaultExplainsItself(t *testing.T) {
	health := healthyDocument()
	health.Database.Connected = false
	health.Database.Error = "dial tcp 127.0.0.1:5432: connect: connection refused"
	health.Status = "degraded"
	host, _ := startedHost(t, health)
	snapshot := host.Snapshot(context.Background())
	if snapshot.State != StateDatabaseFault {
		t.Fatalf("state = %q, want %q", snapshot.State, StateDatabaseFault)
	}
	if !strings.Contains(snapshot.Detail, "connection refused") {
		t.Fatalf("detail = %q, want the measured reason", snapshot.Detail)
	}
}

func TestSnapshotStartFailureShowsTheReason(t *testing.T) {
	host := NewHost(HostOptions{Start: func(context.Context) (Service, error) {
		return nil, errors.New("schema migration check failed: applied \"0006\"")
	}})
	if err := host.Start(context.Background()); err == nil {
		t.Fatal("start returned nil, want the failure")
	}
	snapshot := host.Snapshot(context.Background())
	if snapshot.State != StateDatabaseFault {
		t.Fatalf("state = %q, want %q", snapshot.State, StateDatabaseFault)
	}
	if !strings.Contains(snapshot.Detail, "migration") {
		t.Fatalf("detail = %q, want the start error", snapshot.Detail)
	}
}

func TestSnapshotQueueBacklog(t *testing.T) {
	health := healthyDocument()
	health.Queue.Backlog = 4
	host, _ := startedHost(t, health)
	snapshot := host.Snapshot(context.Background())
	if snapshot.State != StateQueueBacklog {
		t.Fatalf("state = %q, want %q", snapshot.State, StateQueueBacklog)
	}
	if !strings.Contains(snapshot.Detail, "4 event(s)") {
		t.Fatalf("detail = %q", snapshot.Detail)
	}
}

func TestSnapshotProbePanicBecomesFault(t *testing.T) {
	host := NewHost(HostOptions{
		Start: func(context.Context) (Service, error) { return &fakeService{}, nil },
		Probe: func(context.Context) (service.Health, error) { panic("boom") },
	})
	if err := host.Start(context.Background()); err != nil {
		t.Fatalf("start: %v", err)
	}
	snapshot := host.Snapshot(context.Background())
	if snapshot.State != StateDatabaseFault {
		t.Fatalf("state = %q, want %q", snapshot.State, StateDatabaseFault)
	}
	if !strings.Contains(snapshot.Detail, "panicked") {
		t.Fatalf("detail = %q, want the panic report", snapshot.Detail)
	}
}

func TestExitStopsCaptureForGood(t *testing.T) {
	host, svc := startedHost(t, healthyDocument())
	if err := host.Exit(context.Background()); err != nil {
		t.Fatalf("exit: %v", err)
	}
	if svc.shutdowns != 1 {
		t.Fatalf("shutdowns = %d, want 1", svc.shutdowns)
	}
	if !host.Paused() {
		t.Fatal("paused = false after exit")
	}
	if err := host.Resume(context.Background()); err != nil {
		t.Fatalf("resume after exit: %v", err)
	}
}

func TestControllerInterface(t *testing.T) {
	var _ Controller = NewHost(HostOptions{})
}

func TestSnapshotNeverCaches(t *testing.T) {
	health := healthyDocument()
	host, svc := startedHost(t, health)
	first := host.Snapshot(context.Background())
	svc.health = health
	svc.health.Database.Connected = false
	svc.health.Database.Error = "the second probe failed"
	second := host.Snapshot(context.Background())
	if first.State != StateRunning || second.State != StateDatabaseFault {
		t.Fatalf("states = %q then %q, want running then database-fault", first.State, second.State)
	}
}
