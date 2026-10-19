// Package contract holds the wire contracts shared by every connector. This
// file is the Go side of spec/error-event.cue: the same field names (plan
// section 4), the same required set and the same redaction rules. It stays
// dependency-free so connectors can import it without pulling in the driver.
package contract

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"
)

// SchemaVersion is the only ErrorEvent version this build accepts. EKB-011
// adds the compatibility rule when v2 appears.
const SchemaVersion = "v1"

// FingerprintLength is the number of hex characters in a fingerprint:
// sha256(category + NUL + normalized_message)[0:12], as in src/signature.ts.
const FingerprintLength = 12

var (
	fingerprintPattern = regexp.MustCompile(`^[0-9a-f]{12}$`)
	observedAtPattern  = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T`)
)

// Channels are the capture reliability levels from plan section 3.1: A hook
// plugin, B CLI wrapper or structured log, C plain log tail, D UI Automation,
// E OCR, clipboard or manual hotkey.
var validChannels = map[string]bool{"A": true, "B": true, "C": true, "D": true, "E": true}

// Categories mirror src/capture.ts CATEGORY_BY_SOURCE. EKB-016 owns the final
// taxonomy, so this map is the only place to extend.
var validCategories = map[string]bool{
	"tool":         true,
	"command-exit": true,
	"llm":          true,
	"agent":        true,
}

// sensitivePattern is a minimum v1 redaction rule. EKB-014 replaces this list
// with the rules that spec/redact.cue generates.
type sensitivePattern struct {
	what string
	re   *regexp.Regexp
}

var sensitivePatterns = []sensitivePattern{
	{"an API key", regexp.MustCompile(`sk-[A-Za-z0-9_-]{16,}`)},
	{"an AWS key", regexp.MustCompile(`AKIA[0-9A-Z]{16}`)},
	{"a GitHub token", regexp.MustCompile(`ghp_[A-Za-z0-9]{20,}`)},
	{"a bearer credential", regexp.MustCompile(`(?i)bearer[ \t]+[A-Za-z0-9._~+/=-]{16,}`)},
	{"an authorization header", regexp.MustCompile(`(?i)authorization[ \t]*:`)},
	{"an absolute path", regexp.MustCompile(`[A-Za-z]:\\`)},
	{"an absolute path", regexp.MustCompile(`/home/|/Users/`)},
	{"a raw request id", regexp.MustCompile(`(?i)request[_-]?id[ \t]*[:=][ \t]*["']?[A-Za-z0-9]`)},
}

// ErrorEvent is one redacted observation. Events, issues, solutions and
// outcomes are separate entities, so this struct carries no aggregate field;
// DecodeErrorEvent rejects unknown fields such as issue_id or hit_count.
type ErrorEvent struct {
	SchemaVersion string `json:"schema_version"`

	EventID     string `json:"event_id"`
	ConnectorID string `json:"connector_id"`
	SessionID   string `json:"session_id"`
	ObservedAt  string `json:"observed_at"`
	Channel     string `json:"channel"`

	Category  string `json:"category"`
	Severity  string `json:"severity"`
	Transient *bool  `json:"transient"`

	Headline          string `json:"headline"`
	NormalizedMessage string `json:"normalized_message"`
	RedactedSample    string `json:"redacted_sample"`

	// Only present when the failure came from a command or a tool call.
	ErrorCode string `json:"error_code,omitempty"`
	Command   string `json:"command,omitempty"`
	ExitCode  *int   `json:"exit_code,omitempty"`
	ToolName  string `json:"tool_name,omitempty"`

	OS              string            `json:"os"`
	Shell           string            `json:"shell,omitempty"`
	RuntimeVersions map[string]string `json:"runtime_versions,omitempty"`
	ProjectKey      string            `json:"project_key"`

	Fingerprint string   `json:"fingerprint"`
	Evidence    string   `json:"evidence"`
	Confidence  *float64 `json:"confidence"`
}

// DecodeErrorEvent parses one JSON event and validates it. Unknown fields are
// rejected so aggregate data cannot ride along on an event.
func DecodeErrorEvent(data []byte) (ErrorEvent, error) {
	var event ErrorEvent
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&event); err != nil {
		return ErrorEvent{}, fmt.Errorf("decode error event: %w", err)
	}
	if decoder.More() {
		return ErrorEvent{}, errors.New("decode error event: trailing data after the event")
	}
	if err := event.Validate(); err != nil {
		return ErrorEvent{}, err
	}
	return event, nil
}

// Validate enforces the contract: schema version, required fields, closed
// enums, fingerprint shape, timestamp shape and the redaction minimum. It
// returns the first violation, named by field.
func (e ErrorEvent) Validate() error {
	if e.SchemaVersion != SchemaVersion {
		return fmt.Errorf("schema_version: want %q, got %q", SchemaVersion, e.SchemaVersion)
	}

	required := []struct {
		name  string
		value string
	}{
		{"event_id", e.EventID},
		{"connector_id", e.ConnectorID},
		{"session_id", e.SessionID},
		{"observed_at", e.ObservedAt},
		{"channel", e.Channel},
		{"category", e.Category},
		{"severity", e.Severity},
		{"headline", e.Headline},
		{"normalized_message", e.NormalizedMessage},
		{"redacted_sample", e.RedactedSample},
		{"os", e.OS},
		{"project_key", e.ProjectKey},
		{"fingerprint", e.Fingerprint},
		{"evidence", e.Evidence},
	}
	for _, field := range required {
		if field.value == "" {
			return fmt.Errorf("%s: required", field.name)
		}
	}
	if e.Transient == nil {
		return errors.New("transient: required")
	}
	if e.Confidence == nil {
		return errors.New("confidence: required")
	}
	if *e.Confidence < 0 || *e.Confidence > 1 {
		return fmt.Errorf("confidence: want 0..1, got %v", *e.Confidence)
	}

	if !validChannels[e.Channel] {
		return fmt.Errorf("channel: unknown level %q", e.Channel)
	}
	if !validCategories[e.Category] {
		return fmt.Errorf("category: unknown category %q", e.Category)
	}
	if !fingerprintPattern.MatchString(e.Fingerprint) {
		return fmt.Errorf("fingerprint: want %d lowercase hex characters, got %q", FingerprintLength, e.Fingerprint)
	}
	if !observedAtPattern.MatchString(e.ObservedAt) {
		return fmt.Errorf("observed_at: want an RFC 3339 timestamp, got %q", e.ObservedAt)
	}
	if _, err := time.Parse(time.RFC3339, e.ObservedAt); err != nil {
		return fmt.Errorf("observed_at: %w", err)
	}

	texts := []struct {
		name  string
		value string
	}{
		{"headline", e.Headline},
		{"normalized_message", e.NormalizedMessage},
		{"redacted_sample", e.RedactedSample},
	}
	for _, text := range texts {
		for _, pattern := range sensitivePatterns {
			if pattern.re.MatchString(text.value) {
				return fmt.Errorf("%s: still contains %s, redact before the write point", text.name, pattern.what)
			}
		}
	}

	return nil
}
