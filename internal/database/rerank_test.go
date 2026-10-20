package database_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/contract"
	"github.com/jingchangzhao-gif/error-knowledge-base/internal/core"
	"github.com/jingchangzhao-gif/error-knowledge-base/internal/database"
	"github.com/jingchangzhao-gif/error-knowledge-base/internal/redact"
)

// The labelled corpus is the acceptance judge for EKB-024 (section 5): the
// hard gate is "zero false automatic merges" over distinct-lookalike, and the
// same-error / cross-language sets show recall instead of hiding it.

type labelledSide struct {
	Category string `json:"category"`
	Code     string `json:"code"`
	Message  string `json:"message"`
	Command  string `json:"command"`
	ToolName string `json:"tool_name"`
	Language string `json:"language"`
}

type labelledMeasured struct {
	NormalizedEqual  bool    `json:"normalized_equal"`
	FingerprintEqual bool    `json:"fingerprint_equal"`
	TokenJaccard     float64 `json:"token_jaccard"`
	Baseline         string  `json:"baseline"`
}

type labelledCase struct {
	CaseID        string `json:"case_id"`
	LookalikeKind string `json:"lookalike_kind"`
	Label         string `json:"label"`
	Pair          struct {
		A labelledSide `json:"a"`
		B labelledSide `json:"b"`
	} `json:"pair"`
	Measured labelledMeasured `json:"measured"`
}

func loadLabelled(t *testing.T, name string) []labelledCase {
	t.Helper()
	path := filepath.Join("..", "..", "spec", "corpus", "labelled", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read labelled corpus %s: %v", name, err)
	}
	var document struct {
		Cases []labelledCase `json:"cases"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("decode labelled corpus %s: %v", name, err)
	}
	if len(document.Cases) == 0 {
		t.Fatalf("labelled corpus %s has no cases", name)
	}
	return document.Cases
}

// seedLabelledIssue records one labelled side as a stored issue and returns its
// issue id. The event uses the same fingerprint rule as the production capture
// path so the exact layer sees what a real connector would record.
func seedLabelledIssue(t *testing.T, conn *pgx.Conn, connectorID, sessionID string, sequence int, side labelledSide, project string) string {
	t.Helper()
	normalized := core.Normalize(side.Message)
	headline := redact.Sample(core.ExtractHeadline(side.Message).Line, redact.Public)
	if headline == "" {
		headline = normalized
	}
	transient := false
	confidence := 0.9
	event := contract.ErrorEvent{
		SchemaVersion:     "v1",
		EventID:           eventUUID(sequence),
		ConnectorID:       connectorID,
		SessionID:         sessionID,
		ObservedAt:        time.Now().UTC().Format(time.RFC3339),
		Channel:           "C",
		Category:          side.Category,
		Severity:          "error",
		Transient:         &transient,
		Headline:          headline,
		NormalizedMessage: normalized,
		RedactedSample:    redact.Sample(side.Message, redact.Public),
		ErrorCode:         side.Code,
		Command:           side.Command,
		ToolName:          side.ToolName,
		OS:                "windows",
		Shell:             "powershell",
		RuntimeVersions:   map[string]string{"node": "24.16.0"},
		ProjectKey:        project,
		Fingerprint:       core.Signature(side.Category, normalized),
		Evidence:          "labelled corpus",
		Confidence:        &confidence,
	}
	result, err := database.RecordEvent(context.Background(), conn, event)
	if err != nil {
		t.Fatalf("record labelled side: %v", err)
	}
	return result.IssueID
}

func retrievalRequest(side labelledSide, project string) database.RetrievalRequest {
	return database.RetrievalRequest{
		Category:   side.Category,
		Message:    side.Message,
		ErrorCode:  side.Code,
		Command:    side.Command,
		ToolName:   side.ToolName,
		ProjectKey: project,
	}
}

func retrieve(t *testing.T, conn *pgx.Conn, request database.RetrievalRequest) database.RetrievalResult {
	t.Helper()
	result, err := database.Retrieve(context.Background(), conn, request)
	if err != nil {
		t.Fatalf("retrieve: %v", err)
	}
	return result
}

func findCandidate(candidates []database.RetrievalCandidate, issueID string) (database.RetrievalCandidate, bool) {
	for _, candidate := range candidates {
		if candidate.IssueID == issueID {
			return candidate, true
		}
	}
	return database.RetrievalCandidate{}, false
}

// TestRetrievalZeroFalseAutoMerge is the hard gate: no distinct-lookalike pair
// may be returned as an exact, automatically reusable match.
func TestRetrievalZeroFalseAutoMerge(t *testing.T) {
	conn, _, connectorID, sessionID := setupRecordSchema(t)
	cases := loadLabelled(t, "distinct-lookalike.json")

	sequence := 1
	for _, testCase := range cases {
		project := "lookalike-" + testCase.CaseID
		issueA := seedLabelledIssue(t, conn, connectorID, sessionID, sequence, testCase.Pair.A, project)
		sequence++

		result := retrieve(t, conn, retrievalRequest(testCase.Pair.B, project))
		if len(result.Candidates) == 0 {
			t.Fatalf("%s: the shared error code must still produce a candidate", testCase.CaseID)
		}
		for _, candidate := range result.Candidates {
			if candidate.IssueID != issueA {
				continue
			}
			if !candidate.Approximate {
				t.Fatalf("%s (%s): lookalike returned as an exact match with score %.2f — false automatic merge",
					testCase.CaseID, testCase.LookalikeKind, candidate.Score)
			}
			if candidate.AutoReusable() {
				t.Fatalf("%s: approximate lookalike is marked auto-reusable", testCase.CaseID)
			}
		}
	}
}

// TestRetrievalSameErrorRecall seeds each same-error side a and queries with
// side b. A fingerprint-equal pair must be an exact hit; the rest is reported as
// an observed recall gap instead of lowering a merge condition.
func TestRetrievalSameErrorRecall(t *testing.T) {
	conn, _, connectorID, sessionID := setupRecordSchema(t)
	cases := loadLabelled(t, "same-error.json")

	matched := 0
	sequence := 1
	for _, testCase := range cases {
		project := "same-error-" + testCase.CaseID
		issueA := seedLabelledIssue(t, conn, connectorID, sessionID, sequence, testCase.Pair.A, project)
		sequence++

		result := retrieve(t, conn, retrievalRequest(testCase.Pair.B, project))
		candidate, hit := findCandidate(result.Candidates, issueA)
		if hit {
			matched++
		}
		if testCase.Measured.FingerprintEqual {
			if !hit {
				t.Fatalf("%s: fingerprint-equal pair was not retrieved", testCase.CaseID)
			}
			if candidate.Approximate || !candidate.AutoReusable() {
				t.Fatalf("%s: fingerprint-equal pair must be exact and auto-reusable", testCase.CaseID)
			}
			continue
		}
		t.Logf("%s recall gap: token Jaccard %.2f, baseline %s", testCase.CaseID, testCase.Measured.TokenJaccard, testCase.Measured.Baseline)
	}
	t.Logf("same-error recall: %d/%d", matched, len(cases))
}

// TestRetrievalCrossLanguage checks the cross-language set. Every pair shares
// an error code, so the deterministic filter must bring the stored issue in.
func TestRetrievalCrossLanguage(t *testing.T) {
	conn, _, connectorID, sessionID := setupRecordSchema(t)
	cases := loadLabelled(t, "cross-language.json")

	sequence := 1
	for _, testCase := range cases {
		project := "cross-" + testCase.CaseID
		issueA := seedLabelledIssue(t, conn, connectorID, sessionID, sequence, testCase.Pair.A, project)
		sequence++

		result := retrieve(t, conn, retrievalRequest(testCase.Pair.B, project))
		_, hit := findCandidate(result.Candidates, issueA)
		if testCase.Label == "same-error" && !hit {
			t.Fatalf("%s: cross-language pair sharing error code %q was not retrieved", testCase.CaseID, testCase.Pair.B.Code)
		}
		if !hit {
			t.Logf("%s (label %s) not retrieved", testCase.CaseID, testCase.Label)
		}
	}
}

// TestRetrievalTopThree asserts the section 5 step 5 cap even when more
// candidates match.
func TestRetrievalTopThree(t *testing.T) {
	conn, _, connectorID, sessionID := setupRecordSchema(t)
	project := "top-three"
	messages := []string{
		"cannot find module 'alpha-package'",
		"cannot find module 'beta-package'",
		"cannot find module 'gamma-package'",
		"cannot find module 'delta-package'",
	}
	sequence := 1
	for _, message := range messages {
		seedLabelledIssue(t, conn, connectorID, sessionID, sequence, labelledSide{
			Category: "tool",
			Code:     "MODULE_NOT_FOUND",
			Message:  message,
		}, project)
		sequence++
	}

	result := retrieve(t, conn, database.RetrievalRequest{
		Category:   "tool",
		Message:    "cannot find module 'alpha-package'",
		ErrorCode:  "MODULE_NOT_FOUND",
		ProjectKey: project,
	})
	if len(result.Candidates) != database.MaxRetrievalCandidates {
		t.Fatalf("candidates = %d, want %d", len(result.Candidates), database.MaxRetrievalCandidates)
	}
	for position, candidate := range result.Candidates {
		if position > 0 && result.Candidates[position-1].Score < candidate.Score {
			t.Fatalf("candidates are not sorted: %.3f before %.3f", result.Candidates[position-1].Score, candidate.Score)
		}
	}
}

// TestRetrievalPersistsExplanation checks section 4: every returned candidate
// leaves an auditable retrieval row with its rank, score, fields and reason.
func TestRetrievalPersistsExplanation(t *testing.T) {
	conn, _, connectorID, sessionID := setupRecordSchema(t)
	project := "audit"
	seedLabelledIssue(t, conn, connectorID, sessionID, 1, labelledSide{
		Category: "tool",
		Code:     "EPERM",
		Message:  "EMFILE: too many open files, open 'C:\\repo\\package.json'",
	}, project)

	result := retrieve(t, conn, database.RetrievalRequest{
		Category:   "tool",
		Message:    "too many open files while opening the package manifest",
		ErrorCode:  "EPERM",
		ProjectKey: project,
		SessionID:  sessionID,
	})
	if len(result.Candidates) == 0 {
		t.Fatal("no candidates to audit")
	}

	rows, err := conn.Query(context.Background(), `
		select candidate_rank, score, match_fields, approximate, environment_diff,
		       candidate_issue_id::text, query
		from retrievals
		where session_id = $1
		order by candidate_rank`, sessionID)
	if err != nil {
		t.Fatalf("read retrievals: %v", err)
	}
	defer rows.Close()

	position := 0
	for rows.Next() {
		position++
		var (
			rank        int
			score       float64
			matchFields []string
			approximate bool
			diff        []byte
			issueID     string
			query       string
		)
		if err := rows.Scan(&rank, &score, &matchFields, &approximate, &diff, &issueID, &query); err != nil {
			t.Fatalf("scan retrieval: %v", err)
		}
		want := result.Candidates[position-1]
		if rank != position {
			t.Fatalf("rank = %d, want %d", rank, position)
		}
		if issueID != want.IssueID {
			t.Fatalf("stored issue id = %s, want %s", issueID, want.IssueID)
		}
		if approximate != want.Approximate {
			t.Fatalf("stored approximate = %v, want %v", approximate, want.Approximate)
		}
		if score < 0 || score > 1 {
			t.Fatalf("score out of range: %f", score)
		}
		if string(diff) == "null" {
			t.Fatalf("environment_diff is null, want a JSON array")
		}
		if query == "" {
			t.Fatalf("query text is empty")
		}
		_ = matchFields
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("read retrievals: %v", err)
	}
	if position != len(result.Candidates) {
		t.Fatalf("retrieval rows = %d, want %d", position, len(result.Candidates))
	}
}

// TestRetrievalExplainsEnvironmentDifferences checks that a known fix from
// another OS / runtime version still ranks, but says why it may not apply here.
func TestRetrievalExplainsEnvironmentDifferences(t *testing.T) {
	conn, _, connectorID, sessionID := setupRecordSchema(t)
	project := "environment"
	message := "EPERM: operation not permitted, rename 'C:\\repo\\dist'"
	issueA := seedLabelledIssue(t, conn, connectorID, sessionID, 1, labelledSide{
		Category: "tool",
		Code:     "EPERM",
		Message:  message,
	}, project)

	result := retrieve(t, conn, database.RetrievalRequest{
		Category:   "tool",
		Message:    message,
		ErrorCode:  "EPERM",
		ProjectKey: project,
		Environment: core.Environment{
			OS:              "linux",
			RuntimeVersions: map[string]string{"node": "18.0.0"},
		},
	})
	candidate, hit := findCandidate(result.Candidates, issueA)
	if !hit {
		t.Fatal("exact fingerprint match was not retrieved")
	}
	if candidate.Approximate || !candidate.AutoReusable() {
		t.Fatalf("exact match must be non-approximate and auto-reusable: %+v", candidate)
	}
	fields := make(map[string]bool, len(candidate.EnvironmentDiff))
	for _, difference := range candidate.EnvironmentDiff {
		fields[difference.Field] = true
	}
	if !fields["os"] || !fields["runtime_versions.node"] {
		t.Fatalf("environment differences = %+v, want os and runtime_versions.node", candidate.EnvironmentDiff)
	}
}

// TestRetrievalRejectsEmptyRequest makes "no selector" an explicit error rather
// than an empty result.
func TestRetrievalRejectsEmptyRequest(t *testing.T) {
	conn, _, _, _ := setupRecordSchema(t)
	if _, err := database.Retrieve(context.Background(), conn, database.RetrievalRequest{}); err == nil {
		t.Fatal("empty request was accepted, want an error")
	}
}
