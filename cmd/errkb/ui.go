//go:build windows

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

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

	host := ui.NewHost(ui.HostOptions{Start: func(ctx context.Context) (ui.Service, error) {
		daemon, err := service.StartDaemon(ctx, config)
		if err != nil {
			return nil, err
		}
		return daemonService{daemon: daemon}, nil
	}})

	// A failed start must not close the tray: the icon is exactly how the user
	// learns capture is unavailable (acceptance 5 and 7).
	if err := host.Start(context.Background()); err != nil {
		fmt.Fprintf(os.Stderr, "errkb ui: capture did not start: %v\n", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	trayErr := ui.RunTray(ctx, host, *interval)

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
}

// Health probes PostgreSQL and the ledger on every tray tick; the tray never
// shows a cached green (status.go).
func (s daemonService) Health(ctx context.Context) service.Health {
	return s.daemon.Health(ctx)
}

// Shutdown drains in-flight requests, closes the pool and releases the instance
// mutex, so a paused tray leaves nothing running behind the icon.
func (s daemonService) Shutdown(ctx context.Context) error {
	return s.daemon.Shutdown(ctx)
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
