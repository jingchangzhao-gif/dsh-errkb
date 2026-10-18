package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestRerankExactShortCircuit(t *testing.T) {
	query := RerankQuery{
		Fingerprint:       "0123456789ab",
		Code:              "ENOENT",
		NormalizedMessage: "enoent: no such file or directory, open '<path>/package.json'",
		Command:           "pnpm build",
		Environment:       Environment{OS: "windows", Shell: "pwsh", Connector: "dsh"},
	}
	candidates := []RerankCandidate{
		{
			IssueID:           "E-2",
			Fingerprint:       "ffffffffffff",
			Code:              "ENOENT",
			NormalizedMessage: "enoent: no such file or directory, open '<path>/package.json'",
			Command:           "pnpm build",
			Environment:       Environment{OS: "windows", Shell: "pwsh", Connector: "dsh"},
		},
		{
			IssueID:           "E-1",
			Fingerprint:       "0123456789ab",
			Code:              "ENOENT",
			NormalizedMessage: "enoent: no such file or directory, open '<path>/package.json'",
			Command:           "pnpm build",
			Environment:       Environment{OS: "windows", Shell: "pwsh", Connector: "dsh"},
		},
	}

	results := Rerank(query, candidates)
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	if results[0].IssueID != "E-1" {
		t.Errorf("exact candidate is not first: %q", results[0].IssueID)
	}
	if results[0].Approximate {
		t.Error("exact fingerprint hit marked approximate")
	}
	if results[0].Score != 1 {
		t.Errorf("exact hit score = %v, want 1", results[0].Score)
	}
	if !containsField(results[0].MatchedFields, MatchFieldFingerprint) {
		t.Errorf("exact hit matched fields = %v, want fingerprint", results[0].MatchedFields)
	}
	if !results[1].Approximate {
		t.Error("non-exact candidate not marked approximate")
	}
}

// TestRerankApproximateOnlyExact is the central assertion: only a fingerprint
// hit that the other fields do not contradict is exact; every other result is
// approximate, including a fingerprint hit whose command differs.
func TestRerankApproximateOnlyExact(t *testing.T) {
	base := RerankCandidate{
		IssueID:           "E-1",
		Fingerprint:       "0123456789ab",
		Code:              "ENOENT",
		NormalizedMessage: "enoent: no such file or directory",
		Command:           "pnpm build",
		ToolName:          "shell",
		Environment:       Environment{OS: "windows"},
	}

	tests := []struct {
		name        string
		query       RerankQuery
		candidate   RerankCandidate
		approximate bool
	}{
		{
			name: "exact fingerprint",
			query: RerankQuery{Fingerprint: "0123456789ab", Code: "ENOENT", Command: "pnpm build", ToolName: "shell",
				NormalizedMessage: base.NormalizedMessage},
			candidate:   base,
			approximate: false,
		},
		{
			name: "different fingerprint",
			query: RerankQuery{Fingerprint: "ffffffffffff", Code: "ENOENT", Command: "pnpm build", ToolName: "shell",
				NormalizedMessage: base.NormalizedMessage},
			candidate:   base,
			approximate: true,
		},
		{
			name: "fingerprint hit with a different command",
			query: RerankQuery{Fingerprint: "0123456789ab", Code: "ENOENT", Command: "tsc --build", ToolName: "shell",
				NormalizedMessage: base.NormalizedMessage},
			candidate:   base,
			approximate: true,
		},
		{
			name: "fingerprint hit with a different error code",
			query: RerankQuery{Fingerprint: "0123456789ab", Code: "EACCES", Command: "pnpm build", ToolName: "shell",
				NormalizedMessage: base.NormalizedMessage},
			candidate:   base,
			approximate: true,
		},
		{
			name: "fingerprint hit with a different tool",
			query: RerankQuery{Fingerprint: "0123456789ab", Code: "ENOENT", Command: "pnpm build", ToolName: "mcp",
				NormalizedMessage: base.NormalizedMessage},
			candidate:   base,
			approximate: true,
		},
		{
			name: "empty fingerprint never exact",
			query: RerankQuery{Code: "ENOENT", Command: "pnpm build", ToolName: "shell",
				NormalizedMessage: base.NormalizedMessage},
			candidate:   base,
			approximate: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			results := Rerank(test.query, []RerankCandidate{test.candidate})
			if len(results) != 1 {
				t.Fatalf("got %d results, want 1", len(results))
			}
			if results[0].Approximate != test.approximate {
				t.Errorf("Approximate = %v, want %v", results[0].Approximate, test.approximate)
			}
		})
	}
}

func TestRerankEnvironmentDifference(t *testing.T) {
	query := RerankQuery{
		NormalizedMessage: "eperm: operation not permitted",
		Environment: Environment{
			OS:              "windows",
			Shell:           "pwsh",
			Connector:       "dsh",
			RuntimeVersions: map[string]string{"go": "1.26.4", "node": "24.16.0"},
		},
	}
	candidate := RerankCandidate{
		IssueID:           "E-1",
		NormalizedMessage: "eperm: operation not permitted",
		Environment: Environment{
			OS:              "linux",
			Shell:           "bash",
			Connector:       "dsh",
			RuntimeVersions: map[string]string{"go": "1.26.4", "node": "20.11.0"},
		},
	}

	results := Rerank(query, []RerankCandidate{candidate})
	if len(results) != 1 {
		t.Fatalf("got %d results, want 1", len(results))
	}
	diffs := results[0].EnvironmentDiff
	fields := map[string]bool{}
	for _, diff := range diffs {
		fields[diff.Field] = true
	}
	for _, want := range []string{"os", "shell", "runtime_versions.node"} {
		if !fields[want] {
			t.Errorf("environment diff misses %q: %+v", want, diffs)
		}
	}
	if fields["connector"] {
		t.Errorf("connector agrees but is reported as a difference: %+v", diffs)
	}
	if !containsField(results[0].MatchedFields, MatchFieldEnvironment) {
		t.Errorf("matched fields = %v, want environment", results[0].MatchedFields)
	}
}

func TestRerankTopThree(t *testing.T) {
	query := RerankQuery{NormalizedMessage: "eperm: operation not permitted"}
	candidates := make([]RerankCandidate, 0, 5)
	for _, id := range []string{"E-1", "E-2", "E-3", "E-4", "E-5"} {
		candidates = append(candidates, RerankCandidate{IssueID: id, NormalizedMessage: "eperm: operation not permitted"})
	}
	results := Rerank(query, candidates)
	if len(results) != maxCandidates {
		t.Fatalf("got %d results, want %d", len(results), maxCandidates)
	}
}

func TestRerankTieBreak(t *testing.T) {
	query := RerankQuery{NormalizedMessage: "mystery"}
	candidates := []RerankCandidate{
		{IssueID: "E-3", NormalizedMessage: "alpha"},
		{IssueID: "E-1", NormalizedMessage: "beta"},
		{IssueID: "E-2", NormalizedMessage: "gamma"},
	}

	results := Rerank(query, candidates)
	want := []string{"E-1", "E-2", "E-3"}
	got := make([]string, 0, len(results))
	for _, result := range results {
		got = append(got, result.IssueID)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}

	// Input order must not matter.
	reversed := []RerankCandidate{candidates[2], candidates[1], candidates[0]}
	second := Rerank(query, reversed)
	for i := range results {
		if results[i].IssueID != second[i].IssueID || results[i].Score != second[i].Score {
			t.Fatalf("call %d differs: %+v vs %+v", i, results[i], second[i])
		}
	}
}

func TestRerankNoMergeVerdict(t *testing.T) {
	allowed := map[string]bool{
		"IssueID":         true,
		"Score":           true,
		"MatchedFields":   true,
		"Approximate":     true,
		"EnvironmentDiff": true,
	}
	typ := reflect.TypeOf(RerankResult{})
	for i := 0; i < typ.NumField(); i++ {
		name := typ.Field(i).Name
		if !allowed[name] {
			t.Errorf("unexpected field %q in RerankResult", name)
		}
	}
	if typ.NumField() != len(allowed) {
		t.Errorf("RerankResult has %d fields, want %d", typ.NumField(), len(allowed))
	}
}

// lookalikeCase mirrors the labelled corpus shape used by the smoke test.
type labelledPair struct {
	Category string `json:"category"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	Command  string `json:"command"`
	ToolName string `json:"tool_name"`
}

type labelledCase struct {
	CaseID string `json:"case_id"`
	Pair   struct {
		A labelledPair `json:"a"`
		B labelledPair `json:"b"`
	} `json:"pair"`
	Measured struct {
		FingerprintEqual bool `json:"fingerprint_equal"`
	} `json:"measured"`
}

// TestRerankLookalikesAreNeverExact is the corpus smoke test from the card:
// every distinct-lookalike pair, including the pairs that share a fingerprint
// because their messages are identical, must come back approximate.
func TestRerankLookalikesAreNeverExact(t *testing.T) {
	path := filepath.Join("..", "..", "spec", "corpus", "labelled", "distinct-lookalike.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read corpus: %v", err)
	}
	var corpus struct {
		Cases []labelledCase `json:"cases"`
	}
	if err := json.Unmarshal(data, &corpus); err != nil {
		t.Fatalf("parse corpus: %v", err)
	}
	if len(corpus.Cases) == 0 {
		t.Fatal("corpus has no cases")
	}

	exactPairs := 0
	for _, testCase := range corpus.Cases {
		queryPair, candidatePair := testCase.Pair.A, testCase.Pair.B
		query := RerankQuery{
			Fingerprint:       Signature(queryPair.Category, Normalize(queryPair.Message)),
			Code:              queryPair.Code,
			NormalizedMessage: Normalize(queryPair.Message),
			Command:           queryPair.Command,
			ToolName:          queryPair.ToolName,
		}
		candidate := RerankCandidate{
			IssueID:           testCase.CaseID,
			Fingerprint:       Signature(candidatePair.Category, Normalize(candidatePair.Message)),
			Code:              candidatePair.Code,
			NormalizedMessage: Normalize(candidatePair.Message),
			Command:           candidatePair.Command,
			ToolName:          candidatePair.ToolName,
		}

		if testCase.Measured.FingerprintEqual {
			exactPairs++
			if query.Fingerprint != candidate.Fingerprint {
				t.Fatalf("%s: corpus says fingerprints are equal but they are %q and %q", testCase.CaseID, query.Fingerprint, candidate.Fingerprint)
			}
		}

		results := Rerank(query, []RerankCandidate{candidate})
		if len(results) != 1 {
			t.Fatalf("%s: got %d results, want 1", testCase.CaseID, len(results))
		}
		if !results[0].Approximate {
			t.Errorf("%s (%s) was treated as an exact hit", testCase.CaseID, testCase.Pair.A.Command)
		}
	}
	if exactPairs == 0 {
		t.Fatal("corpus has no fingerprint-equal lookalike pairs; the contradiction check is untested")
	}
}

func containsField(fields []MatchField, want MatchField) bool {
	for _, field := range fields {
		if field == want {
			return true
		}
	}
	return false
}
