// The detail view contract (WF-005): one issue with its occurrence, solution
// and outcome history, plus the two solution writes the Web page needs. The
// payloads share the local API envelope; edits reuse the closed state machine
// and the confirmation gate that already exist in the daemon.
package spec

// #DetailViewVersion versions the detail view payloads, separately from the
// envelope and from the other contracts.
#DetailViewVersion: "v1"

#SolutionStatus: "candidate" | "verified" | "deprecated" | "blocked"

#TrustView: {
	score:     float64 & >=0 & <=1
	samples:   int & >=0
	successes: int & >=0
	failures:  int & >=0
	unknowns:  int & >=0
}

#OccurrenceView: {
	id:          string & !=""
	observed_at: string & !=""
	environment?: [string]: string
	headline?: string
}

#VersionView: {
	id:             string & !=""
	version:        int & >=1
	body:           #NoSensitive
	source?:        string
	applies_to?:    string
	risks?:         #NoSensitive
	preconditions?: #NoSensitive
	effective_at:   string & !=""
}

#SolutionView: {
	id:     string & !=""
	status: #SolutionStatus
	versions: [...#VersionView]
}

#OutcomeView: {
	id:          string & !=""
	outcome:     "success" | "failure" | "unknown"
	note?:       string
	observed_at: string & !=""
	environment?: [string]: string
}

#IssueDetailView: {
	id:           string & !=""
	fingerprint:  #Fingerprint
	title:        string & !=""
	category?:    string
	status:       string & !=""
	project_key?: string
	trigger?:     string
	hits:         int & >=0
	first_seen:   string & !=""
	last_seen:    string & !=""
	trust:        #TrustView
	occurrences: [...#OccurrenceView]
	solutions: [...#SolutionView]
	outcomes: [...#OutcomeView]
}

#GetIssueRequest: {
	schema_version: #DetailViewVersion
	issue_id:       string & !=""
}

#GetIssueResponse: {
	schema_version: #DetailViewVersion
	issue:          #IssueDetailView
}

#SetSolutionStatusRequest: {
	schema_version: #DetailViewVersion
	solution_id:    string & !=""
	status:         #SolutionStatus
	confirmed?:     bool
}

#SetSolutionStatusResponse: {
	schema_version: #DetailViewVersion
	solution_id:    string & !=""
	status:         #SolutionStatus
}

#DeleteSolutionRequest: {
	schema_version: #DetailViewVersion
	solution_id:    string & !=""
	confirmed?:     bool
}

#DeleteSolutionResponse: {
	schema_version: #DetailViewVersion
	solution_id:    string & !=""
	deleted:        bool
}
