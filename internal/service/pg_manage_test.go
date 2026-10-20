package service_test

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/service"
)

// postgresBinaries skips the test when the host has no PostgreSQL installation.
func postgresBinaries(t *testing.T) service.PGPaths {
	t.Helper()
	paths, err := service.ResolvePostgresBinaries()
	if err != nil {
		t.Skipf("PostgreSQL is not installed on this host: %v", err)
	}
	return paths
}

func migrationsDir(t *testing.T) string {
	t.Helper()
	return filepath.Join("..", "..", "db", "migrations")
}

func newTestInstance(t *testing.T) (service.Instance, string) {
	t.Helper()
	port, err := service.FreePort()
	if err != nil {
		t.Fatalf("free port: %v", err)
	}
	root := t.TempDir()
	instance := service.DefaultInstance(root)
	instance.Port = port
	return instance, root
}

func portOpen(port int) bool {
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), 400*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func installInstance(t *testing.T, manager *service.PGManager, instance service.Instance) service.Credentials {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	credentials, err := manager.Install(ctx, instance, service.InstallOptions{MigrationsDir: migrationsDir(t)})
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cleanupCancel()
		_ = manager.Stop(cleanupCtx, instance, false)
		_ = os.RemoveAll(instance.DataDir)
	})
	return credentials
}

func TestPGManagerInstallHealthBackupAndUninstall(t *testing.T) {
	paths := postgresBinaries(t)
	manager := service.NewPGManager(paths)
	instance, root := newTestInstance(t)

	conflict, err := manager.Discover(context.Background(), instance)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if !conflict.Usable() {
		t.Fatalf("a fresh temp root reported a conflict: %s", conflict.Error())
	}

	credentials := installInstance(t, manager, instance)
	if credentials.Password == "" || credentials.Password == "postgres" || len(credentials.Password) < 16 {
		t.Fatalf("the generated password is weak or missing")
	}
	if !strings.Contains(credentials.Redacted(), "password stored") {
		t.Fatalf("the redacted description leaks no marker: %q", credentials.Redacted())
	}
	if strings.Contains(credentials.Redacted(), credentials.Password) {
		t.Fatalf("the redacted description contains the password")
	}
	info, err := os.Stat(instance.CredentialsFile)
	if err != nil {
		t.Fatalf("credentials file: %v", err)
	}
	if info.IsDir() {
		t.Fatalf("credentials path is a directory")
	}
	raw, err := os.ReadFile(instance.CredentialsFile)
	if err != nil {
		t.Fatalf("read credentials: %v", err)
	}
	if !strings.Contains(string(raw), credentials.Password) {
		t.Fatalf("the credentials file does not contain the password")
	}

	health := manager.Health(context.Background(), instance)
	if !health.Reachable {
		t.Fatalf("the installed instance is not reachable: %s", health.Error)
	}
	if health.Version == "" || health.LedgerVersion == "" {
		t.Fatalf("health is incomplete: %+v", health)
	}

	backupDir := filepath.Join(root, "backups")
	dumpPath, manifest, err := manager.Backup(context.Background(), instance, backupDir)
	if err != nil {
		t.Fatalf("backup: %v", err)
	}
	if dumpPath == "" || manifest.SHA256 == "" {
		t.Fatalf("backup returned an incomplete manifest: %+v", manifest)
	}
	if _, err := os.Stat(dumpPath); err != nil {
		t.Fatalf("dump does not exist: %v", err)
	}

	// Stop keeps the data: the port closes and the directory stays.
	if err := manager.Stop(context.Background(), instance, false); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if _, err := os.Stat(instance.DataDir); err != nil {
		t.Fatalf("stop deleted the data directory: %v", err)
	}
	if portOpen(instance.Port) {
		t.Fatalf("the port is still open after stop")
	}

	if err := manager.Start(context.Background(), instance); err != nil {
		t.Fatalf("restart: %v", err)
	}
	if got := manager.Health(context.Background(), instance); !got.Reachable {
		t.Fatalf("the restarted instance is not reachable: %s", got.Error)
	}

	report, err := manager.Uninstall(context.Background(), instance, true)
	if err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if !report.Stopped || !report.DataRemoved || !report.Credentials || !report.PortFree {
		t.Fatalf("the uninstall report is incomplete: %+v", report)
	}
	if _, err := os.Stat(instance.DataDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the data directory survived a delete-data uninstall: %v", err)
	}
	if portOpen(instance.Port) {
		t.Fatalf("the port is still open after uninstall")
	}
}

func TestPGManagerUninstallKeepsDataWhenAsked(t *testing.T) {
	paths := postgresBinaries(t)
	manager := service.NewPGManager(paths)
	instance, _ := newTestInstance(t)
	installInstance(t, manager, instance)

	report, err := manager.Uninstall(context.Background(), instance, false)
	if err != nil {
		t.Fatalf("uninstall: %v", err)
	}
	if report.DataRemoved {
		t.Fatalf("the report claims data removal although keepData was chosen")
	}
	if !report.Stopped || !report.PortFree {
		t.Fatalf("the uninstall report is incomplete: %+v", report)
	}
	if _, err := os.Stat(instance.DataDir); err != nil {
		t.Fatalf("the data directory was deleted although keepData was chosen: %v", err)
	}
	if _, err := os.Stat(instance.CredentialsFile); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the credentials file survived the uninstall: %v", err)
	}
}

func TestPGManagerInstallRefusesABusyPort(t *testing.T) {
	paths := postgresBinaries(t)
	manager := service.NewPGManager(paths)
	instance, _ := newTestInstance(t)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = listener.Close() }()
	instance.Port = listener.Addr().(*net.TCPAddr).Port

	_, err = manager.Install(context.Background(), instance, service.InstallOptions{MigrationsDir: migrationsDir(t)})
	if err == nil {
		t.Fatalf("install accepted a busy port")
	}
	if !strings.Contains(err.Error(), "already in use") {
		t.Fatalf("the conflict message does not explain the port: %v", err)
	}
	if _, err := os.Stat(instance.DataDir); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("a refused install left a data directory behind")
	}
}

func TestPGManagerUpgradeRollsBackWhenAMigrationFails(t *testing.T) {
	paths := postgresBinaries(t)
	manager := service.NewPGManager(paths)
	instance, root := newTestInstance(t)
	credentials := installInstance(t, manager, instance)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	conn, err := pgx.Connect(ctx, credentials.DSN())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if _, err := conn.Exec(ctx, `insert into connectors (name, channel, source, actor) values ('upgrade-proof', 'B', 'test', 'test')`); err != nil {
		t.Fatalf("seed connector: %v", err)
	}
	var before int
	if err := conn.QueryRow(ctx, `select count(*) from connectors where name = 'upgrade-proof'`).Scan(&before); err != nil {
		t.Fatalf("count connectors: %v", err)
	}
	var ledgerBefore string
	if err := conn.QueryRow(ctx, `select coalesce(max(version), '') from schema_migrations`).Scan(&ledgerBefore); err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	_ = conn.Close(ctx)

	brokenDir := filepath.Join(root, "broken-migrations")
	if err := os.MkdirAll(brokenDir, 0o700); err != nil {
		t.Fatalf("broken migrations dir: %v", err)
	}
	entries, err := os.ReadDir(migrationsDir(t))
	if err != nil {
		t.Fatalf("read migrations: %v", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(migrationsDir(t), entry.Name()))
		if err != nil {
			t.Fatalf("read migration %s: %v", entry.Name(), err)
		}
		if err := os.WriteFile(filepath.Join(brokenDir, entry.Name()), raw, 0o600); err != nil {
			t.Fatalf("copy migration %s: %v", entry.Name(), err)
		}
	}
	if err := os.WriteFile(filepath.Join(brokenDir, "0009_broken.sql"), []byte("create table broken ("), 0o600); err != nil {
		t.Fatalf("write broken migration: %v", err)
	}

	report, err := manager.Upgrade(ctx, instance, service.UpgradeOptions{
		MigrationsDir: brokenDir,
		BackupDir:     filepath.Join(root, "upgrade-backups"),
	})
	if err == nil {
		t.Fatalf("upgrade succeeded with a broken migration")
	}
	if !report.RolledBack {
		t.Fatalf("the upgrade did not roll back: %+v (%v)", report, err)
	}
	if report.BackupManifest == "" {
		t.Fatalf("the upgrade did not record the pre-upgrade backup")
	}
	if _, err := os.Stat(report.BackupManifest); err != nil {
		t.Fatalf("the pre-upgrade manifest is missing: %v", err)
	}

	conn, err = pgx.Connect(ctx, credentials.DSN())
	if err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	defer func() { _ = conn.Close(context.WithoutCancel(ctx)) }()
	var after int
	if err := conn.QueryRow(ctx, `select count(*) from connectors where name = 'upgrade-proof'`).Scan(&after); err != nil {
		t.Fatalf("count after rollback: %v", err)
	}
	if after != before {
		t.Fatalf("the rollback lost rows: before=%d after=%d", before, after)
	}
	var ledgerAfter string
	if err := conn.QueryRow(ctx, `select coalesce(max(version), '') from schema_migrations`).Scan(&ledgerAfter); err != nil {
		t.Fatalf("read ledger after rollback: %v", err)
	}
	if ledgerAfter != ledgerBefore {
		t.Fatalf("the rollback changed the ledger: before=%q after=%q", ledgerBefore, ledgerAfter)
	}
}

func TestPGManagerUpgradeWithoutNewMigrationsIsANoop(t *testing.T) {
	paths := postgresBinaries(t)
	manager := service.NewPGManager(paths)
	instance, root := newTestInstance(t)
	installInstance(t, manager, instance)

	report, err := manager.Upgrade(context.Background(), instance, service.UpgradeOptions{
		MigrationsDir: migrationsDir(t),
		BackupDir:     filepath.Join(root, "noop-backups"),
	})
	if err != nil {
		t.Fatalf("upgrade without new migrations: %v", err)
	}
	if report.RolledBack {
		t.Fatalf("the upgrade rolled back although nothing failed: %+v", report)
	}
	if len(report.Applied) != 0 {
		t.Fatalf("the upgrade applied migrations that were already in the ledger: %v", report.Applied)
	}
	if report.BackupManifest == "" {
		t.Fatalf("the upgrade did not back up before checking the migrations")
	}
}

// fakeRunner records commands instead of executing them.
type fakeRunner struct {
	mu       sync.Mutex
	commands []string
	scOutput string
}

func (f *fakeRunner) Run(_ context.Context, name string, args ...string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands = append(f.commands, strings.Join(append([]string{name}, args...), " "))
	if strings.HasSuffix(strings.ToLower(name), "sc.exe") {
		return f.scOutput, nil
	}
	return "", nil
}

func (f *fakeRunner) joined() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return strings.Join(f.commands, "\n")
}

func TestPGManagerRegistersAndVerifiesTheService(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("the service registration path is Windows-only")
	}
	paths := service.PGPaths{PgCtl: "pg_ctl.exe"}
	root := t.TempDir()
	instance := service.DefaultInstance(root)
	instance.ServiceName = "errkb-test-service"

	runner := &fakeRunner{scOutput: "SERVICE_NAME: postgresql-x64-16\n"}
	manager := service.NewPGManager(paths)
	manager.Runner = runner

	if err := manager.RegisterService(context.Background(), instance); err != nil {
		t.Fatalf("register: %v", err)
	}
	if !strings.Contains(runner.joined(), "register -N errkb-test-service -D") {
		t.Fatalf("the register command is wrong:\n%s", runner.joined())
	}

	if err := manager.UnregisterService(context.Background(), instance); err != nil {
		t.Fatalf("unregister: %v", err)
	}
	if !strings.Contains(runner.joined(), "unregister -N errkb-test-service") {
		t.Fatalf("the unregister command is wrong:\n%s", runner.joined())
	}

	// A service that is still listed after unregister is an orphan: fail.
	runner.scOutput = "SERVICE_NAME: errkb-test-service\n"
	if err := manager.UnregisterService(context.Background(), instance); err == nil {
		t.Fatalf("unregister accepted a service that is still registered")
	}
}
