// The connector SDK contract v1: the four actions a host adapter may take
// against the core platform, plus the observation it hands over before any
// classification, redaction, fingerprinting or storage happens (§3.2: those
// belong to the core and must never be reimplemented per adapter).
//
// The capability declaration and its probe result are #ConnectorCapability in
// connector-capability.cue. This file adds the call envelopes only and does not
// repeat that shape: no connector, host_version, channel, captures,
// notice_before_diagnosis, fallback or probe field is defined here.
package spec

import "list"

// #ConnectorSDKVersion is the version on every SDK envelope.
#ConnectorSDKVersion: "v1"

// #Observation is what a host can see about one failure before the core turns
// it into an ErrorEvent. Raw text is allowed here; the core redacts before any
// write and classification is never the adapter's job.
#Observation: {
	observed_at: string & =~"^[0-9]{4}-[0-9]{2}-[0-9]{2}T"
	// Which capture source produced this observation (the contract's axis).
	source: #Category
	// Raw text is accepted on the wire: the adapter must not redact (§3.2), and
	// the daemon redacts before any write. A #NoSensitive constraint here would
	// force every adapter to reimplement redaction.
	message: string
	// Optional hints a host may already know.
	error_code?: #NoSensitive
	command?:    string
	exit_code?:  int
	tool_name?:  string
	os?:         string
	shell?:      string
	runtime_versions?: [string]: string
	project_key?: #NoSensitive
	evidence?:    #NoSensitive
	confidence?:  float64 & >=0 & <=1
}

// #ConnectorCaptureRequest hands one observation to the core. The adapter does
// not compute a fingerprint, category or sample; it only translates shape.
#ConnectorCaptureRequest: {
	schema_version: #ConnectorSDKVersion
	connector_id:   string & !=""
	session_id:     string & !=""
	// Which capture channel the connector reports through (A–E); empty defaults
	// to "A", the hook/plugin structured-event channel.
	channel?: #Channel
	// Client-generated idempotency key. A replay after a lost acknowledgement
	// reuses it, so the core answers with the already stored identity instead
	// of counting the observation twice.
	observation_id?: string & !=""
	observation:     #Observation
}

// #ConnectorCaptureResponse mirrors the record result of the core API
// (issue identity and whether this call created the issue).
#ConnectorCaptureResponse: {
	schema_version: #ConnectorSDKVersion
	event_id:       string
	occurrence_id:  string
	issue_id:       string
	fingerprint:    #Fingerprint
	created:        bool
	queued?:        bool
	// A queued observation only knows its event and fingerprint; the replay
	// assigns the occurrence and issue identities.
	if !queued {
		occurrence_id: string & !=""
		issue_id:      string & !=""
	}
}

// #ConnectorLookupRequest asks the core for known fixes. At least one selector
// (issue_id, fingerprint, error_code or normalized_message) must be present;
// the core rejects an empty query rather than returning a silent miss.
#ConnectorLookupRequest: {
	schema_version:      #ConnectorSDKVersion
	connector_id:        string & !=""
	session_id?:         string
	project_key?:        #NoSensitive
	issue_id?:           string
	fingerprint?:        #Fingerprint
	error_code?:         #NoSensitive
	normalized_message?: #NoSensitive
}

// #ConnectorLookupResponse carries the same three-candidate ceiling as the MCP
// contract and says explicitly when the result is degraded (no trigram, no
// candidates, database unavailable) instead of pretending an empty list is a
// definite "no known fix".
#ConnectorLookupResponse: {
	schema_version: #ConnectorSDKVersion
	candidates: [...#Candidate] & list.MaxItems(3)
	degraded: bool
}

// #NoticeKind is the request side: a host either reuses a candidate it already
// found ("hit") or reports a failure that has no known fix yet ("miss").
#NoticeKind: "hit" | "miss"

// #NoticeTrust is how much the host trusts a candidate it is about to reuse.
#NoticeTrust: "trusted" | "doubted" | "suppressed"

// #NoticeResultKind is the response side, mirroring the four notice forms the
// connector renders before diagnosis plus the doubted marker.
#NoticeResultKind: "hit" | "near" | "no-fix" | "miss" | "doubted"

// #ConnectorNoticeRequest asks for the one-line notice to show before the host
// starts diagnosing. The core owns wording, truncation and token accounting.
#ConnectorNoticeRequest: {
	schema_version: #ConnectorSDKVersion
	connector_id:   string & !=""
	session_id?:    string
	kind:           #NoticeKind
	// Present when kind is "hit".
	candidate?: #Candidate
	// Present when kind is "miss": the recorded issue id to mention.
	issue_id?: string
	trust?:    #NoticeTrust
}

#ConnectorNoticeResponse: {
	schema_version: #ConnectorSDKVersion
	kind:           #NoticeResultKind
	text:           #NoSensitive
	chars:          int & >=0
	tokens:         int & >=0
}

// #ConnectorFeedbackRequest records whether a known fix worked in a concrete
// environment. The adapter never computes trust; trust stays in errkb_trust().
#ConnectorFeedbackRequest: {
	schema_version:      #ConnectorSDKVersion
	connector_id:        string & !=""
	session_id?:         string
	issue_id:            string
	solution_version_id: string
	outcome:             #Outcome
	note?:               #NoSensitive
	environment?: {
		os?:        string
		shell?:     string
		connector?: string
		runtime_versions?: [string]: string
	}
}

#ConnectorFeedbackResponse: {
	schema_version: #ConnectorSDKVersion
	outcome_id:     string
	recorded:       bool
}
