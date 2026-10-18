// Configuration and connector capability contracts. Users edit YAML or JSON;
// these structs and their Validate methods are the internal check, so nobody
// has to learn CUE to configure errkb (EKB-007).
package contract

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"time"

	"gopkg.in/yaml.v3"
)

// Defaults used when a YAML/JSON file omits a value. They are the safe ones
// from plan §6: the Named Pipe is on, loopback HTTP is off.
const (
	DefaultPipeName         = "errkb"
	DefaultSearchLimit      = 3
	DefaultCaptureTimeoutMS = 2000
	DefaultSearchTimeoutMS  = 1500
	DefaultMCPTimeoutMS     = 5000
	DefaultReplayIntervalMS = 5000
	MinHTTPTokenLength      = 32
	loopbackHostDefault     = "127.0.0.1"
)

// Config is the user-editable configuration shape.
type Config struct {
	Database  DatabaseConfig  `yaml:"database"`
	Service   ServiceConfig   `yaml:"service"`
	Capture   CaptureConfig   `yaml:"capture"`
	Notices   NoticesConfig   `yaml:"notices"`
	Retention RetentionConfig `yaml:"retention"`
	Redaction RedactionConfig `yaml:"redaction"`
	Search    SearchConfig    `yaml:"search"`
	Timeout   TimeoutConfig   `yaml:"timeout"`
	Offline   OfflineConfig   `yaml:"offline"`
}

// DatabaseConfig points at the development PostgreSQL 16 instance.
type DatabaseConfig struct {
	DSN              string `yaml:"dsn"`
	MaxConnections   *int   `yaml:"max_connections,omitempty"`
	ConnectTimeoutMS *int   `yaml:"connect_timeout_ms,omitempty"`
}

// ServiceConfig holds the two local channels.
type ServiceConfig struct {
	Pipe PipeConfig `yaml:"pipe"`
	HTTP HTTPConfig `yaml:"http"`
}

// PipeConfig is the default channel.
type PipeConfig struct {
	Name    string `yaml:"name,omitempty"`
	Enabled *bool  `yaml:"enabled,omitempty"`
}

// HTTPConfig is the optional loopback listener.
type HTTPConfig struct {
	Enabled *bool  `yaml:"enabled,omitempty"`
	Host    string `yaml:"host,omitempty"`
	Port    *int   `yaml:"port,omitempty"`
	Token   string `yaml:"token,omitempty"`
}

// CaptureConfig can pause capture per connector and per project.
type CaptureConfig struct {
	Enabled          *bool    `yaml:"enabled,omitempty"`
	PausedConnectors []string `yaml:"paused_connectors,omitempty"`
	PausedProjects   []string `yaml:"paused_projects,omitempty"`
}

// NoticesConfig decides whether a known fix may come back before diagnosis.
type NoticesConfig struct {
	BeforeDiagnosis *bool `yaml:"before_diagnosis,omitempty"`
}

// RetentionConfig keeps the three periods separate (plan §6).
type RetentionConfig struct {
	SampleDays     *int `yaml:"sample_days"`
	NormalizedDays *int `yaml:"normalized_days"`
	AuditDays      *int `yaml:"audit_days"`
}

// RedactionConfig carries user-supplied extra patterns.
type RedactionConfig struct {
	ExtraPatterns []string `yaml:"extra_patterns,omitempty"`
}

// SearchConfig limits how many candidates a search returns.
type SearchConfig struct {
	Limit *int `yaml:"limit,omitempty"`
}

// TimeoutConfig bounds the three local call paths.
type TimeoutConfig struct {
	CaptureMS *int `yaml:"capture_ms,omitempty"`
	SearchMS  *int `yaml:"search_ms,omitempty"`
	MCPMS     *int `yaml:"mcp_ms,omitempty"`
}

// OfflineConfig is used while PostgreSQL is unreachable.
type OfflineConfig struct {
	SpoolDir         string `yaml:"spool_dir,omitempty"`
	ReplayIntervalMS *int   `yaml:"replay_interval_ms,omitempty"`
}

// ConnectorCapability is what one host connector declares.
type ConnectorCapability struct {
	Connector             string   `yaml:"connector"`
	HostVersion           string   `yaml:"host_version,omitempty"`
	Channel               string   `yaml:"channel"`
	Captures              []string `yaml:"captures,omitempty"`
	NoticeBeforeDiagnosis bool     `yaml:"notice_before_diagnosis"`
	Fallback              Fallback `yaml:"fallback"`
	Probe                 Probe    `yaml:"probe"`
}

// Fallback is the explicit degradation of a connector.
type Fallback struct {
	Channel string   `yaml:"channel"`
	Missing []string `yaml:"missing,omitempty"`
	Note    string   `yaml:"note,omitempty"`
}

// Probe records one capability probe result.
type Probe struct {
	ProbedAt    string   `yaml:"probed_at"`
	HostVersion string   `yaml:"host_version,omitempty"`
	OK          bool     `yaml:"ok"`
	Missing     []string `yaml:"missing,omitempty"`
}

// eventFields is the closed set of ErrorEvent v1 field names a connector may
// claim to capture. It mirrors spec/connector-capability.cue #EventField.
var eventFields = map[string]bool{
	"schema_version": true, "event_id": true, "connector_id": true,
	"session_id": true, "observed_at": true, "channel": true,
	"category": true, "severity": true, "transient": true,
	"headline": true, "normalized_message": true, "redacted_sample": true,
	"error_code": true, "command": true, "exit_code": true, "tool_name": true,
	"os": true, "shell": true, "runtime_versions": true, "project_key": true,
	"fingerprint": true, "evidence": true, "confidence": true,
}

// LoadConfig parses a YAML or JSON configuration, applies defaults and
// validates it. Unknown keys are rejected so a typo cannot silently pass.
func LoadConfig(data []byte) (Config, error) {
	var cfg Config
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return Config{}, errors.New("decode config: only one document is accepted")
	}
	cfg.applyDefaults()
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// applyDefaults fills every value the user may omit. It runs before Validate.
func (c *Config) applyDefaults() {
	if c.Service.Pipe.Name == "" {
		c.Service.Pipe.Name = DefaultPipeName
	}
	if c.Service.Pipe.Enabled == nil {
		c.Service.Pipe.Enabled = boolPtr(true)
	}
	if c.Service.HTTP.Enabled == nil {
		c.Service.HTTP.Enabled = boolPtr(false)
	}
	if c.Service.HTTP.Host == "" {
		c.Service.HTTP.Host = loopbackHostDefault
	}
	if c.Capture.Enabled == nil {
		c.Capture.Enabled = boolPtr(true)
	}
	if c.Notices.BeforeDiagnosis == nil {
		c.Notices.BeforeDiagnosis = boolPtr(true)
	}
	if c.Search.Limit == nil {
		c.Search.Limit = intPtr(DefaultSearchLimit)
	}
	if c.Timeout.CaptureMS == nil {
		c.Timeout.CaptureMS = intPtr(DefaultCaptureTimeoutMS)
	}
	if c.Timeout.SearchMS == nil {
		c.Timeout.SearchMS = intPtr(DefaultSearchTimeoutMS)
	}
	if c.Timeout.MCPMS == nil {
		c.Timeout.MCPMS = intPtr(DefaultMCPTimeoutMS)
	}
	if c.Offline.ReplayIntervalMS == nil {
		c.Offline.ReplayIntervalMS = intPtr(DefaultReplayIntervalMS)
	}
}

// Validate applies the defaults and then enforces the safety rules: the
// database DSN, at least one local channel, a token and a loopback host for
// HTTP, no notice while capture is off, three retention periods, and the
// timeout bounds.
func (c Config) Validate() error {
	c.applyDefaults()

	if c.Database.DSN == "" {
		return errors.New("database.dsn: required")
	}
	if !*c.Service.Pipe.Enabled && !*c.Service.HTTP.Enabled {
		return errors.New("service: at least one of pipe or http must be enabled")
	}
	if *c.Service.HTTP.Enabled {
		if len(c.Service.HTTP.Token) < MinHTTPTokenLength {
			return fmt.Errorf("service.http.token: required and at least %d characters when http is enabled", MinHTTPTokenLength)
		}
		if host := c.Service.HTTP.Host; host != "127.0.0.1" && host != "::1" && host != "localhost" {
			return fmt.Errorf("service.http.host: want a loopback address, got %q", host)
		}
	}
	if !*c.Capture.Enabled && *c.Notices.BeforeDiagnosis {
		return errors.New("notices.before_diagnosis: cannot be true while capture.enabled is false")
	}
	if c.Retention.SampleDays == nil {
		return errors.New("retention.sample_days: required")
	}
	if c.Retention.NormalizedDays == nil {
		return errors.New("retention.normalized_days: required")
	}
	if c.Retention.AuditDays == nil {
		return errors.New("retention.audit_days: required")
	}
	if *c.Retention.SampleDays < 0 || *c.Retention.NormalizedDays < 0 || *c.Retention.AuditDays < 0 {
		return errors.New("retention: days must be zero or positive")
	}
	if limit := *c.Search.Limit; limit < 1 || limit > 3 {
		return fmt.Errorf("search.limit: want 1..3, got %d", limit)
	}
	if *c.Timeout.CaptureMS <= 0 || *c.Timeout.SearchMS <= 0 || *c.Timeout.MCPMS <= 0 {
		return errors.New("timeout: every value must be positive")
	}
	if *c.Offline.ReplayIntervalMS <= 0 {
		return errors.New("offline.replay_interval_ms: must be positive")
	}
	return nil
}

// LoadConnectorCapability parses and validates one capability declaration.
func LoadConnectorCapability(data []byte) (ConnectorCapability, error) {
	var capability ConnectorCapability
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err := decoder.Decode(&capability); err != nil {
		return ConnectorCapability{}, fmt.Errorf("decode connector capability: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return ConnectorCapability{}, errors.New("decode connector capability: only one document is accepted")
	}
	if err := capability.Validate(); err != nil {
		return ConnectorCapability{}, err
	}
	return capability, nil
}

// Validate enforces the capability contract: a host string, a §3.1 channel,
// known field names, an explicit fallback and a probe timestamp. Host names
// are not enumerated, so adding a host never changes this schema.
func (c ConnectorCapability) Validate() error {
	if c.Connector == "" {
		return errors.New("connector: required")
	}
	if !validChannels[c.Channel] {
		return fmt.Errorf("channel: unknown level %q", c.Channel)
	}
	if err := checkEventFields("captures", c.Captures); err != nil {
		return err
	}
	if !validChannels[c.Fallback.Channel] {
		return fmt.Errorf("fallback.channel: unknown level %q", c.Fallback.Channel)
	}
	if err := checkEventFields("fallback.missing", c.Fallback.Missing); err != nil {
		return err
	}
	if c.Probe.ProbedAt == "" {
		return errors.New("probe.probed_at: required")
	}
	if _, err := time.Parse(time.RFC3339, c.Probe.ProbedAt); err != nil {
		return fmt.Errorf("probe.probed_at: %w", err)
	}
	if err := checkEventFields("probe.missing", c.Probe.Missing); err != nil {
		return err
	}
	if c.NoticeBeforeDiagnosis && len(c.Captures) == 0 {
		return errors.New("notice_before_diagnosis: a connector that captures nothing cannot return a notice")
	}
	return nil
}

func checkEventFields(name string, fields []string) error {
	for _, field := range fields {
		if !eventFields[field] {
			return fmt.Errorf("%s: unknown ErrorEvent field %q", name, field)
		}
	}
	return nil
}

func boolPtr(value bool) *bool { return &value }

func intPtr(value int) *int { return &value }
