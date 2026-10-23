//go:build windows

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/service"
)

// runDoctor implements the `errkb doctor` subcommand. The checks themselves
// live in internal/service; this file only parses flags and renders the report
// as text or as the contract JSON the tray (P6) consumes.
func runDoctor(args []string, jsonMode bool, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("errkb doctor", flag.ContinueOnError)
	flags.SetOutput(stderr)
	dsn := flags.String("dsn", os.Getenv("ERRKB_DSN"), "PostgreSQL connection string (default $ERRKB_DSN); never printed")
	migrations := flags.String("migrations", "", "directory that holds the db/migrations files (default: migrations next to errkb.exe, then db/migrations)")
	configPath := flags.String("config", daemonDataPath("config.yml"), "optional configuration file (defaults apply when absent)")
	queueDir := flags.String("queue-dir", daemonDataPath("queue"), "offline spool directory")
	pipeName := flags.String("pipe", service.DefaultPipeName, "local API pipe path")
	timeout := flags.Duration("timeout", service.DefaultDoctorTimeout, "how long one network-style check may take")
	if err := flags.Parse(args); err != nil {
		return exitUsage
	}

	resolvedDSN, resolvedMigrations, err := resolveDaemonPaths(*dsn, "", *migrations)
	if err != nil {
		fmt.Fprintf(stderr, "errkb doctor: %v\n", err)
		return exitUsage
	}

	report := service.RunDoctor(context.Background(), service.DoctorOptions{
		Version:       version,
		DSN:           resolvedDSN,
		MigrationsDir: resolvedMigrations,
		ConfigPath:    *configPath,
		QueueDir:      *queueDir,
		PipeName:      *pipeName,
		Timeout:       *timeout,
	})

	if jsonMode {
		if err := json.NewEncoder(stdout).Encode(report); err != nil {
			fmt.Fprintf(stderr, "errkb doctor: write the report: %v\n", err)
			return exitInternal
		}
		return doctorStatusExit(report.Status)
	}

	renderDoctor(stdout, report)
	return doctorStatusExit(report.Status)
}

func renderDoctor(out io.Writer, report service.DoctorReport) {
	fmt.Fprintf(out, "errkb doctor %s (%s, %s/%s)\n", report.Version, report.GoVersion, report.GOOS, report.GOARCH)
	fmt.Fprintf(out, "checked %s\n", report.GeneratedAt.Format("2006-01-02 15:04:05Z07:00"))
	fmt.Fprintf(out, "status: %s\n\n", report.Status)
	for _, check := range report.Checks {
		fmt.Fprintf(out, "  %-8s %-11s %s\n", check.Status, check.Name, check.Summary)
		if check.Fix != "" {
			fmt.Fprintf(out, "  %-8s %-11s fix: %s\n", "", "", check.Fix)
		}
	}
}

func doctorStatusExit(status service.DoctorStatus) int {
	if status == service.DoctorFailing {
		return exitInternal
	}
	return exitOK
}
