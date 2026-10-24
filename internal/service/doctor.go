//go:build windows

package service

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/sys/windows"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/contract"
	"github.com/jingchangzhao-gif/error-knowledge-base/internal/database"
	"github.com/jingchangzhao-gif/error-knowledge-base/internal/redact"
)

// DoctorStatus is the three-level verdict a diagnosis reports. The binary
// ok/failing split hides "usable but reduced" states such as a missing pg_trgm
// extension, so every check says exactly which of the three it is.
type DoctorStatus string

const (
	// DoctorOK means the check found a fully working component.
	DoctorOK DoctorStatus = "ok"
	// DoctorDegraded means the component works with less capability, for
	// example fuzzy search without the pg_trgm extension.
	DoctorDegraded DoctorStatus = "degraded"
	// DoctorFailing means the component is broken.
	DoctorFailing DoctorStatus = "failing"
)

// DefaultDoctorTimeout bounds every network-style check (the database
// connection, the pipe dial) so a hung endpoint cannot hang the diagnosis.
const DefaultDoctorTimeout = 5 * time.Second

// DoctorCheck is one category of the diagnosis. Fix is one line the user can
// act on, and only ever set for a non-ok check.
type DoctorCheck struct {
	Name    string            `json:"name"`
	Status  DoctorStatus      `json:"status"`
	Summary string            `json:"summary"`
	Detail  map[string]string `json:"detail,omitempty"`
	Fix     string            `json:"fix,omitempty"`
}

// DoctorReport is the whole diagnosis: the eight categories plus the EKB-028
// health document so the tray (P6) and CI read exactly the same numbers the
// daemon publishes.
type DoctorReport struct {
	Version     string        `json:"version"`
	GoVersion   string        `json:"go_version"`
	GOOS        string        `json:"goos"`
	GOARCH      string        `json:"goarch"`
	GeneratedAt time.Time     `json:"generated_at"`
	Status      DoctorStatus  `json:"status"`
	Checks      []DoctorCheck `json:"checks"`
	Health      *Health       `json:"health,omitempty"`
}

// DoctorOptions configures one diagnosis. It mirrors the daemon flags plus the
// state an external diagnose needs, and it never writes anything.
type DoctorOptions struct {
	// Version is the errkb build stamp (the daemon binary sets "dev" locally).
	Version string
	// DSN is the PostgreSQL connection string. It is never printed.
	DSN string
	// MigrationsDir holds the db/migrations files.
	MigrationsDir string
	// ConfigPath is the optional configuration file.
	ConfigPath string
	// QueueDir is the offline spool directory (EKB-033).
	QueueDir string
	// PipeName is the local API pipe path.
	PipeName string
	// QueueMaxEvents and QueueMaxBytes are the caps the daemon enforces; zero
	// uses the EKB-033 defaults.
	QueueMaxEvents int
	QueueMaxBytes  int64
	// Timeout bounds the database connection and the pipe dial.
	Timeout time.Duration
	// Now is replaceable in tests; nil uses time.Now.
	Now func() time.Time
}

// doctorConnect and doctorTrigramAvailable are replaceable in tests so the
// diagnosis can be exercised without a database or a live extension.
var (
	doctorConnect = func(ctx context.Context, dsn string) (*pgx.Conn, error) {
		return pgx.Connect(ctx, dsn)
	}
	doctorTrigramAvailable = database.TrigramAvailable
)

var (
	doctorPasswordParam = regexp.MustCompile(`(?i)\bpassword\s*=\s*[^\s&]+`)
	doctorURLCredential = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.-]*://[^:/@\s]+:)[^@\s]+@`)
)

// RunDoctor produces the diagnosis. It never returns an error: a broken
// database must not stop the other seven checks from printing, and the caller
// always gets a whole report.
func RunDoctor(ctx context.Context, options DoctorOptions) DoctorReport {
	options = normalizeDoctorOptions(options)
	// A configuration file that cannot be read is reported by the privacy
	// check; the rest of the diagnosis still uses the documented defaults.
	config, configErr := loadDoctorConfig(options)

	report := DoctorReport{
		Version:     options.Version,
		GoVersion:   runtime.Version(),
		GOOS:        runtime.GOOS,
		GOARCH:      runtime.GOARCH,
		GeneratedAt: options.Now().UTC(),
	}
	health := Health{
		Version:   HealthVersion,
		Status:    healthOK,
		CheckedAt: options.Now().UTC(),
	}

	report.Checks = append(report.Checks, doctorVersionCheck(options))

	// The remaining checks share one connection. Its failure is the database
	// check's finding, not an early return.
	conn, connectErr := doctorOpenDatabase(ctx, options)
	if conn != nil {
		defer func() { _ = conn.Close(context.Background()) }()
	}

	databaseCheck, databaseHealth := doctorDatabaseCheck(ctx, conn, connectErr, options)
	health.Database = databaseHealth
	report.Checks = append(report.Checks, databaseCheck)

	migrationCheck, migrationHealth := doctorMigrationsCheck(ctx, conn, options)
	health.Migrations = migrationHealth
	report.Checks = append(report.Checks, migrationCheck)

	report.Checks = append(report.Checks, doctorExtensionCheck(ctx, conn, options))
	report.Checks = append(report.Checks, doctorPipeCheck(ctx, options, config))

	report.Checks = append(report.Checks, doctorMCPCheck(ctx, options))

	report.Checks = append(report.Checks, doctorConnectorsCheck(ctx, conn, options))

	queueCheck, queueHealth := doctorQueueCheck(options)
	health.Queue = queueHealth
	report.Checks = append(report.Checks, queueCheck)

	report.Checks = append(report.Checks, doctorPrivacyCheck(options, config, configErr))

	if !health.Database.Connected || !health.Migrations.OK || health.Queue.Error != "" {
		health.Status = healthDegraded
	}
	report.Health = &health
	report.Status = worstDoctorStatus(report.Checks)
	return report
}

// doctorExitCode maps the verdict to the CLI exit code: a failing diagnosis is
// an error, a degraded one is still a usable report.
func doctorExitCode(status DoctorStatus) int {
	if status == DoctorFailing {
		return 1
	}
	return 0
}

func normalizeDoctorOptions(options DoctorOptions) DoctorOptions {
	if options.Version == "" {
		options.Version = "dev"
	}
	if options.MigrationsDir == "" {
		options.MigrationsDir = "db/migrations"
	}
	if options.QueueDir == "" {
		options.QueueDir = filepath.Join(defaultDataDir(), "queue")
	}
	if options.PipeName == "" {
		options.PipeName = DefaultPipeName
	}
	if options.QueueMaxEvents <= 0 {
		options.QueueMaxEvents = DefaultQueueMaxEvents
	}
	if options.QueueMaxBytes <= 0 {
		options.QueueMaxBytes = DefaultQueueMaxBytes
	}
	if options.Timeout <= 0 {
		options.Timeout = DefaultDoctorTimeout
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return options
}

// loadDoctorConfig reads the optional configuration file. A missing path or a
// missing file is not an error: the contract defaults apply.
func loadDoctorConfig(options DoctorOptions) (*contract.Config, error) {
	if options.ConfigPath == "" {
		return nil, nil
	}
	data, err := os.ReadFile(options.ConfigPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read the configuration file: %w", err)
	}
	config, err := contract.LoadConfig(data)
	if err != nil {
		return nil, fmt.Errorf("parse the configuration file: %w", err)
	}
	return &config, nil
}

// doctorText removes anything machine-specific from text that is about to be
// printed: the DSN and its password, URL credentials and the configured paths.
func doctorText(options DoctorOptions, text string) string {
	if options.DSN != "" {
		text = strings.ReplaceAll(text, options.DSN, "<dsn>")
	}
	text = doctorPasswordParam.ReplaceAllString(text, "password=<password>")
	text = doctorURLCredential.ReplaceAllString(text, "$1<password>@")
	for _, path := range []string{options.ConfigPath, options.QueueDir, options.MigrationsDir} {
		if path != "" {
			text = strings.ReplaceAll(text, path, "<path>")
		}
	}
	return strings.TrimSpace(text)
}

func doctorVersionCheck(options DoctorOptions) DoctorCheck {
	return DoctorCheck{
		Name:    "version",
		Status:  DoctorOK,
		Summary: fmt.Sprintf("errkb %s, Go %s, %s/%s", options.Version, runtime.Version(), runtime.GOOS, runtime.GOARCH),
		Detail: map[string]string{
			"version": options.Version,
			"go":      runtime.Version(),
			"os":      runtime.GOOS,
			"arch":    runtime.GOARCH,
		},
	}
}

func doctorOpenDatabase(ctx context.Context, options DoctorOptions) (*pgx.Conn, error) {
	if options.DSN == "" {
		return nil, fmt.Errorf("no database DSN configured (pass --dsn or set ERRKB_DSN)")
	}
	dialCtx, cancel := context.WithTimeout(ctx, options.Timeout)
	defer cancel()
	return doctorConnect(dialCtx, options.DSN)
}

func doctorDatabaseCheck(ctx context.Context, conn *pgx.Conn, connectErr error, options DoctorOptions) (DoctorCheck, DatabaseHealth) {
	check := DoctorCheck{Name: "database", Detail: make(map[string]string)}
	health := DatabaseHealth{}
	if conn == nil {
		message := "the database is unreachable"
		if connectErr != nil {
			message = doctorText(options, connectErr.Error())
		}
		health.Error = message
		check.Status = DoctorFailing
		check.Summary = message
		check.Fix = "start PostgreSQL and check the DSN (--dsn or database.dsn)"
		return check, health
	}

	if err := conn.Ping(ctx); err != nil {
		message := doctorText(options, err.Error())
		health.Error = message
		check.Status = DoctorFailing
		check.Summary = "connected but the ping failed: " + message
		check.Fix = "restart PostgreSQL or check its logs"
		return check, health
	}
	health.Connected = true

	var version, name string
	var connections int
	query := "select current_setting('server_version'), current_database(), (select count(*) from pg_stat_activity)"
	if err := conn.QueryRow(ctx, query).Scan(&version, &name, &connections); err != nil {
		check.Status = DoctorDegraded
		check.Summary = "connected but the server state could not be read: " + doctorText(options, err.Error())
		check.Fix = "check the database privileges of the errkb user"
		return check, health
	}

	check.Status = DoctorOK
	check.Summary = fmt.Sprintf("PostgreSQL %s, database %s, %d connection(s)", version, name, connections)
	check.Detail["server_version"] = version
	check.Detail["database"] = name
	check.Detail["connections"] = strconv.Itoa(connections)
	return check, health
}

func doctorMigrationsCheck(ctx context.Context, conn *pgx.Conn, options DoctorOptions) (DoctorCheck, MigrationHealth) {
	check := DoctorCheck{Name: "migrations", Detail: make(map[string]string)}
	health := MigrationHealth{}

	migrations, err := database.LoadMigrations(options.MigrationsDir)
	if err != nil {
		check.Status = DoctorFailing
		check.Summary = "the migration files could not be read: " + doctorText(options, err.Error())
		check.Fix = "point --migrations at the directory that holds db/migrations"
		return check, health
	}
	if len(migrations) == 0 {
		check.Status = DoctorFailing
		check.Summary = "no migration files were found in <path>"
		check.Fix = "point --migrations at the directory that holds db/migrations"
		return check, health
	}
	expected := migrations[len(migrations)-1].Version
	health.Expected = expected
	check.Detail["expected"] = expected

	if conn == nil {
		check.Status = DoctorFailing
		check.Summary = "the applied ledger cannot be verified while the database is unreachable"
		check.Fix = "fix the database check first"
		return check, health
	}

	// Verify the ledger against the files without running Migrate: the doctor
	// must not change anything, not even through the migrator.
	rows, err := conn.Query(ctx, "select version, checksum from schema_migrations order by version")
	if err != nil {
		message := doctorText(options, err.Error())
		health.Error = message
		check.Status = DoctorFailing
		check.Summary = "the schema_migrations ledger could not be read: " + message
		check.Fix = "apply the migrations (db/migrations) with the migration command, then restart the daemon"
		return check, health
	}
	recorded := make(map[string]string, len(migrations))
	for rows.Next() {
		var version, checksum string
		if err := rows.Scan(&version, &checksum); err != nil {
			rows.Close()
			health.Error = doctorText(options, err.Error())
			check.Status = DoctorFailing
			check.Summary = "the schema_migrations ledger could not be read: " + health.Error
			check.Fix = "apply the migrations (db/migrations) with the migration command, then restart the daemon"
			return check, health
		}
		recorded[version] = checksum
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		health.Error = doctorText(options, err.Error())
		check.Status = DoctorFailing
		check.Summary = "the schema_migrations ledger could not be read: " + health.Error
		check.Fix = "apply the migrations (db/migrations) with the migration command, then restart the daemon"
		return check, health
	}
	rows.Close()

	current := ""
	for version := range recorded {
		if version > current {
			current = version
		}
	}
	health.Current = current
	check.Detail["applied"] = current

	byVersion := make(map[string]database.Migration, len(migrations))
	for _, migration := range migrations {
		byVersion[migration.Version] = migration
	}
	var problems []string
	for version, checksum := range recorded {
		migration, ok := byVersion[version]
		if !ok {
			problems = append(problems, fmt.Sprintf("%s is recorded but has no file", version))
			continue
		}
		if migration.Checksum != checksum {
			problems = append(problems, fmt.Sprintf("%s changed after it was applied", version))
		}
	}
	sort.Strings(problems)
	if len(problems) > 0 {
		health.Error = strings.Join(problems, "; ")
		check.Status = DoctorFailing
		check.Summary = "the ledger and the files disagree: " + health.Error
		check.Fix = "an applied migration must not be edited; restore the recorded file or add a new migration"
		return check, health
	}

	if current == "" {
		check.Status = DoctorFailing
		check.Summary = "no migration was applied yet (expected " + expected + ")"
		check.Fix = "apply the migrations (db/migrations) with the migration command, then restart the daemon"
		return check, health
	}

	health.OK = current == expected
	if !health.OK {
		check.Status = DoctorDegraded
		check.Summary = fmt.Sprintf("applied %s, expected %s: pending migrations", current, expected)
		check.Fix = "apply the pending migrations, then restart the daemon"
		return check, health
	}

	check.Status = DoctorOK
	check.Summary = fmt.Sprintf("ledger matches all %d migration files (current %s)", len(recorded), current)
	return check, health
}

func doctorExtensionCheck(ctx context.Context, conn *pgx.Conn, options DoctorOptions) DoctorCheck {
	check := DoctorCheck{Name: "extension", Detail: make(map[string]string)}
	if conn == nil {
		check.Status = DoctorFailing
		check.Summary = "pg_trgm cannot be checked while the database is unreachable"
		check.Fix = "fix the database check first"
		return check
	}

	available, err := doctorTrigramAvailable(ctx, conn)
	if err != nil {
		check.Status = DoctorDegraded
		check.Summary = "pg_trgm could not be checked: " + doctorText(options, err.Error())
		check.Fix = "apply the migrations that create errkb_trigram_available()"
		return check
	}
	if !available {
		check.Status = DoctorDegraded
		check.Summary = "pg_trgm is not installed: fuzzy search is unavailable, exact search still works"
		check.Fix = "install the pg_trgm extension on the PostgreSQL server and run CREATE EXTENSION pg_trgm"
		return check
	}

	var version string
	if err := conn.QueryRow(ctx, "select extversion from pg_extension where extname = 'pg_trgm'").Scan(&version); err == nil {
		check.Detail["version"] = version
	}
	check.Status = DoctorOK
	check.Summary = "pg_trgm is installed"
	return check
}

func doctorPipeCheck(ctx context.Context, options DoctorOptions, config *contract.Config) DoctorCheck {
	check := DoctorCheck{Name: "pipe", Detail: make(map[string]string)}
	check.Detail["name"] = options.PipeName

	aclOK := false
	descriptor, err := pipeSecurityDescriptor()
	if err != nil {
		check.Detail["acl_error"] = doctorText(options, err.Error())
	} else {
		check.Detail["acl"] = descriptor
		sids := descriptorSIDs(descriptor)
		current, userErr := doctorCurrentUserSID()
		aclOK = userErr == nil && len(sids) == 2 && sids[0] == current && sids[1] == "SY"
	}

	dialCtx, cancel := context.WithTimeout(ctx, options.Timeout)
	defer cancel()
	client, dialErr := DialPipe(dialCtx, options.PipeName)
	listening := dialErr == nil
	if client != nil {
		_ = client.Close()
	}
	check.Detail["listening"] = strconv.FormatBool(listening)

	httpState := "disabled (default)"
	if config != nil && config.Service.HTTP.Enabled != nil {
		if *config.Service.HTTP.Enabled {
			httpState = "enabled"
		} else {
			httpState = "disabled"
		}
	}
	check.Detail["http"] = httpState

	switch {
	case !aclOK:
		check.Status = DoctorFailing
		check.Summary = "the pipe ACL is not restricted to the current user and SYSTEM"
		check.Fix = "rebuild errkb so the pipe descriptor lists only the current user and SYSTEM"
	case !listening:
		check.Status = DoctorDegraded
		check.Summary = fmt.Sprintf("the daemon is not listening on %s", options.PipeName)
		check.Fix = "start the daemon: errkb daemon"
	default:
		check.Status = DoctorOK
		check.Summary = "listening on " + options.PipeName + " with an owner-only ACL"
	}
	return check
}

// doctorMCPRun invokes the binary's own selftest. Tests replace it because the
// test executable is not errkb.exe.
var doctorMCPRun = func(ctx context.Context, executable string) (string, error) {
	command := exec.CommandContext(ctx, executable, "mcp", "selftest")
	var output strings.Builder
	command.Stdin = nil
	command.Stdout = io.Discard
	command.Stderr = &output
	err := command.Run()
	return output.String(), err
}

// doctorMCPCheck verifies the MCP surface with the binary's own selftest: one
// initialize and one tools/list through the real server, no host or daemon
// involved. A missing or renamed tool therefore shows up in the diagnosis.
func doctorMCPCheck(ctx context.Context, options DoctorOptions) DoctorCheck {
	check := DoctorCheck{Name: "mcp", Detail: make(map[string]string)}
	executable, err := os.Executable()
	if err != nil {
		check.Status = DoctorFailing
		check.Summary = "the errkb executable could not be located: " + doctorText(options, err.Error())
		check.Fix = "run `errkb mcp selftest` from the installed binary"
		return check
	}
	timeout := options.Timeout
	if timeout <= 0 || timeout > 20*time.Second {
		timeout = 20 * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	output, runErr := doctorMCPRun(runCtx, executable)
	if runErr != nil {
		check.Status = DoctorFailing
		check.Summary = "the MCP selftest failed: " + doctorText(options, strings.TrimSpace(output))
		check.Fix = "run `errkb mcp selftest` to see the tool list; rebuild the binary if tools are missing"
		return check
	}
	check.Status = DoctorOK
	check.Summary = strings.TrimSpace(output)
	check.Detail["selftest"] = "initialize and tools/list answered over stdio"
	check.Detail["tools"] = "9"
	return check
}

// descriptorSIDs extracts the trustee of every access-control entry from an
// SDDL descriptor such as D:P(A;;GA;;;S-1-5-21-...)(A;;GA;;;SY).
func descriptorSIDs(descriptor string) []string {
	var sids []string
	for _, ace := range strings.Split(descriptor, "(")[1:] {
		parts := strings.Split(strings.TrimSuffix(ace, ")"), ";")
		if len(parts) == 6 && parts[5] != "" {
			sids = append(sids, parts[5])
		}
	}
	return sids
}

// doctorCurrentUserSID duplicates the two calls the pipe listener uses; the
// diagnosis may not reach into the server code, and the descriptor it verifies
// is the one the server actually binds.
func doctorCurrentUserSID() (string, error) {
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return "", err
	}
	defer func() { _ = token.Close() }()
	user, err := token.GetTokenUser()
	if err != nil {
		return "", err
	}
	return user.User.Sid.String(), nil
}

func doctorConnectorsCheck(ctx context.Context, conn *pgx.Conn, options DoctorOptions) DoctorCheck {
	check := DoctorCheck{Name: "connectors", Detail: make(map[string]string)}
	if conn == nil {
		check.Status = DoctorFailing
		check.Summary = "the registered connectors cannot be read while the database is unreachable"
		check.Fix = "fix the database check first"
		return check
	}

	rows, err := conn.Query(ctx, "select name, channel, version from connectors order by name")
	if err != nil {
		check.Status = DoctorFailing
		check.Summary = "the connectors table could not be read: " + doctorText(options, err.Error())
		check.Fix = "apply the migrations (db/migrations), then register the connector"
		return check
	}
	defer rows.Close()

	var names, channels []string
	for rows.Next() {
		var name, channel, version string
		if err := rows.Scan(&name, &channel, &version); err != nil {
			check.Status = DoctorFailing
			check.Summary = "the connectors table could not be read: " + doctorText(options, err.Error())
			return check
		}
		names = append(names, name)
		channels = append(channels, channel)
	}
	if err := rows.Err(); err != nil {
		check.Status = DoctorFailing
		check.Summary = "the connectors table could not be read: " + doctorText(options, err.Error())
		return check
	}

	check.Detail["count"] = strconv.Itoa(len(names))
	if len(names) == 0 {
		check.Status = DoctorDegraded
		check.Summary = "no connector is registered: capture cannot start yet"
		check.Fix = "register a connector (channel A-E) once the capture cards land"
		return check
	}
	check.Status = DoctorOK
	check.Summary = fmt.Sprintf("%d connector(s) registered", len(names))
	check.Detail["connectors"] = strings.Join(names, ", ")
	check.Detail["channels"] = strings.Join(channels, ", ")
	return check
}

func doctorQueueCheck(options DoctorOptions) (DoctorCheck, QueueHealth) {
	check := DoctorCheck{Name: "queue", Detail: make(map[string]string)}
	health := QueueHealth{}

	entries, err := os.ReadDir(options.QueueDir)
	if err != nil {
		if os.IsNotExist(err) {
			check.Status = DoctorOK
			check.Summary = "empty: the offline queue directory does not exist yet"
			check.Detail["pending"] = "0"
			check.Detail["bytes"] = "0"
			return check, health
		}
		message := doctorText(options, err.Error())
		health.Error = message
		check.Status = DoctorFailing
		check.Summary = "the offline queue could not be read: " + message
		check.Fix = "check the permissions of the spool directory (offline.spool_dir)"
		return check, health
	}

	var pending int
	var bytes int64
	var oldest time.Time
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), queueFileSuffix) {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		pending++
		bytes += info.Size()
		if stamp, ok := queueFileTimestamp(entry.Name()); ok && (oldest.IsZero() || stamp.Before(oldest)) {
			oldest = stamp
		}
	}

	check.Detail["pending"] = strconv.Itoa(pending)
	check.Detail["bytes"] = strconv.FormatInt(bytes, 10)
	check.Detail["max_events"] = strconv.Itoa(options.QueueMaxEvents)
	check.Detail["max_bytes"] = strconv.FormatInt(options.QueueMaxBytes, 10)
	if !oldest.IsZero() {
		age := options.Now().Sub(oldest)
		if age < 0 {
			age = 0
		}
		check.Detail["oldest_age"] = age.Round(time.Second).String()
	} else {
		check.Detail["oldest_age"] = "0s"
	}

	health.Backlog = pending
	overLimit := pending > options.QueueMaxEvents || bytes > options.QueueMaxBytes
	check.Detail["over_limit"] = strconv.FormatBool(overLimit)
	switch {
	case overLimit:
		check.Status = DoctorDegraded
		check.Summary = fmt.Sprintf("%d event(s), %d byte(s): over the cap, the next enqueue drops the oldest event", pending, bytes)
		check.Fix = "start the daemon and let it replay the queue, or remove the spool directory after inspecting it"
	case pending > 0:
		check.Status = DoctorDegraded
		check.Summary = fmt.Sprintf("%d event(s), %d byte(s) waiting for replay", pending, bytes)
		check.Fix = "the daemon replays the queue automatically; start it if it is not running"
	default:
		check.Status = DoctorOK
		check.Summary = "empty: no event is waiting for replay"
	}
	return check, health
}

// queueFileTimestamp reads the nanosecond stamp that prefixes every queue file.
func queueFileTimestamp(name string) (time.Time, bool) {
	base := strings.TrimSuffix(name, queueFileSuffix)
	index := strings.IndexByte(base, '-')
	if index <= 0 {
		return time.Time{}, false
	}
	nanos, err := strconv.ParseInt(base[:index], 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	return time.Unix(0, nanos), true
}

func doctorPrivacyCheck(options DoctorOptions, config *contract.Config, configErr error) DoctorCheck {
	check := DoctorCheck{Name: "privacy", Detail: map[string]string{
		"rules_digest": redact.SourceDigest,
		"share":        "public",
	}}
	capture, notices := "enabled (default)", "before diagnosis (default)"
	retention := "7/90/365 days (default)"
	patterns := "0"

	if configErr != nil {
		check.Status = DoctorDegraded
		check.Summary = "the configuration could not be read: " + doctorText(options, configErr.Error())
		check.Fix = "fix the configuration file, or remove it to use the defaults"
		check.Detail["capture"] = capture
		check.Detail["notices"] = notices
		check.Detail["retention"] = retention
		check.Detail["extra_patterns"] = patterns
		return check
	}
	if config != nil {
		if config.Capture.Enabled != nil {
			capture = onOff(*config.Capture.Enabled)
		}
		if config.Notices.BeforeDiagnosis != nil {
			notices = onOff(*config.Notices.BeforeDiagnosis)
		}
		if config.Retention.SampleDays != nil && config.Retention.NormalizedDays != nil && config.Retention.AuditDays != nil {
			retention = fmt.Sprintf("%d/%d/%d days", *config.Retention.SampleDays, *config.Retention.NormalizedDays, *config.Retention.AuditDays)
		}
		patterns = strconv.Itoa(len(config.Redaction.ExtraPatterns))
	}

	check.Status = DoctorOK
	check.Summary = fmt.Sprintf("public share, capture %s, retention %s", capture, retention)
	check.Detail["capture"] = capture
	check.Detail["notices"] = notices
	check.Detail["retention"] = retention
	check.Detail["extra_patterns"] = patterns
	return check
}

func onOff(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
}

func worstDoctorStatus(checks []DoctorCheck) DoctorStatus {
	worst := DoctorOK
	for _, check := range checks {
		switch check.Status {
		case DoctorFailing:
			return DoctorFailing
		case DoctorDegraded:
			worst = DoctorDegraded
		}
	}
	return worst
}
