package ui

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jingchangzhao-gif/dsh-errkb/internal/database"
	"github.com/jingchangzhao-gif/dsh-errkb/internal/service"
)

// DetailRow is one painted line of the detail page. The model owns the text so
// the Win32 paint loop and the tests read the same data.
type DetailRow struct {
	Kind   string // summary, occurrence, version, outcome, note
	Text   string
	Accent Accent
}

// Detail section kinds.
const (
	DetailKindSummary    = "summary"
	DetailKindOccurrence = "occurrence"
	DetailKindVersion    = "version"
	DetailKindOutcome    = "outcome"
	DetailKindNote       = "note"
)

// ChangeKind names the audited edits the page can request.
type ChangeKind string

// The audited edit kinds.
const (
	ChangeSolutionStatus ChangeKind = "solution-status"
	ChangeIssueMisjudged ChangeKind = "issue-misjudged"
	ChangeDeleteSolution ChangeKind = "delete-solution"
)

// PendingChange is a change that still needs the person's confirmation.
type PendingChange struct {
	Kind       ChangeKind
	IssueID    string
	SolutionID string
	Status     string
	Label      string // what the confirmation prompt should say
}

// ChangeApplier is the service side the page calls. Tests pass a fake; the
// tray passes *service.DetailService.
type ChangeApplier interface {
	ChangeSolutionStatus(ctx context.Context, solutionID, status string, confirmed bool) error
	MarkIssueMisjudged(ctx context.Context, issueID string, confirmed bool) error
	DeleteSolution(ctx context.Context, solutionID string, confirmed bool) error
}

// ErrNoEditor is returned when an edit is requested without a connected applier.
var ErrNoEditor = errors.New("the detail page is not connected to the editor service")

// EnvironmentGroup summarises one environment seen in the occurrences and the
// outcomes, so "does this fix apply on my machine" has a visible answer
// (acceptance 6).
type EnvironmentGroup struct {
	Key         string
	Occurrences int
	Successes   int
	Failures    int
	Unknowns    int
}

// DetailPage is the detail view and the editor state machine without Win32.
// It shows trust read-only and offers no way to execute a fix (acceptances 3
// and 4): the only edit actions are audited service calls behind a
// confirmation step.
type DetailPage struct {
	detail  service.IssueDetail
	editor  ChangeApplier
	pending *PendingChange
	status  string
	copied  string
}

// NewDetailPage wraps loaded data and an optional applier.
func NewDetailPage(detail service.IssueDetail, editor ChangeApplier) *DetailPage {
	return &DetailPage{detail: detail, editor: editor}
}

// SetDetail replaces the loaded data, for example after an edit was applied.
func (p *DetailPage) SetDetail(detail service.IssueDetail) {
	p.detail = detail
}

// Detail returns the loaded data (read-only use).
func (p *DetailPage) Detail() service.IssueDetail { return p.detail }

// Status is the page's footer message (confirmation prompt, copy notice, error).
func (p *DetailPage) Status() string { return p.status }

// Pending returns the change waiting for confirmation, or nil.
func (p *DetailPage) Pending() *PendingChange {
	if p.pending == nil {
		return nil
	}
	copied := *p.pending
	return &copied
}

// Copied returns the last copy payload (the clipboard text).
func (p *DetailPage) Copied() string { return p.copied }

func (p *DetailPage) solution(solutionID string) (service.SolutionInfo, bool) {
	for _, solution := range p.detail.Solutions {
		if solution.ID == solutionID {
			return solution, true
		}
	}
	return service.SolutionInfo{}, false
}

// SummaryRows renders the issue summary, including the read-only trust source.
func (p *DetailPage) SummaryRows() []DetailRow {
	issue := p.detail.Issue
	rows := []DetailRow{
		{Kind: DetailKindSummary, Text: issue.Title, Accent: AccentExact},
		{Kind: DetailKindSummary, Text: fmt.Sprintf("%s · %s · hit %d× · fingerprint %s",
			issue.Status, issue.Category, issue.Hits, issue.Fingerprint), Accent: AccentMuted},
	}
	if issue.ProjectKey != "" {
		rows = append(rows, DetailRow{Kind: DetailKindSummary,
			Text: "project " + issue.ProjectKey, Accent: AccentMuted})
	}
	if issue.Trigger != "" {
		rows = append(rows, DetailRow{Kind: DetailKindSummary,
			Text: "trigger " + shorten(issue.Trigger, 100), Accent: AccentMuted})
	}
	rows = append(rows, DetailRow{Kind: DetailKindSummary,
		Text: fmt.Sprintf("first seen %s · last seen %s",
			issue.FirstSeen.Format("2006-01-02 15:04"), issue.LastSeen.Format("2006-01-02 15:04")),
		Accent: AccentMuted})
	return rows
}

// OccurrenceRows renders every occurrence with the environment it happened in.
func (p *DetailPage) OccurrenceRows() []DetailRow {
	rows := make([]DetailRow, 0, len(p.detail.Occurrences))
	for _, occurrence := range p.detail.Occurrences {
		environment := formatEnvironment(database.Environment{
			OS:              occurrence.OS,
			Shell:           occurrence.Shell,
			Connector:       occurrence.Connector,
			RuntimeVersions: occurrence.RuntimeVersions,
		})
		text := occurrence.OccurredAt.Format("2006-01-02 15:04") + " · " + environment
		if occurrence.Command != "" {
			text += " · " + shorten(occurrence.Command, 60)
		}
		rows = append(rows, DetailRow{Kind: DetailKindOccurrence, Text: text, Accent: AccentMuted})
	}
	return rows
}

// VersionRows renders the whole version history, newest first. Old versions
// remain visible because the editor only appends (acceptance 1).
func (p *DetailPage) VersionRows(solutionID string) []DetailRow {
	solution, ok := p.solution(solutionID)
	if !ok {
		return nil
	}
	rows := []DetailRow{
		{Kind: DetailKindVersion, Text: fmt.Sprintf("solution %s · %s · trust %.2f (%d samples)",
			solution.ID, solution.Status, solution.Trust.Score, solution.Trust.Samples),
			Accent: trustAccent(solution.Status, solution.Trust)},
	}
	for _, version := range sortVersionsNewestFirst(solution.Versions) {
		text := fmt.Sprintf("v%d — %s", version.Version, shorten(version.Body, 120))
		if version.Preconditions != "" {
			text += " — pre: " + shorten(version.Preconditions, 80)
		}
		if version.Risks != "" {
			text += " — risk: " + shorten(version.Risks, 80)
		}
		rows = append(rows, DetailRow{Kind: DetailKindVersion, Text: text, Accent: AccentMuted})
	}
	return rows
}

// OutcomeRows renders the recorded uses with their environment, so success on
// one machine is distinguishable from failure on another.
func (p *DetailPage) OutcomeRows(solutionID string) []DetailRow {
	solution, ok := p.solution(solutionID)
	if !ok {
		return nil
	}
	rows := make([]DetailRow, 0, len(solution.Outcomes))
	for _, outcome := range solution.Outcomes {
		text := fmt.Sprintf("%s · %s · %s", outcome.Kind,
			formatEnvironment(outcome.Environment), outcome.ObservedAt.Format("2006-01-02 15:04"))
		if outcome.Note != "" {
			text += " · " + shorten(outcome.Note, 80)
		}
		accent := AccentMuted
		switch outcome.Kind {
		case string(database.OutcomeSuccess):
			accent = AccentExact
		case string(database.OutcomeFailure):
			accent = AccentApproximate
		}
		rows = append(rows, DetailRow{Kind: DetailKindOutcome, Text: text, Accent: accent})
	}
	return rows
}

// Rows is the whole page in paint order.
func (p *DetailPage) Rows() []DetailRow {
	rows := p.SummaryRows()
	if len(p.detail.Occurrences) > 0 {
		rows = append(rows, DetailRow{Kind: DetailKindNote,
			Text: fmt.Sprintf("occurrences (%d)", len(p.detail.Occurrences)), Accent: AccentMuted})
		rows = append(rows, p.OccurrenceRows()...)
	}
	for _, solution := range p.detail.Solutions {
		rows = append(rows, p.VersionRows(solution.ID)...)
		rows = append(rows, p.OutcomeRows(solution.ID)...)
	}
	return rows
}

// EnvironmentGroups summarises the occurrences and outcomes per environment.
func (p *DetailPage) EnvironmentGroups() []EnvironmentGroup {
	groups := map[string]*EnvironmentGroup{}
	group := func(key string) *EnvironmentGroup {
		if existing, ok := groups[key]; ok {
			return existing
		}
		created := &EnvironmentGroup{Key: key}
		groups[key] = created
		return created
	}
	for _, occurrence := range p.detail.Occurrences {
		group(environmentGroupKey(database.Environment{OS: occurrence.OS, Shell: occurrence.Shell})).Occurrences++
	}
	for _, solution := range p.detail.Solutions {
		for _, outcome := range solution.Outcomes {
			entry := group(environmentGroupKey(outcome.Environment))
			switch outcome.Kind {
			case string(database.OutcomeSuccess):
				entry.Successes++
			case string(database.OutcomeFailure):
				entry.Failures++
			default:
				entry.Unknowns++
			}
		}
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	ordered := make([]EnvironmentGroup, 0, len(keys))
	for _, key := range keys {
		ordered = append(ordered, *groups[key])
	}
	return ordered
}

// CopyVersion builds the copy payload. It always carries the three things the
// card requires before a fix is reused: source, applicable environment and
// risks (acceptance 5). Copying text is not execution: nothing here runs.
func (p *DetailPage) CopyVersion(solutionID, versionID string) (string, bool) {
	solution, ok := p.solution(solutionID)
	if !ok {
		return "", false
	}
	for _, version := range solution.Versions {
		if version.ID != versionID {
			continue
		}
		payload := strings.Join([]string{
			"fix: " + version.Body,
			"source: " + orNone(version.Source),
			"applies to: " + formatEnvironment(version.AppliesTo),
			"risks: " + orNone(version.Risks),
		}, "\n")
		p.copied = payload
		p.status = "copied the fix with its source, environment and risks — nothing was executed"
		return payload, true
	}
	return "", false
}

// RequestSolutionStatus applies a status change. Deprecating or blocking a fix
// changes injection immediately, so those transitions wait for Confirm.
func (p *DetailPage) RequestSolutionStatus(solutionID, status string) error {
	switch status {
	case "candidate", "verified", "deprecated", "blocked":
	default:
		return fmt.Errorf("detail: %q is not a solution status", status)
	}
	if _, ok := p.solution(solutionID); !ok {
		return fmt.Errorf("detail: unknown solution %s", solutionID)
	}
	change := &PendingChange{Kind: ChangeSolutionStatus, SolutionID: solutionID, Status: status,
		Label: "change the fix to " + status}
	if status == "deprecated" || status == "blocked" {
		p.pending = change
		p.status = "confirm: " + change.Label + " (this changes what gets injected)"
		return nil
	}
	return p.apply(context.Background(), change, false)
}

// RequestMisjudge marks the issue itself as a misjudgement. It always needs a
// confirmation because it stops injection for the issue.
func (p *DetailPage) RequestMisjudge() error {
	p.pending = &PendingChange{Kind: ChangeIssueMisjudged, IssueID: p.detail.Issue.ID,
		Label: "mark this issue as a misjudgement"}
	p.status = "confirm: " + p.pending.Label
	return nil
}

// RequestDeleteSolution asks for deletion of a fix and its history. It always
// needs a confirmation and is never performed by this page directly.
func (p *DetailPage) RequestDeleteSolution(solutionID string) error {
	if _, ok := p.solution(solutionID); !ok {
		return fmt.Errorf("detail: unknown solution %s", solutionID)
	}
	p.pending = &PendingChange{Kind: ChangeDeleteSolution, SolutionID: solutionID,
		Label: "delete this fix and its version history"}
	p.status = "confirm: " + p.pending.Label
	return nil
}

// Confirm applies the pending change. It is the only path that can perform a
// status change affecting injection or a deletion.
func (p *DetailPage) Confirm(ctx context.Context) error {
	if p.pending == nil {
		return errors.New("detail: nothing to confirm")
	}
	change := *p.pending
	if err := p.apply(ctx, &change, true); err != nil {
		p.status = "not applied: " + err.Error()
		return err
	}
	p.pending = nil
	return nil
}

// Cancel drops the pending change.
func (p *DetailPage) Cancel() {
	p.pending = nil
	p.status = "cancelled — nothing changed"
}

func (p *DetailPage) apply(ctx context.Context, change *PendingChange, confirmed bool) error {
	if p.editor == nil {
		return ErrNoEditor
	}
	var err error
	switch change.Kind {
	case ChangeSolutionStatus:
		err = p.editor.ChangeSolutionStatus(ctx, change.SolutionID, change.Status, confirmed)
	case ChangeIssueMisjudged:
		err = p.editor.MarkIssueMisjudged(ctx, change.IssueID, confirmed)
	case ChangeDeleteSolution:
		err = p.editor.DeleteSolution(ctx, change.SolutionID, confirmed)
	default:
		return fmt.Errorf("detail: unknown change %q", change.Kind)
	}
	if err != nil {
		return err
	}
	switch change.Kind {
	case ChangeSolutionStatus:
		p.status = "changed the fix to " + change.Status
	case ChangeIssueMisjudged:
		p.status = "marked the issue as a misjudgement"
	case ChangeDeleteSolution:
		p.status = "deleted the fix and its version history"
	}
	return nil
}

// environmentGroupKey builds a stable, human-readable grouping key. Runtime
// versions differ between occurrences of the same machine and outcomes do not
// carry them, so they never take part in grouping.
func environmentGroupKey(environment database.Environment) string {
	parts := make([]string, 0, 2)
	if environment.OS != "" {
		parts = append(parts, environment.OS)
	}
	if environment.Shell != "" {
		parts = append(parts, environment.Shell)
	}
	if len(parts) == 0 {
		return "(unknown environment)"
	}
	return strings.Join(parts, " · ")
}

// formatEnvironment renders the machine in one line. An empty environment is
// honest about meaning "everywhere".
func formatEnvironment(environment database.Environment) string {
	parts := make([]string, 0, 4)
	if environment.OS != "" {
		parts = append(parts, environment.OS)
	}
	if environment.Shell != "" {
		parts = append(parts, environment.Shell)
	}
	if environment.Connector != "" {
		parts = append(parts, environment.Connector)
	}
	if len(environment.RuntimeVersions) > 0 {
		keys := make([]string, 0, len(environment.RuntimeVersions))
		for key := range environment.RuntimeVersions {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			parts = append(parts, key+" "+environment.RuntimeVersions[key])
		}
	}
	if len(parts) == 0 {
		return "(any environment)"
	}
	return strings.Join(parts, " · ")
}

func orNone(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "(not recorded)"
	}
	return value
}

func trustAccent(status string, trust database.Trust) Accent {
	if status == string(database.SolutionBlocked) || status == string(database.SolutionDeprecated) {
		return AccentApproximate
	}
	if trust.Samples > 0 && trust.Score >= 0.7 {
		return AccentExact
	}
	return AccentMuted
}

// sortVersionsNewestFirst keeps the row order stable for both the page and its
// tests; the loader already orders by version desc, this is a safety net.
func sortVersionsNewestFirst(versions []service.VersionInfo) []service.VersionInfo {
	ordered := append([]service.VersionInfo(nil), versions...)
	sort.SliceStable(ordered, func(left, right int) bool {
		return ordered[left].Version > ordered[right].Version
	})
	return ordered
}
