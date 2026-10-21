//go:build windows

// `errkb db` (EKB-056): a fresh install must be able to create its schema
// without a Go toolchain. The installer runs `errkb db migrate` after the
// cluster starts; `errkb db status` inspects the ledger without writing.
//
// The connection facts come from --dsn or from the credentials file the
// installer wrote (EKB-051). The password is never printed: targets and errors
// use the redacted form.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/contract"
	"github.com/jingchangzhao-gif/error-knowledge-base/internal/database"
	"github.com/jingchangzhao-gif/error-knowledge-base/internal/service"
)

// dbReport is the --json shape of one `db` call. It is a CLI envelope; the
// applied and pending lists are version strings in ledger order.
type dbReport struct {
	SchemaVersion string   `json:"schema_version"`
	Command       string   `json:"command"`
	Target        string   `json:"target"`
	Applied       []string `json:"applied"`
	Pending       []string `json:"pending"`
	LedgerUnknown []string `json:"ledger_unknown,omitempty"`
	UpToDate      bool     `json:"up_to_date"`
}

// dbTarget is a resolved connection: the DSN stays in memory, the label is what
// may be printed.
type dbTarget struct {
	dsn   string
	label string
}

// runDB implements `errkb db migrate|status`.
func runDB(args []string, jsonMode bool, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "errkb db: a subcommand is required: migrate or status")
		return exitUsage
	}
	subcommand, rest := args[0], args[1:]
	switch subcommand {
	case "migrate", "status":
	case "help", "--help", "-h":
		fmt.Fprintln(stdout, "usage: errkb db migrate|status [--credentials FILE] [--dsn DSN] [--migrations DIR] [--json]")
		return exitOK
	default:
		fmt.Fprintf(stderr, "errkb db: unknown subcommand %q: use migrate or status\n", subcommand)
		return exitUsage
	}

	flags := flag.NewFlagSet("errkb db "+subcommand, flag.ContinueOnError)
	flags.SetOutput(stderr)
	dsn := flags.String("dsn", os.Getenv("ERRKB_DSN"), "PostgreSQL connection string (default $ERRKB_DSN); never printed")
	credentials := flags.String("credentials", "", `credentials file written by the installer (…\pg\credentials.json)`)
	migrations := flags.String("migrations", "db/migrations", "directory that holds the db/migrations files")
	timeout := flags.Duration("timeout", 2*time.Minute, "how long the database work may take")
	if err := flags.Parse(rest); err != nil {
		return exitUsage
	}
	if len(flags.Args()) != 0 {
		fmt.Fprintf(stderr, "errkb db: unexpected argument %q\n", flags.Args()[0])
		return exitUsage
	}

	target, err := resolveDBTarget(*dsn, *credentials)
	if err != nil {
		fmt.Fprintf(stderr, "errkb db: %v\n", err)
		return exitUsage
	}
	expected, err := database.LoadMigrations(*migrations)
	if err != nil {
		fmt.Fprintf(stderr, "errkb db: %v\n", err)
		return exitInternal
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	report, err := runDBWork(ctx, subcommand, target.dsn, *migrations, expected)
	if err != nil {
		fmt.Fprintf(stderr, "errkb db %s: %v\n", subcommand, err)
		return exitInternal
	}
	report.Target = target.label

	if jsonMode {
		if err := json.NewEncoder(stdout).Encode(report); err != nil {
			fmt.Fprintf(stderr, "errkb db: write the report: %v\n", err)
			return exitInternal
		}
	} else {
		renderDBReport(stdout, report, len(expected))
	}
	if report.UpToDate {
		return exitOK
	}
	return exitInternal
}

// runDBWork connects once and either applies or inspects the ledger. A failed
// query fails the whole call; a migration file is never applied on a status
// request.
func runDBWork(ctx context.Context, subcommand, dsn, migrationsDir string, expected []database.Migration) (dbReport, error) {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return dbReport{}, fmt.Errorf("connect: %w", err)
	}
	defer func() { _ = conn.Close(context.Background()) }()

	report := dbReport{SchemaVersion: contract.ToolSchemaVersion, Command: subcommand}
	if subcommand == "migrate" {
		applied, err := database.Migrate(ctx, conn, migrationsDir)
		if err != nil {
			return dbReport{}, fmt.Errorf("apply the migrations: %w", err)
		}
		report.Applied = applied
		// A successful run leaves nothing pending: Migrate applied every file
		// the ledger was missing or returned an error.
		report.UpToDate = true
		return report, nil
	}

	applied, err := ledgerVersions(ctx, conn)
	if err != nil {
		return dbReport{}, fmt.Errorf("read the ledger: %w", err)
	}
	expectedVersions := migrationVersions(expected)
	report.Applied = applied
	report.Pending = missingVersions(expectedVersions, applied)
	report.LedgerUnknown = unknownVersions(applied, expectedVersions)
	report.UpToDate = len(report.Pending) == 0 && len(report.LedgerUnknown) == 0
	return report, nil
}

// ledgerVersions lists the applied versions. A missing ledger table means the
// schema was never migrated, which is not an error for status.
func ledgerVersions(ctx context.Context, conn *pgx.Conn) ([]string, error) {
	rows, err := conn.Query(ctx, `select version from schema_migrations order by version`)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "42P01" {
			return nil, nil
		}
		return nil, err
	}
	defer rows.Close()

	versions := make([]string, 0, 16)
	for rows.Next() {
		var version string
		if err := rows.Scan(&version); err != nil {
			return nil, err
		}
		versions = append(versions, version)
	}
	return versions, rows.Err()
}

func migrationVersions(migrations []database.Migration) []string {
	versions := make([]string, 0, len(migrations))
	for _, migration := range migrations {
		versions = append(versions, migration.Version)
	}
	return versions
}

func missingVersions(expected, applied []string) []string {
	seen := make(map[string]struct{}, len(applied))
	for _, version := range applied {
		seen[version] = struct{}{}
	}
	missing := make([]string, 0, 4)
	for _, version := range expected {
		if _, ok := seen[version]; !ok {
			missing = append(missing, version)
		}
	}
	return missing
}

func unknownVersions(applied, expected []string) []string {
	known := make(map[string]struct{}, len(expected))
	for _, version := range expected {
		known[version] = struct{}{}
	}
	unknown := make([]string, 0, 2)
	for _, version := range applied {
		if _, ok := known[version]; !ok {
			unknown = append(unknown, version)
		}
	}
	return unknown
}

func renderDBReport(out io.Writer, report dbReport, expected int) {
	fmt.Fprintf(out, "errkb db %s\n", report.Command)
	fmt.Fprintf(out, "  database  %s\n", report.Target)
	fmt.Fprintf(out, "  expected  %d migration(s)\n", expected)
	if len(report.Applied) == 0 {
		fmt.Fprintln(out, "  applied   none")
	} else {
		fmt.Fprintf(out, "  applied   %d: %v\n", len(report.Applied), report.Applied)
	}
	if len(report.LedgerUnknown) != 0 {
		fmt.Fprintf(out, "  unknown   %v (recorded in the ledger but not in this build)\n", report.LedgerUnknown)
	}
	switch {
	case report.Command == "migrate" && len(report.Applied) == 0:
		fmt.Fprintln(out, "  status    up to date")
	case report.Command == "migrate":
		fmt.Fprintf(out, "  status    applied %d migration(s)\n", len(report.Applied))
	case report.UpToDate:
		fmt.Fprintln(out, "  status    up to date")
	default:
		fmt.Fprintf(out, "  status    %d migration(s) pending: %v\n", len(report.Pending), report.Pending)
	}
}

// resolveDBTarget prefers --dsn, then the installer credentials file. Passing
// neither is a usage error: guessing a database would hide the misconfiguration.
func resolveDBTarget(dsn, credentialsPath string) (dbTarget, error) {
	switch {
	case dsn != "":
		return dbTarget{dsn: dsn, label: redactDSN(dsn)}, nil
	case credentialsPath != "":
		credentials, err := service.ReadCredentials(credentialsPath)
		if err != nil {
			return dbTarget{}, err
		}
		return dbTarget{dsn: credentials.DSN(), label: credentials.Redacted()}, nil
	default:
		return dbTarget{}, fmt.Errorf("no database: pass --dsn or --credentials (the installer writes pg\\credentials.json)")
	}
}

// redactDSN drops the password so reports and errors never leak it.
func redactDSN(dsn string) string {
	parsed, err := url.Parse(dsn)
	if err != nil || parsed.Scheme == "" {
		return "(connection string)"
	}
	if parsed.User != nil {
		parsed.User = url.User(parsed.User.Username())
	}
	return parsed.String()
}
