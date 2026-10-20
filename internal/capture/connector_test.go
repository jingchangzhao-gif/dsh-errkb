package capture_test

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/capture"
	"github.com/jingchangzhao-gif/error-knowledge-base/internal/contract"
)

// stubProbe counts how often the host was actually asked. A capability read
// from a file would always return the same answer without touching the host;
// the SDK requires a probe call instead.
type stubProbe struct {
	calls      int
	capability contract.ConnectorCapability
	err        error
}

func (probe *stubProbe) Probe(context.Context) (contract.ConnectorCapability, error) {
	probe.calls++
	return probe.capability, probe.err
}

// stubConnector implements the four SDK calls so the interface can be checked
// both at compile time and during a call.
type stubConnector struct {
	calls []string
}

func (connector *stubConnector) Capture(_ context.Context, request capture.CaptureRequest) (capture.CaptureResponse, error) {
	connector.calls = append(connector.calls, "capture")
	return capture.CaptureResponse{
		SchemaVersion: request.SchemaVersion,
		EventID:       "4a46b8de-3c44-4b6b-8f5a-2e8f1a7c9d10",
		OccurrenceID:  "0f1b0c9e-3f3f-4f7a-9a4a-7e6f2f3a1b2c",
		IssueID:       "2f0b2bd0-1a34-4f01-9d69-5f0e1d2c3b4a",
		Fingerprint:   "f713a9cf9af8",
		Created:       true,
	}, nil
}

func (connector *stubConnector) Lookup(_ context.Context, request capture.LookupRequest) (capture.LookupResponse, error) {
	connector.calls = append(connector.calls, "lookup")
	return capture.LookupResponse{
		SchemaVersion: request.SchemaVersion,
		Candidates:    []contract.Candidate{},
	}, nil
}

func (connector *stubConnector) Notice(_ context.Context, request capture.NoticeRequest) (capture.NoticeResponse, error) {
	connector.calls = append(connector.calls, "notice")
	return capture.NoticeResponse{
		SchemaVersion: request.SchemaVersion,
		Kind:          capture.NoticeResultMiss,
		Text:          "[errkb] E-0001 recorded (1 hit, no fix yet).",
		Chars:         46,
		Tokens:        15,
	}, nil
}

func (connector *stubConnector) Feedback(_ context.Context, request capture.FeedbackRequest) (capture.FeedbackResponse, error) {
	connector.calls = append(connector.calls, "feedback")
	return capture.FeedbackResponse{
		SchemaVersion: request.SchemaVersion,
		OutcomeID:     "8f2d7c1a-5b3e-4c4d-9f0a-1b2c3d4e5f60",
		Recorded:      true,
	}, nil
}

var (
	_ capture.Connector       = (*stubConnector)(nil)
	_ capture.CapabilityProbe = (*stubProbe)(nil)
)

func fullCapability() contract.ConnectorCapability {
	return contract.ConnectorCapability{
		Connector:             "test-host",
		HostVersion:           "1.4.2",
		Channel:               "A",
		Captures:              []string{"event_id", "headline", "error_code"},
		NoticeBeforeDiagnosis: true,
		Fallback: contract.Fallback{
			Channel: "C",
			Missing: []string{},
		},
		Probe: contract.Probe{
			ProbedAt:    "2026-10-19T15:04:05Z",
			HostVersion: "1.4.2",
			OK:          true,
			Missing:     []string{},
		},
	}
}

func TestConnectorSDKExposesTheFourCalls(t *testing.T) {
	connector := &stubConnector{}
	ctx := context.Background()

	captured, err := connector.Capture(ctx, capture.CaptureRequest{
		SchemaVersion: capture.ConnectorSDKVersion,
		ConnectorID:   "test-host",
		SessionID:     "session-1",
		Observation: capture.Observation{
			ObservedAt: "2026-10-19T15:04:05Z",
			Source:     "tool",
			Message:    "open <path> failed",
		},
	})
	if err != nil || captured.Fingerprint == "" || !captured.Created {
		t.Fatalf("capture = %+v, %v", captured, err)
	}

	looked, err := connector.Lookup(ctx, capture.LookupRequest{
		SchemaVersion: capture.ConnectorSDKVersion,
		ConnectorID:   "test-host",
		Fingerprint:   "f713a9cf9af8",
	})
	if err != nil || len(looked.Candidates) != 0 {
		t.Fatalf("lookup = %+v, %v", looked, err)
	}

	noticed, err := connector.Notice(ctx, capture.NoticeRequest{
		SchemaVersion: capture.ConnectorSDKVersion,
		ConnectorID:   "test-host",
		Kind:          capture.NoticeKindMiss,
		IssueID:       "2f0b2bd0-1a34-4f01-9d69-5f0e1d2c3b4a",
	})
	if err != nil || noticed.Text == "" || noticed.Kind != capture.NoticeResultMiss {
		t.Fatalf("notice = %+v, %v", noticed, err)
	}

	fed, err := connector.Feedback(ctx, capture.FeedbackRequest{
		SchemaVersion:     capture.ConnectorSDKVersion,
		ConnectorID:       "test-host",
		IssueID:           "2f0b2bd0-1a34-4f01-9d69-5f0e1d2c3b4a",
		SolutionVersionID: "1c798104-50c4-4f26-afab-fbaa7834ea3a",
		Outcome:           "success",
	})
	if err != nil || !fed.Recorded {
		t.Fatalf("feedback = %+v, %v", fed, err)
	}

	want := []string{"capture", "lookup", "notice", "feedback"}
	if !reflect.DeepEqual(connector.calls, want) {
		t.Fatalf("calls = %v, want %v", connector.calls, want)
	}
}

func TestProbeConnectorRunsTheProbe(t *testing.T) {
	probe := &stubProbe{capability: fullCapability()}
	report, err := capture.ProbeConnector(context.Background(), probe)
	if err != nil {
		t.Fatalf("ProbeConnector: %v", err)
	}
	if probe.calls != 1 {
		t.Fatalf("probe calls = %d, want 1", probe.calls)
	}
	if report.Connector != "test-host" || report.HostVersion != "1.4.2" {
		t.Fatalf("identity = %+v", report)
	}
	if report.ProbedAt != "2026-10-19T15:04:05Z" {
		t.Fatalf("probed_at = %q", report.ProbedAt)
	}
	if _, err := time.Parse(time.RFC3339, report.ProbedAt); err != nil {
		t.Fatalf("probed_at is not RFC3339: %v", err)
	}
	if report.Channel != "A" || report.NoticeBeforeDiagnosis != true {
		t.Fatalf("capabilities = %+v", report)
	}
	if report.Captures == nil || report.Missing == nil {
		t.Fatalf("report slices must not be nil: %+v", report)
	}

	// A second call probes again: the answer is never cached from a file.
	if _, err := capture.ProbeConnector(context.Background(), probe); err != nil {
		t.Fatalf("second ProbeConnector: %v", err)
	}
	if probe.calls != 2 {
		t.Fatalf("probe calls after second run = %d, want 2", probe.calls)
	}
}

func TestProbeConnectorSurfacesProbeErrors(t *testing.T) {
	probe := &stubProbe{err: os.ErrPermission}
	if _, err := capture.ProbeConnector(context.Background(), probe); err == nil {
		t.Fatal("ProbeConnector accepted a failing probe")
	}
	if _, err := capture.ProbeConnector(context.Background(), nil); err == nil {
		t.Fatal("ProbeConnector accepted a nil probe")
	}
}

func TestProbeReportValidation(t *testing.T) {
	cases := []struct {
		name    string
		report  capture.ProbeReport
		wantErr string
	}{
		{name: "connector", report: capture.ProbeReport{Channel: "A", ProbedAt: "2026-10-19T15:04:05Z", Captures: []string{"headline"}}, wantErr: "connector is required"},
		{name: "channel", report: capture.ProbeReport{Connector: "h", Channel: "Z", ProbedAt: "2026-10-19T15:04:05Z", Captures: []string{"headline"}}, wantErr: "unknown channel"},
		{name: "probed_at required", report: capture.ProbeReport{Connector: "h", Channel: "A", Captures: []string{"headline"}}, wantErr: "probed_at is required"},
		{name: "probed_at shape", report: capture.ProbeReport{Connector: "h", Channel: "A", ProbedAt: "yesterday", Captures: []string{"headline"}}, wantErr: "probed_at"},
		{name: "empty and silent", report: capture.ProbeReport{Connector: "h", Channel: "A", ProbedAt: "2026-10-19T15:04:05Z"}, wantErr: "no captures and no declared degradation"},
		{name: "degraded fallback", report: capture.ProbeReport{Connector: "h", Channel: "A", ProbedAt: "2026-10-19T15:04:05Z", Degraded: true}, wantErr: "fallback channel"},
		{name: "accepted", report: capture.ReportProbe(fullCapability())},
		{name: "channel E accepted", report: capture.ProbeReport{Connector: "h", Channel: "E", ProbedAt: "2026-10-19T15:04:05Z", Degraded: true, FallbackChannel: "E", Missing: []string{"headline"}}},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			err := testCase.report.Validate()
			if testCase.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), testCase.wantErr) {
				t.Fatalf("Validate = %v, want error containing %q", err, testCase.wantErr)
			}
		})
	}
}

func degradedCapability() contract.ConnectorCapability {
	capability := fullCapability()
	capability.Channel = "C"
	capability.Probe.OK = false
	capability.Probe.Missing = []string{"error_code", "tool_name"}
	capability.Fallback = contract.Fallback{Channel: "E", Missing: []string{"error_code", "tool_name"}, Note: "window titles are not an interface"}
	return capability
}

func TestDegradationIsExplicit(t *testing.T) {
	if degradation := capture.Degradation(fullCapability()); degradation != nil {
		t.Fatalf("full capability is degraded: %v", degradation)
	}

	degraded := degradedCapability()
	err := capture.Degradation(degraded)
	if err == nil {
		t.Fatal("degraded capability reported no degradation")
	}
	if len(err.Missing) != 2 || err.Fallback.Channel != "E" {
		t.Fatalf("degradation = %+v", err)
	}
	message := err.Error()
	if !strings.Contains(message, "falls back to channel E") || !strings.Contains(message, "error_code") {
		t.Fatalf("error = %q", message)
	}

	report := capture.ReportProbe(degraded)
	if !report.Degraded || report.FallbackChannel != "E" {
		t.Fatalf("report = %+v", report)
	}
}

func TestProbeReportIsDoctorConsumable(t *testing.T) {
	okCheck := capture.ReportProbe(fullCapability()).DoctorCheck()
	if okCheck.Status != capture.DoctorStatusOK || okCheck.Fix != "" {
		t.Fatalf("ok check = %+v", okCheck)
	}

	degradedCheck := capture.ReportProbe(degradedCapability()).DoctorCheck()
	if degradedCheck.Status != capture.DoctorStatusDegraded {
		t.Fatalf("degraded check = %+v", degradedCheck)
	}
	if degradedCheck.Fix == "" || degradedCheck.Detail["missing"] == "" {
		t.Fatalf("degraded check lacks a fix or detail: %+v", degradedCheck)
	}
	if !strings.Contains(degradedCheck.Summary, "falls back to") {
		t.Fatalf("summary = %q", degradedCheck.Summary)
	}

	encoded, err := json.Marshal(degradedCheck)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"name", "status", "summary", "detail", "fix"} {
		if _, ok := decoded[key]; !ok {
			t.Fatalf("doctor check JSON misses %q: %s", key, encoded)
		}
	}
}

func TestProbeReportHasNoWindowTitleDependency(t *testing.T) {
	reportType := reflect.TypeFor[capture.ProbeReport]()
	for index := 0; index < reportType.NumField(); index++ {
		name := strings.ToLower(reportType.Field(index).Name)
		for _, forbidden := range []string{"title", "window", "caption"} {
			if strings.Contains(name, forbidden) {
				t.Fatalf("ProbeReport field %q looks like a window-title dependency", reportType.Field(index).Name)
			}
		}
	}
	for _, required := range []string{"connector", "host_version", "probed_at", "channel"} {
		if _, ok := reportType.FieldByNameFunc(func(name string) bool {
			return jsonName(reportType, name) == required
		}); !ok {
			t.Fatalf("ProbeReport misses %q", required)
		}
	}
}

func jsonName(reportType reflect.Type, fieldName string) string {
	field, ok := reportType.FieldByName(fieldName)
	if !ok {
		return ""
	}
	tag := field.Tag.Get("json")
	if index := strings.Index(tag, ","); index >= 0 {
		tag = tag[:index]
	}
	return tag
}

func TestConnectorSourceKeepsCoreLogicOut(t *testing.T) {
	source, err := os.ReadFile("connector.go")
	if err != nil {
		t.Fatalf("read connector.go: %v", err)
	}
	for _, forbidden := range []string{"regexp.", "redact.", "core.", "database.", "sha256", "Classify(", "Normalize(", "pgx."} {
		if strings.Contains(string(source), forbidden) {
			t.Fatalf("connector.go reimplements platform logic: found %q", forbidden)
		}
	}
}

func TestConnectorContractDoesNotRepeatTheCapabilityShape(t *testing.T) {
	source, err := os.ReadFile("../../spec/connector.cue")
	if err != nil {
		t.Fatalf("read spec/connector.cue: %v", err)
	}
	text := string(source)
	field := regexp.MustCompile(`(?m)^\s*(notice_before_diagnosis|host_version|captures|fallback|probe)\b`)
	if match := field.FindString(text); match != "" {
		t.Fatalf("connector.cue repeats a capability field: %q", match)
	}
	for _, required := range []string{"#ConnectorSDKVersion", "ConnectorCaptureRequest", "ConnectorLookupRequest", "ConnectorNoticeRequest", "ConnectorFeedbackRequest"} {
		if !strings.Contains(text, required) {
			t.Fatalf("connector.cue misses %q", required)
		}
	}
}
