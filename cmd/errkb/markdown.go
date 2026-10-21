//go:build windows

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/database"
)

// runMarkdown implements `errkb markdown import|export`. The legacy ERRORS.md
// format is an exchange format: import writes through the same tables the core
// uses, and export renders the current knowledge base in that format.
func runMarkdown(args []string, jsonMode bool, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "errkb markdown: a subcommand is required: import or export")
		return exitUsage
	}
	subcommand := args[0]
	if subcommand != "import" && subcommand != "export" {
		fmt.Fprintf(stderr, "errkb markdown: unknown subcommand %q (want import or export)\n", subcommand)
		return exitUsage
	}
	flags := flag.NewFlagSet("markdown "+subcommand, flag.ContinueOnError)
	flags.SetOutput(stderr)
	dsn := flags.String("dsn", "", "database connection string (default: $ERRKB_DSN, then the installer credentials)")
	credentials := flags.String("credentials", "", "credentials.json written by the installer")
	timeout := flags.Duration("timeout", 2*time.Minute, "give up after this duration")
	file := flags.String("file", "", "markdown file to import; - reads stdin")
	output := flags.String("out", "", "write the exported markdown here instead of stdout")
	if err := flags.Parse(args[1:]); err != nil {
		return exitUsage
	}
	if subcommand == "import" && *file == "" {
		fmt.Fprintln(stderr, "errkb markdown import: --file is required (- reads stdin)")
		return exitUsage
	}

	target, err := resolveDBTarget(*dsn, *credentials)
	if err != nil {
		fmt.Fprintf(stderr, "errkb markdown: %v\n", err)
		return exitInternal
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	conn, err := pgx.Connect(ctx, target.dsn)
	if err != nil {
		fmt.Fprintf(stderr, "errkb markdown: connect: %v\n", err)
		return exitInternal
	}
	defer func() { _ = conn.Close(context.Background()) }()

	if subcommand == "import" {
		text, err := readMarkdownInput(*file)
		if err != nil {
			fmt.Fprintf(stderr, "errkb markdown import: %v\n", err)
			return exitInternal
		}
		report, err := database.ImportMarkdown(ctx, conn, text)
		if err != nil {
			fmt.Fprintf(stderr, "errkb markdown import: %v\n", err)
			return exitInternal
		}
		if jsonMode {
			payload := struct {
				SchemaVersion string                `json:"schema_version"`
				Command       string                `json:"command"`
				Target        string                `json:"target"`
				Report        database.ImportReport `json:"report"`
			}{SchemaVersion: "v1", Command: "markdown.import", Target: target.label, Report: report}
			if err := json.NewEncoder(stdout).Encode(payload); err != nil {
				fmt.Fprintf(stderr, "errkb markdown import: encode: %v\n", err)
				return exitInternal
			}
			return exitOK
		}
		fmt.Fprintf(stdout, "markdown import: %d entr(y/ies) from %s\n", report.Entries, target.label)
		fmt.Fprintf(stdout, "  issues created %d, updated %d, versions added %d, fingerprint mismatches %d\n",
			report.IssuesCreated, report.IssuesUpdated, report.VersionsAdded, report.FingerprintMismatch)
		return exitOK
	}

	document, err := database.ExportMarkdown(ctx, conn)
	if err != nil {
		fmt.Fprintf(stderr, "errkb markdown export: %v\n", err)
		return exitInternal
	}
	if *output == "" {
		if _, err := io.WriteString(stdout, document); err != nil {
			fmt.Fprintf(stderr, "errkb markdown export: write: %v\n", err)
			return exitInternal
		}
		return exitOK
	}
	if err := os.WriteFile(*output, []byte(document), 0o644); err != nil {
		fmt.Fprintf(stderr, "errkb markdown export: %v\n", err)
		return exitInternal
	}
	if jsonMode {
		payload := struct {
			SchemaVersion string `json:"schema_version"`
			Command       string `json:"command"`
			Target        string `json:"target"`
			File          string `json:"file"`
			Bytes         int    `json:"bytes"`
		}{SchemaVersion: "v1", Command: "markdown.export", Target: target.label, File: *output, Bytes: len(document)}
		if err := json.NewEncoder(stdout).Encode(payload); err != nil {
			fmt.Fprintf(stderr, "errkb markdown export: encode: %v\n", err)
			return exitInternal
		}
		return exitOK
	}
	fmt.Fprintf(stdout, "markdown export: wrote %d byte(s) to %s (from %s)\n", len(document), *output, target.label)
	return exitOK
}

func readMarkdownInput(path string) (string, error) {
	if path == "-" {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", fmt.Errorf("read stdin: %w", err)
		}
		return string(data), nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
