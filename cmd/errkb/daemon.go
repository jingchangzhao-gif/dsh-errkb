//go:build windows

package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/service"
)

// runDaemon implements the `errkb daemon` subcommand: flag parsing, the signal
// handler and the resident lifecycle all live here, the mechanics live in
// internal/service. The dispatch from main() lands with EKB-030, which owns the
// subcommand table.
func runDaemon(args []string) int {
	flags := flag.NewFlagSet("errkb daemon", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	dsn := flags.String("dsn", os.Getenv("ERRKB_DSN"), "PostgreSQL connection string (default $ERRKB_DSN)")
	migrations := flags.String("migrations", "db/migrations", "directory that holds the db/migrations files")
	lockName := flags.String("lock-name", service.DefaultLockName, "Windows named mutex that enforces a single instance")
	pipeName := flags.String("pipe", service.DefaultPipeName, "local Named Pipe the API listens on")
	healthPath := flags.String("health-file", daemonDataPath("health.json"), "machine-readable health document")
	logPath := flags.String("log-file", daemonDataPath("logs", "daemon.log"), "redacted, size-capped log file (never stdout)")
	logMax := flags.Int64("log-max-bytes", service.DefaultLogMaxBytes, "rotate the log after this many bytes")
	healthInterval := flags.Duration("health-interval", service.DefaultHealthInterval, "how often the health document is refreshed")
	if err := flags.Parse(args); err != nil {
		return 2
	}

	config := service.DaemonConfig{
		DSN:            *dsn,
		MigrationsDir:  *migrations,
		LockName:       *lockName,
		PipeName:       *pipeName,
		HealthPath:     *healthPath,
		LogPath:        *logPath,
		LogMaxBytes:    *logMax,
		HealthInterval: *healthInterval,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	daemon, err := service.StartDaemon(ctx, config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "errkb daemon: %v\n", err)
		return 1
	}
	if err := daemon.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "errkb daemon: %v\n", err)
		return 1
	}
	return 0
}

// daemonDataPath keeps the daemon files under the local application data
// directory, next to the health document and the logs.
func daemonDataPath(parts ...string) string {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		if dir, err := os.UserConfigDir(); err == nil {
			base = dir
		}
	}
	if base == "" {
		base = "."
	}
	return filepath.Join(append([]string{base, "errkb"}, parts...)...)
}
