//go:build windows

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jingchangzhao-gif/dsh-errkb/internal/service"
	"github.com/jingchangzhao-gif/dsh-errkb/internal/ui"
)

// uiShutdownTimeout bounds the final graceful stop when the tray closes.
const uiShutdownTimeout = 10 * time.Second

// runUI implements `errkb ui`: it hosts the capture service in this process,
// shows the tray, and refuses to exit before capture has stopped gracefully.
func runUI(args []string) int {
	flags := flag.NewFlagSet("errkb ui", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	dsn := flags.String("dsn", os.Getenv("ERRKB_DSN"), "PostgreSQL connection string (default $ERRKB_DSN)")
	migrations := flags.String("migrations", "db/migrations", "directory that holds the db/migrations files")
	lockName := flags.String("lock-name", service.DefaultLockName, "Windows named mutex that enforces a single instance")
	pipeName := flags.String("pipe", service.DefaultPipeName, "local Named Pipe the API listens on")
	healthPath := flags.String("health-file", daemonDataPath("health.json"), "machine-readable health document")
	logPath := flags.String("log-file", daemonDataPath("logs", "daemon.log"), "redacted, size-capped log file (never stdout)")
	queueDir := flags.String("queue-dir", daemonDataPath("queue"), "offline spool whose backlog the tray reports")
	interval := flags.Duration("interval", time.Second, "how often the tray re-measures the capture state")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}

	config := service.DaemonConfig{
		DSN:           *dsn,
		MigrationsDir: *migrations,
		LockName:      *lockName,
		PipeName:      *pipeName,
		HealthPath:    *healthPath,
		LogPath:       *logPath,
		QueueBacklog: func() (int, error) {
			return spoolBacklog(*queueDir)
		},
	}

	search := &searchBridge{}
	host := ui.NewHost(ui.HostOptions{Start: func(ctx context.Context) (ui.Service, error) {
		daemon, err := service.StartDaemon(ctx, config)
		if err != nil {
			search.clear()
			return nil, err
		}
		search.set(daemon.Pool())
		return daemonService{daemon: daemon, search: search}, nil
	}})

	// A failed start must not close the tray: the icon is exactly how the user
	// learns capture is unavailable (acceptance 5 and 7).
	if err := host.Start(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "errkb ui: capture did not start: %v\n", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	trayErr := ui.RunTray(ctx, host, ui.TrayOptions{Interval: *interval, Search: search.search})

	exitCtx, cancel := context.WithTimeout(context.Background(), uiShutdownTimeout)
	defer cancel()
	if shutdownErr := host.Exit(exitCtx); shutdownErr != nil {
		fmt.Fprintf(os.Stderr, "errkb ui: %v\n", shutdownErr)
		return 1
	}
	if trayErr != nil {
		fmt.Fprintf(os.Stderr, "errkb ui: %v\n", trayErr)
		return 1
	}
	return exitOK
}

// daemonService adapts the concrete daemon to the tray's Service interface.
type daemonService struct {
	daemon *service.Daemon
	search *searchBridge
}

// Health probes PostgreSQL and the ledger on every tray tick; the tray never
// shows a cached green (status.go).
func (s daemonService) Health(ctx context.Context) service.Health {
	return s.daemon.Health(ctx)
}

// Shutdown drains in-flight requests, closes the pool and releases the instance
// mutex, so a paused tray leaves nothing running behind the icon.
func (s daemonService) Shutdown(ctx context.Context) error {
	err := s.daemon.Shutdown(ctx)
	if s.search != nil {
		s.search.clear()
	}
	return err
}

// searchBridge owns the address bar's borrow of the daemon pool. It is nil
// between a failed start and the next resume, which turns into a readable
// "daemon is not running" answer instead of a panic.
type searchBridge struct {
	mu      sync.Mutex
	service *service.SearchService
}

func (b *searchBridge) set(pool *pgxpool.Pool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.service = service.NewSearchService(pool)
}

func (b *searchBridge) clear() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.service = nil
}

func (b *searchBridge) search(ctx context.Context, query string) (service.SearchResults, error) {
	b.mu.Lock()
	searcher := b.service
	b.mu.Unlock()
	if searcher == nil {
		return service.SearchResults{}, service.ErrSearchUnavailable
	}
	return searcher.Search(ctx, query)
}

// spoolBacklog counts the queued event files. It reads the directory instead of
// opening the queue because the tray process may look at a spool another
// process is writing.
func spoolBacklog(dir string) (int, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	backlog := 0
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		backlog++
	}
	return backlog, nil
}
