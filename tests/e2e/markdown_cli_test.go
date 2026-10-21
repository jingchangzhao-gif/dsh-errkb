//go:build windows

package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/database"
)

// TestMarkdownImportExportCLI proves WF-009: the legacy ERRORS.md exchange has
// a supported command, a round trip is idempotent, and export renders the
// issues the importer created.
func TestMarkdownImportExportCLI(t *testing.T) {
	dsn := os.Getenv("ERRKB_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("ERRKB_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	schema := fmt.Sprintf("errkb_md_%d", time.Now().UnixNano())
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
	seed := filepath.Join(repoRoot(t), "seeds", "ERRORS.seed.md")

	runMarkdown := func(args ...string) (string, int) {
		command := exec.Command(binary, append([]string{"markdown"}, args...)...)
		var out, errOut strings.Builder
		command.Stdout = &out
		command.Stderr = &errOut
		err := command.Run()
		code := 0
		if err != nil {
			exitErr, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatalf("run errkb markdown %v: %v", args, err)
			}
			code = exitErr.ExitCode()
		}
		if code != 0 {
			t.Logf("errkb markdown %v exited %d: %s", args, code, errOut.String())
		}
		return out.String(), code
	}

	out, code := runMarkdown("import", "--file", seed, "--dsn", scoped)
	if code != 0 {
		t.Fatalf("markdown import exited %d: %s", code, out)
	}
	db, err := pgx.Connect(ctx, scoped)
	if err != nil {
		t.Fatalf("connect for assertions: %v", err)
	}
	defer func() { _ = db.Close(context.Background()) }()
	var issues int
	if err := db.QueryRow(ctx, `select count(*) from issues`).Scan(&issues); err != nil {
		t.Fatalf("count issues: %v", err)
	}
	if issues != 3 {
		t.Fatalf("imported issues = %d, want 3", issues)
	}

	exportPath := filepath.Join(t.TempDir(), "ERRORS.md")
	out, code = runMarkdown("export", "--out", exportPath, "--dsn", scoped)
	if code != 0 {
		t.Fatalf("markdown export exited %d: %s", code, out)
	}
	exported, err := os.ReadFile(exportPath)
	if err != nil {
		t.Fatalf("read the export: %v", err)
	}
	for _, marker := range []string{"## E-0001 ", "## E-0002 ", "## E-0003 "} {
		if !strings.Contains(string(exported), marker) {
			t.Fatalf("the export is missing %q:\n%s", marker, exported)
		}
	}
	if !strings.Contains(string(exported), "<!-- errkb: sig=") {
		t.Fatalf("the export is missing the machine comment:\n%s", exported)
	}

	// Re-importing the exported document must not create new issues.
	jsonOut, code := runMarkdown("import", "--file", exportPath, "--dsn", scoped, "--json")
	if code != 0 {
		t.Fatalf("round-trip import exited %d: %s", code, jsonOut)
	}
	var payload struct {
		Report database.ImportReport `json:"report"`
	}
	if err := json.Unmarshal([]byte(jsonOut), &payload); err != nil {
		t.Fatalf("decode the import report: %v\n%s", err, jsonOut)
	}
	if payload.Report.IssuesCreated != 0 {
		t.Fatalf("round-trip created %d issues, want 0", payload.Report.IssuesCreated)
	}
	if err := db.QueryRow(ctx, `select count(*) from issues`).Scan(&issues); err != nil {
		t.Fatalf("count issues after the round trip: %v", err)
	}
	if issues != 3 {
		t.Fatalf("issues after the round trip = %d, want 3", issues)
	}
}
