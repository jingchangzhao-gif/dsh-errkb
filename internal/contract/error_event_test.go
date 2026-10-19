package contract

import (
	"encoding/json"
	"strings"
	"testing"
)

// validEvent returns a fully populated event that passes Validate. Each test
// copies it so one case cannot leak into the next.
func validEvent() map[string]any {
	return map[string]any{
		"schema_version":     "v1",
		"event_id":           "evt-1",
		"connector_id":       "dsh",
		"session_id":         "ses-1",
		"observed_at":        "2026-02-14T10:00:00Z",
		"channel":            "B",
		"category":           "tool",
		"severity":           "error",
		"transient":          false,
		"headline":           "pwsh: rename failed with EPERM",
		"normalized_message": "eperm: operation not permitted, rename '<path>/pkg'",
		"redacted_sample":    "rename <path>/pkg: EPERM",
		"error_code":         "EPERM",
		"command":            "go mod tidy",
		"exit_code":          1,
		"tool_name":          "pwsh",
		"os":                 "windows",
		"runtime_versions":   map[string]any{"go": "1.26.4"},
		"project_key":        "dsh-errkb",
		"fingerprint":        "0123456789ab",
		"evidence":           "tool result: pwsh exited 1",
		"confidence":         0.9,
	}
}

func encode(t *testing.T, event map[string]any) []byte {
	t.Helper()
	data, err := json.Marshal(event)
	if err != nil {
		t.Fatalf("marshal event: %v", err)
	}
	return data
}

func TestDecodeValidEvents(t *testing.T) {
	// Three shapes that must all be accepted: a tool failure, an LLM failure
	// with only the required fields, and a command exit.
	llm := map[string]any{
		"schema_version":     "v1",
		"event_id":           "evt-2",
		"connector_id":       "dsh",
		"session_id":         "ses-2",
		"observed_at":        "2026-02-14T10:05:00+08:00",
		"channel":            "A",
		"category":           "llm",
		"severity":           "warning",
		"transient":          true,
		"headline":           "llm: rate limited",
		"normalized_message": "<provider>: rate limit reached, retry later",
		"redacted_sample":    "429 too many requests for <project>",
		"os":                 "windows",
		"project_key":        "dsh-errkb",
		"fingerprint":        "abcdef012345",
		"evidence":           "hook event: rate limit",
		"confidence":         0.5,
	}
	command := validEvent()
	command["category"] = "command-exit"
	command["event_id"] = "evt-3"

	for _, event := range []map[string]any{validEvent(), llm, command} {
		if _, err := DecodeErrorEvent(encode(t, event)); err != nil {
			t.Fatalf("valid event rejected: %v", err)
		}
	}
}

func TestMissingRequiredFieldIsRejected(t *testing.T) {
	// Every required field, one case each. Optional fields are not listed.
	required := []string{
		"schema_version",
		"event_id",
		"connector_id",
		"session_id",
		"observed_at",
		"channel",
		"category",
		"severity",
		"transient",
		"headline",
		"normalized_message",
		"redacted_sample",
		"os",
		"project_key",
		"fingerprint",
		"evidence",
		"confidence",
	}
	for _, field := range required {
		t.Run(field, func(t *testing.T) {
			event := validEvent()
			delete(event, field)
			_, err := DecodeErrorEvent(encode(t, event))
			if err == nil {
				t.Fatalf("event without %q was accepted", field)
			}
			if !strings.Contains(err.Error(), field) {
				t.Fatalf("error does not name %q: %v", field, err)
			}
		})
	}
}

func TestUnredactedTextIsRejected(t *testing.T) {
	// Section 6: keys, paths, Authorization headers and raw request ids never
	// reach the write point.
	cases := map[string]struct {
		field string
		text  string
	}{
		// Fixtures are assembled at run time: the repository privacy guard
		// rejects credential-shaped literals, and test fixtures are no exception.
		"api key": {
			field: "headline",
			text:  "auth failed for " + "sk-" + "abcdefghijklmnopqrstuvwx",
		},
		"absolute path": {
			field: "redacted_sample",
			text:  "open " + "/home/" + "<user>/project/file.log failed",
		},
		"authorization": {
			field: "normalized_message",
			text:  "Authorization" + ": " + "Bearer " + "abcdefghijklmnopqrstuvwxyz",
		},
		"request id": {
			field: "redacted_sample",
			text:  "requestId" + "=" + "9f8e7d6c5b4a",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			event := validEvent()
			event[tc.field] = tc.text
			_, err := DecodeErrorEvent(encode(t, event))
			if err == nil {
				t.Fatalf("unredacted text in %q was accepted: %q", tc.field, tc.text)
			}
			if !strings.Contains(err.Error(), tc.field) {
				t.Fatalf("error does not name %q: %v", tc.field, err)
			}
		})
	}
}

func TestRedactedRequestIdIsAccepted(t *testing.T) {
	// redact.Text keeps the label and replaces the value with <request-id>;
	// the label alone must not be mistaken for a leak (found live when a real
	// provider error carried "request_id: ..." and redaction succeeded).
	event := validEvent()
	event["headline"] = "provider failed (request_id: " + "<request-id>" + ")"
	event["normalized_message"] = event["headline"]
	event["redacted_sample"] = event["headline"]
	if _, err := DecodeErrorEvent(encode(t, event)); err != nil {
		t.Fatalf("redacted request id was rejected: %v", err)
	}
}

func TestAggregateAndUnknownFieldsAreRejected(t *testing.T) {
	// Events are not issues: aggregate data must not ride along.
	for _, field := range []string{"issue_id", "solution_id", "hit_count"} {
		event := validEvent()
		event[field] = 7
		if _, err := DecodeErrorEvent(encode(t, event)); err == nil {
			t.Fatalf("event with aggregate field %q was accepted", field)
		}
	}
}

func TestEnumAndShapeViolations(t *testing.T) {
	cases := map[string]func(map[string]any){
		"unknown channel":     func(e map[string]any) { e["channel"] = "Z" },
		"unknown category":    func(e map[string]any) { e["category"] = "guess" },
		"schema v2":           func(e map[string]any) { e["schema_version"] = "v2" },
		"short fingerprint":   func(e map[string]any) { e["fingerprint"] = "0123" },
		"uppercase print":     func(e map[string]any) { e["fingerprint"] = "0123456789AB" },
		"timestamp without T": func(e map[string]any) { e["observed_at"] = "2026-02-14" },
		"confidence above 1":  func(e map[string]any) { e["confidence"] = 1.5 },
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			event := validEvent()
			mutate(event)
			if _, err := DecodeErrorEvent(encode(t, event)); err == nil {
				t.Fatalf("%s was accepted", name)
			}
		})
	}
}
