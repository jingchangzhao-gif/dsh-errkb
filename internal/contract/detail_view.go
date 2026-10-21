package contract

import (
	"errors"
	"time"
)

// GetIssueRequest asks for one issue with its occurrence, solution and outcome
// history. It is the read side of the Web detail page (WF-005); edits reuse the
// existing record_solution / set_status operations plus their own additions.
type GetIssueRequest struct {
	SchemaVersion string `json:"schema_version"`
	IssueID       string `json:"issue_id"`
}

// Validate keeps the request envelope identical to the other operations.
func (r GetIssueRequest) Validate() error {
	if r.SchemaVersion != SchemaVersion {
		return errors.New("get_issue.request.schema_version: unsupported version " + r.SchemaVersion)
	}
	if r.IssueID == "" {
		return errors.New("get_issue.request.issue_id: required")
	}
	return nil
}

// GetIssueResponse carries the whole detail document in one answer so the page
// never stitches several round trips together.
type GetIssueResponse struct {
	SchemaVersion string       `json:"schema_version"`
	Issue         IssueDetailV `json:"issue"`
}

// Validate rejects a response the page could not render.
func (r GetIssueResponse) Validate() error {
	if r.SchemaVersion != SchemaVersion {
		return errors.New("get_issue.response.schema_version: unsupported version " + r.SchemaVersion)
	}
	return r.Issue.Validate()
}

// IssueDetailV is the flat JSON shape of one issue. Trust is read-only here by
// design: no write path exists in this contract.
type IssueDetailV struct {
	ID          string        `json:"id"`
	Fingerprint string        `json:"fingerprint"`
	Title       string        `json:"title"`
	Category    string        `json:"category"`
	Status      string        `json:"status"`
	ProjectKey  string        `json:"project_key,omitempty"`
	Trigger     string        `json:"trigger,omitempty"`
	Hits        int           `json:"hits"`
	FirstSeen   time.Time     `json:"first_seen"`
	LastSeen    time.Time     `json:"last_seen"`
	Trust       TrustView     `json:"trust"`
	Occurrences []OccurrenceV `json:"occurrences"`
	Solutions   []SolutionV   `json:"solutions"`
	Outcomes    []OutcomeV    `json:"outcomes"`
}

// TrustView is display-only; data comes from errkb_trust().
type TrustView struct {
	Score     float64 `json:"score"`
	Samples   int64   `json:"samples"`
	Successes int64   `json:"successes"`
	Failures  int64   `json:"failures"`
	Unknowns  int64   `json:"unknowns"`
}

// OccurrenceV is one captured occurrence with its host environment.
type OccurrenceV struct {
	ID          string            `json:"id"`
	ObservedAt  time.Time         `json:"observed_at"`
	Environment map[string]string `json:"environment,omitempty"`
	Headline    string            `json:"headline,omitempty"`
}

// SolutionV carries the full version history, newest first.
type SolutionV struct {
	ID       string     `json:"id"`
	Status   string     `json:"status"`
	Versions []VersionV `json:"versions"`
}

// VersionV is one appended solution version.
type VersionV struct {
	ID            string    `json:"id"`
	Version       int       `json:"version"`
	Body          string    `json:"body"`
	Source        string    `json:"source,omitempty"`
	AppliesTo     string    `json:"applies_to,omitempty"`
	Risks         string    `json:"risks,omitempty"`
	Preconditions string    `json:"preconditions,omitempty"`
	EffectiveAt   time.Time `json:"effective_at"`
}

// OutcomeV is one recorded outcome against a solution version.
type OutcomeV struct {
	ID          string            `json:"id"`
	Outcome     string            `json:"outcome"`
	Note        string            `json:"note,omitempty"`
	ObservedAt  time.Time         `json:"observed_at"`
	Environment map[string]string `json:"environment,omitempty"`
}

// Validate applies the few checks the page relies on.
func (i IssueDetailV) Validate() error {
	if i.ID == "" {
		return errors.New("get_issue.response.issue.id: required")
	}
	if i.Fingerprint == "" {
		return errors.New("get_issue.response.issue.fingerprint: required")
	}
	if i.Title == "" {
		return errors.New("get_issue.response.issue.title: required")
	}
	return nil
}

// solution statuses the daemon state machine accepts.
var solutionStatuses = map[string]bool{
	"candidate":  true,
	"verified":   true,
	"deprecated": true,
	"blocked":    true,
}

// SetSolutionStatusRequest moves one solution through the closed state machine.
// Confirmed must be true for the destructive transitions (deprecated, blocked);
// the daemon enforces it, the Web page only relays the intent.
type SetSolutionStatusRequest struct {
	SchemaVersion string `json:"schema_version"`
	SolutionID    string `json:"solution_id"`
	Status        string `json:"status"`
	Confirmed     bool   `json:"confirmed,omitempty"`
}

func (r SetSolutionStatusRequest) Validate() error {
	if r.SchemaVersion != SchemaVersion {
		return errors.New("set_solution_status.request.schema_version: unsupported version " + r.SchemaVersion)
	}
	if r.SolutionID == "" {
		return errors.New("set_solution_status.request.solution_id: required")
	}
	if !solutionStatuses[r.Status] {
		return errors.New("set_solution_status.request.status: unknown status " + r.Status)
	}
	return nil
}

type SetSolutionStatusResponse struct {
	SchemaVersion string `json:"schema_version"`
	SolutionID    string `json:"solution_id"`
	Status        string `json:"status"`
}

func (r SetSolutionStatusResponse) Validate() error {
	if r.SchemaVersion != SchemaVersion {
		return errors.New("set_solution_status.response.schema_version: unsupported version " + r.SchemaVersion)
	}
	if r.SolutionID == "" {
		return errors.New("set_solution_status.response.solution_id: required")
	}
	return nil
}

// DeleteSolutionRequest removes a solution and its versions. Confirmed is the
// second confirmation the UI promised; the daemon refuses without it.
type DeleteSolutionRequest struct {
	SchemaVersion string `json:"schema_version"`
	SolutionID    string `json:"solution_id"`
	Confirmed     bool   `json:"confirmed,omitempty"`
}

func (r DeleteSolutionRequest) Validate() error {
	if r.SchemaVersion != SchemaVersion {
		return errors.New("delete_solution.request.schema_version: unsupported version " + r.SchemaVersion)
	}
	if r.SolutionID == "" {
		return errors.New("delete_solution.request.solution_id: required")
	}
	return nil
}

type DeleteSolutionResponse struct {
	SchemaVersion string `json:"schema_version"`
	SolutionID    string `json:"solution_id"`
	Deleted       bool   `json:"deleted"`
}

func (r DeleteSolutionResponse) Validate() error {
	if r.SchemaVersion != SchemaVersion {
		return errors.New("delete_solution.response.schema_version: unsupported version " + r.SchemaVersion)
	}
	if r.SolutionID == "" {
		return errors.New("delete_solution.response.solution_id: required")
	}
	return nil
}
