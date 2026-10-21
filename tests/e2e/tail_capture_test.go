//go:build windows

package e2e_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/database"
)

// TestLogTailRecordsThroughTheDaemon proves the template-C claim end to end:
// `errkb tail` follows a log file and records its failures through the daemon,
// on a connector row it creates itself (channel C), and replays reuse the same
// issue.
func TestLogTailRecordsThroughTheDaemon(t *testing.T) {
	dsn := os.Getenv("ERRKB_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("ERRKB_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	schema := fmt.Sprintf("errkb_tail_%d", time.Now().UnixNano())

	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect the test database: %v", err)
	}
	defer func() { _ = admin.Close(context.Background()) }()
	if _, err := admin.Exec(ctx, "create schema "+schema); err != nil {
		t.Fatalf("create the test schema: %v", err)
	}
	t.Cleanup(func() { _, _ = admin.Exec(context.Background(), "drop schema if exists "+schema+" cascade") })
	scoped := withSearchPath(dsn, schema)

	conn, err := pgx.Connect(ctx, scoped)
	if err != nil {
		t.Fatalf("connect the schema: %v", err)
	}
	if _, err := database.Migrate(ctx, conn, migrationsDir(t)); err != nil {
		t.Fatalf("migrate the schema: %v", err)
	}
	_ = conn.Close(ctx)

	binary := buildBinary(t)
	stamp := time.Now().UnixNano()
	pipeName := fmt.Sprintf(`\\.\pipe\errkb-tail-%d`, stamp)
	lockName := fmt.Sprintf(`Local\errkb-tail-%d`, stamp)
	startDaemon(t, binary, scoped, pipeName, lockName, t.TempDir())

	logPath := filepath.Join(t.TempDir(), "app.log")
	contents := "info: starting up\n" +
		"error: permission denied (os error 13)\n" +
		"info: retrying\n" +
		"npm ERR! code ENOENT\n"
	if err := os.WriteFile(logPath, []byte(contents), 0o600); err != nil {
		t.Fatalf("write the log file: %v", err)
	}

	out, code := runCLI(t, binary, pipeName, "tail",
		"--file", logPath, "--connector", "tail-e2e", "--session", "tail-e2e",
		"--project", "tail-e2e", "--poll", "50ms", "--once", "--timeout", "25s")
	if code != 0 {
		t.Fatalf("errkb tail exited %d: %s", code, out)
	}
	if !strings.Contains(out, "recorded as issue") {
		t.Fatalf("errkb tail did not report a recorded issue: %s", out)
	}

	db, err := pgx.Connect(ctx, scoped)
	if err != nil {
		t.Fatalf("connect the schema for assertions: %v", err)
	}
	defer func() { _ = db.Close(context.Background()) }()
	var issues, events, hits int
	if err := db.QueryRow(ctx, `select count(*) from issues where project_key = 'tail-e2e'`).Scan(&issues); err != nil {
		t.Fatalf("count issues: %v", err)
	}
	if err := db.QueryRow(ctx, `select count(*) from occurrences where project_key = 'tail-e2e'`).Scan(&events); err != nil {
		t.Fatalf("count occurrences: %v", err)
	}
	if err := db.QueryRow(ctx, `select sum(hits) from issues where project_key = 'tail-e2e'`).Scan(&hits); err != nil {
		t.Fatalf("sum hits: %v", err)
	}
	if issues != 1 || events != 1 || hits != 1 {
		t.Fatalf("after one tailed failure: issues=%d events=%d hits=%d, want 1/1/1", issues, events, hits)
	}
	var channel string
	if err := db.QueryRow(ctx, `select channel from connectors where name = 'tail-e2e'`).Scan(&channel); err != nil {
		t.Fatalf("read the tail connector: %v", err)
	}
	if channel != "C" {
		t.Fatalf("tail connector channel = %q, want C", channel)
	}

	// A second pass over the same file reuses the issue through the fingerprint.
	if _, code := runCLI(t, binary, pipeName, "tail",
		"--file", logPath, "--connector", "tail-e2e", "--session", "tail-e2e",
		"--project", "tail-e2e", "--poll", "50ms", "--once", "--timeout", "25s"); code != 0 {
		t.Fatalf("second errkb tail exited %d", code)
	}
	if err := db.QueryRow(ctx, `select count(*) from occurrences where project_key = 'tail-e2e'`).Scan(&events); err != nil {
		t.Fatalf("count occurrences after replay: %v", err)
	}
	if err := db.QueryRow(ctx, `select sum(hits) from issues where project_key = 'tail-e2e'`).Scan(&hits); err != nil {
		t.Fatalf("sum hits after replay: %v", err)
	}
	if err := db.QueryRow(ctx, `select count(*) from issues where project_key = 'tail-e2e'`).Scan(&issues); err != nil {
		t.Fatalf("count issues after replay: %v", err)
	}
	if issues != 1 || events != 2 || hits != 2 {
		t.Fatalf("after the replay: issues=%d events=%d hits=%d, want 1/2/2", issues, events, hits)
	}
}
