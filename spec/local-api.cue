// The local Named Pipe API envelope (EKB-008). Payloads reuse the MCP
// definitions from spec/mcp-tools.cue by reference: same field names, same
// validation, two envelopes. The two channels cannot drift because there is
// only one copy of each payload shape.
package spec

// #EnvelopeVersion versions the envelope itself, separately from the six
// operation versions carried inside every payload.
#EnvelopeVersion: "v1"

#Operation: "lookup" | "search" | "record" | "feedback" | "list" | "stats"

// #ToolRequest and #ToolResponse are unions of the MCP payload definitions.
#ToolRequest:
	#LookupRequest | #SearchRequest | #RecordRequest |
		#FeedbackRequest | #ListRequest | #StatsRequest

#ToolResponse:
	#LookupResponse | #SearchResponse | #RecordResponse |
		#FeedbackResponse | #ListResponse | #StatsResponse

#ErrorCode:
	"invalid_request" | "not_found" | "unavailable" | "conflict" | "internal"

#LocalError: {
	code:      #ErrorCode
	message:   string & !=""
	retryable: bool
}

// #Request is one newline-delimited JSON line on the pipe.
#Request: {
	schema_version: #EnvelopeVersion
	request_id:     string & !=""
	operation:      #Operation
	payload:        #ToolRequest
}

// #Response carries exactly one of result and error. CUE keeps both optional
// because a definition cannot express "exactly one"; internal/contract/
// local_api.go enforces it when decoding.
#Response: {
	schema_version: #EnvelopeVersion
	request_id:     string & !=""
	operation:      #Operation
	result?:        #ToolResponse
	error?:         #LocalError
}
