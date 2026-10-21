//go:build windows

package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/service"
)

// runPG manages the local PostgreSQL cluster. The logic lives in
// service.PGManager; the installer and the logon autostart script call this
// command so there is exactly one implementation of the lifecycle.
func runPG(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		pgUsage(stderr)
		return exitUsage
	}
	action := args[0]
	switch action {
	case "install", "start", "stop", "uninstall", "status", "discover":
	case "help", "--help", "-h":
		pgUsage(stdout)
		return exitOK
	default:
		fmt.Fprintf(stderr, "errkb pg: unknown action %q\n", action)
		pgUsage(stderr)
		return exitUsage
	}

	flags := flag.NewFlagSet("pg "+action, flag.ContinueOnError)
	flags.SetOutput(stderr)
	dataDir := flags.String("data-dir", defaultPGDataDir(), "PostgreSQL data directory")
	port := flags.Int("port", service.DefaultPGPort, "cluster port")
	database := flags.String("database", service.DefaultPGDatabase, "application database")
	superuser := flags.String("superuser", service.DefaultPGSuperuser, "superuser role")
	migrations := flags.String("migrations", defaultPGMigrationsDir(), "migration directory for a fresh install")
	deleteData := flags.Bool("delete-data", false, "delete the cluster data during stop or uninstall")
	if err := flags.Parse(args[1:]); err != nil {
		return exitUsage
	}

	absoluteDataDir, err := filepath.Abs(*dataDir)
	if err != nil {
		fmt.Fprintf(stderr, "errkb pg: resolve the data directory: %v\n", err)
		return exitInternal
	}
	root := filepath.Dir(absoluteDataDir)
	instance := service.Instance{
		Name:            "errkb",
		DataDir:         absoluteDataDir,
		Port:            *port,
		Database:        *database,
		Superuser:       *superuser,
		CredentialsFile: filepath.Join(root, "credentials.json"),
	}
	if err := instance.Validate(); err != nil {
		fmt.Fprintf(stderr, "errkb pg: %v\n", err)
		return exitUsage
	}

	paths, err := service.ResolvePostgresBinaries()
	if err != nil {
		fmt.Fprintf(stderr, "errkb pg: %v\n", err)
		return exitInternal
	}
	manager := service.NewPGManager(paths)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	switch action {
	case "install":
		absoluteMigrations, err := filepath.Abs(*migrations)
		if err != nil {
			fmt.Fprintf(stderr, "errkb pg: resolve the migrations directory: %v\n", err)
			return exitInternal
		}
		if info, err := os.Stat(absoluteMigrations); err != nil || !info.IsDir() {
			fmt.Fprintf(stderr, "errkb pg: migrations directory %q is not a directory\n", *migrations)
			return exitUsage
		}
		credentials, err := manager.Install(ctx, instance, service.InstallOptions{MigrationsDir: absoluteMigrations})
		if err != nil {
			fmt.Fprintf(stderr, "errkb pg: install: %v\n", err)
			return exitInternal
		}
		fmt.Fprintf(stdout, "installed: %s\n", credentials.Redacted())
		fmt.Fprintf(stdout, "data directory: %s\n", instance.DataDir)
		fmt.Fprintf(stdout, "credentials: %s\n", instance.CredentialsFile)
		return exitOK
	case "start":
		if err := manager.Start(ctx, instance); err != nil {
			fmt.Fprintf(stderr, "errkb pg: start: %v\n", err)
			return exitInternal
		}
		fmt.Fprintf(stdout, "started: %s\n", instance.DataDir)
		return exitOK
	case "stop":
		if err := manager.Stop(ctx, instance, *deleteData); err != nil {
			fmt.Fprintf(stderr, "errkb pg: stop: %v\n", err)
			return exitInternal
		}
		if *deleteData {
			fmt.Fprintf(stdout, "stopped and deleted: %s\n", instance.DataDir)
		} else {
			fmt.Fprintf(stdout, "stopped: %s\n", instance.DataDir)
		}
		return exitOK
	case "uninstall":
		report, err := manager.Uninstall(ctx, instance, *deleteData)
		if err != nil {
			fmt.Fprintf(stderr, "errkb pg: uninstall: %v\n", err)
			return exitInternal
		}
		fmt.Fprintf(stdout, "stopped: %t\n", report.Stopped)
		fmt.Fprintf(stdout, "credentials removed: %t\n", report.Credentials)
		fmt.Fprintf(stdout, "data removed: %t\n", report.DataRemoved)
		fmt.Fprintf(stdout, "port free: %t\n", report.PortFree)
		return exitOK
	case "status":
		health := manager.Health(ctx, instance)
		fmt.Fprintf(stdout, "data directory: %s\n", instance.DataDir)
		fmt.Fprintf(stdout, "credentials: %s\n", instance.CredentialsFile)
		fmt.Fprintf(stdout, "port: %d\n", instance.Port)
		fmt.Fprintf(stdout, "reachable: %t\n", health.Reachable)
		if health.Version != "" {
			fmt.Fprintf(stdout, "server: %s\n", health.Version)
		}
		if health.LedgerVersion != "" {
			fmt.Fprintf(stdout, "schema: %s\n", health.LedgerVersion)
		}
		if health.Error != "" {
			fmt.Fprintf(stdout, "error: %s\n", health.Error)
		}
		if !health.Reachable {
			return exitInternal
		}
		return exitOK
	case "discover":
		conflict, err := manager.Discover(ctx, instance)
		if err != nil {
			fmt.Fprintf(stderr, "errkb pg: discover: %v\n", err)
			return exitInternal
		}
		fmt.Fprintf(stdout, "port %d in use: %t\n", instance.Port, conflict.PortInUse)
		fmt.Fprintf(stdout, "data directory present: %t\n", conflict.DataDirPresent)
		if len(conflict.Services) > 0 {
			fmt.Fprintf(stdout, "services: %s\n", strings.Join(conflict.Services, ", "))
		}
		if len(conflict.Installations) > 0 {
			fmt.Fprintf(stdout, "installations: %s\n", strings.Join(conflict.Installations, ", "))
		}
		if conflict.Usable() {
			return exitOK
		}
		return exitInternal
	}
	return exitInternal
}

func pgUsage(w io.Writer) {
	fmt.Fprint(w, `Usage: errkb pg <action> [flags]

Actions:
  install     create a cluster, start it, create the database and migrate
  start       start the cluster
  stop        stop the cluster (--delete-data removes the data)
  uninstall   stop the cluster and remove credentials (--delete-data removes data)
  status      print the cluster reachability and schema version
  discover    report port, data-directory and service conflicts

Flags:
  --data-dir      PostgreSQL data directory (default: <exe dir>\pg\data)
  --port          cluster port (default 5433)
  --database      application database (default errkb)
  --superuser     superuser role (default postgres)
  --migrations    migrations directory for install (default: <exe dir>\migrations)
  --delete-data   remove the cluster data during stop or uninstall
`)
}

func defaultPGDataDir() string {
	executable, err := os.Executable()
	if err != nil {
		return filepath.Join("pg", "data")
	}
	return filepath.Join(filepath.Dir(executable), "pg", "data")
}

func defaultPGMigrationsDir() string {
	executable, err := os.Executable()
	if err != nil {
		return "migrations"
	}
	return filepath.Join(filepath.Dir(executable), "migrations")
}
