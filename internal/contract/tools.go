// The Go side of spec/mcp-tools.cue: one payload set, two channels (EKB-008).
// MCP stdio and the local Named Pipe API both decode into these structs and
// call the same Validate methods, so the field names and rules cannot drift.
package contract

import (
	"errors"
	"fmt"
	"time"
)

// ToolSchemaVersion is what every operation declares today. Each operation
// keeps its own schema_version field so one can move without the others
// (EKB-011 owns the compatibility rule).
const ToolSchemaVersion = "v1"

// ConnectorSDKVersion is the version on the connector capture envelope
// (spec/connector.cue #ConnectorSDKVersion). It is its own anchor: the six MCP
// operations can move without touching the connector SDK.
const ConnectorSDKVersion = "v1"

// MaxCandidates is the plan section 5 ceiling: a response with four candidates
// is invalid, and only a candidate with approximate=false may auto-apply.
const MaxCandidates = 3

// Operation names shared by the MCP tool names (err_<operation>) and the local
// envelope. They are the nine operations the plan fixes; nothing else exists.
const (
	OperationLookup   = "lookup"
	OperationSearch   = "search"
	OperationRecord   = "record"
	OperationFeedback = "feedback"
	OperationList     = "list"
	OperationStats    = "stats"
	// OperationCapture is the connector SDK capture call (spec/connector.cue).
	// It is a local API operation only: MCP must never become passive capture.
	OperationCapture = "capture"
	// OperationRecordSolution and OperationSetStatus are the two write-back
	// tools: without them a host cannot record a fix or correct an issue status,
	// so the knowledge base could only grow by importing Markdown.
	OperationRecordSolution = "record_solution"
	OperationSetStatus      = "set_status"
)

// matchReasons is the plan section 5 order. The first three prove "same
// error"; the rest only produce candidates that need confirmation.
var (
	validMatchReasons = map[string]bool{
		"issue_id": true, "error_code": true, "exact_fingerprint": true,
		"deterministic_filter": true, "trigram_candidate": true, "jaccard_rerank": true,
	}
	exactMatchReasons = map[string]bool{
		"issue_id": true, "error_code": true, "exact_fingerprint": true,
	}
	validOutcomes  = map[string]bool{"success": true, "failure": true, "unknown": true}
	validListKinds = map[string]bool{"issue": true, "solution": true}
)

// CandidateSolution is the verified fix a hit carries (EKB-035). It is
// optional: a brand-new issue has none, and the caller must see that absence
// instead of an empty body. Only verified versions are attached - an
// unverified candidate is not advice.
type CandidateSolution struct {
	SolutionVersionID string  `json:"solution_version_id"`
	Body              string  `json:"body"`
	Source            string  `json:"source"`
	AppliesTo         string  `json:"applies_to"`
	Trust             float64 `json:"trust"`
}

// Validate mirrors the CUE definition so a malformed solution cannot cross
// the pipe.
func (s CandidateSolution) Validate() error {
	if s.SolutionVersionID == "" {
		return errors.New("candidate.solution.solution_version_id: required")
	}
	if s.Body == "" {
		return errors.New("candidate.solution.body: required")
	}
	if s.Source == "" {
		return errors.New("candidate.solution.source: required")
	}
	if s.Trust < 0 || s.Trust > 1 {
		return fmt.Errorf("candidate.solution.trust: want 0..1, got %v", s.Trust)
	}
	return checkRedacted("candidate.solution.body", s.Body)
}

// Candidate is one reason-carrying hit.
type Candidate struct {
	IssueID     string             `json:"issue_id"`
	Fingerprint string             `json:"fingerprint"`
	Title       string             `json:"title"`
	MatchReason string             `json:"match_reason"`
	MatchDetail string             `json:"match_detail"`
	Approximate bool               `json:"approximate"`
	Confidence  *float64           `json:"confidence"`
	Hits        *int               `json:"hits,omitempty"`
	Solution    *CandidateSolution `json:"solution,omitempty"`
}

// Validate enforces the reason fields and the exact/fuzzy split: an exact
// reason with approximate=true is a contract violation, not a style choice.
func (c Candidate) Validate() error {
	if c.IssueID == "" {
		return errors.New("candidate.issue_id: required")
	}
	if !fingerprintPattern.MatchString(c.Fingerprint) {
		return fmt.Errorf("candidate.fingerprint: want %d lowercase hex characters, got %q", FingerprintLength, c.Fingerprint)
	}
	if c.Title == "" {
		return errors.New("candidate.title: required")
	}
	if !validMatchReasons[c.MatchReason] {
		return fmt.Errorf("candidate.match_reason: unknown reason %q", c.MatchReason)
	}
	if c.MatchDetail == "" {
		return errors.New("candidate.match_detail: required")
	}
	if c.Confidence == nil {
		return errors.New("candidate.confidence: required")
	}
	if *c.Confidence < 0 || *c.Confidence > 1 {
		return fmt.Errorf("candidate.confidence: want 0..1, got %v", *c.Confidence)
	}
	if c.Hits != nil && *c.Hits < 0 {
		return fmt.Errorf("candidate.hits: want a non-negative count, got %d", *c.Hits)
	}
	if exactMatchReasons[c.MatchReason] && c.Approximate {
		return fmt.Errorf("candidate.approximate: %q is exact, want false", c.MatchReason)
	}
	if !exactMatchReasons[c.MatchReason] && !c.Approximate {
		return fmt.Errorf("candidate.approximate: %q only produces candidates, want true", c.MatchReason)
	}
	if c.Solution != nil {
		if err := c.Solution.Validate(); err != nil {
			return err
		}
	}
	return checkRedacted("candidate.title", c.Title)
}

// LookupRequest asks the question "I know what this error is". It is not the
// same operation as SearchRequest: the plan forbids one fuzzy query behind
// both.
type LookupRequest struct {
	SchemaVersion     string `json:"schema_version"`
	Issue             string `json:"issue,omitempty"`
	ErrorCode         string `json:"error_code,omitempty"`
	Fingerprint       string `json:"fingerprint,omitempty"`
	NormalizedMessage string `json:"normalized_message,omitempty"`
	ProjectKey        string `json:"project_key,omitempty"`
}

// Validate requires at least one discriminator; a lookup with none would be a
// search in disguise.
func (r LookupRequest) Validate() error {
	if err := requireVersion(OperationLookup, r.SchemaVersion); err != nil {
		return err
	}
	if r.Issue == "" && r.ErrorCode == "" && r.Fingerprint == "" && r.NormalizedMessage == "" {
		return errors.New("lookup: one of issue, error_code, fingerprint or normalized_message is required")
	}
	if r.Fingerprint != "" && !fingerprintPattern.MatchString(r.Fingerprint) {
		return fmt.Errorf("lookup.fingerprint: want %d lowercase hex characters, got %q", FingerprintLength, r.Fingerprint)
	}
	// A lookup is a query, not a write: raw host text is accepted here and the
	// daemon normalizes it before matching (the stored side is redacted).
	return nil
}

// LookupResponse carries at most MaxCandidates hits with their reasons.
type LookupResponse struct {
	SchemaVersion string      `json:"schema_version"`
	Candidates    []Candidate `json:"candidates"`
}

// Validate applies the candidate ceiling and the per-candidate rules.
func (r LookupResponse) Validate() error {
	return validateCandidates(OperationLookup, r.SchemaVersion, r.Candidates)
}

// SearchRequest asks the free-text question.
type SearchRequest struct {
	SchemaVersion string `json:"schema_version"`
	Query         string `json:"query"`
	ProjectKey    string `json:"project_key,omitempty"`
	Limit         *int   `json:"limit,omitempty"`
}

// Validate keeps the search limit inside the contract ceiling and scans the
// query for unredacted secrets before it ever reaches the database.
func (r SearchRequest) Validate() error {
	if err := requireVersion(OperationSearch, r.SchemaVersion); err != nil {
		return err
	}
	if r.Query == "" {
		return errors.New("search.query: required")
	}
	if r.Limit != nil && (*r.Limit < 1 || *r.Limit > MaxCandidates) {
		return fmt.Errorf("search.limit: want 1..%d, got %d", MaxCandidates, *r.Limit)
	}
	// A search is a query, not a write: raw host text is accepted here and the
	// daemon normalizes it before matching (the stored side is redacted).
	return nil
}

// SearchResponse is the same candidate shape as lookup: one reason per hit.
type SearchResponse struct {
	SchemaVersion string      `json:"schema_version"`
	Candidates    []Candidate `json:"candidates"`
}

// Validate applies the candidate ceiling and the per-candidate rules.
func (r SearchResponse) Validate() error {
	return validateCandidates(OperationSearch, r.SchemaVersion, r.Candidates)
}

// RecordRequest submits one redacted observation.
type RecordRequest struct {
	SchemaVersion string     `json:"schema_version"`
	Event         ErrorEvent `json:"event"`
}

// Validate reuses ErrorEvent.Validate, so a record cannot smuggle in an event
// the capture path would have rejected.
func (r RecordRequest) Validate() error {
	if err := requireVersion(OperationRecord, r.SchemaVersion); err != nil {
		return err
	}
	if err := r.Event.Validate(); err != nil {
		return fmt.Errorf("record.event: %w", err)
	}
	return nil
}

// RecordResponse reports what the write created or reused.
type RecordResponse struct {
	SchemaVersion string `json:"schema_version"`
	EventID       string `json:"event_id"`
	OccurrenceID  string `json:"occurrence_id"`
	IssueID       string `json:"issue_id"`
	Fingerprint   string `json:"fingerprint"`
	Created       bool   `json:"created"`
}

// Validate checks the identifiers and the fingerprint shape.
func (r RecordResponse) Validate() error {
	if err := requireVersion(OperationRecord, r.SchemaVersion); err != nil {
		return err
	}
	for _, field := range []struct {
		name  string
		value string
	}{{"event_id", r.EventID}, {"occurrence_id", r.OccurrenceID}, {"issue_id", r.IssueID}} {
		if field.value == "" {
			return fmt.Errorf("record.%s: required", field.name)
		}
	}
	if !fingerprintPattern.MatchString(r.Fingerprint) {
		return fmt.Errorf("record.fingerprint: want %d lowercase hex characters, got %q", FingerprintLength, r.Fingerprint)
	}
	return nil
}

// FeedbackRequest submits an outcome. It has no trust field on purpose:
// errkb_trust() computes trust from outcomes (EKB-021).
type FeedbackRequest struct {
	SchemaVersion     string `json:"schema_version"`
	IssueID           string `json:"issue_id"`
	SolutionVersionID string `json:"solution_version_id"`
	Outcome           string `json:"outcome"`
	Note              string `json:"note,omitempty"`
}

// Validate locks the outcome enum and keeps the note redacted.
func (r FeedbackRequest) Validate() error {
	if err := requireVersion(OperationFeedback, r.SchemaVersion); err != nil {
		return err
	}
	if r.IssueID == "" {
		return errors.New("feedback.issue_id: required")
	}
	if r.SolutionVersionID == "" {
		return errors.New("feedback.solution_version_id: required")
	}
	if !validOutcomes[r.Outcome] {
		return fmt.Errorf("feedback.outcome: unknown outcome %q", r.Outcome)
	}
	return checkRedacted("feedback.note", r.Note)
}

// FeedbackResponse is the stored outcome identity.
type FeedbackResponse struct {
	SchemaVersion string `json:"schema_version"`
	OutcomeID     string `json:"outcome_id"`
	Recorded      bool   `json:"recorded"`
}

// Validate checks the recorded outcome id.
func (r FeedbackResponse) Validate() error {
	if err := requireVersion(OperationFeedback, r.SchemaVersion); err != nil {
		return err
	}
	if r.OutcomeID == "" {
		return errors.New("feedback.outcome_id: required")
	}
	return nil
}

// ListRequest pages through issues or solutions.
type ListRequest struct {
	SchemaVersion string `json:"schema_version"`
	Kind          string `json:"kind"`
	ProjectKey    string `json:"project_key,omitempty"`
	Limit         *int   `json:"limit,omitempty"`
	Cursor        string `json:"cursor,omitempty"`
}

// Validate locks the kind enum and a page size of 1..100.
func (r ListRequest) Validate() error {
	if err := requireVersion(OperationList, r.SchemaVersion); err != nil {
		return err
	}
	if !validListKinds[r.Kind] {
		return fmt.Errorf("list.kind: unknown kind %q", r.Kind)
	}
	if r.Limit != nil && (*r.Limit < 1 || *r.Limit > 100) {
		return fmt.Errorf("list.limit: want 1..100, got %d", *r.Limit)
	}
	return nil
}

// ListItem is one page entry.
type ListItem struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	UpdatedAt string `json:"updated_at"`
	Hits      *int   `json:"hits,omitempty"`
	Status    string `json:"status,omitempty"`
	Fix       string `json:"fix,omitempty"`
}

// Validate checks the entry and its timestamp.
func (i ListItem) Validate() error {
	if i.ID == "" {
		return errors.New("list.item.id: required")
	}
	if i.Title == "" {
		return errors.New("list.item.title: required")
	}
	if i.UpdatedAt == "" {
		return errors.New("list.item.updated_at: required")
	}
	if _, err := time.Parse(time.RFC3339, i.UpdatedAt); err != nil {
		return fmt.Errorf("list.item.updated_at: %w", err)
	}
	if i.Hits != nil && *i.Hits < 0 {
		return fmt.Errorf("list.item.hits: want a non-negative count, got %d", *i.Hits)
	}
	if i.Status != "" && !validIssueStatuses[i.Status] {
		return fmt.Errorf("list.item.status: unknown status %q", i.Status)
	}
	if i.Fix != "" {
		if err := checkRedacted("list.item.fix", i.Fix); err != nil {
			return err
		}
	}
	return checkRedacted("list.item.title", i.Title)
}

// ListResponse is one page plus a cursor when more pages exist.
type ListResponse struct {
	SchemaVersion string     `json:"schema_version"`
	Items         []ListItem `json:"items"`
	NextCursor    string     `json:"next_cursor,omitempty"`
}

// Validate checks every entry.
func (r ListResponse) Validate() error {
	if err := requireVersion(OperationList, r.SchemaVersion); err != nil {
		return err
	}
	for i, item := range r.Items {
		if err := item.Validate(); err != nil {
			return fmt.Errorf("item %d: %w", i, err)
		}
	}
	return nil
}

// StatsRequest is the optional project filter.
type StatsRequest struct {
	SchemaVersion string `json:"schema_version"`
	ProjectKey    string `json:"project_key,omitempty"`
}

// Validate checks the version.
func (r StatsRequest) Validate() error {
	return requireVersion(OperationStats, r.SchemaVersion)
}

// StatsTotals counts the four entities from plan section 4.
type StatsTotals struct {
	Events    int `json:"events"`
	Issues    int `json:"issues"`
	Solutions int `json:"solutions"`
	Outcomes  int `json:"outcomes"`
}

// StatsRetrieval is the retrieval latency summary from plan section 13.
type StatsRetrieval struct {
	P50MS int `json:"p50_ms"`
	P95MS int `json:"p95_ms"`
}

// StatsTokens keeps estimated and measured savings apart: merging them would
// overstate the product.
type StatsTokens struct {
	EstimatedSaved int `json:"estimated_saved"`
	MeasuredSaved  int `json:"measured_saved"`
	MeasuredEvents int `json:"measured_events"`
}

// StatsResponse is the knowledge-base summary.
type StatsResponse struct {
	SchemaVersion string         `json:"schema_version"`
	Totals        StatsTotals    `json:"totals"`
	Retrieval     StatsRetrieval `json:"retrieval"`
	Tokens        StatsTokens    `json:"tokens"`
}

// Validate rejects negative counters and an impossible p95 < p50.
func (r StatsResponse) Validate() error {
	if err := requireVersion(OperationStats, r.SchemaVersion); err != nil {
		return err
	}
	counters := []struct {
		name  string
		value int
	}{
		{"totals.events", r.Totals.Events}, {"totals.issues", r.Totals.Issues},
		{"totals.solutions", r.Totals.Solutions}, {"totals.outcomes", r.Totals.Outcomes},
		{"retrieval.p50_ms", r.Retrieval.P50MS}, {"retrieval.p95_ms", r.Retrieval.P95MS},
		{"tokens.estimated_saved", r.Tokens.EstimatedSaved},
		{"tokens.measured_saved", r.Tokens.MeasuredSaved},
		{"tokens.measured_events", r.Tokens.MeasuredEvents},
	}
	for _, counter := range counters {
		if counter.value < 0 {
			return fmt.Errorf("stats.%s: must be zero or positive", counter.name)
		}
	}
	if r.Retrieval.P95MS < r.Retrieval.P50MS {
		return fmt.Errorf("stats.retrieval: p95_ms %d is below p50_ms %d", r.Retrieval.P95MS, r.Retrieval.P50MS)
	}
	return nil
}

// requireVersion names the operation so a wrong version is easy to locate.
func requireVersion(operation, version string) error {
	if version != ToolSchemaVersion {
		return fmt.Errorf("%s.schema_version: want %q, got %q", operation, ToolSchemaVersion, version)
	}
	return nil
}

// validateCandidates applies the ceiling and the per-candidate rules for the
// two read operations.
func validateCandidates(operation, version string, candidates []Candidate) error {
	if err := requireVersion(operation, version); err != nil {
		return err
	}
	if len(candidates) > MaxCandidates {
		return fmt.Errorf("%s: want at most %d candidates, got %d", operation, MaxCandidates, len(candidates))
	}
	for i, candidate := range candidates {
		if err := candidate.Validate(); err != nil {
			return fmt.Errorf("candidate %d: %w", i, err)
		}
	}
	return nil
}

// checkRedacted reuses the ErrorEvent redaction minimum on every user-supplied
// text field, so a query or a note cannot carry a secret into the database.
func checkRedacted(field, text string) error {
	if text == "" {
		return nil
	}
	for _, pattern := range sensitivePatterns {
		if pattern.re.MatchString(text) {
			return fmt.Errorf("%s: still contains %s, redact before the write point", field, pattern.what)
		}
	}
	return nil
}

// ConnectorObservation is the raw failure shape a connector hands to the core
// (spec/connector.cue #Observation, EKB-036). Nothing here is classified,
// redacted, fingerprinted or stored yet: those are the core's job, and an
// adapter that reimplements them violates plan section 3.2.
type ConnectorObservation struct {
	ObservedAt      string            `json:"observed_at"`
	Source          string            `json:"source"`
	Message         string            `json:"message"`
	ErrorCode       string            `json:"error_code,omitempty"`
	Command         string            `json:"command,omitempty"`
	ExitCode        *int              `json:"exit_code,omitempty"`
	ToolName        string            `json:"tool_name,omitempty"`
	OS              string            `json:"os,omitempty"`
	Shell           string            `json:"shell,omitempty"`
	RuntimeVersions map[string]string `json:"runtime_versions,omitempty"`
	ProjectKey      string            `json:"project_key,omitempty"`
	Evidence        string            `json:"evidence,omitempty"`
	Confidence      *float64          `json:"confidence,omitempty"`
}

// Validate enforces the shape the capture pipeline relies on. Raw message text
// is allowed on purpose; redaction happens before the database write.
func (o ConnectorObservation) Validate() error {
	if _, err := time.Parse(time.RFC3339, o.ObservedAt); err != nil {
		return fmt.Errorf("observation.observed_at: %v", err)
	}
	if !validCategories[o.Source] {
		return fmt.Errorf("observation.source: unknown source %q", o.Source)
	}
	if o.Confidence != nil && (*o.Confidence < 0 || *o.Confidence > 1) {
		return fmt.Errorf("observation.confidence: want 0..1, got %v", *o.Confidence)
	}
	for key, value := range o.RuntimeVersions {
		if key == "" || value == "" {
			return errors.New("observation.runtime_versions: keys and values must be non-empty")
		}
	}
	return nil
}

// CaptureRequest is the connector SDK capture envelope (spec/connector.cue).
type CaptureRequest struct {
	SchemaVersion string               `json:"schema_version"`
	ConnectorID   string               `json:"connector_id"`
	SessionID     string               `json:"session_id"`
	Channel       string               `json:"channel,omitempty"`
	ObservationID string               `json:"observation_id,omitempty"`
	Observation   ConnectorObservation `json:"observation"`
}

// Validate checks the envelope. ConnectorID is the connector name an adapter
// knows; the core maps it to the row's uuid. An empty channel defaults to "A".
func (r CaptureRequest) Validate() error {
	if r.SchemaVersion != ConnectorSDKVersion {
		return fmt.Errorf("capture.schema_version: want %q, got %q", ConnectorSDKVersion, r.SchemaVersion)
	}
	if r.ConnectorID == "" {
		return errors.New("capture.connector_id: required")
	}
	if r.SessionID == "" {
		return errors.New("capture.session_id: required")
	}
	if r.Channel != "" && !validChannels[r.Channel] {
		return fmt.Errorf("capture.channel: unknown channel %q", r.Channel)
	}
	return r.Observation.Validate()
}

// CaptureResponse mirrors the record result: the issue identity and whether
// this call created the issue.
type CaptureResponse struct {
	SchemaVersion string `json:"schema_version"`
	EventID       string `json:"event_id"`
	OccurrenceID  string `json:"occurrence_id"`
	IssueID       string `json:"issue_id"`
	Fingerprint   string `json:"fingerprint"`
	Created       bool   `json:"created"`
}

// Validate rejects an empty identity and a malformed fingerprint.
func (r CaptureResponse) Validate() error {
	if r.SchemaVersion != ConnectorSDKVersion {
		return fmt.Errorf("capture.schema_version: want %q, got %q", ConnectorSDKVersion, r.SchemaVersion)
	}
	identities := []struct {
		name  string
		value string
	}{
		{"event_id", r.EventID}, {"occurrence_id", r.OccurrenceID}, {"issue_id", r.IssueID},
	}
	for _, identity := range identities {
		if identity.value == "" {
			return fmt.Errorf("capture.%s: required", identity.name)
		}
	}
	if !fingerprintPattern.MatchString(r.Fingerprint) {
		return fmt.Errorf("capture.fingerprint: want 12 lowercase hex characters, got %q", r.Fingerprint)
	}
	return nil
}

// validIssueStatuses is the closed issue-status set (db/migrations/0001).
var validIssueStatuses = map[string]bool{
	"open": true, "resolved": true, "wontfix": true, "misjudged": true,
}

// SolutionEnvironment narrows the hosts a solution applies to. An empty object
// means "no restriction", never "unknown".
type SolutionEnvironment struct {
	OS              string            `json:"os,omitempty"`
	Shell           string            `json:"shell,omitempty"`
	Connector       string            `json:"connector,omitempty"`
	RuntimeVersions map[string]string `json:"runtime_versions,omitempty"`
}

// Validate rejects empty entries such as a runtime version without a value.
func (e SolutionEnvironment) Validate() error {
	for key, value := range e.RuntimeVersions {
		if key == "" || value == "" {
			return errors.New("environment.runtime_versions: keys and values must be non-empty")
		}
	}
	return nil
}

// RecordSolutionRequest records a fix against an issue. Trust is not part of
// the request: errkb_trust() derives it from outcomes, so a caller cannot
// promote its own solution.
type RecordSolutionRequest struct {
	SchemaVersion string               `json:"schema_version"`
	IssueID       string               `json:"issue_id"`
	Body          string               `json:"body"`
	Preconditions string               `json:"preconditions,omitempty"`
	Environment   *SolutionEnvironment `json:"environment,omitempty"`
	Source        string               `json:"source,omitempty"`
	Actor         string               `json:"actor,omitempty"`
}

// Validate enforces the write boundary: a body must be present and no
// request text may still contain a secret.
func (r RecordSolutionRequest) Validate() error {
	if err := requireVersion(OperationRecordSolution, r.SchemaVersion); err != nil {
		return err
	}
	if r.IssueID == "" {
		return errors.New("record_solution.issue_id: required")
	}
	if r.Body == "" {
		return errors.New("record_solution.body: required")
	}
	if err := checkRedacted("record_solution.body", r.Body); err != nil {
		return err
	}
	if err := checkRedacted("record_solution.preconditions", r.Preconditions); err != nil {
		return err
	}
	if r.Environment != nil {
		if err := r.Environment.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// RecordSolutionResponse names the solution and the revision just appended.
type RecordSolutionResponse struct {
	SchemaVersion     string `json:"schema_version"`
	SolutionID        string `json:"solution_id"`
	SolutionVersionID string `json:"solution_version_id"`
	Version           int    `json:"version"`
	Recorded          bool   `json:"recorded"`
}

// Validate rejects an empty identity or a version below one.
func (r RecordSolutionResponse) Validate() error {
	if err := requireVersion(OperationRecordSolution, r.SchemaVersion); err != nil {
		return err
	}
	if r.SolutionID == "" {
		return errors.New("record_solution.solution_id: required")
	}
	if r.SolutionVersionID == "" {
		return errors.New("record_solution.solution_version_id: required")
	}
	if r.Version < 1 {
		return fmt.Errorf("record_solution.version: want at least 1, got %d", r.Version)
	}
	return nil
}

// SetStatusRequest changes an issue's status.
type SetStatusRequest struct {
	SchemaVersion string `json:"schema_version"`
	IssueID       string `json:"issue_id"`
	Status        string `json:"status"`
}

// Validate checks the closed status set.
func (r SetStatusRequest) Validate() error {
	if err := requireVersion(OperationSetStatus, r.SchemaVersion); err != nil {
		return err
	}
	if r.IssueID == "" {
		return errors.New("set_status.issue_id: required")
	}
	if !validIssueStatuses[r.Status] {
		return fmt.Errorf("set_status.status: unknown status %q", r.Status)
	}
	return nil
}

// SetStatusResponse echoes the stored status.
type SetStatusResponse struct {
	SchemaVersion string `json:"schema_version"`
	IssueID       string `json:"issue_id"`
	Status        string `json:"status"`
}

// Validate rejects an empty identity or a status outside the closed set.
func (r SetStatusResponse) Validate() error {
	if err := requireVersion(OperationSetStatus, r.SchemaVersion); err != nil {
		return err
	}
	if r.IssueID == "" {
		return errors.New("set_status.issue_id: required")
	}
	if !validIssueStatuses[r.Status] {
		return fmt.Errorf("set_status.status: unknown status %q", r.Status)
	}
	return nil
}
