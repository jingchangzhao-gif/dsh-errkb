// The ErrorEvent wire contract. Section 4 of the implementation plan lists the
// fields; EKB-006 turns this skeleton into the validated v1 definition.
package spec

#ErrorEvent: {
	schema_version: string
	event_id:       string
	connector_id:   string
	session_id:     string
	observed_at:    string
	channel:        string

	category:  string
	severity:  string
	transient: bool

	headline:           string
	normalized_message: string
	redacted_sample:    string

	error_code: string
	command:    string
	exit_code:  int
	tool_name:  string

	os:    string
	shell: string
	runtime_versions: [string]: string
	project_key: string

	fingerprint: string
	evidence:    string
	confidence:  number
}
