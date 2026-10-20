//go:build windows

package ui

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/service"
)

// State is the coarse capture condition the tray shows. The P6 row promises
// exactly four: running, paused, database fault and queue backlog.
type State string

const (
	// StateRunning means capture is on and the database answers.
	StateRunning State = "running"
	// StatePaused means the user paused capture; nothing is written or queued.
	StatePaused State = "paused"
	// StateDatabaseFault means capture cannot reach its database or the service
	// failed to start.
	StateDatabaseFault State = "database-fault"
	// StateQueueBacklog means capture works but events are waiting to replay.
	StateQueueBacklog State = "queue-backlog"
)

// Snapshot is one measurement rendered for the tray. Health is the EKB-028
// health document, the same struct EKB-034 diagnosis embeds, so the tray and
// `errkb doctor` can never disagree about a state.
type Snapshot struct {
	State     State
	Title     string
	Detail    string
	Health    service.Health
	CheckedAt time.Time
}

// Service is the part of the daemon the tray owns. *service.Daemon satisfies it
// through the adapter in cmd/errkb/ui.go.
type Service interface {
	Health(ctx context.Context) service.Health
	Shutdown(ctx context.Context) error
}

// Starter starts capture. Pause stops the service (that is what really stops
// writes and queue growth); Resume calls the starter again.
type Starter func(ctx context.Context) (Service, error)

// Probe replaces the daemon measurement in tests; production leaves it nil and
// uses Service.Health, which probes PostgreSQL on every call.
type Probe func(ctx context.Context) (service.Health, error)

// HostOptions configures a tray host.
type HostOptions struct {
	// Start creates and starts the capture service.
	Start Starter
	// Probe is a test seam; nil means Service.Health.
	Probe Probe
	// Now is a test seam; nil means time.Now.
	Now func() time.Time
}

// Controller is everything the tray needs from the host.
type Controller interface {
	Snapshot(ctx context.Context) Snapshot
	Pause(ctx context.Context) error
	Resume(ctx context.Context) error
	Exit(ctx context.Context) error
	Paused() bool
}

// Host owns the capture service the tray drives. Pause shuts the service down
// gracefully (EKB-028): in-flight requests drain, the pool and the instance
// mutex are released, no half-written transaction survives. Resume starts a new
// instance with the same configuration.
type Host struct {
	mu     sync.Mutex
	opts   HostOptions
	svc    Service
	paused bool
	fault  string
}

// NewHost builds an empty host. Call Start before showing the tray.
func NewHost(options HostOptions) *Host {
	if options.Now == nil {
		options.Now = time.Now
	}
	return &Host{opts: options}
}

// Start brings capture up. A failure is recorded and returned; the tray keeps
// running and shows the reason instead of exiting.
func (h *Host) Start(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.paused {
		return nil
	}
	svc, err := h.startLocked(ctx)
	if err != nil {
		h.fault = err.Error()
		return err
	}
	h.svc = svc
	h.fault = ""
	return nil
}

func (h *Host) startLocked(ctx context.Context) (Service, error) {
	if h.opts.Start == nil {
		return nil, fmt.Errorf("errkb ui: no capture service configured")
	}
	return h.opts.Start(ctx)
}

// Pause stops capture. The returned error is the shutdown error; the service is
// gone either way, so the tray never claims capture is on when it is not.
func (h *Host) Pause(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.paused = true
	if h.svc == nil {
		return nil
	}
	err := h.svc.Shutdown(ctx)
	h.svc = nil
	if err != nil {
		h.fault = err.Error()
		return err
	}
	h.fault = ""
	return nil
}

// Resume starts capture again after a pause or a fault.
func (h *Host) Resume(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.paused = false
	svc, err := h.startLocked(ctx)
	if err != nil {
		h.fault = err.Error()
		return err
	}
	h.svc = svc
	h.fault = ""
	return nil
}

// Exit is Pause for the process: capture stops for good and the tray closes.
func (h *Host) Exit(ctx context.Context) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.paused = true
	if h.svc == nil {
		return nil
	}
	err := h.svc.Shutdown(ctx)
	h.svc = nil
	return err
}

// Paused reports the user's pause intent.
func (h *Host) Paused() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.paused
}

// Snapshot measures the service now. It never returns a cached measurement: the
// tray exists to answer "can I use it right now", and a stale green icon is
// worse than no icon.
func (h *Host) Snapshot(ctx context.Context) Snapshot {
	h.mu.Lock()
	svc, paused, fault, now := h.svc, h.paused, h.fault, h.opts.Now
	probe := h.opts.Probe
	h.mu.Unlock()

	snapshot := Snapshot{CheckedAt: now().UTC()}
	switch {
	case paused:
		snapshot.State = StatePaused
		snapshot.Title = "errkb: capture paused"
		snapshot.Detail = "nothing is written or queued while paused; resume from the tray menu"
		return snapshot
	case svc == nil:
		snapshot.State = StateDatabaseFault
		snapshot.Title = "errkb: capture unavailable"
		snapshot.Detail = fault
		if snapshot.Detail == "" {
			snapshot.Detail = "the capture service is not running"
		}
		return snapshot
	}

	health, err := probeHealth(ctx, svc, probe)
	snapshot.Health = health
	if err != nil {
		snapshot.State = StateDatabaseFault
		snapshot.Title = "errkb: database unavailable"
		snapshot.Detail = err.Error()
		return snapshot
	}

	snapshot.State = classify(health)
	snapshot.Title, snapshot.Detail = describe(snapshot.State, health, fault)
	return snapshot
}

// probeHealth runs one real measurement and converts a panicking probe into an
// error: a broken UI measurement must never take the daemon down.
func probeHealth(ctx context.Context, svc Service, probe Probe) (health service.Health, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("errkb ui: the status probe panicked: %v", recovered)
		}
	}()
	if probe != nil {
		return probe(ctx)
	}
	return svc.Health(ctx), nil
}

// classify turns one health document into a state. It encodes no health rules
// of its own beyond the priority order; the verdict comes from EKB-028.
func classify(health service.Health) State {
	switch {
	case !health.Database.Connected:
		return StateDatabaseFault
	case health.Queue.Backlog > 0:
		return StateQueueBacklog
	default:
		return StateRunning
	}
}

// describe renders the tray title and one readable line (acceptance 5: a fault
// shows its reason, not just a red dot).
func describe(state State, health service.Health, fault string) (title, detail string) {
	switch state {
	case StateDatabaseFault:
		reason := health.Database.Error
		if reason == "" {
			reason = fault
		}
		if reason == "" {
			reason = "the database did not answer"
		}
		return "errkb: database unavailable", reason
	case StateQueueBacklog:
		detail = fmt.Sprintf("%d event(s) waiting to replay; they stay on disk until the database answers", health.Queue.Backlog)
		if health.Queue.Error != "" {
			detail += " (" + health.Queue.Error + ")"
		}
		return "errkb: queue backlog", detail
	default:
		detail = fmt.Sprintf("capturing failures · %d connection(s) · %d in flight · queue %d",
			health.Connections.Total, health.Requests.InFlight, health.Queue.Backlog)
		if !health.Migrations.OK {
			detail += " · schema is not at the expected migration"
		}
		return "errkb: running", detail
	}
}
