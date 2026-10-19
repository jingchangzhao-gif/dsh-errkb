package database_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/jingchangzhao-gif/dsh-errkb/internal/database"
)

// recordKnownIssue records one failure and returns the issue it created.
func recordKnownIssue(t *testing.T) (*pgx.Conn, string) {
	t.Helper()
	ctx := context.Background()
	conn, _, connector, session := setupRecordSchema(t)
	if _, err := database.RecordEvent(ctx, conn, recordFixture(connector, session, eventUUID(1), "open failed: EPERM", "proj-a")); err != nil {
		t.Fatalf("record the fixture event: %v", err)
	}
	var issueID string
	if err := conn.QueryRow(ctx, `select id from issues limit 1`).Scan(&issueID); err != nil {
		t.Fatalf("read the fixture issue: %v", err)
	}
	return conn, issueID
}

func TestRecordSolutionVerifiesItAndResolvesTheIssue(t *testing.T) {
	ctx := context.Background()
	conn, issueID := recordKnownIssue(t)

	recorded, err := database.RecordSolution(ctx, conn, issueID, "chmod the directory, then retry", "linux only", database.Environment{OS: "windows", RuntimeVersions: map[string]string{"node": "24.16.0"}}, "test", "test")
	if err != nil {
		t.Fatalf("record solution: %v", err)
	}
	if recorded.SolutionID == "" || recorded.SolutionVersionID == "" || recorded.Version != 1 {
		t.Fatalf("recorded identity = %+v, want non-empty ids and version 1", recorded)
	}

	var solutionStatus, solutionIssue, body, preconditions, source, actor string
	err = conn.QueryRow(ctx, `
		select s.status, s.issue_id, sv.body, sv.preconditions, sv.source, sv.actor
		from solutions s
		join solution_versions sv on sv.solution_id = s.id
		where sv.id = $1
	`, recorded.SolutionVersionID).Scan(&solutionStatus, &solutionIssue, &body, &preconditions, &source, &actor)
	if err != nil {
		t.Fatalf("read the recorded solution version: %v", err)
	}
	if solutionStatus != "verified" || solutionIssue != issueID {
		t.Fatalf("solution = (%s, %s), want (verified, %s)", solutionStatus, solutionIssue, issueID)
	}
	if body != "chmod the directory, then retry" || preconditions != "linux only" || source != "test" || actor != "test" {
		t.Fatalf("version payload = (%q, %q, %q, %q)", body, preconditions, source, actor)
	}

	var issueStatus string
	if err := conn.QueryRow(ctx, `select status from issues where id = $1`, issueID).Scan(&issueStatus); err != nil {
		t.Fatalf("read the issue status: %v", err)
	}
	if issueStatus != "resolved" {
		t.Fatalf("issue status = %s, want resolved", issueStatus)
	}
}

func TestRecordSolutionAppendsAVersionToTheKnownSolution(t *testing.T) {
	ctx := context.Background()
	conn, issueID := recordKnownIssue(t)

	first, err := database.RecordSolution(ctx, conn, issueID, "first fix", "", database.Environment{}, "test", "test")
	if err != nil {
		t.Fatalf("record the first solution: %v", err)
	}
	// A later run saw the same fingerprint, so the issue is open again before
	// the second fix arrives; the write path resolves it once more.
	if _, err := conn.Exec(ctx, `update issues set status = 'open' where id = $1`, issueID); err != nil {
		t.Fatalf("reopen the issue: %v", err)
	}
	second, err := database.RecordSolution(ctx, conn, issueID, "second fix", "only on a cold start", database.Environment{}, "test", "test")
	if err != nil {
		t.Fatalf("record the second solution: %v", err)
	}
	if second.SolutionID != first.SolutionID {
		t.Fatalf("solution ids differ: %s then %s", first.SolutionID, second.SolutionID)
	}
	if second.Version != 2 {
		t.Fatalf("second version = %d, want 2", second.Version)
	}
	var versions int
	if err := conn.QueryRow(ctx, `select count(*) from solution_versions where solution_id = $1`, first.SolutionID).Scan(&versions); err != nil {
		t.Fatalf("count solution versions: %v", err)
	}
	if versions != 2 {
		t.Fatalf("solution versions = %d, want 2", versions)
	}
}

func TestRecordSolutionFailsForAMissingIssue(t *testing.T) {
	ctx := context.Background()
	conn, _ := recordKnownIssue(t)

	_, err := database.RecordSolution(ctx, conn, unregisteredConnectorID(), "fix", "", database.Environment{}, "test", "test")
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("record solution for a missing issue = %v, want pgx.ErrNoRows", err)
	}
}

func TestSetIssueStatusChangesTheRow(t *testing.T) {
	ctx := context.Background()
	conn, issueID := recordKnownIssue(t)

	if err := database.SetIssueStatus(ctx, conn, issueID, "wontfix"); err != nil {
		t.Fatalf("set status: %v", err)
	}
	var status string
	if err := conn.QueryRow(ctx, `select status from issues where id = $1`, issueID).Scan(&status); err != nil {
		t.Fatalf("read the status: %v", err)
	}
	if status != "wontfix" {
		t.Fatalf("status = %s, want wontfix", status)
	}

	if err := database.SetIssueStatus(ctx, conn, issueID, "guessed"); err == nil || !strings.Contains(err.Error(), "unknown status") {
		t.Fatalf("unknown status = %v, want a closed-set rejection", err)
	}
	if err := database.SetIssueStatus(ctx, conn, unregisteredConnectorID(), "resolved"); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("missing issue = %v, want pgx.ErrNoRows", err)
	}
}
