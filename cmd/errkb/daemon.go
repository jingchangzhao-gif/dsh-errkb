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
	migrations := flags.String("migrations", "", "directory that holds the db/migrations files (default: migrations next to errkb.exe, then db/migrations)")
	lockName := flags.String("lock-name", service.DefaultLockName, "Windows named mutex that enforces a single instance")
	pipeName := flags.String("pipe", service.DefaultPipeName, "local Named Pipe the API listens on")
	healthPath := flags.String("health-file", daemonDataPath("health.json"), "machine-readable health document")
	logPath := flags.String("log-file", daemonDataPath("logs", "daemon.log"), "redacted, size-capped log file (never stdout)")
	logMax := flags.Int64("log-max-bytes", service.DefaultLogMaxBytes, "rotate the log after this many bytes")
	healthInterval := flags.Duration("health-interval", service.DefaultHealthInterval, "how often the health document is refreshed")
	configPath := flags.String("config", "", "settings file (default: config.yaml next to errkb.exe)")
	queueDir := flags.String("queue-dir", daemonDataPath("queue"), "offline spool for already-redacted events; empty disables it")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if *configPath == "" {
		*configPath = defaultSettingsPath()
	}

	resolvedDSN, resolvedMigrations, err := resolveDaemonPaths(*dsn, "", *migrations)
	if err != nil {
		fmt.Fprintf(os.Stderr, "errkb daemon: %v\n", err)
		return exitUsage
	}
	if resolvedDSN == "" {
		fmt.Fprintln(os.Stderr, "errkb daemon: no database: pass --dsn or install the product (pg\\credentials.json next to errkb.exe)")
		return exitUsage
	}

	config := service.DaemonConfig{
		DSN:            resolvedDSN,
		MigrationsDir:  resolvedMigrations,
		LockName:       *lockName,
		PipeName:       *pipeName,
		HealthPath:     *healthPath,
		LogPath:        *logPath,
		LogMaxBytes:    *logMax,
		HealthInterval: *healthInterval,
		ConfigPath:     *configPath,
		QueueDir:       *queueDir,
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

// defaultSettingsPath returns the config.yaml next to errkb.exe when the
// installed product has one; an empty result means "use the defaults", so a
// source-tree daemon keeps running without a settings file.
func defaultSettingsPath() string {
	executable, err := os.Executable()
	if err != nil {
		return ""
	}
	candidate := filepath.Join(filepath.Dir(executable), "config.yaml")
	if _, statErr := os.Stat(candidate); statErr != nil {
		return ""
	}
	return candidate
}
