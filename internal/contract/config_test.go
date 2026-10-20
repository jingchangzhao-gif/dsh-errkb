package contract

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// validConfigYAML is the sample a user could actually write: YAML, no CUE.
const validConfigYAML = `
database:
  dsn: postgres://errkb:errkb@127.0.0.1:5432/errkb
  max_connections: 4
service:
  pipe:
    name: errkb
  http:
    enabled: false
capture:
  enabled: true
  paused_connectors: [codex]
  paused_projects: [scratch]
notices:
  before_diagnosis: true
redaction:
  extra_patterns: ["internal-token-[0-9]+"]
search:
  limit: 3
timeout:
  capture_ms: 2000
  search_ms: 1500
  mcp_ms: 5000
offline:
  spool_dir: '%LOCALAPPDATA%\errkb\spool'
  replay_interval_ms: 5000
retention:
  sample_days: 7
  normalized_days: 90
  audit_days: 365
`

// minimalConfigYAML is what a user may start from; defaults fill the rest.
const minimalConfigYAML = `
database:
  dsn: postgres://errkb:errkb@127.0.0.1:5432/errkb
retention:
  sample_days: 7
  normalized_days: 90
  audit_days: 365
`

func TestLoadConfigAcceptsDocumentedYAML(t *testing.T) {
	config, err := LoadConfig([]byte(validConfigYAML))
	if err != nil {
		t.Fatalf("LoadConfig() error = %v, want a valid configuration", err)
	}
	if !*config.Service.Pipe.Enabled {
		t.Error("pipe.enabled = false, want the safe default true")
	}
	if *config.Service.HTTP.Enabled {
		t.Error("http.enabled = true, want the safe default false")
	}
	if got, want := config.Service.HTTP.Host, "127.0.0.1"; got != want {
		t.Errorf("http.host = %q, want %q", got, want)
	}
	if got, want := *config.Search.Limit, 3; got != want {
		t.Errorf("search.limit = %d, want %d", got, want)
	}
	if got, want := *config.Retention.AuditDays, 365; got != want {
		t.Errorf("retention.audit_days = %d, want %d", got, want)
	}
	if len(config.Capture.PausedProjects) != 1 || config.Capture.PausedProjects[0] != "scratch" {
		t.Errorf("capture.paused_projects = %v, want [scratch]", config.Capture.PausedProjects)
	}
}

func TestLoadConfigAppliesDefaults(t *testing.T) {
	config, err := LoadConfig([]byte(minimalConfigYAML))
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if !*config.Notices.BeforeDiagnosis {
		t.Error("notices.before_diagnosis = false, want the default true")
	}
	if !*config.Capture.Enabled {
		t.Error("capture.enabled = false, want the default true")
	}
	if got := config.Service.Pipe.Name; got != DefaultPipeName {
		t.Errorf("service.pipe.name = %q, want %q", got, DefaultPipeName)
	}
	if got := *config.Search.Limit; got != DefaultSearchLimit {
		t.Errorf("search.limit = %d, want the default %d", got, DefaultSearchLimit)
	}
	if got := *config.Timeout.MCPMS; got != DefaultMCPTimeoutMS {
		t.Errorf("timeout.mcp_ms = %d, want the default %d", got, DefaultMCPTimeoutMS)
	}
}

func TestLoadConfigRejectsUnsafeValues(t *testing.T) {
	// Every document is a raw YAML literal: an escape sequence such as
	// "\n" inside a Go string can look like a drive path to the repository
	// privacy guard, so the fixtures keep real newlines instead.
	cases := []struct {
		name     string
		document string
		want     string
	}{
		{
			name: "missing DSN",
			document: `
database:
  max_connections: 4
retention:
  sample_days: 7
  normalized_days: 90
  audit_days: 365
`,
			want: "database.dsn: required",
		},
		{
			name: "unknown key",
			document: `
databse:
  dsn: postgres://errkb:errkb@127.0.0.1:5432/errkb
retention:
  sample_days: 7
  normalized_days: 90
  audit_days: 365
`,
			want: "databse",
		},
		{
			name: "every channel disabled",
			document: `
database:
  dsn: postgres://errkb:errkb@127.0.0.1:5432/errkb
retention:
  sample_days: 7
  normalized_days: 90
  audit_days: 365
service:
  pipe:
    enabled: false
  http:
    enabled: false
`,
			want: "at least one of pipe or http",
		},
		{
			name: "http without a token",
			document: `
database:
  dsn: postgres://errkb:errkb@127.0.0.1:5432/errkb
retention:
  sample_days: 7
  normalized_days: 90
  audit_days: 365
service:
  http:
    enabled: true
`,
			want: "service.http.token",
		},
		{
			name: "http with a short token",
			document: `
database:
  dsn: postgres://errkb:errkb@127.0.0.1:5432/errkb
retention:
  sample_days: 7
  normalized_days: 90
  audit_days: 365
service:
  http:
    enabled: true
    token: short
`,
			want: "at least 32 characters",
		},
		{
			name: "http on a public address",
			document: `
database:
  dsn: postgres://errkb:errkb@127.0.0.1:5432/errkb
retention:
  sample_days: 7
  normalized_days: 90
  audit_days: 365
service:
  http:
    enabled: true
    host: 0.0.0.0
    token: 0123456789abcdef0123456789abcdef01234567
`,
			want: "loopback",
		},
		{
			name: "capture off while notices stay on",
			document: `
database:
  dsn: postgres://errkb:errkb@127.0.0.1:5432/errkb
retention:
  sample_days: 7
  normalized_days: 90
  audit_days: 365
capture:
  enabled: false
`,
			want: "capture.enabled is false",
		},
		{
			name: "missing retention period",
			document: `
database:
  dsn: postgres://errkb:errkb@127.0.0.1:5432/errkb
retention:
  sample_days: 7
  normalized_days: 90
`,
			want: "retention.audit_days: required",
		},
		{
			name: "negative retention",
			document: `
database:
  dsn: postgres://errkb:errkb@127.0.0.1:5432/errkb
retention:
  sample_days: -1
  normalized_days: 90
  audit_days: 365
`,
			want: "retention: days",
		},
		{
			name: "search limit above three",
			document: minimalConfigYAML + `
search:
  limit: 4
`,
			want: "search.limit",
		},
		{
			name: "zero timeout",
			document: minimalConfigYAML + `
timeout:
  search_ms: 0
`,
			want: "timeout: every value",
		},
		{
			name: "zero replay interval",
			document: minimalConfigYAML + `
offline:
  replay_interval_ms: 0
`,
			want: "offline.replay_interval_ms",
		},
		{
			name:     "malformed document",
			document: "database: [\n",
			want:     "decode config",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := LoadConfig([]byte(testCase.document))
			if err == nil {
				t.Fatal("LoadConfig() error = nil, want a rejection")
			}
			if !strings.Contains(err.Error(), testCase.want) {
				t.Errorf("LoadConfig() error = %q, want it to mention %q", err, testCase.want)
			}
		})
	}
}

const capabilityYAML = `
connector: dsh
host_version: 0.2.0-rc.2
channel: A
captures: [event_id, channel, category, headline, normalized_message, fingerprint, confidence]
notice_before_diagnosis: true
fallback:
  channel: B
  missing: [exit_code, tool_name]
  note: "dsh does not expose a shell exit code"
probe:
  probed_at: "2026-10-17T00:00:00Z"
  host_version: 0.2.0-rc.2
  ok: true
  missing: [exit_code]
`

func TestLoadConnectorCapabilityAcceptsDocumentedYAML(t *testing.T) {
	capability, err := LoadConnectorCapability([]byte(capabilityYAML))
	if err != nil {
		t.Fatalf("LoadConnectorCapability() error = %v", err)
	}
	if capability.Connector != "dsh" || capability.Channel != "A" {
		t.Errorf("capability = %+v, want connector dsh on channel A", capability)
	}
	if len(capability.Fallback.Missing) == 0 {
		t.Error("fallback.missing = empty, want the declared degradation")
	}
}

func TestLoadConnectorCapabilityAcceptsAnUnknownHost(t *testing.T) {
	document := strings.Replace(capabilityYAML, "connector: dsh", "connector: future-host", 1)
	if _, err := LoadConnectorCapability([]byte(document)); err != nil {
		t.Fatalf("LoadConnectorCapability() error = %v, want host names to stay open", err)
	}
}

func TestLoadConnectorCapabilityRejectsInvalidDeclarations(t *testing.T) {
	const known = "[event_id, channel, category, headline, normalized_message, fingerprint, confidence]"
	cases := []struct {
		name     string
		document string
		want     string
	}{
		{
			name:     "unknown channel level",
			document: strings.Replace(capabilityYAML, "channel: A", "channel: Z", 1),
			want:     "unknown level",
		},
		{
			name:     "notice without any capture",
			document: strings.Replace(capabilityYAML, "captures: "+known, "captures: []", 1),
			want:     "cannot return a notice",
		},
		{
			name:     "field the event does not have",
			document: strings.Replace(capabilityYAML, "captures: [event_id,", "captures: [issue_id, event_id,", 1),
			want:     "unknown ErrorEvent field",
		},
		{
			name:     "unknown fallback field",
			document: strings.Replace(capabilityYAML, "missing: [exit_code, tool_name]", "missing: [hit_count]", 1),
			want:     "unknown ErrorEvent field",
		},
		{
			name:     "missing probe timestamp",
			document: strings.Replace(capabilityYAML, `  probed_at: "2026-10-17T00:00:00Z"`, "", 1),
			want:     "probe.probed_at: required",
		},
		{
			name:     "probe timestamp that is not RFC 3339",
			document: strings.Replace(capabilityYAML, "2026-10-17T00:00:00Z", "yesterday", 1),
			want:     "probe.probed_at",
		},
		{
			name:     "unknown key",
			document: strings.Replace(capabilityYAML, "connector: dsh", "conector: dsh", 1),
			want:     "conector",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := LoadConnectorCapability([]byte(testCase.document))
			if err == nil {
				t.Fatal("LoadConnectorCapability() error = nil, want a rejection")
			}
			if !strings.Contains(err.Error(), testCase.want) {
				t.Errorf("LoadConnectorCapability() error = %q, want it to mention %q", err, testCase.want)
			}
		})
	}
}

// TestLoadConfigAcceptsGenericHostTemplates proves the EKB-042 template block
// is a real configuration, not a sketch.
func TestLoadConfigAcceptsGenericHostTemplates(t *testing.T) {
	document := validConfigYAML + `
templates:
  cli:
    command: ["kimi", "--print"]
    capture_exit_codes: true
  log:
    path: C:/logs/kimi.log
    encoding: utf-16le
    rotation:
      max_bytes: 1048576
      keep: 2
  manual:
    note: "pass the error text through errkb run"
`
	if _, err := LoadConfig([]byte(document)); err != nil {
		t.Fatalf("LoadConfig() error = %v, want the template block accepted", err)
	}
}

func TestLoadConfigRejectsInvalidTemplates(t *testing.T) {
	cases := map[string]string{
		// Raw string literals keep the YAML readable and avoid escape sequences
		// that the repository privacy guard reads as a drive path.
		"unknown encoding": `templates:
  log:
    path: C:/x.log
    encoding: latin-1
`,
		"log enabled, no path": `templates:
  log:
    enabled: true
`,
		"negative rotation keep": `templates:
  log:
    path: C:/x.log
    rotation:
      keep: -1
`,
		"empty command part": `templates:
  cli:
    command: ["ok", ""]
`,
	}
	for name, extra := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := LoadConfig([]byte(validConfigYAML + extra)); err == nil {
				t.Fatal("LoadConfig() error = nil, want the template block rejected")
			}
		})
	}
}

// TestShippedGenericTemplateIsValid keeps connectors/templates/cli-log/config.yaml
// honest: the file users copy must load through the real contract.
func TestShippedGenericTemplateIsValid(t *testing.T) {
	path := filepath.Join("..", "..", "connectors", "templates", "cli-log", "config.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if _, err := LoadConfig(data); err != nil {
		t.Fatalf("LoadConfig(%s) error = %v", path, err)
	}
}
