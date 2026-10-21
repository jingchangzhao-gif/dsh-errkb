// The connector capability contract: what a host can capture, whether it can
// receive a known fix before diagnosing, and how it degrades. Host names are
// open strings: a window title is not an interface contract (§3.2), so a new
// host never changes this schema.
package spec

// #EventField names the ErrorEvent v1 fields a connector may fill in.
#EventField:
	"schema_version" | "event_id" | "connector_id" | "session_id" |
	"observed_at" | "channel" | "category" | "severity" | "transient" |
	"headline" | "normalized_message" | "redacted_sample" |
	"error_code" | "command" | "exit_code" | "tool_name" |
	"os" | "shell" | "runtime_versions" | "project_key" |
	"fingerprint" | "evidence" | "confidence"

#ConnectorCapability: {
	// Host identifier, e.g. "dsh", "codex", "claude-code".
	connector: string & !=""
	// The host version this declaration was measured against.
	host_version: string
	// The reliability level from §3.1 that this connector delivers.
	channel: #Channel
	// What this connector fills in at that level.
	captures: [...#EventField]
	// Whether the host can receive a known fix before it starts diagnosing.
	notice_before_diagnosis: bool
	// Explicit degradation: the level and fields that remain when the best
	// channel is unavailable (§3.2 "能力不足时明确降级").
	fallback: {
		channel: #Channel
		missing: [...#EventField]
		note?: string
	}
	// Shape of the live capability probe recorded for this host.
	probe: {
		probed_at:    string & =~"^[0-9]{4}-[0-9]{2}-[0-9]{2}T"
		host_version: string
		ok:           bool
		missing: [...#EventField]
	}
}
