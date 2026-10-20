package database_test

import (
	"context"
	"math"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/database"
)

// newIssueAndSolution creates one issue through the real recording path, then a
// candidate solution for it.
func newIssueAndSolution(t *testing.T, conn *pgx.Conn, connector, session string) (issueID, solutionID string) {
	t.Helper()
	ctx := context.Background()

	event := recordFixture(connector, session, eventUUID(2000), recordFixtureMessage, "proj")
	recorded, err := database.RecordEvent(ctx, conn, event)
	if err != nil {
		t.Fatalf("record the issue: %v", err)
	}

	solutionID, err = database.CreateSolution(ctx, conn, recorded.IssueID, "test", "test")
	if err != nil {
		t.Fatalf("create solution: %v", err)
	}
	return recorded.IssueID, solutionID
}

func solutionStatus(t *testing.T, conn *pgx.Conn, solutionID string) string {
	t.Helper()
	var status string
	if err := conn.QueryRow(context.Background(), "select status from solutions where id = $1", solutionID).Scan(&status); err != nil {
		t.Fatalf("read solution status: %v", err)
	}
	return status
}

// TestSolutionStatusMachine locks the closed state machine: the defined
// transitions pass, everything else is rejected by the database.
func TestSolutionStatusMachine(t *testing.T) {
	ctx := context.Background()
	conn, _, connector, session := setupRecordSchema(t)
	issueID, solutionID := newIssueAndSolution(t, conn, connector, session)

	steps := []database.SolutionStatus{
		database.SolutionVerified,
		database.SolutionBlocked,
		database.SolutionDeprecated,
	}
	for _, next := range steps {
		if err := database.TransitionSolution(ctx, conn, solutionID, next); err != nil {
			t.Fatalf("legal transition to %s: %v", next, err)
		}
		if got := solutionStatus(t, conn, solutionID); got != string(next) {
			t.Fatalf("status = %s after transition to %s, want %s", got, next, next)
		}
	}

	if err := database.TransitionSolution(ctx, conn, solutionID, database.SolutionVerified); err == nil {
		t.Fatal("deprecated -> verified was accepted, want a database rejection")
	} else if !strings.Contains(err.Error(), "illegal solution status transition") {
		t.Fatalf("deprecated -> verified error = %v, want the transition message", err)
	}

	_, second := newIssueAndSolution(t, conn, connector, session)
	if err := database.TransitionSolution(ctx, conn, second, database.SolutionBlocked); err != nil {
		t.Fatalf("candidate -> blocked: %v", err)
	}
	if err := database.TransitionSolution(ctx, conn, second, database.SolutionVerified); err == nil {
		t.Fatal("blocked -> verified was accepted, want a database rejection")
	}

	if _, err := conn.Exec(ctx, `
		insert into solutions (issue_id, status, source, actor)
		values ($1, 'accepted', 'test', 'test')
	`, issueID); err == nil {
		t.Fatal("a status outside the enum was accepted, want a check-constraint rejection")
	} else if !strings.Contains(err.Error(), "solutions_status_check") {
		t.Fatalf("unknown status error = %v, want the check constraint name", err)
	}
	if err := database.TransitionSolution(ctx, conn, solutionID, database.SolutionStatus("guessed")); err == nil {
		t.Fatal("an invented status passed the Go guard")
	}
}

// TestSolutionVersionsKeepHistory appends three revisions and checks that the
// earlier text, preconditions, risk and applicable environment survive.
func TestSolutionVersionsKeepHistory(t *testing.T) {
	ctx := context.Background()
	conn, _, connector, session := setupRecordSchema(t)
	_, solutionID := newIssueAndSolution(t, conn, connector, session)

	windows := database.Environment{OS: "windows", RuntimeVersions: map[string]string{"node": "24.16.0"}}
	linux := database.Environment{OS: "linux", RuntimeVersions: map[string]string{"node": "20.0.0"}}

	first, err := database.AddSolutionVersion(ctx, conn, solutionID,
		"restart the daemon", "docker is running", "drops in-flight jobs", windows, "test", "test")
	if err != nil {
		t.Fatalf("add version 1: %v", err)
	}
	if first.Version != 1 {
		t.Fatalf("first version = %d, want 1", first.Version)
	}
	if first.EffectiveAt.IsZero() {
		t.Fatal("effective_at was not set")
	}
	if first.AppliesTo.OS != "windows" || first.AppliesTo.RuntimeVersions["node"] != "24.16.0" {
		t.Fatalf("applies_to round trip = %+v", first.AppliesTo)
	}
	if first.Preconditions != "docker is running" || first.Risks != "drops in-flight jobs" {
		t.Fatalf("preconditions/risks round trip = %+v", first)
	}

	second, err := database.AddSolutionVersion(ctx, conn, solutionID,
		"restart the daemon with --graceful", "", "slower", linux, "test", "test")
	if err != nil {
		t.Fatalf("add version 2: %v", err)
	}
	if second.Version != 2 {
		t.Fatalf("second version = %d, want 2", second.Version)
	}
	if _, err := database.AddSolutionVersion(ctx, conn, solutionID, "", "", "", database.Environment{}, "test", "test"); err == nil {
		t.Fatal("an empty body was accepted, want a rejection")
	}

	var count int
	if err := conn.QueryRow(ctx, "select count(*) from solution_versions where solution_id = $1", solutionID).Scan(&count); err != nil {
		t.Fatalf("count versions: %v", err)
	}
	if count != 2 {
		t.Fatalf("stored versions = %d, want 2", count)
	}

	if _, err := database.AddSolutionVersion(ctx, conn, solutionID,
		"undo the workaround", "", "", database.Environment{}, "test", "test"); err != nil {
		t.Fatalf("add version 3: %v", err)
	}

	var storedBody string
	if err := conn.QueryRow(ctx, `
		select body from solution_versions where solution_id = $1 and version = 1
	`, solutionID).Scan(&storedBody); err != nil {
		t.Fatalf("read version 1: %v", err)
	}
	if storedBody != "restart the daemon" {
		t.Fatalf("version 1 body = %q, history was overwritten", storedBody)
	}
}

// TestOutcomesAreEnvironmentSpecific records the same solution in two
// environments and proves the trust calculation answers per environment.
func TestOutcomesAreEnvironmentSpecific(t *testing.T) {
	ctx := context.Background()
	conn, _, connector, session := setupRecordSchema(t)
	issueID, solutionID := newIssueAndSolution(t, conn, connector, session)

	version, err := database.AddSolutionVersion(ctx, conn, solutionID,
		"restart the daemon", "", "", database.Environment{}, "test", "test")
	if err != nil {
		t.Fatalf("add version: %v", err)
	}

	windows := database.Environment{OS: "windows", RuntimeVersions: map[string]string{"node": "24.16.0"}}
	linux := database.Environment{OS: "linux", RuntimeVersions: map[string]string{"node": "20.0.0"}}

	if _, err := database.RecordOutcome(ctx, conn, issueID, version.ID,
		database.OutcomeSuccess, "worked", windows, "", "test", "test"); err != nil {
		t.Fatalf("record windows success: %v", err)
	}
	if _, err := database.RecordOutcome(ctx, conn, issueID, version.ID,
		database.OutcomeFailure, "broke the socket", linux, "", "test", "test"); err != nil {
		t.Fatalf("record linux failure: %v", err)
	}

	windowsTrust, err := database.SolutionTrust(ctx, conn, solutionID, windows)
	if err != nil {
		t.Fatalf("trust on windows: %v", err)
	}
	if windowsTrust.Samples != 1 || windowsTrust.Successes != 1 || windowsTrust.Failures != 0 {
		t.Fatalf("windows trust counts = %+v, want one success", windowsTrust)
	}
	if windowsTrust.Score <= 0.5 {
		t.Fatalf("windows score = %v, want better than the 0.5 prior", windowsTrust.Score)
	}

	linuxTrust, err := database.SolutionTrust(ctx, conn, solutionID, linux)
	if err != nil {
		t.Fatalf("trust on linux: %v", err)
	}
	if linuxTrust.Samples != 1 || linuxTrust.Successes != 0 || linuxTrust.Failures != 1 {
		t.Fatalf("linux trust counts = %+v, want one failure", linuxTrust)
	}
	if linuxTrust.Score >= 0.5 {
		t.Fatalf("linux score = %v, want worse than the 0.5 prior", linuxTrust.Score)
	}

	// A different runtime version in the same os is not the same environment.
	narrowed := database.Environment{OS: "windows", RuntimeVersions: map[string]string{"node": "20.0.0"}}
	empty, err := database.SolutionTrust(ctx, conn, solutionID, narrowed)
	if err != nil {
		t.Fatalf("trust on another runtime: %v", err)
	}
	if empty.Samples != 0 || empty.Score != 0.5 {
		t.Fatalf("other runtime trust = %+v, want zero samples and the 0.5 prior", empty)
	}

	// No environment constraint sees both outcomes.
	overall, err := database.SolutionTrust(ctx, conn, solutionID, database.Environment{})
	if err != nil {
		t.Fatalf("trust with no environment: %v", err)
	}
	if overall.Samples != 2 {
		t.Fatalf("overall samples = %d, want 2", overall.Samples)
	}
}

// TestOutcomeInheritsOccurrenceEnvironment checks the convenience path: an
// outcome recorded against an occurrence and without an explicit environment
// takes the occurrence's environment, so evidence never loses its machine.
func TestOutcomeInheritsOccurrenceEnvironment(t *testing.T) {
	ctx := context.Background()
	conn, _, connector, session := setupRecordSchema(t)
	event := recordFixture(connector, session, eventUUID(2001), recordFixtureMessage, "proj")
	recorded, err := database.RecordEvent(ctx, conn, event)
	if err != nil {
		t.Fatalf("record the occurrence: %v", err)
	}

	solutionID, err := database.CreateSolution(ctx, conn, recorded.IssueID, "test", "test")
	if err != nil {
		t.Fatalf("create solution: %v", err)
	}
	version, err := database.AddSolutionVersion(ctx, conn, solutionID, "fix", "", "", database.Environment{}, "test", "test")
	if err != nil {
		t.Fatalf("add version: %v", err)
	}

	if _, err := database.RecordOutcome(ctx, conn, recorded.IssueID, version.ID,
		database.OutcomeSuccess, "", database.Environment{}, recorded.OccurrenceID, "test", "test"); err != nil {
		t.Fatalf("record outcome: %v", err)
	}

	trust, err := database.SolutionTrust(ctx, conn, solutionID,
		database.Environment{OS: "windows", Shell: "powershell"})
	if err != nil {
		t.Fatalf("trust: %v", err)
	}
	if trust.Samples != 1 || trust.Successes != 1 {
		t.Fatalf("trust after inheriting the occurrence environment = %+v, want one success", trust)
	}
}

// TestUnknownOutcomeIsPreserved proves unknown is stored rather than dropped.
func TestUnknownOutcomeIsPreserved(t *testing.T) {
	ctx := context.Background()
	conn, _, connector, session := setupRecordSchema(t)
	issueID, solutionID := newIssueAndSolution(t, conn, connector, session)

	version, err := database.AddSolutionVersion(ctx, conn, solutionID, "fix", "", "", database.Environment{}, "test", "test")
	if err != nil {
		t.Fatalf("add version: %v", err)
	}
	if _, err := database.RecordOutcome(ctx, conn, issueID, version.ID,
		database.OutcomeUnknown, "nobody checked", database.Environment{}, "", "test", "test"); err != nil {
		t.Fatalf("record unknown: %v", err)
	}

	var stored int
	if err := conn.QueryRow(ctx, "select count(*) from outcomes where outcome = 'unknown'").Scan(&stored); err != nil {
		t.Fatalf("count unknowns: %v", err)
	}
	if stored != 1 {
		t.Fatalf("unknown outcomes stored = %d, want 1", stored)
	}

	trust, err := database.SolutionTrust(ctx, conn, solutionID, database.Environment{})
	if err != nil {
		t.Fatalf("trust: %v", err)
	}
	if trust.Samples != 1 || trust.Unknowns != 1 || trust.Successes != 0 || trust.Failures != 0 {
		t.Fatalf("trust with an unknown = %+v, want one sample, one unknown, no verdict", trust)
	}
	if trust.Score != 0.5 {
		t.Fatalf("score with only an unknown = %v, want the 0.5 prior", trust.Score)
	}
}

// TestTrustIsDeterministicAndConservative pins the formula: identical calls give
// identical numbers, one success is not treated as certainty, and volume pulls
// the value away from the prior.
func TestTrustIsDeterministicAndConservative(t *testing.T) {
	ctx := context.Background()
	conn, _, connector, session := setupRecordSchema(t)
	issueID, solutionID := newIssueAndSolution(t, conn, connector, session)

	version, err := database.AddSolutionVersion(ctx, conn, solutionID, "fix", "", "", database.Environment{}, "test", "test")
	if err != nil {
		t.Fatalf("add version: %v", err)
	}

	read := func() database.Trust {
		t.Helper()
		trust, err := database.SolutionTrust(ctx, conn, solutionID, database.Environment{})
		if err != nil {
			t.Fatalf("trust: %v", err)
		}
		return trust
	}

	if got := read(); got.Samples != 0 || got.Score != 0.5 {
		t.Fatalf("no evidence = %+v, want zero samples and the 0.5 prior", got)
	}

	if _, err := database.RecordOutcome(ctx, conn, issueID, version.ID,
		database.OutcomeSuccess, "", database.Environment{}, "", "test", "test"); err != nil {
		t.Fatalf("record success: %v", err)
	}
	one := read()
	if math.Abs(one.Score-0.6) > 1e-9 || one.Samples != 1 {
		t.Fatalf("one success = %+v, want 0.6 over one sample", one)
	}

	for i := 0; i < 4; i++ {
		if _, err := database.RecordOutcome(ctx, conn, issueID, version.ID,
			database.OutcomeSuccess, "", database.Environment{}, "", "test", "test"); err != nil {
			t.Fatalf("record success %d: %v", i, err)
		}
	}
	five := read()
	if math.Abs(five.Score-7.0/9.0) > 1e-9 || five.Samples != 5 {
		t.Fatalf("five successes = %+v, want 7/9 over five samples", five)
	}

	again := read()
	if again != five {
		t.Fatalf("repeat call changed the result: %+v vs %+v", again, five)
	}

	for i := 0; i < 5; i++ {
		if _, err := database.RecordOutcome(ctx, conn, issueID, version.ID,
			database.OutcomeFailure, "", database.Environment{}, "", "test", "test"); err != nil {
			t.Fatalf("record failure %d: %v", i, err)
		}
	}
	mixed := read()
	if math.Abs(mixed.Score-7.0/14.0) > 1e-9 || mixed.Samples != 10 || mixed.Failures != 5 {
		t.Fatalf("five successes plus five failures = %+v, want 7/14 over ten samples", mixed)
	}
	if mixed.Score >= five.Score {
		t.Fatalf("failures did not lower the score: %v vs %v", mixed.Score, five.Score)
	}
}

// TestTrustIsDatabaseOnly proves no stored column can carry a trust value, so
// a client has no write path for it.
func TestTrustIsDatabaseOnly(t *testing.T) {
	ctx := context.Background()
	conn, _, _, _ := setupRecordSchema(t)

	var columns int
	if err := conn.QueryRow(ctx, `
		select count(*)
		from information_schema.columns
		where table_schema = current_schema()
			and column_name like '%trust%'
	`).Scan(&columns); err != nil {
		t.Fatalf("inspect columns: %v", err)
	}
	if columns != 0 {
		t.Fatalf("%d trust-shaped columns exist, want none; trust must be derived only", columns)
	}

	var functions int
	if err := conn.QueryRow(ctx, `
		select count(*)
		from pg_proc
		where proname = 'errkb_trust'
	`).Scan(&functions); err != nil {
		t.Fatalf("inspect functions: %v", err)
	}
	if functions != 1 {
		t.Fatalf("errkb_trust definitions = %d, want exactly 1", functions)
	}
}
