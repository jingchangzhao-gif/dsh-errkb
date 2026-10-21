// Package service — pg_manage.go manages a local PostgreSQL instance for the
// installed product (EKB-051).
//
// The release is an installed application (implementation-plan §2): the
// installer owns a PostgreSQL cluster under the user profile and the runtime
// keeps it healthy. PostgreSQL stays an explicit dependency. This file holds the
// lifecycle decisions; packaging/windows/postgres.ps1 only orchestrates the
// binaries and packaging/windows/installer.iss only drives the flow.
//
// Destructive actions never happen silently: Install refuses a busy port or an
// existing cluster, Stop and Uninstall take an explicit deleteData flag, and
// credentials are generated locally instead of shipping a default password.
//
// Shortcut: the Windows service registration path needs an elevated token and
// is only exercised through the CommandRunner seam here; the live acceptance
// record uses process mode (pg_ctl start/stop). Upgrade to a service-backed
// check once EKB-052 has an elevated clean-VM runner.
package service

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/database"
)

const (
	// DefaultPGPort is deliberately not 5432: the development host and many
	// user machines already run PostgreSQL there, and the card forbids blindly
	// taking the default port.
	DefaultPGPort         = 5433
	DefaultPGDatabase     = "errkb"
	DefaultPGSuperuser    = "postgres"
	DefaultPGReadyTimeout = 90 * time.Second
	DefaultPGStopTimeout  = 90 * time.Second

	pgVersionFileName = "PG_VERSION"
	pgCredentialsName = "credentials.json"
	pgLogDirName      = "logs"
	pgLogFileName     = "postgresql.log"
	pgInstanceName    = "errkb"
)

// PGPaths are the PostgreSQL client and server binaries the lifecycle needs.
type PGPaths struct {
	BinDir    string
	PgCtl     string
	Initdb    string
	Psql      string
	PgDump    string
	PgRestore string
}

// FindPostgresInstallations returns the PostgreSQL installation directories
// found at the usual Windows locations. It is used for the conflict message
// and for installer guidance; it never starts anything.
func FindPostgresInstallations() []string {
	var found []string
	seen := map[string]bool{}
	for _, dir := range candidatePostgresDirs() {
		if seen[dir] {
			continue
		}
		if _, ok := postgresPathsIn(dir); !ok {
			continue
		}
		seen[dir] = true
		found = append(found, dir)
	}
	return found
}

// ResolvePostgresBinaries locates a PostgreSQL installation. It checks
// ERRKB_PG_BIN first, then the usual install directories, then PATH. It
// requires every tool the lifecycle uses, so a half-broken install fails here
// instead of in the middle of an upgrade.
func ResolvePostgresBinaries() (PGPaths, error) {
	if dir := strings.TrimSpace(os.Getenv("ERRKB_PG_BIN")); dir != "" {
		if paths, ok := postgresPathsIn(dir); ok {
			return paths, nil
		}
		return PGPaths{}, fmt.Errorf("ERRKB_PG_BIN=%q does not contain the PostgreSQL tools (pg_ctl, initdb, psql, pg_dump, pg_restore)", dir)
	}
	for _, dir := range candidatePostgresDirs() {
		if paths, ok := postgresPathsIn(dir); ok {
			return paths, nil
		}
	}
	if runtime.GOOS == "windows" {
		return PGPaths{}, fmt.Errorf("no PostgreSQL 16 installation found: look in %%ProgramFiles%%\\PostgreSQL\\16\\bin, set ERRKB_PG_BIN, or add pg_ctl to PATH")
	}
	return PGPaths{}, fmt.Errorf("no PostgreSQL installation found: set ERRKB_PG_BIN or add pg_ctl to PATH")
}

func candidatePostgresDirs() []string {
	var dirs []string
	for _, env := range []string{"ProgramW6432", "ProgramFiles", "ProgramFiles(x86)"} {
		base := strings.TrimSpace(os.Getenv(env))
		if base == "" {
			continue
		}
		for _, version := range []string{"17", "16", "15"} {
			dirs = append(dirs, filepath.Join(base, "PostgreSQL", version, "bin"))
		}
	}
	return dirs
}

func postgresPathsIn(dir string) (PGPaths, bool) {
	paths := PGPaths{BinDir: dir}
	for _, tool := range []struct {
		name string
		dest *string
	}{
		{"pg_ctl", &paths.PgCtl},
		{"initdb", &paths.Initdb},
		{"psql", &paths.Psql},
		{"pg_dump", &paths.PgDump},
		{"pg_restore", &paths.PgRestore},
	} {
		candidate := filepath.Join(dir, toolExecutable(tool.name))
		if info, err := os.Stat(candidate); err != nil || info.IsDir() {
			return PGPaths{}, false
		}
		*tool.dest = candidate
	}
	return paths, true
}

func toolExecutable(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

// CommandRunner is the seam the tests use to inspect the exact commands the
// lifecycle issues (for example the service registration path, which needs an
// elevated token the test host does not have).
type CommandRunner interface {
	Run(ctx context.Context, name string, args ...string) (string, error)
}

// ExecRunner runs the real binaries.
type ExecRunner struct{}

// Run executes one command and returns its combined output on failure.
//
// The output is captured through a temporary file rather than a pipe: pg_ctl
// starts the postmaster as a detached child that inherits the standard
// handles, so a pipe would never reach EOF and CombinedOutput would hang until
// the server exits. A real file (or NUL) has no such lifetime coupling.
func (ExecRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	outputFile, err := os.CreateTemp("", "errkb-command-*.log")
	if err != nil {
		return "", err
	}
	outputPath := outputFile.Name()
	defer func() { _ = os.Remove(outputPath) }()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = outputFile
	cmd.Stderr = outputFile
	runErr := cmd.Run()
	if closeErr := outputFile.Close(); closeErr != nil && runErr == nil {
		runErr = closeErr
	}
	output, readErr := os.ReadFile(outputPath)
	if readErr != nil && runErr == nil {
		runErr = readErr
	}
	return string(output), runErr
}

// Instance describes one managed cluster.
type Instance struct {
	Name            string
	DataDir         string
	Port            int
	Database        string
	Superuser       string
	CredentialsFile string
	// ServiceName optionally registers the cluster as a Windows service. It is
	// empty for the default process mode (a per-installation process that the
	// installer starts), which needs no elevation.
	ServiceName string
}

// DefaultInstance returns the instance layout used by the installer.
func DefaultInstance(root string) Instance {
	return Instance{
		Name:            pgInstanceName,
		DataDir:         filepath.Join(root, "data"),
		Port:            DefaultPGPort,
		Database:        DefaultPGDatabase,
		Superuser:       DefaultPGSuperuser,
		CredentialsFile: filepath.Join(root, pgCredentialsName),
	}
}

var (
	pgIdentifierPattern = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)
	pgNamePattern       = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)
)

// Validate rejects configurations that would make a destructive action
// ambiguous (relative paths, reserved ports, invalid identifiers).
func (i Instance) Validate() error {
	if !pgNamePattern.MatchString(i.Name) {
		return fmt.Errorf("instance: name %q must be 1-32 characters of a-z, 0-9, '-' or '_'", i.Name)
	}
	if i.DataDir == "" || !filepath.IsAbs(i.DataDir) {
		return fmt.Errorf("instance: data directory must be an absolute path")
	}
	clean := filepath.Clean(i.DataDir)
	if clean == filepath.VolumeName(clean)+string(os.PathSeparator) || len(clean) < 8 {
		return fmt.Errorf("instance: data directory %q is too broad to manage", i.DataDir)
	}
	if i.Port < 1024 || i.Port > 65535 {
		return fmt.Errorf("instance: port %d must be between 1024 and 65535", i.Port)
	}
	if !pgIdentifierPattern.MatchString(i.Database) {
		return fmt.Errorf("instance: database %q is not a plain lower-case identifier", i.Database)
	}
	if !pgIdentifierPattern.MatchString(i.Superuser) {
		return fmt.Errorf("instance: superuser %q is not a plain lower-case identifier", i.Superuser)
	}
	if i.CredentialsFile == "" || !filepath.IsAbs(i.CredentialsFile) {
		return fmt.Errorf("instance: credentials file must be an absolute path")
	}
	return nil
}

// Credentials are the connection facts stored next to the data directory. The
// password is generated locally and never logged.
type Credentials struct {
	Version     int    `json:"version"`
	Port        int    `json:"port"`
	Database    string `json:"database"`
	Superuser   string `json:"superuser"`
	Password    string `json:"password"`
	DataDir     string `json:"data_dir"`
	ServiceName string `json:"service_name,omitempty"`
}

// DSN returns the application connection string.
func (c Credentials) DSN() string {
	return c.dsn(c.Database)
}

// AdminDSN returns a connection string to the maintenance database.
func (c Credentials) AdminDSN() string {
	return c.dsn("postgres")
}

func (c Credentials) dsn(database string) string {
	return fmt.Sprintf("postgres://%s:%s@127.0.0.1:%d/%s?sslmode=disable",
		c.Superuser, urlSafePassword(c.Password), c.Port, database)
}

// Redacted returns a description safe for logs and error messages.
func (c Credentials) Redacted() string {
	return fmt.Sprintf("postgres://%s@127.0.0.1:%d/%s (password stored in the credentials file)",
		c.Superuser, c.Port, c.Database)
}

func urlSafePassword(password string) string {
	replacer := strings.NewReplacer("%", "%25", "@", "%40", "/", "%2F", ":", "%3A", "?", "%3F", "#", "%23", "[", "%5B", "]", "%5D", "&", "%26")
	return replacer.Replace(password)
}

// PGHealth is the runtime check of the managed instance.
type PGHealth struct {
	Reachable     bool
	Version       string
	Database      string
	LedgerVersion string
	Error         string
}

// Conflict is what Detect found before an install or stop.
type Conflict struct {
	PortInUse      bool
	DataDirPresent bool
	Services       []string
	Installations  []string
}

// Usable reports whether an install can start without asking the user.
func (c Conflict) Usable() bool {
	return !c.PortInUse && !c.DataDirPresent
}

// Error renders the conflict with the three choices the card requires.
func (c Conflict) Error() string {
	var lines []string
	if c.PortInUse {
		lines = append(lines, "the port is already in use: choose another port or adopt the existing instance")
	}
	if c.DataDirPresent {
		lines = append(lines, "a PostgreSQL cluster already exists in the data directory: start it, adopt it, or remove it explicitly")
	}
	if len(c.Services) > 0 {
		lines = append(lines, "PostgreSQL services on this machine: "+strings.Join(c.Services, ", "))
	}
	if len(c.Installations) > 0 {
		lines = append(lines, "PostgreSQL installations found: "+strings.Join(c.Installations, ", "))
	}
	return strings.Join(lines, "; ")
}

// InstallOptions configures a fresh cluster.
type InstallOptions struct {
	MigrationsDir string
	Password      string
}

// UpgradeOptions configures a schema upgrade.
type UpgradeOptions struct {
	MigrationsDir string
	BackupDir     string
}

// UpgradeReport is the audit record of one upgrade attempt.
type UpgradeReport struct {
	BackupManifest string
	Applied        []string
	RolledBack     bool
	RollbackError  string
}

// UninstallReport is the audit record of one uninstall attempt.
type UninstallReport struct {
	Stopped        bool
	ServiceRemoved bool
	DataRemoved    bool
	Credentials    bool
	PortFree       bool
}

// PGManager is the lifecycle manager. Runner is replaceable so the service
// registration path can be tested without an elevated token.
type PGManager struct {
	Paths  PGPaths
	Runner CommandRunner
	Now    func() time.Time
}

// NewPGManager returns a manager using the real binaries.
func NewPGManager(paths PGPaths) *PGManager {
	return &PGManager{Paths: paths, Runner: ExecRunner{}, Now: time.Now}
}

func (m *PGManager) runner() CommandRunner {
	if m.Runner == nil {
		return ExecRunner{}
	}
	return m.Runner
}

func (m *PGManager) now() time.Time {
	if m.Now == nil {
		return time.Now()
	}
	return m.Now()
}

// Discover looks for clusters and services that would conflict with instance.
func (m *PGManager) Discover(ctx context.Context, instance Instance) (Conflict, error) {
	conflict := Conflict{
		PortInUse:      portOpen(instance.Port),
		DataDirPresent: isPostgresDataDir(instance.DataDir),
		Installations:  FindPostgresInstallations(),
	}
	services, err := m.windowsServices(ctx)
	if err != nil {
		return conflict, err
	}
	for _, service := range services {
		if strings.Contains(strings.ToLower(service), "postgres") {
			conflict.Services = append(conflict.Services, service)
		}
	}
	return conflict, nil
}

// windowsServices lists the services the current token can see. It returns no
// error when the service database is unreadable (a filtered token), because
// the rest of the conflict report is still useful.
func (m *PGManager) windowsServices(ctx context.Context) ([]string, error) {
	if runtime.GOOS != "windows" {
		return nil, nil
	}
	output, err := m.runner().Run(ctx, "sc.exe", "query", "state=", "all")
	if err != nil {
		return nil, nil
	}
	var services []string
	for _, line := range strings.Split(output, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(strings.ToUpper(trimmed), "SERVICE_NAME:") {
			continue
		}
		name := strings.TrimSpace(strings.TrimPrefix(trimmed, "SERVICE_NAME:"))
		services = append(services, name)
	}
	return services, nil
}

// Install creates a cluster, starts it, creates the application database and
// applies the migrations. Nothing is left behind when a step fails.
func (m *PGManager) Install(ctx context.Context, instance Instance, options InstallOptions) (Credentials, error) {
	if err := instance.Validate(); err != nil {
		return Credentials{}, err
	}
	conflict, err := m.Discover(ctx, instance)
	if err != nil {
		return Credentials{}, err
	}
	if !conflict.Usable() {
		return Credentials{}, fmt.Errorf("install: %s", conflict.Error())
	}
	if options.MigrationsDir == "" {
		return Credentials{}, fmt.Errorf("install: a migrations directory is required")
	}

	password := options.Password
	if password == "" {
		password, err = generatePassword()
		if err != nil {
			return Credentials{}, err
		}
	}
	if len(password) < 16 {
		return Credentials{}, fmt.Errorf("install: the generated password is too short")
	}

	root := filepath.Dir(instance.DataDir)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return Credentials{}, fmt.Errorf("install: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(root, pgLogDirName), 0o700); err != nil {
		return Credentials{}, fmt.Errorf("install: %w", err)
	}

	passwordFile, err := os.CreateTemp(root, "password-*.tmp")
	if err != nil {
		return Credentials{}, fmt.Errorf("install: %w", err)
	}
	passwordPath := passwordFile.Name()
	if _, err := passwordFile.WriteString(password + "\n"); err != nil {
		_ = passwordFile.Close()
		_ = os.Remove(passwordPath)
		return Credentials{}, fmt.Errorf("install: %w", err)
	}
	if err := passwordFile.Close(); err != nil {
		_ = os.Remove(passwordPath)
		return Credentials{}, fmt.Errorf("install: %w", err)
	}
	defer os.Remove(passwordPath)

	initCtx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	output, err := m.runner().Run(initCtx, m.Paths.Initdb,
		"-D", instance.DataDir,
		"-U", instance.Superuser,
		"--pwfile", passwordPath,
		"--auth-host=scram-sha-256",
		"--auth-local=scram-sha-256",
		"-E", "UTF8",
		"--locale=C",
	)
	if err != nil {
		_ = os.RemoveAll(instance.DataDir)
		return Credentials{}, fmt.Errorf("install: initdb: %w: %s", err, strings.TrimSpace(output))
	}

	if err := appendPostgresConfig(instance); err != nil {
		_ = os.RemoveAll(instance.DataDir)
		return Credentials{}, err
	}

	cleanup := func() {
		_ = m.stopQuietly(context.WithoutCancel(ctx), instance)
		_ = os.RemoveAll(instance.DataDir)
		_ = os.Remove(instance.DataDir)
	}
	if err := m.Start(ctx, instance); err != nil {
		cleanup()
		return Credentials{}, err
	}

	credentials := Credentials{
		Version:     1,
		Port:        instance.Port,
		Database:    instance.Database,
		Superuser:   instance.Superuser,
		Password:    password,
		DataDir:     instance.DataDir,
		ServiceName: instance.ServiceName,
	}
	if err := m.createDatabase(ctx, credentials); err != nil {
		cleanup()
		return Credentials{}, err
	}
	if err := m.migrate(ctx, credentials, options.MigrationsDir); err != nil {
		cleanup()
		return Credentials{}, err
	}
	if err := writeCredentials(instance.CredentialsFile, credentials); err != nil {
		cleanup()
		return Credentials{}, err
	}
	return credentials, nil
}

// Adopt records an existing instance without creating anything. The installer
// uses it when the user chooses "use the existing instance".
func (m *PGManager) Adopt(ctx context.Context, instance Instance, dsn string) (Credentials, error) {
	if err := instance.Validate(); err != nil {
		return Credentials{}, err
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return Credentials{}, fmt.Errorf("adopt: connect: %w", err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	cfg := conn.Config()
	credentials := Credentials{
		Version:   1,
		Database:  cfg.Database,
		Superuser: cfg.User,
		Password:  cfg.Password,
		Port:      int(cfg.Port),
	}
	if err := writeCredentials(instance.CredentialsFile, credentials); err != nil {
		return Credentials{}, err
	}
	return credentials, nil
}

func (m *PGManager) createDatabase(ctx context.Context, credentials Credentials) error {
	admin, err := pgx.Connect(ctx, credentials.AdminDSN())
	if err != nil {
		return fmt.Errorf("install: create database: %w", err)
	}
	defer func() { _ = admin.Close(context.WithoutCancel(ctx)) }()
	if _, err := admin.Exec(ctx, `create database `+pgx.Identifier{credentials.Database}.Sanitize()); err != nil {
		return fmt.Errorf("install: create database %q: %w", credentials.Database, err)
	}
	return nil
}

func (m *PGManager) migrate(ctx context.Context, credentials Credentials, migrationsDir string) error {
	conn, err := pgx.Connect(ctx, credentials.DSN())
	if err != nil {
		return fmt.Errorf("install: connect to the new database: %w", err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	if _, err := database.Migrate(ctx, conn, migrationsDir); err != nil {
		return fmt.Errorf("install: migrate: %w", err)
	}
	return nil
}

// Start starts the cluster and waits until it accepts connections. It returns
// nil when the cluster is already listening, so the logon autostart path stays
// idempotent.
func (m *PGManager) Start(ctx context.Context, instance Instance) error {
	if !isPostgresDataDir(instance.DataDir) {
		return fmt.Errorf("start: %s is not a PostgreSQL data directory", instance.DataDir)
	}
	if portOpen(instance.Port) {
		return nil
	}
	logPath := filepath.Join(filepath.Dir(instance.DataDir), pgLogDirName, pgLogFileName)
	startCtx, cancel := context.WithTimeout(ctx, DefaultPGReadyTimeout+30*time.Second)
	defer cancel()
	if output, err := m.runner().Run(startCtx, m.Paths.PgCtl,
		"-D", instance.DataDir,
		"-w", "-t", "120",
		"-l", logPath,
		"start",
	); err != nil {
		return fmt.Errorf("start: pg_ctl start: %w: %s (log: %s)", err, strings.TrimSpace(output), logPath)
	}
	deadline := m.now().Add(DefaultPGReadyTimeout)
	for {
		if portOpen(instance.Port) {
			return nil
		}
		if m.now().After(deadline) {
			return fmt.Errorf("start: the port %d did not open within %s (log: %s)", instance.Port, DefaultPGReadyTimeout, logPath)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

func (m *PGManager) stopQuietly(ctx context.Context, instance Instance) error {
	if !isPostgresDataDir(instance.DataDir) {
		return nil
	}
	stopCtx, cancel := context.WithTimeout(ctx, DefaultPGStopTimeout)
	defer cancel()
	_, err := m.runner().Run(stopCtx, m.Paths.PgCtl, "-D", instance.DataDir, "-m", "fast", "-w", "-t", "90", "stop")
	return err
}

// Stop stops the cluster. Stopping an already stopped cluster is a no-op, so
// the uninstaller and autostart paths are idempotent. When deleteData is set the
// cluster files are removed afterwards (after a guard proves the directory
// really is a cluster).
func (m *PGManager) Stop(ctx context.Context, instance Instance, deleteData bool) error {
	if isPostgresDataDir(instance.DataDir) && portOpen(instance.Port) {
		if err := m.stopQuietly(ctx, instance); err != nil {
			return fmt.Errorf("stop: %w", err)
		}
		deadline := m.now().Add(15 * time.Second)
		for portOpen(instance.Port) {
			if m.now().After(deadline) {
				return fmt.Errorf("stop: the port %d is still open", instance.Port)
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	if deleteData {
		if _, err := os.Stat(instance.DataDir); errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return removePostgresDataDir(instance.DataDir)
	}
	return nil
}

// Health connects with the stored credentials and reports the server version
// and the migration ledger.
func (m *PGManager) Health(ctx context.Context, instance Instance) PGHealth {
	health := PGHealth{Database: instance.Database}
	credentials, err := readCredentials(instance.CredentialsFile)
	if err != nil {
		health.Error = err.Error()
		return health
	}
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	conn, err := pgx.Connect(checkCtx, credentials.DSN())
	if err != nil {
		health.Error = err.Error()
		return health
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	if err := conn.QueryRow(checkCtx, `select current_setting('server_version'), current_database()`).Scan(&health.Version, &health.Database); err != nil {
		health.Error = err.Error()
		return health
	}
	health.Reachable = true
	_ = conn.QueryRow(checkCtx, `select coalesce(max(version), '') from schema_migrations`).Scan(&health.LedgerVersion)
	return health
}

// Connect opens the application database with the stored credentials.
func (m *PGManager) Connect(ctx context.Context, instance Instance) (*pgx.Conn, error) {
	credentials, err := readCredentials(instance.CredentialsFile)
	if err != nil {
		return nil, err
	}
	return pgx.Connect(ctx, credentials.DSN())
}

// Backup dumps the cluster database and returns the dump path and manifest.
func (m *PGManager) Backup(ctx context.Context, instance Instance, dir string) (string, database.BackupManifest, error) {
	conn, err := m.Connect(ctx, instance)
	if err != nil {
		return "", database.BackupManifest{}, fmt.Errorf("backup: %w", err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	manifest, err := database.BackupDatabase(ctx, conn, dir)
	if err != nil {
		return "", database.BackupManifest{}, err
	}
	return filepath.Join(dir, manifest.Dump), manifest, nil
}

// Upgrade backs up the database, applies the migrations, and restores the
// backup when a migration fails. The dump is kept either way.
func (m *PGManager) Upgrade(ctx context.Context, instance Instance, options UpgradeOptions) (UpgradeReport, error) {
	report := UpgradeReport{}
	if options.MigrationsDir == "" || options.BackupDir == "" {
		return report, fmt.Errorf("upgrade: migrations and backup directories are required")
	}
	health := m.Health(ctx, instance)
	if !health.Reachable {
		return report, fmt.Errorf("upgrade: the instance is not reachable: %s", health.Error)
	}

	conn, err := m.Connect(ctx, instance)
	if err != nil {
		return report, fmt.Errorf("upgrade: %w", err)
	}
	manifest, err := database.BackupDatabase(ctx, conn, options.BackupDir)
	_ = conn.Close(context.WithoutCancel(ctx))
	if err != nil {
		return report, fmt.Errorf("upgrade: the pre-upgrade backup failed: %w", err)
	}
	report.BackupManifest = filepath.Join(options.BackupDir, manifest.Dump+".json")

	conn, err = m.Connect(ctx, instance)
	if err != nil {
		return report, fmt.Errorf("upgrade: %w", err)
	}
	applied, migrateErr := database.Migrate(ctx, conn, options.MigrationsDir)
	_ = conn.Close(context.WithoutCancel(ctx))
	if migrateErr == nil {
		report.Applied = applied
		return report, nil
	}

	rollbackErr := m.restoreInPlace(ctx, instance, report.BackupManifest)
	report.RolledBack = rollbackErr == nil
	if rollbackErr != nil {
		report.RollbackError = rollbackErr.Error()
		return report, fmt.Errorf("upgrade: migrate failed: %w; rollback failed: %v", migrateErr, rollbackErr)
	}
	return report, fmt.Errorf("upgrade: migrate failed and the instance was restored from %s: %w", report.BackupManifest, migrateErr)
}

func (m *PGManager) restoreInPlace(ctx context.Context, instance Instance, manifestPath string) error {
	credentials, err := readCredentials(instance.CredentialsFile)
	if err != nil {
		return err
	}
	admin, err := pgx.Connect(ctx, credentials.AdminDSN())
	if err != nil {
		return fmt.Errorf("rollback: connect: %w", err)
	}
	defer func() { _ = admin.Close(context.WithoutCancel(ctx)) }()
	if err := database.DropDatabase(ctx, admin, instance.Database); err != nil {
		return err
	}
	if _, err := database.RestoreDatabase(ctx, admin, manifestPath, instance.Database); err != nil {
		return err
	}
	return nil
}

// Uninstall stops the cluster, removes the optional service and deletes the
// data directory only when the caller asked for it. The report is the audit
// trail: it records which leftovers are gone.
func (m *PGManager) Uninstall(ctx context.Context, instance Instance, deleteData bool) (UninstallReport, error) {
	report := UninstallReport{}
	if isPostgresDataDir(instance.DataDir) {
		if err := m.stopQuietly(ctx, instance); err != nil {
			return report, fmt.Errorf("uninstall: stop: %w", err)
		}
		report.Stopped = true
	}
	if instance.ServiceName != "" {
		if err := m.UnregisterService(ctx, instance); err != nil {
			return report, err
		}
		report.ServiceRemoved = true
	}
	if deleteData {
		if err := removePostgresDataDir(instance.DataDir); err != nil {
			return report, err
		}
		report.DataRemoved = true
	}
	if err := os.Remove(instance.CredentialsFile); err == nil {
		report.Credentials = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return report, fmt.Errorf("uninstall: credentials: %w", err)
	}
	deadline := m.now().Add(15 * time.Second)
	for portOpen(instance.Port) {
		if m.now().After(deadline) {
			return report, fmt.Errorf("uninstall: the port %d is still open", instance.Port)
		}
		time.Sleep(200 * time.Millisecond)
	}
	report.PortFree = true
	return report, nil
}

// RegisterService registers a Windows service for the cluster. It needs an
// elevated token; the default install path uses process mode instead.
func (m *PGManager) RegisterService(ctx context.Context, instance Instance) error {
	if runtime.GOOS != "windows" {
		return fmt.Errorf("service registration is only supported on Windows")
	}
	if instance.ServiceName == "" {
		return fmt.Errorf("service: no service name was configured")
	}
	output, err := m.runner().Run(ctx, m.Paths.PgCtl, "register",
		"-N", instance.ServiceName,
		"-D", instance.DataDir,
		"-S", "auto",
		"-w",
	)
	if err != nil {
		return fmt.Errorf("service: pg_ctl register: %w: %s", err, strings.TrimSpace(output))
	}
	return nil
}

// UnregisterService removes the service and fails when it is still listed.
func (m *PGManager) UnregisterService(ctx context.Context, instance Instance) error {
	if instance.ServiceName == "" {
		return nil
	}
	output, err := m.runner().Run(ctx, m.Paths.PgCtl, "unregister", "-N", instance.ServiceName)
	if err != nil {
		return fmt.Errorf("service: pg_ctl unregister: %w: %s", err, strings.TrimSpace(output))
	}
	services, listErr := m.windowsServices(ctx)
	if listErr != nil {
		return listErr
	}
	for _, service := range services {
		if strings.EqualFold(service, instance.ServiceName) {
			return fmt.Errorf("service: %s is still registered after unregister", instance.ServiceName)
		}
	}
	return nil
}

func appendPostgresConfig(instance Instance) error {
	configPath := filepath.Join(instance.DataDir, "postgresql.conf")
	file, err := os.OpenFile(configPath, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("install: write postgresql.conf: %w", err)
	}
	defer func() { _ = file.Close() }()
	block := fmt.Sprintf("\n# errkb managed cluster (EKB-051)\nport = %d\nlisten_addresses = '127.0.0.1'\n", instance.Port)
	if _, err := file.WriteString(block); err != nil {
		return fmt.Errorf("install: write postgresql.conf: %w", err)
	}
	return nil
}

func isPostgresDataDir(dir string) bool {
	if dir == "" {
		return false
	}
	info, err := os.Stat(filepath.Join(dir, pgVersionFileName))
	return err == nil && !info.IsDir()
}

// removePostgresDataDir deletes a cluster directory only after proving it is
// one, so a wrong path can never delete unrelated files.
func removePostgresDataDir(dir string) error {
	clean := filepath.Clean(dir)
	if !isPostgresDataDir(clean) {
		return fmt.Errorf("refusing to delete %q: it is not a PostgreSQL data directory", dir)
	}
	if clean == filepath.VolumeName(clean)+string(os.PathSeparator) || len(clean) < 8 {
		return fmt.Errorf("refusing to delete %q: the path is too broad", dir)
	}
	if err := os.RemoveAll(clean); err != nil {
		return fmt.Errorf("delete data directory: %w", err)
	}
	if _, err := os.Stat(clean); err == nil {
		return fmt.Errorf("delete data directory: %q still exists", clean)
	}
	return nil
}

func portOpen(port int) bool {
	if port <= 0 {
		return false
	}
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 400*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// FreePort asks the operating system for a port the host is not using. Tests
// use it instead of hard-coding a value; the installer exposes the same choice.
func FreePort() (int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer func() { _ = listener.Close() }()
	return listener.Addr().(*net.TCPAddr).Port, nil
}

func generatePassword() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("credentials: generate password: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

func writeCredentials(path string, credentials Credentials) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("credentials: %w", err)
	}
	encoded, err := json.MarshalIndent(credentials, "", "  ")
	if err != nil {
		return fmt.Errorf("credentials: %w", err)
	}
	temp := path + ".tmp"
	if err := os.WriteFile(temp, append(encoded, '\n'), 0o600); err != nil {
		return fmt.Errorf("credentials: %w", err)
	}
	if err := os.Rename(temp, path); err != nil {
		_ = os.Remove(temp)
		return fmt.Errorf("credentials: %w", err)
	}
	return nil
}

// ReadCredentials loads the file the installer writes next to the data
// directory (EKB-051). It is exported so the CLI can turn it into a DSN without
// parsing JSON itself; the password never leaves the process.
func ReadCredentials(path string) (Credentials, error) {
	return readCredentials(path)
}

func readCredentials(path string) (Credentials, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Credentials{}, fmt.Errorf("credentials: %w", err)
	}
	var credentials Credentials
	if err := json.Unmarshal(raw, &credentials); err != nil {
		return Credentials{}, fmt.Errorf("credentials: %w", err)
	}
	if credentials.Password == "" || credentials.Port <= 0 || credentials.Database == "" {
		return Credentials{}, fmt.Errorf("credentials: the file %s is incomplete", path)
	}
	return credentials, nil
}
