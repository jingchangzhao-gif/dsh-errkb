//go:build windows

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/database"
)

// doctorTestConfiguration writes a minimal valid configuration file: the DSN
// plus the contract defaults applied by LoadConfig.
func doctorTestConfiguration(t *testing.T, dsn string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yml")
	// The lines are joined at run time: a literal "\n" next to a letter and a
	// colon looks like a Windows drive path to the repository privacy guard.
	lines := []string{
		"database:",
		"  dsn: " + dsn,
		"retention:",
		"  sample_days: 7",
		"  normalized_days: 90",
		"  audit_days: 365",
	}
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write the test configuration: %v", err)
	}
	return path
}

func doctorTestOptions(t *testing.T, dsn string) DoctorOptions {
	t.Helper()
	return DoctorOptions{
		Version:       "test",
		DSN:           dsn,
		MigrationsDir: "../../db/migrations",
		ConfigPath:    doctorTestConfiguration(t, dsn),
		QueueDir:      filepath.Join(t.TempDir(), "queue"),
		PipeName:      newTestPipeName(),
		Timeout:       3 * time.Second,
	}
}

// doctorTestDatabase prepares a throwaway schema: the full migration chain and
// one registered connector. The doctor itself never writes; all of this is
// setup. The connection the doctor opens is redirected into that schema, so the
// shared database keeps no state and the daemon tests stay independent.
func doctorTestDatabase(t *testing.T) (*pgx.Conn, string) {
	t.Helper()
	dsn := os.Getenv("ERRKB_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skipf("set ERRKB_TEST_DATABASE_URL to run the doctor database checks")
	}
	pool, schema := newDaemonSchema(t)
	ctx := context.Background()
	acquired, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire a doctor test connection: %v", err)
	}
	t.Cleanup(acquired.Release)
	conn := acquired.Conn()
	if _, err := database.Migrate(ctx, conn, "../../db/migrations"); err != nil {
		t.Fatalf("migrate the test schema: %v", err)
	}
	_, err = conn.Exec(ctx,
		"insert into connectors (name, channel, source, actor) values ('doctor-test', 'B', 'test', 'test')")
	if err != nil {
		t.Fatalf("register the test connector: %v", err)
	}
	useDoctorSchema(t, schema)
	return conn, dsn
}

// useDoctorSchema redirects the doctor's own connection into the test schema
// instead of the default search_path.
func useDoctorSchema(t *testing.T, schema string) {
	t.Helper()
	original := doctorConnect
	doctorConnect = func(ctx context.Context, dsn string) (*pgx.Conn, error) {
		config, err := pgx.ParseConfig(dsn)
		if err != nil {
			return nil, err
		}
		config.RuntimeParams["search_path"] = schema + ", public"
		return pgx.ConnectConfig(ctx, config)
	}
	t.Cleanup(func() { doctorConnect = original })
}

func doctorCheckByName(t *testing.T, report DoctorReport, name string) DoctorCheck {
	t.Helper()
	for _, check := range report.Checks {
		if check.Name == name {
			return check
		}
	}
	t.Fatalf("the report has no %q check", name)
	return DoctorCheck{}
}

func doctorSnapshot(t *testing.T, conn *pgx.Conn) map[string]int64 {
	t.Helper()
	ctx := context.Background()
	tables := []string{
		"connectors", "events", "issues", "occurrences", "solutions",
		"solution_versions", "outcomes", "retrievals", "notices", "audit_log",
	}
	snapshot := make(map[string]int64, len(tables)+1)
	for _, table := range tables {
		var count int64
		if err := conn.QueryRow(ctx, "select count(*) from "+table).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		snapshot[table] = count
	}
	var ledger int64
	if err := conn.QueryRow(ctx, "select count(*) from schema_migrations").Scan(&ledger); err != nil {
		t.Fatalf("count the migration ledger: %v", err)
	}
	snapshot["schema_migrations"] = ledger
	return snapshot
}

func TestDoctorReportsEveryCategory(t *testing.T) {
	_, dsn := doctorTestDatabase(t)
	options := doctorTestOptions(t, dsn)
	_, pipeName := startTestPipe(t, PipeOptions{API: NewAPI(&fakeBackend{})})
	options.PipeName = pipeName

	report := RunDoctor(context.Background(), options)

	want := []string{"version", "database", "migrations", "extension", "pipe", "connectors", "queue", "privacy"}
	if len(report.Checks) != len(want) {
		encoded, _ := json.Marshal(report)
		t.Fatalf("the report has %d checks, want %d: %s", len(report.Checks), len(want), encoded)
	}
	for index, name := range want {
		if report.Checks[index].Name != name {
			t.Fatalf("check %d is %q, want %q", index, report.Checks[index].Name, name)
		}
	}
	for _, check := range report.Checks {
		if check.Status != DoctorOK {
			encoded, _ := json.Marshal(check)
			t.Fatalf("check %q is %q, want ok: %s", check.Name, check.Status, encoded)
		}
		if check.Fix != "" {
			t.Fatalf("the ok check %q carries a fix hint: %q", check.Name, check.Fix)
		}
	}
	if report.Status != DoctorOK {
		t.Fatalf("the report status is %q, want ok", report.Status)
	}
	if report.Health == nil {
		t.Fatal("the report does not embed the EKB-028 health document")
	}
	if !report.Health.Database.Connected {
		t.Fatalf("health does not report the database as connected: %+v", report.Health.Database)
	}
	if !report.Health.Migrations.OK || report.Health.Migrations.Current != report.Health.Migrations.Expected {
		t.Fatalf("health migrations disagree: %+v", report.Health.Migrations)
	}
}

func TestDoctorKeepsGoingWhenTheDatabaseIsDown(t *testing.T) {
	options := doctorTestOptions(t, "postgres://postgres:postgres@127.0.0.1:1/errkb?sslmode=disable&connect_timeout=1")
	options.PipeName = newTestPipeName()

	report := RunDoctor(context.Background(), options)

	if len(report.Checks) != 8 {
		t.Fatalf("the report has %d checks, want 8 even with the database down", len(report.Checks))
	}
	if report.Status != DoctorFailing {
		t.Fatalf("the report status is %q, want failing", report.Status)
	}
	wanted := map[string]DoctorStatus{
		"version":    DoctorOK,
		"database":   DoctorFailing,
		"migrations": DoctorFailing,
		"extension":  DoctorFailing,
		"pipe":       DoctorDegraded,
		"connectors": DoctorFailing,
		"queue":      DoctorOK,
		"privacy":    DoctorOK,
	}
	for name, status := range wanted {
		check := doctorCheckByName(t, report, name)
		if check.Status != status {
			t.Fatalf("check %q is %q, want %q: %s", name, check.Status, status, check.Summary)
		}
		if status != DoctorOK && check.Fix == "" {
			t.Fatalf("the non-ok check %q has no fix hint", name)
		}
	}
	if report.Health == nil || report.Health.Database.Connected {
		t.Fatalf("health does not report the database as unreachable: %+v", report.Health)
	}
}

func TestDoctorCallsAMissingTrigramDegraded(t *testing.T) {
	_, dsn := doctorTestDatabase(t)
	options := doctorTestOptions(t, dsn)
	_, pipeName := startTestPipe(t, PipeOptions{API: NewAPI(&fakeBackend{})})
	options.PipeName = pipeName

	original := doctorTrigramAvailable
	doctorTrigramAvailable = func(context.Context, *pgx.Conn) (bool, error) { return false, nil }
	defer func() { doctorTrigramAvailable = original }()

	report := RunDoctor(context.Background(), options)

	extension := doctorCheckByName(t, report, "extension")
	if extension.Status != DoctorDegraded {
		t.Fatalf("the extension check is %q, want degraded", extension.Status)
	}
	if extension.Fix == "" {
		t.Fatal("the degraded extension check has no fix hint")
	}
	if report.Status != DoctorDegraded {
		t.Fatalf("the report status is %q, want degraded (never failing for a missing extension)", report.Status)
	}
}

func TestDoctorNeverPrintsThePassword(t *testing.T) {
	password := "s3cr3t-doctor"
	options := doctorTestOptions(t, "postgres://postgres:"+password+"@127.0.0.1:1/errkb?sslmode=disable&connect_timeout=1")
	options.PipeName = newTestPipeName()

	report := RunDoctor(context.Background(), options)

	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("encode the report: %v", err)
	}
	if strings.Contains(string(encoded), password) {
		t.Fatalf("the report leaks the connection password: %s", encoded)
	}
	database := doctorCheckByName(t, report, "database")
	if database.Status != DoctorFailing || database.Summary == "" {
		t.Fatalf("the database check is %+v, want a failing check with a summary", database)
	}
}

func TestDoctorIsReadOnly(t *testing.T) {
	conn, dsn := doctorTestDatabase(t)
	options := doctorTestOptions(t, dsn)
	_, pipeName := startTestPipe(t, PipeOptions{API: NewAPI(&fakeBackend{})})
	options.PipeName = pipeName

	before := doctorSnapshot(t, conn)
	report := RunDoctor(context.Background(), options)
	if report.Status != DoctorOK {
		encoded, _ := json.Marshal(report)
		t.Fatalf("the diagnosis is not ok, so the read-only proof is meaningless: %s", encoded)
	}
	after := doctorSnapshot(t, conn)

	if len(before) != len(after) {
		t.Fatalf("the table snapshot changed shape: %d -> %d", len(before), len(after))
	}
	for table, count := range before {
		if after[table] != count {
			t.Fatalf("the doctor changed %s: %d -> %d", table, count, after[table])
		}
	}
	if _, err := os.Stat(options.QueueDir); !os.IsNotExist(err) {
		t.Fatalf("the doctor created the queue directory %q", options.QueueDir)
	}
}

func TestDoctorReportsAQueueOverTheCap(t *testing.T) {
	queueDir := t.TempDir()
	options := doctorTestOptions(t, "postgres://postgres:postgres@127.0.0.1:1/errkb?sslmode=disable&connect_timeout=1")
	options.QueueDir = queueDir
	options.QueueMaxEvents = 1
	options.PipeName = newTestPipeName()

	now := time.Now()
	for index := 0; index < 2; index++ {
		name := fmt.Sprintf("%020d-event-%d.json", now.Add(time.Duration(index)*time.Nanosecond).UnixNano(), index)
		if err := os.WriteFile(filepath.Join(queueDir, name), []byte(`{"version":1}`), 0o600); err != nil {
			t.Fatalf("write the queue fixture: %v", err)
		}
	}

	report := RunDoctor(context.Background(), options)

	queue := doctorCheckByName(t, report, "queue")
	if queue.Status != DoctorDegraded {
		t.Fatalf("the queue check is %q, want degraded (over the cap)", queue.Status)
	}
	if queue.Detail["pending"] != strconv.Itoa(2) {
		t.Fatalf("the queue check reports pending=%q, want 2", queue.Detail["pending"])
	}
	if queue.Detail["over_limit"] != "true" {
		t.Fatalf("the queue check reports over_limit=%q, want true", queue.Detail["over_limit"])
	}
	if !strings.Contains(queue.Summary, "over the cap") {
		t.Fatalf("the queue summary is %q", queue.Summary)
	}
	if report.Health == nil || report.Health.Queue.Backlog != 2 {
		t.Fatalf("the embedded health document does not carry the backlog: %+v", report.Health)
	}
}
