package ui

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jingchangzhao-gif/dsh-errkb/internal/database"
	"github.com/jingchangzhao-gif/dsh-errkb/internal/service"
)

func sampleDetail() service.IssueDetail {
	seen := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	return service.IssueDetail{
		Issue: service.IssueInfo{
			ID: "2f5d6f43-9c1b-4f0c-a4a4-3a2f0e0f6e1a", Title: "EPERM: operation not permitted, rename",
			Category: "tool", Status: "open", Fingerprint: "0123456789ab",
			ProjectKey: "demo", Trigger: "pnpm install", Hits: 4,
			FirstSeen: seen, LastSeen: seen.Add(time.Hour),
		},
		Occurrences: []service.OccurrenceInfo{
			{ID: "occ-1", OccurredAt: seen, OS: "windows", Shell: "powershell",
				RuntimeVersions: map[string]string{"go": "go1.26.4"}, Command: "pnpm install",
				Headline: "EPERM: operation not permitted, rename"},
			{ID: "occ-2", OccurredAt: seen.Add(time.Minute), OS: "linux", Shell: "bash",
				RuntimeVersions: map[string]string{"node": "20.11.0"}, Command: "pnpm install",
				Headline: "EPERM: operation not permitted, rename"},
		},
		Solutions: []service.SolutionInfo{{
			ID: "sol-1", Status: "verified", CreatedAt: seen,
			Trust: database.Trust{Score: 0.75, Samples: 4, Successes: 3, Failures: 1},
			Versions: []service.VersionInfo{
				{ID: "v2", Version: 2, Body: "Close every file handle before retrying.", Source: "owner",
					Risks: "interrupts other builds", AppliesTo: database.Environment{OS: "windows"},
					EffectiveAt: seen.Add(time.Hour)},
				{ID: "v1", Version: 1, Body: "Retry the install.", Source: "owner",
					AppliesTo: database.Environment{OS: "windows", Shell: "powershell"}},
			},
			Outcomes: []service.OutcomeInfo{
				{ID: "o1", SolutionVersionID: "v1", Kind: "success", ObservedAt: seen,
					Environment: database.Environment{OS: "windows", Shell: "powershell"}},
				{ID: "o2", SolutionVersionID: "v1", Kind: "failure", ObservedAt: seen,
					Environment: database.Environment{OS: "linux", Shell: "bash"}},
			},
		}},
	}
}

func TestDetailShowsTheWholeHistoryWithoutOverwriting(t *testing.T) {
	page := NewDetailPage(sampleDetail(), nil)
	rows := page.VersionRows("sol-1")
	text := strings.Join(rowTexts(rows), "\n")
	for _, want := range []string{"v1", "v2", "Retry the install", "Close every file handle", "trust 0.75"} {
		if !strings.Contains(text, want) {
			t.Fatalf("version rows %q are missing %q", text, want)
		}
	}
	if strings.Index(text, "v2") > strings.Index(text, "v1") {
		t.Fatalf("versions are not newest-first: %q", text)
	}
}

func TestDetailSeparatesOccurrencesByEnvironment(t *testing.T) {
	page := NewDetailPage(sampleDetail(), nil)
	text := strings.Join(rowTexts(page.OccurrenceRows()), "\n")
	if !strings.Contains(text, "windows") || !strings.Contains(text, "powershell") {
		t.Fatalf("occurrence rows lost the Windows environment: %q", text)
	}
	if !strings.Contains(text, "linux") || !strings.Contains(text, "bash") {
		t.Fatalf("occurrence rows lost the Linux environment: %q", text)
	}

	groups := page.EnvironmentGroups()
	if len(groups) < 2 {
		t.Fatalf("environment groups = %+v, want both machines", groups)
	}
	var windows, linux *EnvironmentGroup
	for index := range groups {
		switch {
		case strings.Contains(groups[index].Key, "windows"):
			windows = &groups[index]
		case strings.Contains(groups[index].Key, "linux"):
			linux = &groups[index]
		}
	}
	if windows == nil || linux == nil {
		t.Fatalf("environment groups = %+v, want windows and linux", groups)
	}
	if windows.Successes < 1 || linux.Failures < 1 {
		t.Fatalf("outcome counts did not reach their environments: windows=%+v linux=%+v", windows, linux)
	}
}

func TestDetailCopyCarriesSourceEnvironmentAndRisks(t *testing.T) {
	page := NewDetailPage(sampleDetail(), nil)
	payload, ok := page.CopyVersion("sol-1", "v2")
	if !ok {
		t.Fatal("copy of the newest version failed")
	}
	for _, want := range []string{"Close every file handle", "source: owner", "applies to: windows", "risks: interrupts other builds"} {
		if !strings.Contains(payload, want) {
			t.Fatalf("copy payload %q is missing %q", payload, want)
		}
	}
	if strings.Contains(strings.ToLower(payload), "exec") {
		t.Fatalf("copy payload must not suggest execution: %q", payload)
	}

	payload, ok = page.CopyVersion("sol-1", "v1")
	if !ok {
		t.Fatal("copy of the older version failed")
	}
	if !strings.Contains(payload, "risks: (not recorded)") {
		t.Fatalf("copy payload %q hid the missing risk field", payload)
	}
}

type fakeApplier struct {
	calls     []string
	confirmed []bool
}

func (f *fakeApplier) ChangeSolutionStatus(ctx context.Context, solutionID, status string, confirmed bool) error {
	f.calls = append(f.calls, "status:"+status)
	f.confirmed = append(f.confirmed, confirmed)
	return nil
}

func (f *fakeApplier) MarkIssueMisjudged(ctx context.Context, issueID string, confirmed bool) error {
	f.calls = append(f.calls, "misjudged")
	f.confirmed = append(f.confirmed, confirmed)
	return nil
}

func (f *fakeApplier) DeleteSolution(ctx context.Context, solutionID string, confirmed bool) error {
	f.calls = append(f.calls, "delete")
	f.confirmed = append(f.confirmed, confirmed)
	return nil
}

func TestDetailConfirmsInjectionAffectingChanges(t *testing.T) {
	applier := &fakeApplier{}
	page := NewDetailPage(sampleDetail(), applier)

	// A verified->verified style change needs no confirmation.
	if err := page.RequestSolutionStatus("sol-1", "candidate"); err != nil {
		t.Fatalf("request candidate: %v", err)
	}
	if len(applier.calls) != 1 || applier.confirmed[0] {
		t.Fatalf("non-blocking change calls = %+v confirmed = %+v", applier.calls, applier.confirmed)
	}

	for _, destructive := range []string{"deprecated", "blocked"} {
		page = NewDetailPage(sampleDetail(), applier)
		applier.calls, applier.confirmed = nil, nil
		if err := page.RequestSolutionStatus("sol-1", destructive); err != nil {
			t.Fatalf("request %s: %v", destructive, err)
		}
		if page.Pending() == nil {
			t.Fatalf("%s ran without confirmation", destructive)
		}
		if len(applier.calls) != 0 {
			t.Fatalf("%s reached the applier before confirmation", destructive)
		}
		page.Cancel()
		if page.Pending() != nil || len(applier.calls) != 0 {
			t.Fatalf("%s was not cancelled cleanly", destructive)
		}
		if err := page.RequestSolutionStatus("sol-1", destructive); err != nil {
			t.Fatalf("re-request %s: %v", destructive, err)
		}
		if err := page.Confirm(context.Background()); err != nil {
			t.Fatalf("confirm %s: %v", destructive, err)
		}
		if len(applier.calls) != 1 || !applier.confirmed[0] {
			t.Fatalf("%s confirm calls = %+v confirmed = %+v", destructive, applier.calls, applier.confirmed)
		}
	}

	page = NewDetailPage(sampleDetail(), applier)
	applier.calls, applier.confirmed = nil, nil
	if err := page.RequestMisjudge(); err != nil {
		t.Fatalf("request misjudge: %v", err)
	}
	if page.Pending() == nil {
		t.Fatal("misjudgement ran without confirmation")
	}
	if err := page.Confirm(context.Background()); err != nil {
		t.Fatalf("confirm misjudge: %v", err)
	}
	if len(applier.calls) != 1 || !applier.confirmed[0] {
		t.Fatalf("misjudge calls = %+v confirmed = %+v", applier.calls, applier.confirmed)
	}

	page = NewDetailPage(sampleDetail(), applier)
	applier.calls, applier.confirmed = nil, nil
	if err := page.RequestDeleteSolution("sol-1"); err != nil {
		t.Fatalf("request delete: %v", err)
	}
	if page.Pending() == nil {
		t.Fatal("deletion ran without confirmation")
	}
	if err := page.Confirm(context.Background()); err != nil {
		t.Fatalf("confirm delete: %v", err)
	}
	if len(applier.calls) != 1 || !applier.confirmed[0] {
		t.Fatalf("delete calls = %+v confirmed = %+v", applier.calls, applier.confirmed)
	}

	if err := page.Confirm(context.Background()); err == nil {
		t.Fatal("a second confirm succeeded with nothing pending")
	}
}

func TestDetailHasNoExecutionOrTrustSetter(t *testing.T) {
	pageType := reflect.TypeOf(&DetailPage{})
	for index := 0; index < pageType.NumMethod(); index++ {
		name := pageType.Method(index).Name
		switch {
		case strings.Contains(name, "Execute"), strings.Contains(name, "RunFix"),
			strings.Contains(name, "SetTrust"), strings.Contains(name, "Retry"):
			t.Fatalf("detail page exposes %q; EKB-046 forbids executing fixes or setting trust", name)
		}
	}
	applierType := reflect.TypeOf((*ChangeApplier)(nil)).Elem()
	if applierType.NumMethod() != 3 {
		t.Fatalf("ChangeApplier has %d methods, want exactly the three audited edits", applierType.NumMethod())
	}
}

func TestDetailRowsIncludeSummaryAndNoteHeadings(t *testing.T) {
	page := NewDetailPage(sampleDetail(), nil)
	rows := page.Rows()
	if len(rows) < 4 {
		t.Fatalf("page rows = %d, want the summary, occurrences and solutions", len(rows))
	}
	var kinds []string
	for _, row := range rows {
		kinds = append(kinds, row.Kind)
	}
	joined := strings.Join(kinds, ",")
	for _, kind := range []string{DetailKindSummary, DetailKindOccurrence, DetailKindVersion, DetailKindOutcome} {
		if !strings.Contains(joined, kind) {
			t.Fatalf("page rows are missing kind %q: %s", kind, joined)
		}
	}
}

func rowTexts(rows []DetailRow) []string {
	texts := make([]string, 0, len(rows))
	for _, row := range rows {
		texts = append(texts, row.Text)
	}
	return texts
}
