package service

import (
	"context"
	"strings"
	"testing"

	"github.com/jingchangzhao-gif/dsh-errkb/internal/core"
	"github.com/jingchangzhao-gif/dsh-errkb/internal/database"
)

func TestEstimateNoticeTokens(t *testing.T) {
	cases := []struct {
		text string
		want int
	}{
		{"", 0},
		{"   \n\t ", 0},
		{"abcd", 1},
		{"abcdefgh", 2},
		{"错误信息", 1},
	}
	for _, testCase := range cases {
		if got := EstimateNoticeTokens(testCase.text); got != testCase.want {
			t.Fatalf("EstimateNoticeTokens(%q) = %d, want %d", testCase.text, got, testCase.want)
		}
	}
}

func TestComposeNoticeShowsBadgesTrustAndRisk(t *testing.T) {
	candidates := []NoticeCandidate{
		{
			Rank: 1, Title: "EPERM: rename failed",
			Approximate: false, Trust: 0.8, Hits: 5,
			SolutionBody:   "Close the handles and retry.",
			SolutionSource: "claude", AppliesTo: `{"os":"windows"}`, Risk: "stops other builds",
		},
		{
			Rank: 2, Title: "EPERM: rename failed again",
			Approximate: true, Trust: 0.5, Hits: 2,
			SolutionBody:   "Retry later.",
			SolutionSource: "", AppliesTo: "", Risk: "",
		},
	}
	title, body := composeNotice(candidates)
	if !strings.Contains(title, "2 possible fixes") {
		t.Fatalf("title = %q, want the candidate count", title)
	}
	for _, wanted := range []string{
		"[exact match]",
		"[approximate — confirm before reuse]",
		"trust 0.80 (based on 5 recorded occurrence(s))",
		"trust 0.50 (based on 2 recorded occurrence(s))",
		"source: unknown",
		"applies to: no recorded environment",
		"risk: not recorded",
	} {
		if !strings.Contains(body, wanted) {
			t.Fatalf("body is missing %q:\n%s", wanted, body)
		}
	}
	if !strings.Contains(body, "\n1) EPERM: rename failed\n") {
		t.Fatalf("body lost the numbered candidate header:\n%s", body)
	}
}

// TestNoticeSuppressesRepeatsAndRecordsTheDelivery is the card's acceptance 7
// and 8 together: the composed content and token estimate land in the notices
// table, and a second notification for the same issue inside the window is
// suppressed instead of recorded again.
func TestNoticeSuppressesRepeatsAndRecordsTheDelivery(t *testing.T) {
	pool, conn, cleanup := searchTestPool(t)
	defer cleanup()
	ctx := context.Background()
	connectorID, sessionID := insertSearchConnector(t, conn)
	event := recordSearchEvent(t, conn, connectorID, sessionID,
		"EPERM: operation not permitted, rename 'C:/repo/app'", "EPERM", "pnpm install")

	var issueID string
	if err := conn.QueryRow(ctx,
		`select id::text from issues where fingerprint = $1 and project_key = $2`,
		event.Fingerprint, event.ProjectKey).Scan(&issueID); err != nil {
		t.Fatalf("find the issue: %v", err)
	}
	solutionID, err := database.CreateSolution(ctx, conn, issueID, "claude", "owner")
	if err != nil {
		t.Fatalf("create the solution: %v", err)
	}
	if _, err := database.AddSolutionVersion(ctx, conn, solutionID,
		"Close the file handles, then rerun the install.", "",
		"stops other builds while the handles are closed",
		database.Environment{OS: "linux", Shell: "bash"}, "claude", "owner"); err != nil {
		t.Fatalf("add the version: %v", err)
	}
	if err := database.TransitionSolution(ctx, conn, solutionID, database.SolutionVerified); err != nil {
		t.Fatalf("verify the solution: %v", err)
	}
	// The applicability note compares the environment stored with the
	// occurrence (not the solution's applies_to), so record the failure on
	// Linux and ask the notice from Windows.
	if _, err := conn.Exec(ctx,
		`update occurrences set environment = '{"os":"linux","shell":"bash"}'::jsonb`); err != nil {
		t.Fatalf("set the occurrence environment: %v", err)
	}

	notices := NewNoticeService(pool)
	query := NoticeQuery{
		Fingerprint: event.Fingerprint,
		ProjectKey:  event.ProjectKey,
		Environment: core.Environment{OS: "windows", Shell: "powershell"},
	}

	first, err := notices.Notice(ctx, query)
	if err != nil {
		t.Fatalf("first notice: %v", err)
	}
	if !first.Visible() {
		t.Fatalf("first notice is invisible: %+v", first)
	}
	if len(first.Candidates) != 1 {
		t.Fatalf("candidates = %d, want 1", len(first.Candidates))
	}
	candidate := first.Candidates[0]
	if candidate.Approximate {
		t.Fatalf("a verified fingerprint match was marked approximate: %+v", candidate)
	}
	if first.TokenEstimate <= 0 {
		t.Fatalf("token estimate = %d, want a positive estimate", first.TokenEstimate)
	}
	for _, wanted := range []string{
		"estimated size", "not an exact count",
		"source: claude", "risk: stops other builds while the handles are closed",
	} {
		if !strings.Contains(first.Body, wanted) {
			t.Fatalf("body is missing %q:\n%s", wanted, first.Body)
		}
	}
	if !strings.Contains(first.Body, "may not apply") {
		t.Fatalf("body did not warn about the environment difference:\n%s", first.Body)
	}

	var stored int
	if err := conn.QueryRow(ctx, `select count(*) from notices`).Scan(&stored); err != nil {
		t.Fatalf("count notices: %v", err)
	}
	if stored != 1 {
		t.Fatalf("notices rows = %d, want 1", stored)
	}
	var body string
	var tokens int
	if err := conn.QueryRow(ctx, `select body, estimated_tokens from notices limit 1`).Scan(&body, &tokens); err != nil {
		t.Fatalf("read the notice row: %v", err)
	}
	if !strings.Contains(body, "Close the file handles") || !strings.Contains(body, "estimated size") {
		t.Fatalf("stored body = %q, want the composed content", body)
	}
	if tokens != first.TokenEstimate {
		t.Fatalf("stored estimated_tokens = %d, want %d", tokens, first.TokenEstimate)
	}

	second, err := notices.Notice(ctx, query)
	if err != nil {
		t.Fatalf("second notice: %v", err)
	}
	if !second.Suppressed || second.Visible() {
		t.Fatalf("second notice suppressed=%v visible=%v, want suppressed", second.Suppressed, second.Visible())
	}
	if err := conn.QueryRow(ctx, `select count(*) from notices`).Scan(&stored); err != nil {
		t.Fatalf("count notices after the repeat: %v", err)
	}
	if stored != 1 {
		t.Fatalf("a suppressed repeat was recorded: rows = %d", stored)
	}
}

// TestNoticeStaysSilentWithoutAKnownFix keeps the service from notifying about
// a failure that has no solution yet.
func TestNoticeStaysSilentWithoutAKnownFix(t *testing.T) {
	pool, conn, cleanup := searchTestPool(t)
	defer cleanup()
	ctx := context.Background()
	connectorID, sessionID := insertSearchConnector(t, conn)
	recordSearchEvent(t, conn, connectorID, sessionID,
		"ENOENT: no such file or directory, open 'C:/repo/missing.ts'", "ENOENT", "node build.js")

	notices := NewNoticeService(pool)
	notice, err := notices.Notice(ctx, NoticeQuery{Message: "ENOENT: no such file or directory"})
	if err != nil {
		t.Fatalf("notice without a fix: %v", err)
	}
	if notice.Visible() || len(notice.Candidates) != 0 {
		t.Fatalf("notice = %+v, want no candidates and no visibility", notice)
	}
	var stored int
	if err := conn.QueryRow(ctx, `select count(*) from notices`).Scan(&stored); err != nil {
		t.Fatalf("count notices: %v", err)
	}
	if stored != 0 {
		t.Fatalf("a fix-less notice was recorded: rows = %d", stored)
	}
}
