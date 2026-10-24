//go:build windows

// Package service runs the errkb daemon: one instance per machine user, a
// graceful drain on stop, a machine-readable health document and size-capped,
// redacted logs.
//
// The single-instance rule is a Windows named mutex, not a PID file: the kernel
// releases the mutex when the process dies, so a crashed daemon can always be
// started again, while a PID file would go stale and block startup until someone
// deleted it by hand. Logs are redacted through internal/redact before they are
// written (§6) and never touch stdout: stdout belongs to the MCP stdio channel
// (EKB-037).
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/contract"
	"github.com/jingchangzhao-gif/error-knowledge-base/internal/database"
	"github.com/jingchangzhao-gif/error-knowledge-base/internal/redact"
)

const (
	// DefaultLockName is the named mutex that keeps the daemon single-instance.
	DefaultLockName = `Local\errkb-daemon`
	// DefaultLogMaxBytes is the size after which the log rotates once.
	DefaultLogMaxBytes int64 = 4 << 20
	// DefaultHealthInterval is how often the health document is refreshed.
	DefaultHealthInterval = 30 * time.Second
	// HealthVersion is the schema version of the health document.
	HealthVersion = 1

	healthOK       = "ok"
	healthDegraded = "degraded"
)

var (
	// ErrAlreadyRunning means another daemon instance holds the instance mutex.
	ErrAlreadyRunning = errors.New("another errkb daemon instance is already running")
	// ErrShuttingDown means the daemon no longer accepts new work.
	ErrShuttingDown = errors.New("the errkb daemon is shutting down")
)

// DaemonConfig is everything the daemon needs to start.
type DaemonConfig struct {
	// DSN is the PostgreSQL connection string. Ignored when Pool is set.
	DSN string
	// Pool is an optional pre-built connection pool; when set the daemon does
	// not own it and leaves it open on shutdown.
	Pool *pgxpool.Pool
	// MigrationsDir holds the db/migrations files; the daemon checks the applied
	// ledger against the last file before it starts serving.
	MigrationsDir string
	// LockName is the Windows named mutex name. Empty means DefaultLockName.
	LockName string
	// HealthPath is where the machine-readable health document is written.
	HealthPath string
	// LogPath is the redacted, size-capped log file. Logs never go to stdout.
	LogPath string
	// LogMaxBytes caps one log file; one rotated backup is kept.
	LogMaxBytes int64
	// HealthInterval is how often Run refreshes the health document.
	HealthInterval time.Duration
	// PipeName is the local Named Pipe the API listens on. Empty means
	// DefaultPipeName.
	PipeName string
	// ConfigPath is an optional YAML settings file. When it exists the daemon
	// enforces its capture gate (global pause, paused connectors/projects).
	ConfigPath string
	// QueueDir is the offline spool directory. Empty disables queueing.
	QueueDir string
	// QueueBacklog reports the offline spool backlog; nil falls back to the
	// internal queue opened from QueueDir.
	QueueBacklog func() (int, error)
}

func (cfg DaemonConfig) normalize() (DaemonConfig, error) {
	if cfg.Pool == nil && cfg.DSN == "" {
		return cfg, errors.New("errkb daemon: a DSN or a connection pool is required")
	}
	if cfg.MigrationsDir == "" {
		cfg.MigrationsDir = "db/migrations"
	}
	if cfg.LockName == "" {
		cfg.LockName = DefaultLockName
	}
	if cfg.LogMaxBytes <= 0 {
		cfg.LogMaxBytes = DefaultLogMaxBytes
	}
	if cfg.HealthInterval <= 0 {
		cfg.HealthInterval = DefaultHealthInterval
	}
	if cfg.HealthPath == "" {
		cfg.HealthPath = defaultHealthPath()
	}
	if cfg.LogPath == "" {
		cfg.LogPath = defaultLogPath()
	}
	if cfg.QueueBacklog == nil {
		cfg.QueueBacklog = func() (int, error) { return 0, nil }
	}
	if cfg.PipeName == "" {
		cfg.PipeName = DefaultPipeName
	}
	return cfg, nil
}

// Health is the machine-readable daemon state. It is written atomically to
// DaemonConfig.HealthPath so an external tool only ever sees whole documents.
type Health struct {
	Version     int              `json:"version"`
	Status      string           `json:"status"`
	CheckedAt   time.Time        `json:"checked_at"`
	Database    DatabaseHealth   `json:"database"`
	Migrations  MigrationHealth  `json:"migrations"`
	Queue       QueueHealth      `json:"queue"`
	Connections ConnectionHealth `json:"connections"`
	Requests    RequestHealth    `json:"requests"`
}

// DatabaseHealth reports whether the pool can reach PostgreSQL.
type DatabaseHealth struct {
	Connected bool   `json:"connected"`
	Error     string `json:"error,omitempty"`
}

// MigrationHealth compares the applied ledger with the expected last migration.
type MigrationHealth struct {
	Current  string `json:"current"`
	Expected string `json:"expected"`
	OK       bool   `json:"ok"`
	Error    string `json:"error,omitempty"`
}

// QueueHealth reports the offline spool backlog (EKB-033 owns the real queue).
type QueueHealth struct {
	Backlog int    `json:"backlog"`
	Error   string `json:"error,omitempty"`
}

// ConnectionHealth reports the current pool state.
type ConnectionHealth struct {
	Total int32 `json:"total"`
	InUse int32 `json:"in_use"`
	Idle  int32 `json:"idle"`
}

// RequestHealth reports how many tracked requests are still in flight.
type RequestHealth struct {
	InFlight int `json:"in_flight"`
}

// Daemon is a running errkb service instance.
type Daemon struct {
	cfg      DaemonConfig
	pool     *pgxpool.Pool
	ownsPool bool
	lock     *instanceLock
	log      *daemonLog
	pipe     *PipeServer
	queue    *Queue
	cancelQ  context.CancelFunc

	healthMu sync.Mutex

	mu           sync.Mutex
	closing      bool
	inFlight     int
	drained      chan struct{}
	shutdownOnce sync.Once
	shutdownErr  error
}

// StartDaemon takes the instance mutex, opens the log, connects the pool and
// verifies the schema migration ledger. A stale or missing ledger fails the
// start: the daemon never serves from a schema it does not know.
func StartDaemon(ctx context.Context, cfg DaemonConfig) (*Daemon, error) {
	cfg, err := cfg.normalize()
	if err != nil {
		return nil, err
	}

	lock, err := acquireInstanceLock(cfg.LockName)
	if err != nil {
		return nil, err
	}

	log, err := openDaemonLog(cfg.LogPath, cfg.LogMaxBytes)
	if err != nil {
		lock.Release()
		return nil, fmt.Errorf("errkb daemon: open log %q: %w", cfg.LogPath, err)
	}

	pool := cfg.Pool
	ownsPool := false
	if pool == nil {
		pool, err = pgxpool.New(ctx, cfg.DSN)
		if err != nil {
			log.Close()
			lock.Release()
			return nil, fmt.Errorf("errkb daemon: open database pool: %w", err)
		}
		ownsPool = true
	}

	daemon := &Daemon{
		cfg:      cfg,
		pool:     pool,
		ownsPool: ownsPool,
		lock:     lock,
		log:      log,
		drained:  make(chan struct{}),
	}

	current, expected, err := daemon.migrationState(ctx)
	if err != nil {
		daemon.releaseResources()
		return nil, fmt.Errorf("errkb daemon: schema migration check failed: %w", err)
	}
	if current == "" || current != expected {
		daemon.releaseResources()
		return nil, fmt.Errorf("errkb daemon: schema migration check failed: applied version %q, expected %q (run `errkb db migrate` first)", current, expected)
	}

	// Capture settings and the offline queue live with the single writer
	// (EKB-033 + EKB-047): the gate refuses paused writes before they reach the
	// database, and unreachable writes spool their already-redacted events.
	backend := NewSQLBackend(pool)
	if cfg.ConfigPath != "" {
		settings, readErr := os.ReadFile(cfg.ConfigPath)
		switch {
		case readErr == nil:
			config, loadErr := contract.LoadConfig(settings)
			if loadErr != nil {
				daemon.releaseResources()
				return nil, fmt.Errorf("errkb daemon: load settings %q: %w", cfg.ConfigPath, loadErr)
			}
			backend.WithCaptureGate(CaptureGate(config))
		case !errors.Is(readErr, os.ErrNotExist):
			daemon.releaseResources()
			return nil, fmt.Errorf("errkb daemon: read settings %q: %w", cfg.ConfigPath, readErr)
		}
	}
	if cfg.QueueDir != "" {
		queue, queueErr := OpenQueue(QueueOptions{Dir: cfg.QueueDir})
		if queueErr != nil {
			daemon.releaseResources()
			return nil, fmt.Errorf("errkb daemon: open the offline queue: %w", queueErr)
		}
		queueCtx, cancelQueue := context.WithCancel(context.Background())
		daemon.queue = queue
		daemon.cancelQ = cancelQueue
		backend.WithQueue(queue)
		daemon.cfg.QueueBacklog = func() (int, error) { return int(queue.Stats().Pending), nil }
		go func() {
			runErr := queue.Run(queueCtx, QueueRunOptions{
				Recorder: func(ctx context.Context, event contract.ErrorEvent) (database.RecordResult, error) {
					conn, acquireErr := pool.Acquire(ctx)
					if acquireErr != nil {
						return database.RecordResult{}, acquireErr
					}
					defer conn.Release()
					return database.RecordEvent(ctx, conn.Conn(), event)
				},
				Available: func(ctx context.Context) error { return pool.Ping(ctx) },
			})
			if runErr != nil && !errors.Is(runErr, context.Canceled) {
				daemon.Log("warn", fmt.Sprintf("offline queue replay stopped: %v", runErr))
			}
		}()
	}

	if _, err := daemon.RefreshHealth(ctx); err != nil {
		daemon.Log("warn", fmt.Sprintf("write the initial health document: %v", err))
	}

	// The local API (EKB-029) serves the PostgreSQL backend (EKB-035). Until
	// this file called ListenPipe nothing listened on the default pipe, so the
	// CLI could never reach the database.
	pipe, err := ListenPipe(PipeOptions{
		Name:         cfg.PipeName,
		API:          NewAPI(backend),
		BeginRequest: daemon.BeginRequest,
	})
	if err != nil {
		daemon.releaseResources()
		return nil, fmt.Errorf("errkb daemon: listen on the local pipe: %w", err)
	}
	daemon.pipe = pipe
	go func() {
		if serveErr := pipe.Serve(ctx); serveErr != nil {
			daemon.Log("warn", fmt.Sprintf("local API stopped: %v", serveErr))
		}
	}()

	daemon.Log("info", fmt.Sprintf("daemon started (mutex %s, schema %s, pipe %s)", cfg.LockName, current, pipe.Name()))
	return daemon, nil
}

// releaseResources unwinds a failed start.
func (d *Daemon) releaseResources() {
	if d.cancelQ != nil {
		d.cancelQ()
	}
	if d.pipe != nil {
		_ = d.pipe.Close()
	}
	if d.ownsPool {
		d.pool.Close()
	}
	d.log.Close()
	d.lock.Release()
}

// Pool exposes the connection pool to the protocol layer (EKB-029).
func (d *Daemon) Pool() *pgxpool.Pool { return d.pool }

// LogPath is the file this daemon writes its redacted log to.
func (d *Daemon) LogPath() string { return d.cfg.LogPath }

// HealthPath is the file the machine-readable health document is written to.
func (d *Daemon) HealthPath() string { return d.cfg.HealthPath }

// Log writes one redacted, size-capped line. It never writes to stdout.
func (d *Daemon) Log(level, message string) {
	if d == nil || d.log == nil {
		return
	}
	_ = d.log.Log(level, message)
}

// BeginRequest registers one unit of in-flight work. The returned function must
// be called exactly once when the work ends. After Shutdown started it returns
// ErrShuttingDown, which is how the protocol layer stops accepting connections.
func (d *Daemon) BeginRequest() (func(), error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closing {
		return nil, ErrShuttingDown
	}
	d.inFlight++
	var once sync.Once
	return func() {
		once.Do(func() {
			d.mu.Lock()
			defer d.mu.Unlock()
			d.inFlight--
			if d.closing && d.inFlight == 0 {
				close(d.drained)
			}
		})
	}, nil
}

// InFlight reports how many tracked requests are running.
func (d *Daemon) InFlight() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.inFlight
}

// Health measures the daemon now. It never fails: every failure is recorded in
// the document and flips Status to "degraded", so health is always readable.
func (d *Daemon) Health(ctx context.Context) Health {
	h := Health{
		Version:   HealthVersion,
		Status:    healthOK,
		CheckedAt: time.Now().UTC(),
	}

	if err := d.pool.Ping(ctx); err != nil {
		h.Database.Error = err.Error()
		h.Status = healthDegraded
	} else {
		h.Database.Connected = true
	}

	current, expected, err := d.migrationState(ctx)
	h.Migrations.Current = current
	h.Migrations.Expected = expected
	if err != nil {
		h.Migrations.Error = err.Error()
		h.Status = healthDegraded
	} else if expected == "" || current != expected {
		h.Status = healthDegraded
	} else {
		h.Migrations.OK = true
	}

	if backlog, err := d.cfg.QueueBacklog(); err != nil {
		h.Queue.Error = err.Error()
		h.Status = healthDegraded
	} else {
		h.Queue.Backlog = backlog
	}

	stat := d.pool.Stat()
	h.Connections = ConnectionHealth{
		Total: stat.TotalConns(),
		InUse: stat.AcquiredConns(),
		Idle:  stat.IdleConns(),
	}
	h.Requests.InFlight = d.InFlight()
	return h
}

// RefreshHealth measures the daemon and writes the document atomically to
// HealthPath. An empty path only measures.
func (d *Daemon) RefreshHealth(ctx context.Context) (Health, error) {
	h := d.Health(ctx)
	if d.cfg.HealthPath == "" {
		return h, nil
	}
	d.healthMu.Lock()
	defer d.healthMu.Unlock()
	if err := writeHealthDocument(d.cfg.HealthPath, h); err != nil {
		return h, err
	}
	return h, nil
}

// migrationState reads the applied ledger and the expected last migration.
func (d *Daemon) migrationState(ctx context.Context) (current, expected string, err error) {
	migrations, err := database.LoadMigrations(d.cfg.MigrationsDir)
	if err != nil {
		return "", "", fmt.Errorf("load migrations from %q: %w", d.cfg.MigrationsDir, err)
	}
	if len(migrations) == 0 {
		return "", "", fmt.Errorf("no migrations found in %q", d.cfg.MigrationsDir)
	}
	expected = migrations[len(migrations)-1].Version
	if err := d.pool.QueryRow(ctx, `select coalesce(max(version), '') from schema_migrations`).Scan(&current); err != nil {
		return "", expected, fmt.Errorf("read the schema_migrations ledger: %w", err)
	}
	return current, expected, nil
}

// Run refreshes the health document until ctx is cancelled, then shuts the
// daemon down gracefully (in-flight requests first, then the pool and the lock).
func (d *Daemon) Run(ctx context.Context) error {
	ticker := time.NewTicker(d.cfg.HealthInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			return d.Shutdown(shutdownCtx)
		case <-ticker.C:
			if _, err := d.RefreshHealth(ctx); err != nil {
				d.Log("warn", fmt.Sprintf("refresh health: %v", err))
			}
		}
	}
}

// Shutdown stops accepting work, waits for in-flight requests (bounded by ctx),
// then closes the pool it owns, closes the log and releases the instance mutex.
// It must not use os.Exit: that would strand half-written transactions.
func (d *Daemon) Shutdown(ctx context.Context) error {
	d.shutdownOnce.Do(func() {
		d.mu.Lock()
		d.closing = true
		if d.inFlight == 0 {
			close(d.drained)
		}
		d.mu.Unlock()

		select {
		case <-d.drained:
		case <-ctx.Done():
			d.shutdownErr = fmt.Errorf("errkb daemon: %d request(s) still in flight: %w", d.InFlight(), ctx.Err())
		}

		d.Log("info", fmt.Sprintf("daemon stopping (%d in flight)", d.InFlight()))
		// Stop the replay before the pool closes; no new connection may slide in
		// between the drain and the pool close. In-flight requests are already
		// counted by BeginRequest and finish on the open connections.
		if d.cancelQ != nil {
			d.cancelQ()
		}
		if d.pipe != nil {
			_ = d.pipe.Close()
		}
		if d.ownsPool {
			d.pool.Close()
		}
		d.log.Close()
		d.lock.Release()
	})
	return d.shutdownErr
}

// writeHealthDocument writes the document through a temp file and a rename, so
// a reader never sees a partial JSON document.
func writeHealthDocument(path string, health Health) error {
	document, err := json.MarshalIndent(health, "", "  ")
	if err != nil {
		return fmt.Errorf("encode the health document: %w", err)
	}
	document = append(document, '\n')
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create the health directory %q: %w", dir, err)
	}
	temp, err := os.CreateTemp(dir, ".health-*.json")
	if err != nil {
		return fmt.Errorf("create the health temp file: %w", err)
	}
	tempName := temp.Name()
	defer func() {
		_ = os.Remove(tempName)
	}()
	if _, err := temp.Write(document); err != nil {
		temp.Close()
		return fmt.Errorf("write the health document: %w", err)
	}
	if err := temp.Close(); err != nil {
		return fmt.Errorf("close the health document: %w", err)
	}
	if err := os.Rename(tempName, path); err != nil {
		return fmt.Errorf("replace the health document: %w", err)
	}
	return nil
}

// defaultHealthPath and defaultLogPath keep the daemon files under the machine
// user's local application data.
func defaultDataDir() string {
	if dir := os.Getenv("LOCALAPPDATA"); dir != "" {
		return filepath.Join(dir, "errkb")
	}
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "errkb")
	}
	return "errkb"
}

func defaultHealthPath() string { return filepath.Join(defaultDataDir(), "health.json") }

func defaultLogPath() string { return filepath.Join(defaultDataDir(), "logs", "daemon.log") }

// instanceLock is a Windows named mutex held for the daemon's lifetime.
type instanceLock struct {
	handle syscall.Handle
}

var (
	kernel32         = syscall.NewLazyDLL("kernel32.dll")
	procCreateMutexW = kernel32.NewProc("CreateMutexW")
	procCloseHandle  = kernel32.NewProc("CloseHandle")
)

func acquireInstanceLock(name string) (*instanceLock, error) {
	if name == "" {
		name = DefaultLockName
	}
	namePtr, err := syscall.UTF16PtrFromString(name)
	if err != nil {
		return nil, fmt.Errorf("errkb daemon: invalid instance mutex name %q: %w", name, err)
	}
	handle, _, callErr := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(namePtr)))
	if handle == 0 {
		return nil, fmt.Errorf("errkb daemon: create the instance mutex %q: %w", name, callErr)
	}
	if callErr == syscall.ERROR_ALREADY_EXISTS {
		procCloseHandle.Call(handle)
		return nil, fmt.Errorf("%w (mutex %s)", ErrAlreadyRunning, name)
	}
	return &instanceLock{handle: syscall.Handle(handle)}, nil
}

func (l *instanceLock) Release() {
	if l == nil || l.handle == 0 {
		return
	}
	procCloseHandle.Call(uintptr(l.handle))
	l.handle = 0
}

// daemonLog is a redacted, size-capped file log with one rotated backup.
type daemonLog struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	file     *os.File
	size     int64
}

func openDaemonLog(path string, maxBytes int64) (*daemonLog, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create the log directory %q: %w", dir, err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open the log file: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("stat the log file: %w", err)
	}
	return &daemonLog{path: path, maxBytes: maxBytes, file: file, size: info.Size()}, nil
}

// Log writes one line. The redaction happens on the complete line before the
// write, so a credential can never reach the disk (§6).
func (l *daemonLog) Log(level, message string) error {
	if l == nil {
		return nil
	}
	message = strings.NewReplacer("\r", " ", "\n", " ").Replace(message)
	line := fmt.Sprintf("%s %-5s %s\n", time.Now().UTC().Format(time.RFC3339), strings.ToUpper(level), message)
	line = redact.Text(line, redact.Public)

	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return errors.New("the daemon log is closed")
	}
	if l.size+int64(len(line)) > l.maxBytes {
		if err := l.rotateLocked(); err != nil {
			return err
		}
	}
	written, err := l.file.WriteString(line)
	l.size += int64(written)
	return err
}

// rotateLocked renames the current file to <path>.1 (replacing an older backup)
// and starts an empty file, so the log never grows without bound.
func (l *daemonLog) rotateLocked() error {
	if err := l.file.Close(); err != nil {
		return fmt.Errorf("close the log before rotating: %w", err)
	}
	backup := l.path + ".1"
	if err := os.Remove(backup); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove the old log backup: %w", err)
	}
	if err := os.Rename(l.path, backup); err != nil {
		return fmt.Errorf("rotate the log: %w", err)
	}
	file, err := os.OpenFile(l.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return fmt.Errorf("reopen the log after rotating: %w", err)
	}
	l.file = file
	l.size = 0
	return nil
}

func (l *daemonLog) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	err := l.file.Close()
	l.file = nil
	return err
}
