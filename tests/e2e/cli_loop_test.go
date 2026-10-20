//go:build windows

// The EKB-035 end-to-end acceptance: one real failing command, the real daemon,
// the real PostgreSQL store and the real retrieval. Nothing here is simulated:
// the test builds the production binary, starts the production daemon, runs a
// genuinely failing command twice and asserts on the rows the loop wrote.
//
// It needs a real database and therefore skips unless ERRKB_TEST_DATABASE_URL
// is set. It runs on this Windows host only (the daemon, the pipe and the CLI
// are Windows-only), which is exactly the configuration the plan's P4 gate
// targets.
package e2e_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/contract"
	"github.com/jingchangzhao-gif/error-knowledge-base/internal/core"
	"github.com/jingchangzhao-gif/error-knowledge-base/internal/database"
	"github.com/jingchangzhao-gif/error-knowledge-base/internal/redact"
	"github.com/jingchangzhao-gif/error-knowledge-base/internal/service"
)

const (
	e2eProject  = "e2e"
	e2eTimeout  = 2 * time.Minute
	seedMessage = "The system cannot find the specified file."
)

// artifactDir is where the test copies the real command output when set, so the
// demo record (docs/e2e-cli-loop.md) can quote an actual run instead of a
// hand-written transcript.
func artifactDir() string {
	return strings.TrimSpace(os.Getenv("ERRKB_E2E_ARTIFACT_DIR"))
}

func recordArtifact(t *testing.T, name, content string) {
	t.Helper()
	dir := artifactDir()
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("create the artifact directory: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatalf("write the artifact %s: %v", name, err)
	}
}

// commandRun is the CLI's --json shape for one wrapped run (EKB-035).
type commandRun struct {
	ExitCode int                      `json:"exit_code"`
	Record   *contract.RecordResponse `json:"record,omitempty"`
	Lookup   *contract.LookupResponse `json:"lookup,omitempty"`
}

func TestCLIErrorLoop(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("ERRKB_TEST_DATABASE_URL"))
	if dsn == "" {
		t.Skip("set ERRKB_TEST_DATABASE_URL to run the real end-to-end loop")
	}
	ctx, cancel := context.WithTimeout(context.Background(), e2eTimeout)
	defer cancel()

	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("connect to the test database: %v", err)
	}
	defer func() { _ = admin.Close(context.Background()) }()

	schema := fmt.Sprintf("errkb_e2e_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "create schema "+schema); err != nil {
		t.Fatalf("create the e2e schema: %v", err)
	}
	defer func() {
		_, _ = admin.Exec(context.Background(), "drop schema if exists "+schema+" cascade")
	}()
	scopedDSN := withSearchPath(dsn, schema)

	conn, err := pgx.Connect(ctx, scopedDSN)
	if err != nil {
		t.Fatalf("connect to the e2e schema: %v", err)
	}
	defer func() { _ = conn.Close(context.Background()) }()
	if _, err := database.Migrate(ctx, conn, migrationsDir(t)); err != nil {
		t.Fatalf("migrate the e2e schema: %v", err)
	}

	var connectorID, sessionID string
	if err := conn.QueryRow(ctx, `
		insert into connectors (name, version, channel, capabilities, source, actor)
		values ('e2e', '0.0.0', 'B', '{}'::jsonb, 'e2e', 'e2e') returning id::text`).Scan(&connectorID); err != nil {
		t.Fatalf("insert the e2e connector: %v", err)
	}
	if err := conn.QueryRow(ctx, `
		insert into sessions (connector_id, external_id, started_at, source, actor)
		values ($1, 'e2e-session', now(), 'e2e', 'e2e') returning id::text`, connectorID).Scan(&sessionID); err != nil {
		t.Fatalf("insert the e2e session: %v", err)
	}

	binary := buildBinary(t)
	pipeName := fmt.Sprintf(`\\.\pipe\errkb-e2e-%d`, time.Now().UnixNano())
	lockName := fmt.Sprintf(`Local\errkb-e2e-%d`, time.Now().UnixNano())
	dataDir := t.TempDir()
	daemonLog := startDaemon(t, binary, scopedDSN, pipeName, lockName, dataDir)
	_ = daemonLog

	// Environment pre-check: the card asks the script to skip with a reason
	// when doctor is not all green.
	doctorOutput, doctorCode := runCLI(t, binary, pipeName, "doctor", "--dsn", scopedDSN,
		"--migrations", migrationsDir(t), "--queue-dir", filepath.Join(dataDir, "queue"),
		"--pipe", pipeName, "--json")
	var report service.DoctorReport
	if err := json.Unmarshal([]byte(doctorOutput), &report); err != nil {
		t.Fatalf("doctor --json is not a DoctorReport (exit %d): %v\n%s", doctorCode, err, doctorOutput)
	}
	if report.Status != "ok" {
		var notOK []string
		for _, check := range report.Checks {
			if check.Status != "ok" {
				notOK = append(notOK, fmt.Sprintf("%s=%s (%s) %v", check.Name, string(check.Status), check.Summary, check.Detail))
			}
		}
		t.Skipf("environment pre-check is not green (%s); run `errkb doctor` and fix it first", strings.Join(notOK, ", "))
	}
	recordArtifact(t, "01-doctor.json", doctorOutput)

	missing := filepath.Join(dataDir, "e2e-missing-file.txt")
	if _, err := os.Stat(missing); err == nil {
		t.Fatalf("the failing command needs a path that does not exist: %s", missing)
	}

	// Scenario one: a real failing command, first run.
	firstOutput, firstCode := runCLI(t, binary, pipeName, "run",
		"--connector", connectorID, "--session", sessionID, "--project", e2eProject, "--json",
		"--", "cmd", "/c", "type", missing)
	var first commandRun
	if err := json.Unmarshal([]byte(firstOutput), &first); err != nil {
		t.Fatalf("the first run did not print the run envelope (exit %d): %v\n%s", firstCode, err, firstOutput)
	}
	if firstCode == 0 || first.ExitCode == 0 {
		t.Fatalf("the wrapped command must fail and pass its exit code through, got cli=%d child=%d", firstCode, first.ExitCode)
	}
	if first.Record == nil || first.Record.IssueID == "" {
		t.Fatalf("the first run did not record an issue: %s", firstOutput)
	}
	if first.Record.Created != true {
		t.Fatalf("the first run must create the issue, got created=%v", first.Record.Created)
	}
	for _, candidate := range first.Lookup.Candidates {
		if candidate.Solution != nil {
			t.Fatalf("the first run returned a solution for a brand-new issue: %+v", candidate.Solution)
		}
	}
	if got := countRows(t, conn, `select count(*) from issues where project_key = $1`, e2eProject); got != 1 {
		t.Fatalf("want one issue after the first run, got %d", got)
	}
	if got := countRows(t, conn, `select count(*) from retrievals where candidate_issue_id = $1::uuid`, first.Record.IssueID); got != 1 {
		t.Fatalf("want one retrieval row after the first run, got %d", got)
	}
	if got := countRows(t, conn, `select count(*) from retrievals where candidate_issue_id = $1::uuid and 'fingerprint' = any(match_fields)`, first.Record.IssueID); got != 1 {
		t.Fatalf("want the retrieval row to name the fingerprint reason, got %d", got)
	}
	recordArtifact(t, "02-run-first.json", firstOutput)

	// A known fix, recorded through the product's own solution path.
	body := "Open the missing path in Explorer and check that the drive and the folder exist, then rerun the command."
	solutionID, err := database.CreateSolution(ctx, conn, first.Record.IssueID, "e2e", "e2e")
	if err != nil {
		t.Fatalf("create the solution: %v", err)
	}
	version, err := database.AddSolutionVersion(ctx, conn, solutionID, body, "", "", database.Environment{OS: "windows"}, "e2e", "e2e")
	if err != nil {
		t.Fatalf("add the solution version: %v", err)
	}
	if err := database.TransitionSolution(ctx, conn, solutionID, database.SolutionVerified); err != nil {
		t.Fatalf("verify the solution: %v", err)
	}

	// Scenario two: the same failure again returns the known fix.
	secondOutput, secondCode := runCLI(t, binary, pipeName, "run",
		"--connector", connectorID, "--session", sessionID, "--project", e2eProject,
		"--", "cmd", "/c", "type", missing)
	if secondCode == 0 {
		t.Fatalf("the wrapped command must fail again, got exit 0\n%s", secondOutput)
	}
	for _, want := range []string{first.Record.IssueID, body, "source e2e", "applies to os=windows", "version "} {
		if !strings.Contains(secondOutput, want) {
			t.Fatalf("the second run does not show %q:\n%s", want, secondOutput)
		}
	}
	if got := countRows(t, conn, `select count(*) from retrievals where candidate_issue_id = $1::uuid`, first.Record.IssueID); got < 2 {
		t.Fatalf("want a retrieval row for the second search too, got %d", got)
	}
	if got := countRows(t, conn, `select count(*) from events`); got != 2 {
		t.Fatalf("want two recorded events after two runs, got %d", got)
	}
	recordArtifact(t, "03-run-second.txt", secondOutput)

	// Scenario three: the outcome of the fix is recorded through the contract.
	feedbackOutput, feedbackCode := runCLI(t, binary, pipeName, "feedback", first.Record.IssueID, version.ID, "--outcome", "success", "--json")
	if feedbackCode != 0 {
		t.Fatalf("feedback failed with exit %d:\n%s", feedbackCode, feedbackOutput)
	}
	if got := countRows(t, conn, `select count(*) from outcomes where issue_id = $1::uuid`, first.Record.IssueID); got != 1 {
		t.Fatalf("want one outcome row, got %d", got)
	}
	recordArtifact(t, "04-feedback.json", feedbackOutput)

	// Scenario four: a near duplicate is a candidate, never an exact hit.
	seedNearDuplicate(t, conn, connectorID, sessionID)
	searchOutput, searchCode := runCLI(t, binary, pipeName, "search",
		"The system cannot find the file specified.", "--json")
	if searchCode != 0 {
		t.Fatalf("search failed with exit %d:\n%s", searchCode, searchOutput)
	}
	var search contract.SearchResponse
	if err := json.Unmarshal([]byte(searchOutput), &search); err != nil {
		t.Fatalf("search --json is not a SearchResponse: %v\n%s", err, searchOutput)
	}
	approximate := 0
	for _, candidate := range search.Candidates {
		if candidate.MatchReason == "exact_fingerprint" {
			t.Fatalf("a near duplicate was returned as an exact fingerprint hit: %+v", candidate)
		}
		if candidate.Approximate {
			approximate++
		}
	}
	if approximate == 0 {
		t.Fatalf("want at least one approximate candidate, got %+v", search.Candidates)
	}
	humanSearch, _ := runCLI(t, binary, pipeName, "search", "The system cannot find the file specified.")
	if !strings.Contains(humanSearch, "APPROXIMATE - confirm before reuse") {
		t.Fatalf("the human output must flag the approximate hit:\n%s", humanSearch)
	}
	recordArtifact(t, "05-search.json", searchOutput)
	recordArtifact(t, "06-search-human.txt", humanSearch)

	// The store now shows the closed loop: two real failures, one issue, one
	// solution, one outcome, one near-duplicate issue.
	statsOutput, statsCode := runCLI(t, binary, pipeName, "stats", "--json")
	if statsCode != 0 {
		t.Fatalf("stats failed with exit %d:\n%s", statsCode, statsOutput)
	}
	var stats contract.StatsResponse
	if err := json.Unmarshal([]byte(statsOutput), &stats); err != nil {
		t.Fatalf("stats --json is not a StatsResponse: %v\n%s", err, statsOutput)
	}
	if stats.Totals.Issues != 2 || stats.Totals.Solutions != 1 || stats.Totals.Outcomes != 1 {
		t.Fatalf("unexpected totals after the loop: %+v", stats.Totals)
	}
	if got := countRows(t, conn, `select count(*) from outcomes where issue_id = $1::uuid and outcome = 'success'`, first.Record.IssueID); got != 1 {
		t.Fatalf("the recorded outcome is not a success, got %d", got)
	}
	recordArtifact(t, "07-stats.json", statsOutput)
	recordDatabaseArtifacts(t, conn)
}

// recordDatabaseArtifacts writes the rows the loop produced so the demo record
// can quote the store instead of just asserting that it worked.
func recordDatabaseArtifacts(t *testing.T, conn *pgx.Conn) {
	t.Helper()
	if artifactDir() == "" {
		return
	}
	var out strings.Builder
	queries := []struct{ title, sql string }{
		{"issues", `select id::text, title, status, hits, fingerprint from issues order by created_at`},
		{"events", `select event_id::text, headline, error_code, fingerprint from events order by observed_at`},
		{"retrievals", `select candidate_rank, candidate_issue_id::text, match_fields::text, score, approximate from retrievals order by created_at`},
		{"solutions", `select s.id::text, s.status, sv.version, sv.body, sv.source, sv.applies_to::text from solutions s join solution_versions sv on sv.solution_id = s.id order by sv.version`},
		{"outcomes", `select outcome, issue_id::text, solution_version_id::text from outcomes`},
	}
	for _, query := range queries {
		rows, err := conn.Query(context.Background(), query.sql)
		if err != nil {
			t.Fatalf("dump %s: %v", query.title, err)
		}
		fmt.Fprintf(&out, "== %s ==\n", query.title)
		fields := rows.FieldDescriptions()
		names := make([]string, len(fields))
		for i, field := range fields {
			names[i] = string(field.Name)
		}
		fmt.Fprintf(&out, "%s\n", strings.Join(names, " | "))
		for rows.Next() {
			values, err := rows.Values()
			if err != nil {
				rows.Close()
				t.Fatalf("dump %s: %v", query.title, err)
			}
			parts := make([]string, len(values))
			for i, value := range values {
				parts[i] = fmt.Sprint(value)
			}
			fmt.Fprintf(&out, "%s\n", strings.Join(parts, " | "))
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatalf("dump %s: %v", query.title, err)
		}
		rows.Close()
		fmt.Fprintln(&out)
	}
	recordArtifact(t, "08-database.txt", out.String())
}

// seedNearDuplicate inserts one prior observation whose message shares almost
// every token with the failing command's message but has a different normalized
// form, so it can only ever be an approximate candidate.
func seedNearDuplicate(t *testing.T, conn *pgx.Conn, connectorID, sessionID string) {
	t.Helper()
	normalized := core.Normalize(seedMessage)
	headline := core.ExtractHeadline(seedMessage).Line
	confidence := 0.5
	transient := false
	event := contract.ErrorEvent{
		SchemaVersion:     contract.SchemaVersion,
		EventID:           newEventID(t),
		ConnectorID:       connectorID,
		SessionID:         sessionID,
		ObservedAt:        time.Now().UTC().Format(time.RFC3339),
		Channel:           "B",
		Category:          "command-exit",
		Severity:          "error",
		Transient:         &transient,
		Headline:          redact.Sample(headline, redact.Public),
		NormalizedMessage: normalized,
		RedactedSample:    redact.Sample(seedMessage, redact.Public),
		OS:                "windows",
		ProjectKey:        e2eProject,
		Fingerprint:       core.Signature("command-exit", normalized),
		Evidence:          "e2e seed",
		Confidence:        &confidence,
	}
	if err := event.Validate(); err != nil {
		t.Fatalf("the seeded event is invalid: %v", err)
	}
	if _, err := database.RecordEvent(context.Background(), conn, event); err != nil {
		t.Fatalf("seed the near duplicate: %v", err)
	}
}

func newEventID(t *testing.T) string {
	t.Helper()
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		t.Fatalf("generate an event id: %v", err)
	}
	raw[6] = (raw[6] & 0x0f) | 0x40
	raw[8] = (raw[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(raw)
	return fmt.Sprintf("%s-%s-%s-%s-%s", encoded[0:8], encoded[8:12], encoded[12:16], encoded[16:20], encoded[20:32])
}

func countRows(t *testing.T, conn *pgx.Conn, query string, args ...any) int {
	t.Helper()
	var count int
	if err := conn.QueryRow(context.Background(), query, args...).Scan(&count); err != nil {
		t.Fatalf("count rows (%s): %v", query, err)
	}
	return count
}

func runCLI(t *testing.T, binary, pipeName string, args ...string) (string, int) {
	t.Helper()
	command := exec.Command(binary, args...)
	command.Env = append(os.Environ(), "ERRKB_PIPE="+pipeName)
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	err := command.Run()
	code := 0
	if err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("run errkb %v: %v", args, err)
		}
		code = exitErr.ExitCode()
	}
	if code != 0 {
		t.Logf("errkb %v exited %d (expected for failing commands)\nstderr: %s", args, code, stderr.String())
	}
	return stdout.String(), code
}

func buildBinary(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), "errkb.exe")
	build := exec.Command("go", "build", "-o", binary, "./cmd/errkb")
	build.Dir = repoRoot(t)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build errkb: %v\n%s", err, output)
	}
	return binary
}

func startDaemon(t *testing.T, binary, dsn, pipeName, lockName, dataDir string) *bytes.Buffer {
	t.Helper()
	logs := &bytes.Buffer{}
	command := exec.Command(binary, "daemon",
		"--dsn", dsn,
		"--migrations", migrationsDir(t),
		"--lock-name", lockName,
		"--pipe", pipeName,
		"--health-file", filepath.Join(dataDir, "health.json"),
		"--log-file", filepath.Join(dataDir, "daemon.log"),
		"--health-interval", "5s",
	)
	command.Stdout = logs
	command.Stderr = logs
	if err := command.Start(); err != nil {
		t.Fatalf("start the daemon: %v", err)
	}
	t.Cleanup(func() {
		_ = command.Process.Kill()
		_ = command.Wait()
	})

	deadline := time.Now().Add(30 * time.Second)
	for {
		dialCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		client, err := service.DialPipe(dialCtx, pipeName)
		cancel()
		if err == nil {
			_ = client.Close()
			return logs
		}
		if time.Now().After(deadline) {
			t.Fatalf("the daemon did not start listening on %s: %v\n%s", pipeName, err, logs.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate the e2e test file")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func migrationsDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(repoRoot(t), "db", "migrations")
}

func withSearchPath(dsn, schema string) string {
	separator := "?"
	if strings.Contains(dsn, "?") {
		separator = "&"
	}
	return dsn + separator + "search_path=" + schema + ",public"
}
