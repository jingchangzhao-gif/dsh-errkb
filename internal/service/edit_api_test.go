package service

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/database"
)

func editIssue(t *testing.T, conn *pgx.Conn, message string) string {
	t.Helper()
	connectorID, sessionID := insertSearchConnector(t, conn)
	event := recordSearchEvent(t, conn, connectorID, sessionID, message, "EPERM", "pnpm install")
	var issueID string
	if err := conn.QueryRow(context.Background(),
		`select id::text from issues where fingerprint = $1`, event.Fingerprint).Scan(&issueID); err != nil {
		t.Fatalf("find the issue: %v", err)
	}
	return issueID
}

func auditRows(t *testing.T, conn *pgx.Conn, action string) int {
	t.Helper()
	var count int
	if err := conn.QueryRow(context.Background(),
		`select count(*) from audit_log where action = $1`, action).Scan(&count); err != nil {
		t.Fatalf("count audit rows: %v", err)
	}
	return count
}

func TestEditApiAppendsVersionsAndAuditsThem(t *testing.T) {
	pool, conn, cleanup := searchTestPool(t)
	defer cleanup()
	issueID := editIssue(t, conn, "EPERM: operation not permitted, rename 'C:/repo/app'")
	edits := NewDetailService(pool)
	ctx := context.Background()

	first, err := edits.AddVersion(ctx, VersionInput{
		IssueID:   issueID,
		Body:      "Retry the install after closing every file handle.",
		Risks:     "interrupts other builds",
		AppliesTo: database.Environment{OS: "windows"},
	})
	if err != nil {
		t.Fatalf("add the first version: %v", err)
	}
	if first.Version != 1 || first.SolutionID == "" || first.ID == "" {
		t.Fatalf("first version = %+v, want version 1 of a new solution", first)
	}

	second, err := edits.AddVersion(ctx, VersionInput{
		IssueID:    issueID,
		SolutionID: first.SolutionID,
		Body:       "Close every file handle first, then retry the install.",
	})
	if err != nil {
		t.Fatalf("add the second version: %v", err)
	}
	if second.Version != 2 || second.SolutionID != first.SolutionID {
		t.Fatalf("second version = %+v, want version 2 of the same solution", second)
	}

	detail, err := edits.Load(ctx, issueID)
	if err != nil {
		t.Fatalf("load the detail: %v", err)
	}
	if len(detail.Solutions) != 1 {
		t.Fatalf("solutions = %d, want one", len(detail.Solutions))
	}
	solution := detail.Solutions[0]
	if len(solution.Versions) != 2 {
		t.Fatalf("versions = %+v, want the full history", solution.Versions)
	}
	if solution.Versions[0].Version != 2 || solution.Versions[1].Version != 1 {
		t.Fatalf("versions are not newest-first: %+v", solution.Versions)
	}
	if !strings.Contains(solution.Versions[1].Body, "Retry the install") {
		t.Fatalf("the older body was overwritten: %q", solution.Versions[1].Body)
	}
	if solution.Trust.Samples != 0 || solution.Trust.Score != 0.5 {
		t.Fatalf("trust before outcomes = %+v, want the prior (0.5)", solution.Trust)
	}
	if got := auditRows(t, conn, "solution.version.added"); got != 2 {
		t.Fatalf("version audit rows = %d, want 2", got)
	}
}

func TestEditApiConfirmsInjectionAffectingChanges(t *testing.T) {
	pool, conn, cleanup := searchTestPool(t)
	defer cleanup()
	issueID := editIssue(t, conn, "EPERM: operation not permitted, rename 'C:/repo/app'")
	edits := NewDetailService(pool)
	ctx := context.Background()

	version, err := edits.AddVersion(ctx, VersionInput{IssueID: issueID, Body: "Retry the install."})
	if err != nil {
		t.Fatalf("add a version: %v", err)
	}

	if err := edits.ChangeSolutionStatus(ctx, version.SolutionID, "verified", false); err != nil {
		t.Fatalf("verify the solution: %v", err)
	}
	if got := auditRows(t, conn, "solution.status.changed"); got != 1 {
		t.Fatalf("status audit rows = %d, want 1", got)
	}

	if err := edits.ChangeSolutionStatus(ctx, version.SolutionID, "blocked", false); !errors.Is(err, ErrConfirmationRequired) {
		t.Fatalf("blocking without confirmation = %v, want ErrConfirmationRequired", err)
	}
	detail, err := edits.Load(ctx, issueID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if detail.Solutions[0].Status != "verified" {
		t.Fatalf("the unconfirmed transition happened: %q", detail.Solutions[0].Status)
	}
	if got := auditRows(t, conn, "solution.status.changed"); got != 1 {
		t.Fatalf("an unconfirmed transition was audited (%d rows)", got)
	}

	if err := edits.ChangeSolutionStatus(ctx, version.SolutionID, "blocked", true); err != nil {
		t.Fatalf("confirm the block: %v", err)
	}
	detail, err = edits.Load(ctx, issueID)
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if detail.Solutions[0].Status != "blocked" {
		t.Fatalf("status = %q, want blocked", detail.Solutions[0].Status)
	}
	if got := auditRows(t, conn, "solution.status.changed"); got != 2 {
		t.Fatalf("confirmed audit rows = %d, want 2", got)
	}

	var from, to string
	if err := conn.QueryRow(ctx, `
		select coalesce(detail->>'from', ''), coalesce(detail->>'to', '')
		from audit_log where action = 'solution.status.changed'
		order by id desc limit 1`).Scan(&from, &to); err != nil {
		t.Fatalf("read the audit detail: %v", err)
	}
	if from != "verified" || to != "blocked" {
		t.Fatalf("audit transition = %s -> %s, want verified -> blocked", from, to)
	}

	if err := edits.MarkIssueMisjudged(ctx, issueID, false); !errors.Is(err, ErrConfirmationRequired) {
		t.Fatalf("misjudging without confirmation = %v, want ErrConfirmationRequired", err)
	}
	if err := edits.MarkIssueMisjudged(ctx, issueID, true); err != nil {
		t.Fatalf("confirm the misjudgement: %v", err)
	}
	if got := auditRows(t, conn, "issue.status.changed"); got != 1 {
		t.Fatalf("issue status audit rows = %d, want 1", got)
	}

	second, err := edits.AddVersion(ctx, VersionInput{IssueID: issueID, Body: "Try another fix."})
	if err != nil {
		t.Fatalf("open a second solution: %v", err)
	}
	if err := edits.DeleteSolution(ctx, second.SolutionID, false); !errors.Is(err, ErrConfirmationRequired) {
		t.Fatalf("deleting without confirmation = %v, want ErrConfirmationRequired", err)
	}
	detail, err = edits.Load(ctx, issueID)
	if err != nil {
		t.Fatalf("reload before the confirmed delete: %v", err)
	}
	if len(detail.Solutions) != 2 {
		t.Fatalf("solutions after an unconfirmed delete = %d, want 2", len(detail.Solutions))
	}
	if err := edits.DeleteSolution(ctx, second.SolutionID, true); err != nil {
		t.Fatalf("confirm the delete: %v", err)
	}
	detail, err = edits.Load(ctx, issueID)
	if err != nil {
		t.Fatalf("reload after the delete: %v", err)
	}
	if len(detail.Solutions) != 1 {
		t.Fatalf("solutions after the delete = %d, want 1", len(detail.Solutions))
	}
	if got := auditRows(t, conn, "solution.deleted"); got != 1 {
		t.Fatalf("deletion audit rows = %d, want 1", got)
	}
}

func TestEditApiShowsTrustButOnlyRecordsOutcomes(t *testing.T) {
	pool, conn, cleanup := searchTestPool(t)
	defer cleanup()
	issueID := editIssue(t, conn, "EPERM: operation not permitted, rename 'C:/repo/app'")
	edits := NewDetailService(pool)
	ctx := context.Background()

	version, err := edits.AddVersion(ctx, VersionInput{IssueID: issueID, Body: "Retry the install."})
	if err != nil {
		t.Fatalf("add a version: %v", err)
	}
	if _, err := database.RecordOutcome(ctx, conn, issueID, version.ID, database.OutcomeSuccess,
		"", database.Environment{OS: "windows"}, "", "test", "test"); err != nil {
		t.Fatalf("record an outcome: %v", err)
	}
	detail, err := edits.Load(ctx, issueID)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	solution := detail.Solutions[0]
	if solution.Trust.Samples != 1 || solution.Trust.Successes != 1 {
		t.Fatalf("trust = %+v, want one success counted", solution.Trust)
	}
	if len(solution.Outcomes) != 1 || solution.Outcomes[0].Kind != "success" {
		t.Fatalf("outcomes = %+v, want the recorded success", solution.Outcomes)
	}

	serviceType := reflect.TypeOf(&DetailService{})
	for index := 0; index < serviceType.NumMethod(); index++ {
		name := serviceType.Method(index).Name
		if strings.Contains(name, "Trust") {
			t.Fatalf("DetailService exposes %q; EKB-046 forbids setting trust from the editor", name)
		}
	}
}

func TestEditApiRejectsIncompleteEdits(t *testing.T) {
	pool, conn, cleanup := searchTestPool(t)
	defer cleanup()
	issueID := editIssue(t, conn, "EPERM: operation not permitted, rename 'C:/repo/app'")
	edits := NewDetailService(pool)
	ctx := context.Background()

	if _, err := edits.AddVersion(ctx, VersionInput{IssueID: issueID}); err == nil {
		t.Fatal("an empty body was accepted")
	}
	if _, err := edits.AddVersion(ctx, VersionInput{Body: "No issue."}); err == nil {
		t.Fatal("a missing issue id was accepted")
	}
	if err := edits.ChangeSolutionStatus(ctx, "", "verified", false); err == nil {
		t.Fatal("a missing solution id was accepted")
	}
	if err := edits.ChangeSolutionStatus(ctx, "00000000-0000-4000-8000-000000000000", "verified", true); !errors.Is(err, ErrDetailNotFound) {
		t.Fatalf("unknown solution = %v, want ErrDetailNotFound", err)
	}
	if _, err := edits.Load(ctx, "00000000-0000-4000-8000-000000000000"); !errors.Is(err, ErrDetailNotFound) {
		t.Fatalf("unknown issue = %v, want ErrDetailNotFound", err)
	}
	if _, err := edits.Load(ctx, "not-a-uuid"); err == nil {
		t.Fatal("an invalid issue id was accepted")
	}
}
