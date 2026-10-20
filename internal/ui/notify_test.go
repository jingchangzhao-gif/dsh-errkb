package ui

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/core"
	"github.com/jingchangzhao-gif/error-knowledge-base/internal/service"
)

func sampleCandidate(rank int) service.NoticeCandidate {
	return service.NoticeCandidate{
		Rank:              rank,
		IssueID:           fmt.Sprintf("issue-%d", rank),
		Fingerprint:       "0123456789ab",
		Title:             fmt.Sprintf("EPERM: rename failed (candidate %d)", rank),
		MatchReason:       "exact_fingerprint",
		Approximate:       rank > 1,
		Trust:             0.6,
		Hits:              3,
		SolutionVersionID: "version-1",
		SolutionBody:      "Close the file handles, then rerun the install.",
		SolutionSource:    "claude",
		AppliesTo:         `{"os":"linux","shell":"bash"}`,
		Risk:              "stops other builds while the handles are closed",
	}
}

func sampleNotice(count int) service.Notice {
	notice := service.Notice{
		Kind:          service.NoticeKindDesktop,
		Title:         "errkb: known fix for this failure",
		TokenEstimate: 42,
		IssueID:       "issue-1",
	}
	for index := 0; index < count; index++ {
		notice.Candidates = append(notice.Candidates, sampleCandidate(index+1))
	}
	notice.Body = "The errkb knowledge base already recorded this failure."
	return notice
}

func TestNotificationCapsAtThreeCandidates(t *testing.T) {
	notification := NewNotification(sampleNotice(5))
	if got := len(notification.Candidates()); got != 3 {
		t.Fatalf("candidates = %d, want the hard cap of 3", got)
	}
	if got := len(notification.Rows()); got != 3 {
		t.Fatalf("rows = %d, want 3", got)
	}
	notification.MoveSelection(10)
	if selected, ok := notification.SelectedCandidate(); !ok || selected.Rank != 3 {
		t.Fatalf("selection after moving past the end = %+v/%v, want rank 3", selected, ok)
	}
}

func TestNotificationNeverTakesFocusOrExecutes(t *testing.T) {
	notification := NewNotification(sampleNotice(1))
	if notification.Modal() || notification.StealsFocus() || notification.Executes() {
		t.Fatalf("notification is modal=%v stealsFocus=%v executes=%v, want all false",
			notification.Modal(), notification.StealsFocus(), notification.Executes())
	}
	// No method may offer to run a fix: the whole exported surface is view,
	// selection, copy and open. A new method has to be justified here.
	methods := reflect.TypeOf(notification)
	allowed := map[string]bool{
		"Body": true, "Candidates": true, "CopySelected": true, "Executes": true,
		"Modal": true, "MoveSelection": true, "OpenSelectedIssue": true, "Rows": true,
		"Select": true, "SelectedCandidate": true, "StealsFocus": true, "Title": true,
		"TokenNote": true, "Visible": true,
	}
	for index := 0; index < methods.NumMethod(); index++ {
		name := methods.Method(index).Name
		if !allowed[name] {
			t.Fatalf("unexpected notification method %q; only view, copy and open may exist", name)
		}
	}
}

func TestNotificationExplainsApplicability(t *testing.T) {
	notice := sampleNotice(1)
	notice.Candidates[0].EnvironmentDiff = []core.EnvironmentDifference{
		{Field: "os", Query: "windows", Candidate: "linux"},
	}
	notification := NewNotification(notice)
	rows := notification.Rows()
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if !strings.Contains(rows[0].Warning, "may not apply") || !strings.Contains(rows[0].Warning, "os") {
		t.Fatalf("warning = %q, want a may-not-apply note naming the environment field", rows[0].Warning)
	}
}

func TestNotificationShowsTrustWithSampleSize(t *testing.T) {
	notification := NewNotification(sampleNotice(1))
	rows := notification.Rows()
	if !strings.Contains(rows[0].Trust, "trust 0.60") {
		t.Fatalf("trust line = %q, want the trust score", rows[0].Trust)
	}
	if !strings.Contains(rows[0].Trust, "3 recorded occurrence") {
		t.Fatalf("trust line = %q, want the sample size next to the score", rows[0].Trust)
	}
}

func TestNotificationCopyKeepsSourceEnvironmentRisk(t *testing.T) {
	notification := NewNotification(sampleNotice(1))
	copied, ok := notification.CopySelected()
	if !ok {
		t.Fatal("copy returned no text")
	}
	for _, wanted := range []string{"Close the file handles", "source: claude", "applies to:", "risk:", "issue: issue-1"} {
		if !strings.Contains(copied, wanted) {
			t.Fatalf("copied text = %q, want it to contain %q", copied, wanted)
		}
	}
}

func TestNotificationTokenNoteIsAnEstimate(t *testing.T) {
	notification := NewNotification(sampleNotice(1))
	note := notification.TokenNote()
	if !strings.Contains(note, "42") {
		t.Fatalf("token note = %q, want the estimate", note)
	}
	if !strings.Contains(note, "estimated") || !strings.Contains(note, "not an exact count") {
		t.Fatalf("token note = %q, want explicit estimate wording", note)
	}
}

func TestNotificationSelectionClampsAndCopies(t *testing.T) {
	notification := NewNotification(sampleNotice(2))
	notification.MoveSelection(-5)
	if selected, ok := notification.SelectedCandidate(); !ok || selected.Rank != 1 {
		t.Fatalf("selection after moving up = %+v/%v, want rank 1", selected, ok)
	}
	notification.MoveSelection(1)
	if selected, ok := notification.SelectedCandidate(); !ok || selected.Rank != 2 {
		t.Fatalf("selection after moving down = %+v/%v, want rank 2", selected, ok)
	}
	notification.Select(99)
	if selected, ok := notification.SelectedCandidate(); !ok || selected.Rank != 2 {
		t.Fatalf("out-of-range select moved the highlight: %+v/%v", selected, ok)
	}
	if copied, ok := notification.CopySelected(); !ok || !strings.Contains(copied, "issue-2") {
		t.Fatalf("copied text does not belong to the selected candidate: %q/%v", copied, ok)
	}
}

func TestNotificationStaysHiddenForSuppressedAndEmpty(t *testing.T) {
	suppressed := sampleNotice(1)
	suppressed.Suppressed = true
	if NewNotification(suppressed).Visible() {
		t.Fatal("a suppressed notice is visible")
	}
	if NewNotification(service.Notice{}).Visible() {
		t.Fatal("an empty notice is visible")
	}
	if _, ok := NewNotification(service.Notice{}).SelectedCandidate(); ok {
		t.Fatal("an empty notice has a selected candidate")
	}
	if _, ok := NewNotification(sampleNotice(1)).OpenSelectedIssue(); !ok {
		t.Fatal("open issue returned no id")
	}
}
