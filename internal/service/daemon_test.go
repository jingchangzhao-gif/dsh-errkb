//go:build windows

package service

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/database"
)

// newDaemonSchema creates an empty database schema and a pool whose connections
// use it as their search_path.
func newDaemonSchema(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()
	dsn := os.Getenv("ERRKB_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skipf("set ERRKB_TEST_DATABASE_URL to run the daemon tests")
	}
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to the test database: %v", err)
	}
	schema := fmt.Sprintf("errkb_service_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "create schema "+quoted); err != nil {
		_ = admin.Close(ctx)
		t.Fatalf("create the test schema: %v", err)
	}

	poolConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse the test DSN: %v", err)
	}
	poolConfig.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "set search_path to "+quoted+", public")
		return err
	}
	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		t.Fatalf("open the test pool: %v", err)
	}
	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(context.Background(), "drop schema if exists "+quoted+" cascade")
		_ = admin.Close(context.Background())
	})
	return pool, schema
}

// migratedDaemonPool creates a schema with the full migration chain applied.
func migratedDaemonPool(t *testing.T) (*pgxpool.Pool, string) {
	t.Helper()
	pool, schema := newDaemonSchema(t)
	ctx := context.Background()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire a migration connection: %v", err)
	}
	defer conn.Release()
	if _, err := database.Migrate(ctx, conn.Conn(), "../../db/migrations"); err != nil {
		t.Fatalf("migrate the test schema: %v", err)
	}
	return pool, schema
}

func daemonTestConfig(t *testing.T, pool *pgxpool.Pool) DaemonConfig {
	t.Helper()
	return DaemonConfig{
		Pool:           pool,
		MigrationsDir:  "../../db/migrations",
		LockName:       fmt.Sprintf(`Local\errkb-service-test-%d`, time.Now().UnixNano()),
		HealthPath:     filepath.Join(t.TempDir(), "health.json"),
		LogPath:        filepath.Join(t.TempDir(), "daemon.log"),
		LogMaxBytes:    64 << 10,
		HealthInterval: 50 * time.Millisecond,
	}
}

func startTestDaemon(t *testing.T, config DaemonConfig) *Daemon {
	t.Helper()
	daemon, err := StartDaemon(context.Background(), config)
	if err != nil {
		t.Fatalf("start the daemon: %v", err)
	}
	t.Cleanup(func() { stopTestDaemon(t, daemon) })
	return daemon
}

func stopTestDaemon(t *testing.T, daemon *Daemon) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := daemon.Shutdown(ctx); err != nil {
		t.Fatalf("shut the daemon down: %v", err)
	}
}

func waitDaemonClosing(t *testing.T, daemon *Daemon, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		daemon.mu.Lock()
		closing := daemon.closing
		daemon.mu.Unlock()
		if closing {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the daemon never started shutting down")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestDaemonRefusesSecondInstance(t *testing.T) {
	pool, _ := migratedDaemonPool(t)
	config := daemonTestConfig(t, pool)
	first := startTestDaemon(t, config)

	second, err := StartDaemon(context.Background(), config)
	if err == nil {
		stopTestDaemon(t, second)
		t.Fatal("a second daemon instance started while the first held the mutex")
	}
	if !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("second instance error = %v, want ErrAlreadyRunning", err)
	}
	if !strings.Contains(err.Error(), config.LockName) {
		t.Fatalf("second instance error does not name the mutex: %v", err)
	}

	stopTestDaemon(t, first)
	third, err := StartDaemon(context.Background(), config)
	if err != nil {
		t.Fatalf("start after the first instance stopped: %v", err)
	}
	stopTestDaemon(t, third)
}

func TestDaemonRestartsAfterACrash(t *testing.T) {
	name := fmt.Sprintf(`Local\errkb-service-crash-%d`, time.Now().UnixNano())
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("locate the test executable: %v", err)
	}
	command := exec.Command(executable, "-test.run=^TestDaemonInstanceLockHelper$", "-test.v")
	command.Env = append(os.Environ(), "ERRKB_DAEMON_HELPER_LOCK="+name)
	command.Stderr = os.Stderr
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatalf("capture the helper stdout: %v", err)
	}
	if err := command.Start(); err != nil {
		t.Fatalf("start the lock helper: %v", err)
	}
	t.Cleanup(func() {
		_ = command.Process.Kill()
		_ = command.Wait()
	})

	ready := make(chan struct{})
	go func() {
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			if scanner.Text() == "HELPER READY" {
				close(ready)
				return
			}
		}
	}()
	select {
	case <-ready:
	case <-time.After(20 * time.Second):
		t.Fatal("the lock helper never reported that it held the instance mutex")
	}

	if lock, err := acquireInstanceLock(name); err == nil {
		lock.Release()
		t.Fatal("acquired the instance mutex while the helper held it")
	} else if !errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("acquire while the helper holds it = %v, want ErrAlreadyRunning", err)
	}

	if err := command.Process.Kill(); err != nil {
		t.Fatalf("kill the lock helper: %v", err)
	}
	_ = command.Wait()

	deadline := time.Now().Add(20 * time.Second)
	var lock *instanceLock
	for {
		lock, err = acquireInstanceLock(name)
		if err == nil {
			break
		}
		if !errors.Is(err, ErrAlreadyRunning) {
			t.Fatalf("acquire after the helper crash = %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("the kernel kept the instance mutex after the helper crash: %v", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	lock.Release()

	pool, _ := migratedDaemonPool(t)
	config := daemonTestConfig(t, pool)
	config.LockName = name
	restarted := startTestDaemon(t, config)
	stopTestDaemon(t, restarted)
}

// TestDaemonInstanceLockHelper is the child process of
// TestDaemonRestartsAfterACrash: it takes the instance mutex, announces itself
// and blocks until the parent kills it.
func TestDaemonInstanceLockHelper(t *testing.T) {
	name := os.Getenv("ERRKB_DAEMON_HELPER_LOCK")
	if name == "" {
		t.Skip("this test is only the child process of TestDaemonRestartsAfterACrash")
	}
	lock, err := acquireInstanceLock(name)
	if err != nil {
		fmt.Printf("HELPER ERROR %v\n", err)
		return
	}
	_ = lock
	fmt.Println("HELPER READY")
	select {}
}

func TestDaemonGracefulStopWaitsForInFlightWork(t *testing.T) {
	pool, _ := migratedDaemonPool(t)
	daemon := startTestDaemon(t, daemonTestConfig(t, pool))
	ctx := context.Background()

	release, err := daemon.BeginRequest()
	if err != nil {
		t.Fatalf("begin the in-flight request: %v", err)
	}

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin the in-flight transaction: %v", err)
	}
	const fingerprint = "0000000000aa" // twelve lowercase hex characters
	if _, err := tx.Exec(ctx,
		`insert into issues (fingerprint, title, category, source, actor) values ($1, 'drained write', 'tool', 'test', 'test')`,
		fingerprint); err != nil {
		t.Fatalf("write inside the in-flight transaction: %v", err)
	}

	stopped := make(chan error, 1)
	go func() { stopped <- daemon.Shutdown(context.Background()) }()
	waitDaemonClosing(t, daemon, 5*time.Second)

	if _, err := daemon.BeginRequest(); !errors.Is(err, ErrShuttingDown) {
		t.Fatalf("BeginRequest during shutdown = %v, want ErrShuttingDown", err)
	}
	select {
	case err := <-stopped:
		t.Fatalf("shutdown returned while work was in flight: %v", err)
	case <-time.After(300 * time.Millisecond):
	}

	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit the in-flight transaction: %v", err)
	}
	release()

	select {
	case err := <-stopped:
		if err != nil {
			t.Fatalf("shutdown after the drain: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("shutdown did not finish after the in-flight work ended")
	}

	var count int
	if err := pool.QueryRow(ctx, `select count(*) from issues where fingerprint = $1`, fingerprint).Scan(&count); err != nil {
		t.Fatalf("count the committed row: %v", err)
	}
	if count != 1 {
		t.Fatalf("committed rows after shutdown = %d, want 1 (no half-written transaction)", count)
	}
}

func TestDaemonHealthIsMachineReadable(t *testing.T) {
	pool, _ := migratedDaemonPool(t)
	config := daemonTestConfig(t, pool)
	config.QueueBacklog = func() (int, error) { return 7, nil }
	daemon := startTestDaemon(t, config)
	ctx := context.Background()

	health, err := daemon.RefreshHealth(ctx)
	if err != nil {
		t.Fatalf("refresh the health document: %v", err)
	}
	if health.Version != HealthVersion {
		t.Fatalf("health version = %d, want %d", health.Version, HealthVersion)
	}
	if health.Status != healthOK {
		t.Fatalf("health status = %q, want %q", health.Status, healthOK)
	}
	if !health.Database.Connected {
		t.Fatalf("health reports the database as disconnected: %+v", health.Database)
	}

	migrations, err := database.LoadMigrations(config.MigrationsDir)
	if err != nil {
		t.Fatalf("load the expected migrations: %v", err)
	}
	expected := migrations[len(migrations)-1].Version
	if !health.Migrations.OK || health.Migrations.Expected != expected || health.Migrations.Current != expected {
		t.Fatalf("health migration status = %+v, want current and expected %q", health.Migrations, expected)
	}
	if health.Queue.Backlog != 7 {
		t.Fatalf("health queue backlog = %d, want the configured 7", health.Queue.Backlog)
	}
	if health.Connections.Total < 1 {
		t.Fatalf("health connection count = %d, want at least one", health.Connections.Total)
	}
	if health.Requests.InFlight != 0 {
		t.Fatalf("health in-flight count = %d, want 0", health.Requests.InFlight)
	}

	raw, err := os.ReadFile(config.HealthPath)
	if err != nil {
		t.Fatalf("read the health document: %v", err)
	}
	var decoded Health
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("the health document is not valid JSON: %v", err)
	}
	if decoded.Status != healthOK || decoded.Migrations.Expected != expected || decoded.Queue.Backlog != 7 {
		t.Fatalf("decoded health document = %+v", decoded)
	}

	release, err := daemon.BeginRequest()
	if err != nil {
		t.Fatalf("begin a request: %v", err)
	}
	inFlight, err := daemon.RefreshHealth(ctx)
	if err != nil {
		t.Fatalf("refresh while a request is in flight: %v", err)
	}
	if inFlight.Requests.InFlight != 1 {
		t.Fatalf("health in-flight count = %d, want 1", inFlight.Requests.InFlight)
	}
	release()
}

func TestDaemonRejectsAnUnmigratedSchema(t *testing.T) {
	pool, _ := newDaemonSchema(t) // intentionally not migrated
	config := daemonTestConfig(t, pool)

	daemon, err := StartDaemon(context.Background(), config)
	if err == nil {
		stopTestDaemon(t, daemon)
		t.Fatal("the daemon started on a schema without a migration ledger")
	}
	if !strings.Contains(err.Error(), "migration") {
		t.Fatalf("startup error does not name the migration check: %v", err)
	}
	if errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("the failed startup leaked the instance mutex: %v", err)
	}
	if _, err := StartDaemon(context.Background(), config); errors.Is(err, ErrAlreadyRunning) {
		t.Fatalf("a second failed startup hit a leaked instance mutex: %v", err)
	}
}

func TestDaemonLogsAreRedactedCappedAndOffStdout(t *testing.T) {
	pool, _ := migratedDaemonPool(t)
	config := daemonTestConfig(t, pool)
	config.LogMaxBytes = 1024
	daemon := startTestDaemon(t, config)

	secret := "abc" + "def" + "ghi" + "jkl" + "mno" + "pqr"
	daemon.Log("info", "request failed with Bear"+"er "+secret)

	logged, err := os.ReadFile(config.LogPath)
	if err != nil {
		t.Fatalf("read the daemon log: %v", err)
	}
	if strings.Contains(string(logged), secret) {
		t.Fatal("a credential-shaped token reached the log; the redaction must run before the write")
	}
	if !strings.Contains(string(logged), "<secret>") {
		t.Fatalf("the redaction placeholder is missing from the log: %q", string(logged))
	}

	// stdout is the MCP stdio channel: the daemon must never write there.
	original := os.Stdout
	readPipe, writePipe, err := os.Pipe()
	if err != nil {
		t.Fatalf("create the stdout pipe: %v", err)
	}
	os.Stdout = writePipe
	daemon.Log("info", "stdout probe")
	os.Stdout = original
	if err := writePipe.Close(); err != nil {
		t.Fatalf("close the stdout pipe: %v", err)
	}
	var leaked bytes.Buffer
	if _, err := io.Copy(&leaked, readPipe); err != nil {
		t.Fatalf("read the captured stdout: %v", err)
	}
	_ = readPipe.Close()
	if leaked.Len() != 0 {
		t.Fatalf("the daemon wrote to stdout: %q", leaked.String())
	}

	// Flood the log: it must rotate instead of growing past the cap.
	for i := 0; i < 200; i++ {
		daemon.Log("info", strings.Repeat("x", 96))
	}
	if _, err := os.Stat(config.LogPath + ".1"); err != nil {
		t.Fatalf("the log never rotated under the %d-byte cap: %v", config.LogMaxBytes, err)
	}
	for _, path := range []string{config.LogPath, config.LogPath + ".1"} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat %s: %v", path, err)
		}
		if info.Size() > config.LogMaxBytes {
			t.Fatalf("%s is %d bytes, above the %d-byte cap", path, info.Size(), config.LogMaxBytes)
		}
	}
}
