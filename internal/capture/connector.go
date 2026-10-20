// The connector SDK: the uniform surface every host adapter speaks. Adapters
// translate an observable failure into an ErrorEvent and, where the host allows
// it, ask for known fixes and return outcomes. Everything else stays in the
// core platform (§3.2): classification, redaction, fingerprinting, storage,
// search, solution trust and audit are never reimplemented here.
//
// Payloads are decoded by encoding/json, which ignores unknown fields: the SDK
// tolerates extra data from newer adapters, and the strict hostile-wire
// contract is the local API's decoder, not this package.
package capture

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/contract"
)

// ConnectorSDKVersion is the version on every SDK envelope.
const ConnectorSDKVersion = "v1"

// Notice kinds a host requests and the result kinds the core returns. They
// mirror the notice forms the DSH connector renders before diagnosis.
const (
	NoticeKindHit  = "hit"
	NoticeKindMiss = "miss"

	NoticeResultHit     = "hit"
	NoticeResultNear    = "near"
	NoticeResultNoFix   = "no-fix"
	NoticeResultMiss    = "miss"
	NoticeResultDoubted = "doubted"
)

// Trust levels a host may attach to a candidate it is about to reuse.
const (
	NoticeTrustTrusted    = "trusted"
	NoticeTrustDoubted    = "doubted"
	NoticeTrustSuppressed = "suppressed"
)

// Doctor status values; they match the names errkb doctor already renders.
const (
	DoctorStatusOK       = "ok"
	DoctorStatusDegraded = "degraded"
	DoctorStatusFailing  = "failing"
)

// Connector is what one host adapter implements. A host that can only capture
// still implements every method and answers with an explicit error; a silent
// no-op is never allowed.
type Connector interface {
	Capture(ctx context.Context, request CaptureRequest) (CaptureResponse, error)
	Lookup(ctx context.Context, request LookupRequest) (LookupResponse, error)
	Notice(ctx context.Context, request NoticeRequest) (NoticeResponse, error)
	Feedback(ctx context.Context, request FeedbackRequest) (FeedbackResponse, error)
}

// CapabilityProbe answers "what can this host version actually do right now".
// It must probe the host, never read a capability declaration file: interfaces
// change, and a window title is not an interface contract (§3.2).
type CapabilityProbe interface {
	Probe(ctx context.Context) (contract.ConnectorCapability, error)
}

// Observation is what a host can see about one failure before the core turns
// it into an ErrorEvent. Raw text is allowed; the core redacts before any write.
type Observation struct {
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

// CaptureRequest hands one observation to the platform.
type CaptureRequest struct {
	SchemaVersion string      `json:"schema_version"`
	ConnectorID   string      `json:"connector_id"`
	SessionID     string      `json:"session_id"`
	Observation   Observation `json:"observation"`
}

// CaptureResponse mirrors the core record result.
type CaptureResponse struct {
	SchemaVersion string `json:"schema_version"`
	EventID       string `json:"event_id"`
	OccurrenceID  string `json:"occurrence_id"`
	IssueID       string `json:"issue_id"`
	Fingerprint   string `json:"fingerprint"`
	Created       bool   `json:"created"`
}

// LookupRequest asks for known fixes. At least one selector must be present.
type LookupRequest struct {
	SchemaVersion     string `json:"schema_version"`
	ConnectorID       string `json:"connector_id"`
	SessionID         string `json:"session_id,omitempty"`
	ProjectKey        string `json:"project_key,omitempty"`
	IssueID           string `json:"issue_id,omitempty"`
	Fingerprint       string `json:"fingerprint,omitempty"`
	ErrorCode         string `json:"error_code,omitempty"`
	NormalizedMessage string `json:"normalized_message,omitempty"`
}

// LookupResponse carries at most three reason-carrying candidates and says
// explicitly when the result is degraded instead of pretending an empty list
// means "no known fix".
type LookupResponse struct {
	SchemaVersion string               `json:"schema_version"`
	Candidates    []contract.Candidate `json:"candidates"`
	Degraded      bool                 `json:"degraded"`
}

// NoticeRequest asks for the one-line notice to show before diagnosing.
type NoticeRequest struct {
	SchemaVersion string              `json:"schema_version"`
	ConnectorID   string              `json:"connector_id"`
	SessionID     string              `json:"session_id,omitempty"`
	Kind          string              `json:"kind"`
	Candidate     *contract.Candidate `json:"candidate,omitempty"`
	IssueID       string              `json:"issue_id,omitempty"`
	Trust         string              `json:"trust,omitempty"`
}

// NoticeResponse is the rendered, truncated and counted notice.
type NoticeResponse struct {
	SchemaVersion string `json:"schema_version"`
	Kind          string `json:"kind"`
	Text          string `json:"text"`
	Chars         int    `json:"chars"`
	Tokens        int    `json:"tokens"`
}

// FeedbackEnvironment is the environment an outcome was observed in.
type FeedbackEnvironment struct {
	OS              string            `json:"os,omitempty"`
	Shell           string            `json:"shell,omitempty"`
	Connector       string            `json:"connector,omitempty"`
	RuntimeVersions map[string]string `json:"runtime_versions,omitempty"`
}

// FeedbackRequest records whether a known fix worked.
type FeedbackRequest struct {
	SchemaVersion     string               `json:"schema_version"`
	ConnectorID       string               `json:"connector_id"`
	SessionID         string               `json:"session_id,omitempty"`
	IssueID           string               `json:"issue_id"`
	SolutionVersionID string               `json:"solution_version_id"`
	Outcome           string               `json:"outcome"`
	Note              string               `json:"note,omitempty"`
	Environment       *FeedbackEnvironment `json:"environment,omitempty"`
}

// FeedbackResponse is the core's acknowledgement.
type FeedbackResponse struct {
	SchemaVersion string `json:"schema_version"`
	OutcomeID     string `json:"outcome_id"`
	Recorded      bool   `json:"recorded"`
}

// DegradedError is the explicit degradation every adapter must return when the
// host cannot provide what the caller asked for. It is enumerable by design:
// the missing fields and the declared fallback are part of the error.
type DegradedError struct {
	Connector string
	Missing   []string
	Fallback  contract.Fallback
}

// Error implements error.
func (e *DegradedError) Error() string {
	if len(e.Missing) == 0 {
		return fmt.Sprintf("connector %s: capability unavailable (falls back to channel %s)", e.Connector, e.Fallback.Channel)
	}
	return fmt.Sprintf("connector %s: missing %v (falls back to channel %s)", e.Connector, e.Missing, e.Fallback.Channel)
}

// Degradation turns a capability declaration into the explicit error to return
// when the probe says the host cannot deliver it. It returns nil only when the
// probe succeeded and nothing is missing.
func Degradation(capability contract.ConnectorCapability) *DegradedError {
	if capability.Probe.OK && len(capability.Probe.Missing) == 0 {
		return nil
	}
	missing := append([]string(nil), capability.Probe.Missing...)
	if len(missing) == 0 {
		// A failed probe with no field list still names the whole declaration.
		missing = append(missing, capability.Captures...)
	}
	return &DegradedError{
		Connector: capability.Connector,
		Missing:   missing,
		Fallback:  capability.Fallback,
	}
}

// ProbeReport is the doctor-consumable summary of one capability probe. It
// carries the host identity, its version, the probing time and the enumerated
// gaps; the diagnosis never depends on a window title.
type ProbeReport struct {
	Connector             string   `json:"connector"`
	HostVersion           string   `json:"host_version"`
	Channel               string   `json:"channel"`
	Captures              []string `json:"captures"`
	NoticeBeforeDiagnosis bool     `json:"notice_before_diagnosis"`
	Degraded              bool     `json:"degraded"`
	FallbackChannel       string   `json:"fallback_channel"`
	Missing               []string `json:"missing"`
	ProbedAt              string   `json:"probed_at"`
}

// ReportProbe builds the structured report from one capability declaration.
func ReportProbe(capability contract.ConnectorCapability) ProbeReport {
	report := ProbeReport{
		Connector:             capability.Connector,
		HostVersion:           capability.HostVersion,
		Channel:               capability.Channel,
		Captures:              append([]string(nil), capability.Captures...),
		NoticeBeforeDiagnosis: capability.NoticeBeforeDiagnosis,
		Degraded:              Degradation(capability) != nil,
		FallbackChannel:       capability.Fallback.Channel,
		Missing:               append([]string(nil), capability.Probe.Missing...),
		ProbedAt:              capability.Probe.ProbedAt,
	}
	sort.Strings(report.Captures)
	sort.Strings(report.Missing)
	if report.Captures == nil {
		report.Captures = []string{}
	}
	if report.Missing == nil {
		report.Missing = []string{}
	}
	return report
}

// ProbeConnector runs the probe once and returns the structured result. The
// answer comes from the host in front of us, never from a configuration file.
func ProbeConnector(ctx context.Context, probe CapabilityProbe) (ProbeReport, error) {
	if probe == nil {
		return ProbeReport{}, errors.New("connector probe: a probe is required")
	}
	capability, err := probe.Probe(ctx)
	if err != nil {
		return ProbeReport{}, fmt.Errorf("connector probe: %w", err)
	}
	report := ReportProbe(capability)
	if err := report.Validate(); err != nil {
		return ProbeReport{}, err
	}
	return report, nil
}

// Validate checks the parts a report must always carry.
func (r ProbeReport) Validate() error {
	if r.Connector == "" {
		return errors.New("probe report: connector is required")
	}
	if !validChannel(r.Channel) {
		return fmt.Errorf("probe report: unknown channel %q", r.Channel)
	}
	if r.ProbedAt == "" {
		return errors.New("probe report: probed_at is required")
	}
	if _, err := time.Parse(time.RFC3339, r.ProbedAt); err != nil {
		return fmt.Errorf("probe report: probed_at: %w", err)
	}
	if len(r.Captures) == 0 && !r.Degraded {
		return errors.New("probe report: no captures and no declared degradation")
	}
	if r.Degraded && !validChannel(r.FallbackChannel) {
		return fmt.Errorf("probe report: degraded report needs a fallback channel, got %q", r.FallbackChannel)
	}
	return nil
}

// DoctorCheck is the shape errkb doctor renders, defined here so the probe can
// be reported without importing the service package.
type DoctorCheck struct {
	Name    string            `json:"name"`
	Status  string            `json:"status"`
	Summary string            `json:"summary"`
	Detail  map[string]string `json:"detail"`
	Fix     string            `json:"fix"`
}

// DoctorCheck converts the report into one doctor row. A degraded probe yields
// a non-empty fix hint; a fully capable probe yields status ok.
func (r ProbeReport) DoctorCheck() DoctorCheck {
	check := DoctorCheck{
		Name: "connector:" + r.Connector,
		Detail: map[string]string{
			"host_version":            r.HostVersion,
			"channel":                 r.Channel,
			"captures":                fmt.Sprintf("%d", len(r.Captures)),
			"notice_before_diagnosis": fmt.Sprintf("%t", r.NoticeBeforeDiagnosis),
			"probed_at":               r.ProbedAt,
		},
	}
	switch {
	case r.Degraded:
		check.Status = DoctorStatusDegraded
		check.Summary = fmt.Sprintf("channel %s, falls back to %s", r.Channel, r.FallbackChannel)
		check.Detail["missing"] = fmt.Sprintf("%v", r.Missing)
		check.Fix = fmt.Sprintf("upgrade the host or install the %s fallback: missing %v", r.FallbackChannel, r.Missing)
	default:
		check.Status = DoctorStatusOK
		check.Summary = fmt.Sprintf("channel %s with %d captured fields", r.Channel, len(r.Captures))
	}
	return check
}

func validChannel(level string) bool {
	switch level {
	case "A", "B", "C", "D", "E":
		return true
	default:
		return false
	}
}
