// The ErrorEvent v1 wire contract. Every field name below is taken verbatim
// from section 4 of the implementation plan; the struct is closed, so no
// aggregation field (issue_id, solution_id, hit_count, ...) can be added.
package spec

// #SchemaVersion is the only version this contract accepts. It is the anchor
// for the compatibility rules that EKB-011 adds when v2 appears.
#SchemaVersion: "v1"

// #Channel is the capture reliability level from section 3.1: A hook plugin
// or structured event API, B CLI wrapper or structured log, C plain log tail,
// D Windows UI Automation, E OCR, clipboard or manual hotkey.
#Channel: "A" | "B" | "C" | "D" | "E"

// #Category mirrors the categories the DSH plugin already records
// (src/capture.ts CATEGORY_BY_SOURCE). EKB-016 owns the final taxonomy.
#Category: "tool" | "command-exit" | "llm" | "agent"

// #Fingerprint is sha256(category + NUL + normalized_message)[0:12], the same
// form as src/signature.ts.
#Fingerprint: string & =~"^[0-9a-f]{12}$"

// #NoSensitive marks text that was redacted before the write point
// (section 6). These are the minimum v1 patterns only: EKB-014 replaces this
// definition with the rules in spec/redact.cue.
#NoSensitive: string &
	// API tokens: OpenAI-style, AWS, GitHub.
	!~"sk-[A-Za-z0-9_-]{16,}" &
	!~"AKIA[0-9A-Z]{16}" &
	!~"ghp_[A-Za-z0-9]{20,}" &
	// Bearer credentials and Authorization headers.
	!~"[Bb]earer[ \t]+[A-Za-z0-9._~+/=-]{16,}" &
	!~"(?i)authorization[ \t]*:" &
	// Absolute paths, which redaction collapses to <path>.
	!~"[A-Za-z]:\\\\" &
	!~"/home/|/Users/" &
	// Raw request identifiers. The redacted form keeps the label and replaces
	// the value with <request-id>, so the pattern requires an
	// identifier-shaped value after the separator.
	!~"(?i)request[_-]?id[ \t]*[:=][ \t]*[\"']?[A-Za-z0-9]"

// #ErrorEvent is one redacted observation. Events, issues, solutions and
// outcomes stay separate entities, so this struct carries no aggregate.
#ErrorEvent: {
	schema_version: #SchemaVersion

	event_id:     string & !=""
	connector_id: string & !=""
	session_id:   string & !=""
	observed_at:  string & =~"^[0-9]{4}-[0-9]{2}-[0-9]{2}T" // RFC 3339
	channel:      #Channel

	category:  #Category
	severity:  string & !="" // taxonomy is fixed in EKB-016
	transient: bool

	headline:           #NoSensitive & !=""
	normalized_message: #NoSensitive & !=""
	redacted_sample:    #NoSensitive & !=""

	// Only present when the failure came from a command or a tool call.
	error_code?: string
	command?:    string
	exit_code?:  int
	tool_name?:  string

	os:     string & !=""
	shell?: string
	// runtime_versions is an optional key/value table, e.g. node: 24.16.0.
	runtime_versions?: [string]: string
	project_key: string & !=""

	fingerprint: #Fingerprint
	evidence:    string & !=""
	confidence:  >=0 & <=1
}
