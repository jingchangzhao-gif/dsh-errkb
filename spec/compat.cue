// Schema compatibility rules for the five errkb contracts.
//
// This file is executable, not documentation. The `compat` job in
// .github/workflows/ci.yml exports it from the merge base and from HEAD,
// classifies the difference with the rule table below, and fails the build
// when a breaking or needs-version-bump change lands without a schema_version
// increase. The same job first runs the checker's self-test, so a rule that
// stops firing is itself a failure.
//
// The three change classes
//   compatible          - an added optional field or a new enum value; both
//                         directions keep old writers valid, so no version bump.
//   breaking            - removing or renaming a field, changing a field
//                         between required and optional, shrinking an enum,
//                         changing the unknown-field policy, moving a contract.
//                         Requires a schema_version increase (rule R9) and a
//                         migration or read path on the consuming side.
//   needs-version-bump  - the shape is unchanged but the meaning changed
//                         (semantics text, decoder binding, version anchor).
//                         Same bytes, different interpretation: bump first.
//
// Unknown-field policy
//   Every v1 decoder is strict: internal/contract/error_event.go and
//   internal/contract/local_api.go call DisallowUnknownFields, and
//   internal/contract/config.go calls KnownFields(true). The declared policy
//   below therefore matches what the code does. Persisted ErrorEvent rows are
//   read back through version-specific readers during database upgrades
//   (EKB-053); that tolerant read path is deliberately not declared here,
//   because it is not the v1 wire decoder.
//
// Version anchors
//   error_event           #SchemaVersion in spec/error-event.cue
//   local_api             #EnvelopeVersion in spec/local-api.cue
//   mcp_tools             schema_version on every request and response payload
//   config                no on-wire version; this manifest owns its version
//   connector_capability  no on-wire version; this manifest owns its version
//
// How to change a contract
//   1. Edit the CUE file and, in the same commit, the matching entry in
//      `contracts` (fields, required, enums). The R12 check fails on drift.
//   2. Classify the change against the rules. A breaking or meaning change
//      must also raise schema_version from vN to a strictly greater vN.
//   3. The compatibility job proves the classification on the real diff.
//
// Contracts are declared per file; every spec/*.cue except this file must
// appear here (R14), so no schema can change outside the review.
package spec

compat: {
	rules: [
		{id: "R1", change_class: "compatible", verdict: "allow", why: "An added optional field keeps every existing writer valid; old readers ignore it and new readers may use it."},
		{id: "R2", change_class: "breaking", verdict: "fail", why: "A new required field invalidates every existing producer, so the version must move and a read path for older writers must exist."},
		{id: "R3", change_class: "breaking", verdict: "fail", why: "Removing a field destroys information that old producers still send and old rows still store."},
		{id: "R4", change_class: "breaking", verdict: "fail", why: "Changing a field between required and optional changes what is valid; either direction breaks one side."},
		{id: "R5", change_class: "compatible", verdict: "allow", why: "A larger enum rejects fewer values; no existing writer becomes invalid."},
		{id: "R6", change_class: "breaking", verdict: "fail", why: "A smaller enum, or a removed enum set, rejects values that used to be valid."},
		{id: "R7", change_class: "needs-version-bump", verdict: "fail without a version bump", why: "Meaning changed while the shape stayed: the same bytes are read differently, so the version must move even though the diff looks harmless."},
		{id: "R8", change_class: "breaking", verdict: "fail", why: "The unknown-field policy is part of the wire contract; changing it changes which payloads are accepted."},
		{id: "R9", change_class: "breaking", verdict: "fail", why: "Any breaking or needs-version-bump change must raise schema_version from vN to a strictly greater vN."},
		{id: "R10", change_class: "compatible", verdict: "allow", why: "A new contract has no old producers to break."},
		{id: "R11", change_class: "static", verdict: "fail", why: "Removing a contract strands every client that still speaks it; this needs an owner decision, not a silent schema gate."},
		{id: "R12", change_class: "static", verdict: "fail", why: "The manifest is the baseline: if it disagrees with the CUE definitions, later diffs would measure the wrong surface."},
		{id: "R13", change_class: "static", verdict: "fail", why: "A declared unknown-field policy that its decoder does not implement is a false statement in the contract."},
		{id: "R14", change_class: "static", verdict: "fail", why: "A spec file outside the manifest could change without any compatibility review."},
		{id: "R15", change_class: "static", verdict: "fail", why: "An unexercised rule is a rule that can rot; every declared rule must have a self-test case that fires."},
		{id: "R16", change_class: "breaking", verdict: "fail", why: "Moving or renaming a contract changes where clients find it, so it is a breaking change."},
		{id: "R17", change_class: "compatible", verdict: "allow", why: "Adding a definition that only optional fields reference (for example a nested solution object) accepts more payloads and invalidates no existing writer."},
	]
	contracts: {
		error_event: {
			file: "spec/error-event.cue"
			definitions: ["ErrorEvent"]
			schema_version: "v1"
			semantics:      "One redacted ErrorEvent v1 observation: identity, capture context, sanitized text and the fingerprint. Issues, solutions and hit counts are separate entities and never appear on the event."
			unknown_fields: "reject"
			decoder: {
				file:   "internal/contract/error_event.go"
				marker: "DisallowUnknownFields"
			}
			version_anchor: "#SchemaVersion"
			fields: [
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
				"error_code",
				"command",
				"exit_code",
				"tool_name",
				"os",
				"shell",
				"runtime_versions",
				"project_key",
				"fingerprint",
				"evidence",
				"confidence",
			]
			required: [
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
			]
			enums: {
				channel: ["A", "B", "C", "D", "E"]
				category: ["tool", "command-exit", "llm", "agent"]
			}
		}
		config: {
			file: "spec/config.cue"
			definitions: ["Config"]
			schema_version: "v1"
			semantics:      "User-editable daemon configuration, written as YAML or JSON: database connection, local service channels, capture pause filters, notice policy, retention periods, redaction extensions, search limit, timeouts and the offline spool. It has no on-wire version field; this manifest owns its version."
			unknown_fields: "reject"
			decoder: {
				file:   "internal/contract/config.go"
				marker: "KnownFields(true)"
			}
			fields: [
				"database",
				"database.dsn",
				"database.max_connections",
				"database.connect_timeout_ms",
				"service",
				"service.pipe",
				"service.pipe.name",
				"service.pipe.enabled",
				"service.http",
				"service.http.enabled",
				"service.http.host",
				"service.http.port",
				"service.http.token",
				"capture",
				"capture.enabled",
				"capture.paused_connectors",
				"capture.paused_projects",
				"notices",
				"notices.before_diagnosis",
				"retention",
				"retention.sample_days",
				"retention.normalized_days",
				"retention.audit_days",
				"redaction",
				"redaction.extra_patterns",
				"search",
				"search.limit",
				"timeout",
				"timeout.capture_ms",
				"timeout.search_ms",
				"timeout.mcp_ms",
				"offline",
				"offline.spool_dir",
				"offline.replay_interval_ms",
			]
			required: [
				"database",
				"database.dsn",
				"service",
				"service.pipe",
				"service.pipe.enabled",
				"service.http",
				"service.http.enabled",
				"capture",
				"capture.enabled",
				"notices",
				"notices.before_diagnosis",
				"retention",
				"retention.sample_days",
				"retention.normalized_days",
				"retention.audit_days",
				"redaction",
				"search",
				"search.limit",
				"timeout",
				"timeout.capture_ms",
				"timeout.search_ms",
				"timeout.mcp_ms",
				"offline",
				"offline.replay_interval_ms",
			]
		}
		connector: {
			file: "spec/connector.cue"
			definitions: ["Observation", "ConnectorCaptureRequest", "ConnectorCaptureResponse", "ConnectorLookupRequest", "ConnectorLookupResponse", "ConnectorNoticeRequest", "ConnectorNoticeResponse", "ConnectorFeedbackRequest", "ConnectorFeedbackResponse"]
			schema_version: "v1"
			semantics:      "The four connector SDK actions (capture, lookup, notice, feedback) a host adapter may take against the core: versioned envelopes, a raw observation before the core classifies and redacts it, at most three reason-carrying candidates, explicit degraded flags and an outcome that never computes trust. The capability declaration and its probe result stay #ConnectorCapability in connector-capability.cue and are not repeated here."
			unknown_fields: "tolerate"
			decoder: {
				file:   "internal/capture/connector.go"
				marker: "DisallowUnknownFields"
			}
			version_anchor: "#ConnectorSDKVersion"
			fields: [
				"ConnectorCaptureRequest.channel",
				"ConnectorCaptureRequest.connector_id",
				"ConnectorCaptureRequest.observation",
				"ConnectorCaptureRequest.observation_id",
				"ConnectorCaptureRequest.schema_version",
				"ConnectorCaptureRequest.session_id",
				"ConnectorCaptureResponse.created",
				"ConnectorCaptureResponse.event_id",
				"ConnectorCaptureResponse.fingerprint",
				"ConnectorCaptureResponse.issue_id",
				"ConnectorCaptureResponse.occurrence_id",
				"ConnectorCaptureResponse.schema_version",
				"ConnectorFeedbackRequest.connector_id",
				"ConnectorFeedbackRequest.environment",
				"ConnectorFeedbackRequest.environment.connector",
				"ConnectorFeedbackRequest.environment.os",
				"ConnectorFeedbackRequest.environment.runtime_versions",
				"ConnectorFeedbackRequest.environment.shell",
				"ConnectorFeedbackRequest.issue_id",
				"ConnectorFeedbackRequest.note",
				"ConnectorFeedbackRequest.outcome",
				"ConnectorFeedbackRequest.schema_version",
				"ConnectorFeedbackRequest.session_id",
				"ConnectorFeedbackRequest.solution_version_id",
				"ConnectorFeedbackResponse.outcome_id",
				"ConnectorFeedbackResponse.recorded",
				"ConnectorFeedbackResponse.schema_version",
				"ConnectorLookupRequest.connector_id",
				"ConnectorLookupRequest.error_code",
				"ConnectorLookupRequest.fingerprint",
				"ConnectorLookupRequest.issue_id",
				"ConnectorLookupRequest.normalized_message",
				"ConnectorLookupRequest.project_key",
				"ConnectorLookupRequest.schema_version",
				"ConnectorLookupRequest.session_id",
				"ConnectorLookupResponse.candidates",
				"ConnectorLookupResponse.degraded",
				"ConnectorLookupResponse.schema_version",
				"ConnectorNoticeRequest.candidate",
				"ConnectorNoticeRequest.connector_id",
				"ConnectorNoticeRequest.issue_id",
				"ConnectorNoticeRequest.kind",
				"ConnectorNoticeRequest.schema_version",
				"ConnectorNoticeRequest.session_id",
				"ConnectorNoticeRequest.trust",
				"ConnectorNoticeResponse.chars",
				"ConnectorNoticeResponse.kind",
				"ConnectorNoticeResponse.schema_version",
				"ConnectorNoticeResponse.text",
				"ConnectorNoticeResponse.tokens",
				"Observation.command",
				"Observation.confidence",
				"Observation.error_code",
				"Observation.evidence",
				"Observation.exit_code",
				"Observation.message",
				"Observation.observed_at",
				"Observation.os",
				"Observation.project_key",
				"Observation.runtime_versions",
				"Observation.shell",
				"Observation.source",
				"Observation.tool_name",
			]
			required: [
				"ConnectorCaptureRequest.connector_id",
				"ConnectorCaptureRequest.observation",
				"ConnectorCaptureRequest.schema_version",
				"ConnectorCaptureRequest.session_id",
				"ConnectorCaptureResponse.created",
				"ConnectorCaptureResponse.event_id",
				"ConnectorCaptureResponse.fingerprint",
				"ConnectorCaptureResponse.issue_id",
				"ConnectorCaptureResponse.occurrence_id",
				"ConnectorCaptureResponse.schema_version",
				"ConnectorFeedbackRequest.connector_id",
				"ConnectorFeedbackRequest.issue_id",
				"ConnectorFeedbackRequest.outcome",
				"ConnectorFeedbackRequest.schema_version",
				"ConnectorFeedbackRequest.solution_version_id",
				"ConnectorFeedbackResponse.outcome_id",
				"ConnectorFeedbackResponse.recorded",
				"ConnectorFeedbackResponse.schema_version",
				"ConnectorLookupRequest.connector_id",
				"ConnectorLookupRequest.schema_version",
				"ConnectorLookupResponse.candidates",
				"ConnectorLookupResponse.degraded",
				"ConnectorLookupResponse.schema_version",
				"ConnectorNoticeRequest.connector_id",
				"ConnectorNoticeRequest.kind",
				"ConnectorNoticeRequest.schema_version",
				"ConnectorNoticeResponse.chars",
				"ConnectorNoticeResponse.kind",
				"ConnectorNoticeResponse.schema_version",
				"ConnectorNoticeResponse.text",
				"ConnectorNoticeResponse.tokens",
				"Observation.message",
				"Observation.observed_at",
				"Observation.source",
			]
			enums: {
				notice_kind: ["hit", "miss"]
				notice_trust: ["trusted", "doubted", "suppressed"]
				notice_result_kind: ["hit", "near", "no-fix", "miss", "doubted"]
			}
		}
		connector_capability: {
			file: "spec/connector-capability.cue"
			definitions: ["ConnectorCapability"]
			schema_version: "v1"
			semantics:      "What one host can capture at its reliability channel, whether it can receive a known fix before diagnosing, and the explicit fallback when the best channel is unavailable. It has no on-wire version field; this manifest owns its version."
			unknown_fields: "reject"
			decoder: {
				file:   "internal/contract/config.go"
				marker: "KnownFields(true)"
			}
			fields: [
				"connector",
				"host_version",
				"channel",
				"captures",
				"notice_before_diagnosis",
				"fallback",
				"fallback.channel",
				"fallback.missing",
				"fallback.note",
				"probe",
				"probe.probed_at",
				"probe.host_version",
				"probe.ok",
				"probe.missing",
			]
			required: [
				"connector",
				"host_version",
				"channel",
				"captures",
				"notice_before_diagnosis",
				"fallback",
				"fallback.channel",
				"fallback.missing",
				"probe",
				"probe.probed_at",
				"probe.host_version",
				"probe.ok",
				"probe.missing",
			]
			enums: {
				channel: ["A", "B", "C", "D", "E"]
				event_field: ["schema_version", "event_id", "connector_id", "session_id", "observed_at", "channel", "category", "severity", "transient", "headline", "normalized_message", "redacted_sample", "error_code", "command", "exit_code", "tool_name", "os", "shell", "runtime_versions", "project_key", "fingerprint", "evidence", "confidence"]
			}
		}
		mcp_tools: {
			file: "spec/mcp-tools.cue"
			definitions: ["LookupRequest", "LookupResponse", "SearchRequest", "SearchResponse", "RecordRequest", "RecordResponse", "FeedbackRequest", "FeedbackResponse", "ListRequest", "ListResponse", "StatsRequest", "StatsResponse", "Candidate", "Solution", "ListItem", "Tool"]
			schema_version: "v1"
			semantics:      "The six MCP operations (lookup, search, record, feedback, list, stats) with their request and response payloads, the reason-carrying candidate shape and the three-candidate ceiling. Each payload carries its own schema_version."
			unknown_fields: "reject"
			decoder: {
				file:   "internal/contract/local_api.go"
				marker: "DisallowUnknownFields"
			}
			version_anchor: "per_operation"
			fields: [
				"LookupRequest.schema_version",
				"LookupRequest.issue",
				"LookupRequest.error_code",
				"LookupRequest.fingerprint",
				"LookupRequest.normalized_message",
				"LookupRequest.project_key",
				"LookupResponse.schema_version",
				"LookupResponse.candidates",
				"SearchRequest.schema_version",
				"SearchRequest.query",
				"SearchRequest.project_key",
				"SearchRequest.limit",
				"SearchResponse.schema_version",
				"SearchResponse.candidates",
				"RecordRequest.schema_version",
				"RecordRequest.event",
				"RecordResponse.schema_version",
				"RecordResponse.event_id",
				"RecordResponse.occurrence_id",
				"RecordResponse.issue_id",
				"RecordResponse.fingerprint",
				"RecordResponse.created",
				"FeedbackRequest.schema_version",
				"FeedbackRequest.issue_id",
				"FeedbackRequest.solution_version_id",
				"FeedbackRequest.outcome",
				"FeedbackRequest.note",
				"FeedbackResponse.schema_version",
				"FeedbackResponse.outcome_id",
				"FeedbackResponse.recorded",
				"ListRequest.schema_version",
				"ListRequest.kind",
				"ListRequest.project_key",
				"ListRequest.limit",
				"ListRequest.cursor",
				"ListResponse.schema_version",
				"ListResponse.items",
				"ListResponse.next_cursor",
				"StatsRequest.schema_version",
				"StatsRequest.project_key",
				"StatsResponse.schema_version",
				"StatsResponse.totals",
				"StatsResponse.totals.events",
				"StatsResponse.totals.issues",
				"StatsResponse.totals.solutions",
				"StatsResponse.totals.outcomes",
				"StatsResponse.retrieval",
				"StatsResponse.retrieval.p50_ms",
				"StatsResponse.retrieval.p95_ms",
				"StatsResponse.tokens",
				"StatsResponse.tokens.estimated_saved",
				"StatsResponse.tokens.measured_saved",
				"StatsResponse.tokens.measured_events",
				"Candidate.issue_id",
				"Candidate.fingerprint",
				"Candidate.title",
				"Candidate.match_reason",
				"Candidate.match_detail",
				"Candidate.approximate",
				"Candidate.confidence",
				"Candidate.solution",
				"Solution.solution_version_id",
				"Solution.body",
				"Solution.source",
				"Solution.applies_to",
				"Solution.trust",
				"ListItem.id",
				"ListItem.title",
				"ListItem.updated_at",
				"Tool.name",
				"Tool.description",
				"Tool.read_only",
				"Tool.input",
				"Tool.output",
			]
			required: [
				"LookupRequest.schema_version",
				"LookupResponse.schema_version",
				"LookupResponse.candidates",
				"SearchRequest.schema_version",
				"SearchRequest.query",
				"SearchResponse.schema_version",
				"SearchResponse.candidates",
				"RecordRequest.schema_version",
				"RecordRequest.event",
				"RecordResponse.schema_version",
				"RecordResponse.event_id",
				"RecordResponse.occurrence_id",
				"RecordResponse.issue_id",
				"RecordResponse.fingerprint",
				"RecordResponse.created",
				"FeedbackRequest.schema_version",
				"FeedbackRequest.issue_id",
				"FeedbackRequest.solution_version_id",
				"FeedbackRequest.outcome",
				"FeedbackResponse.schema_version",
				"FeedbackResponse.outcome_id",
				"FeedbackResponse.recorded",
				"ListRequest.schema_version",
				"ListRequest.kind",
				"ListResponse.schema_version",
				"ListResponse.items",
				"StatsRequest.schema_version",
				"StatsResponse.schema_version",
				"StatsResponse.totals",
				"StatsResponse.totals.events",
				"StatsResponse.totals.issues",
				"StatsResponse.totals.solutions",
				"StatsResponse.totals.outcomes",
				"StatsResponse.retrieval",
				"StatsResponse.retrieval.p50_ms",
				"StatsResponse.retrieval.p95_ms",
				"StatsResponse.tokens",
				"StatsResponse.tokens.estimated_saved",
				"StatsResponse.tokens.measured_saved",
				"StatsResponse.tokens.measured_events",
				"Candidate.issue_id",
				"Candidate.fingerprint",
				"Candidate.title",
				"Candidate.match_reason",
				"Candidate.match_detail",
				"Candidate.approximate",
				"Candidate.confidence",
				"Solution.solution_version_id",
				"Solution.body",
				"Solution.source",
				"Solution.applies_to",
				"Solution.trust",
				"ListItem.id",
				"ListItem.title",
				"ListItem.updated_at",
				"Tool.name",
				"Tool.description",
				"Tool.read_only",
				"Tool.input",
				"Tool.output",
			]
			enums: {
				match_reason: ["issue_id", "error_code", "exact_fingerprint", "deterministic_filter", "trigram_candidate", "jaccard_rerank"]
				outcome: ["success", "failure", "unknown"]
				list_kind: ["issue", "solution"]
			}
		}
		local_api: {
			file: "spec/local-api.cue"
			definitions: ["Request", "Response", "LocalError"]
			schema_version: "v1"
			semantics:      "One newline-delimited JSON envelope on the local named pipe: a versioned envelope with request id, operation and exactly one of result and error; payloads reuse the MCP tool definitions verbatim."
			unknown_fields: "reject"
			decoder: {
				file:   "internal/contract/local_api.go"
				marker: "DisallowUnknownFields"
			}
			version_anchor: "#EnvelopeVersion"
			fields: [
				"Request.schema_version",
				"Request.request_id",
				"Request.operation",
				"Request.payload",
				"Response.schema_version",
				"Response.request_id",
				"Response.operation",
				"Response.result",
				"Response.error",
				"LocalError.code",
				"LocalError.message",
				"LocalError.retryable",
			]
			required: [
				"Request.schema_version",
				"Request.request_id",
				"Request.operation",
				"Request.payload",
				"Response.schema_version",
				"Response.request_id",
				"Response.operation",
				"LocalError.code",
				"LocalError.message",
				"LocalError.retryable",
			]
			enums: {
				operation: ["lookup", "search", "record", "feedback", "list", "stats", "capture"]
				error_code: ["invalid_request", "not_found", "unavailable", "conflict", "internal"]
			}
		}
		redact: {
			file: "spec/redact.cue"
			definitions: ["Redact"]
			schema_version: "v1"
			semantics:      "The redaction rules document: every credential and personal-data family, its replacement placeholder, the public and private policies, the home-prefix handling and the privacy-guard alternatives. It is a build-time document: the generated Go and TypeScript artifacts are what run, and the conformance corpus proves they agree."
			unknown_fields: "tolerate"
			decoder: {
				file:   "internal/redact/rules_gen.go"
				marker: "DisallowUnknownFields"
			}
			version_anchor: "#RedactVersion"
			fields: [
				"schema_version",
				"placeholders",
				"rules",
				"home_prefix",
				"path_guards",
			]
			required: [
				"schema_version",
				"placeholders",
				"rules",
				"home_prefix",
				"path_guards",
			]
		}
	}
}
