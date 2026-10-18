package core

import (
	"sort"
	"strings"
)

// maxCandidates is the hard top-3 limit from §5. It is a constant, not an
// option: the search surface is designed to show at most three.
const maxCandidates = 3

// Scoring weights. They are declared together so the ranking is one visible
// decision. The CJK factor is not a separate term: Tokenize splits CJK runs into
// character bigrams, so Jaccard already measures them.
const (
	weightTokenSimilarity = 0.55
	weightErrorCode       = 0.15
	weightCommand         = 0.10
	weightToolName        = 0.05
	weightEnvironment     = 0.15
)

// MatchField names one reason a candidate matched. It is an enumerable field,
// not free text, because a retrieval row persists it for audit (§4).
type MatchField string

const (
	// MatchFieldFingerprint: the stored fingerprint equals the query's.
	MatchFieldFingerprint MatchField = "fingerprint"
	// MatchFieldErrorCode: both sides carry the same non-empty error code.
	MatchFieldErrorCode MatchField = "error_code"
	// MatchFieldCommand: both sides carry the same non-empty command.
	MatchFieldCommand MatchField = "command"
	// MatchFieldTokenSimilarity: the normalized messages share tokens.
	MatchFieldTokenSimilarity MatchField = "token_similarity"
	// MatchFieldEnvironment: at least one environment value agrees.
	MatchFieldEnvironment MatchField = "environment"
)

// Environment is the comparable context of a query or a candidate. Empty values
// mean "unknown" and are never reported as differences.
type Environment struct {
	OS              string
	Shell           string
	Connector       string
	RuntimeVersions map[string]string
}

// RerankQuery is the search context: what the user is looking at right now.
type RerankQuery struct {
	Fingerprint       string
	Code              string
	NormalizedMessage string
	Command           string
	ToolName          string
	Environment       Environment
}

// RerankCandidate is one stored issue with the fields the ranker compares.
type RerankCandidate struct {
	IssueID           string
	Fingerprint       string
	Code              string
	NormalizedMessage string
	Command           string
	ToolName          string
	Environment       Environment
}

// EnvironmentDifference records one environment field that does not agree.
// It is reported instead of silently lowering the score, so the surface can say
// "this known fix may not apply here" and why.
type EnvironmentDifference struct {
	Field     string
	Query     string
	Candidate string
}

// RerankResult is one ranked candidate with its explanation. It deliberately has
// no merge verdict: a merge is a database decision that needs user confirmation
// (§5); this struct only says "candidate, with this score, for these reasons".
type RerankResult struct {
	IssueID         string
	Score           float64
	MatchedFields   []MatchField
	Approximate     bool
	EnvironmentDiff []EnvironmentDifference
}

// Rerank ranks candidates for one query and explains each of the top three.
//
// A candidate is "exact" only when the fingerprint matches and the fields the
// fingerprint does not cover (error code, command, tool name) do not contradict
// each other: the fingerprint is derived from the category and normalized
// message only, so the same message under a different command is a different
// problem. Such a candidate is reported with Approximate=false and sorted first.
// Everything else is Approximate=true, always.
//
// Ties break by exact first, then IssueID ascending, so the output is identical
// for the same input in any order. The function is pure: no I/O, no clock, no
// counters.
func Rerank(query RerankQuery, candidates []RerankCandidate) []RerankResult {
	results := make([]RerankResult, 0, len(candidates))
	for _, candidate := range candidates {
		results = append(results, rankOne(query, candidate))
	}
	sort.Slice(results, func(i, j int) bool {
		if results[i].Score != results[j].Score {
			return results[i].Score > results[j].Score
		}
		if results[i].Approximate != results[j].Approximate {
			return !results[i].Approximate
		}
		return results[i].IssueID < results[j].IssueID
	})
	if len(results) > maxCandidates {
		results = results[:maxCandidates]
	}
	return results
}

func rankOne(query RerankQuery, candidate RerankCandidate) RerankResult {
	queryTokens := Tokenize(query.NormalizedMessage)
	candidateTokens := Tokenize(candidate.NormalizedMessage)
	similarity := Jaccard(queryTokens, candidateTokens)
	codeMatches := bothNonEmpty(query.Code, candidate.Code) && equalFoldTrim(query.Code, candidate.Code)
	commandMatches := bothNonEmpty(query.Command, candidate.Command) && strings.TrimSpace(query.Command) == strings.TrimSpace(candidate.Command)
	toolMatches := bothNonEmpty(query.ToolName, candidate.ToolName) && equalFoldTrim(query.ToolName, candidate.ToolName)
	environmentScore, differences, environmentMatches := compareEnvironment(query.Environment, candidate.Environment)
	fingerprintMatches := query.Fingerprint != "" && query.Fingerprint == candidate.Fingerprint
	contradiction := contradicts(query, candidate)

	score := weightTokenSimilarity*similarity +
		weightErrorCode*boolScore(codeMatches) +
		weightCommand*boolScore(commandMatches) +
		weightToolName*boolScore(toolMatches) +
		weightEnvironment*environmentScore

	matched := make([]MatchField, 0, 4)
	if fingerprintMatches {
		matched = append(matched, MatchFieldFingerprint)
	}
	if codeMatches {
		matched = append(matched, MatchFieldErrorCode)
	}
	if commandMatches {
		matched = append(matched, MatchFieldCommand)
	}
	if similarity > 0 {
		matched = append(matched, MatchFieldTokenSimilarity)
	}
	if environmentMatches > 0 {
		matched = append(matched, MatchFieldEnvironment)
	}

	if fingerprintMatches && !contradiction {
		return RerankResult{
			IssueID:         candidate.IssueID,
			Score:           1,
			MatchedFields:   matched,
			Approximate:     false,
			EnvironmentDiff: differences,
		}
	}
	return RerankResult{
		IssueID:         candidate.IssueID,
		Score:           score,
		MatchedFields:   matched,
		Approximate:     true,
		EnvironmentDiff: differences,
	}
}

// contradicts reports whether a field the fingerprint does not cover disagrees.
// Only fields both sides provide can contradict; an unknown value never makes a
// match approximate by itself, but it never makes it exact either.
func contradicts(query RerankQuery, candidate RerankCandidate) bool {
	if bothNonEmpty(query.Code, candidate.Code) && !equalFoldTrim(query.Code, candidate.Code) {
		return true
	}
	if bothNonEmpty(query.Command, candidate.Command) && strings.TrimSpace(query.Command) != strings.TrimSpace(candidate.Command) {
		return true
	}
	return bothNonEmpty(query.ToolName, candidate.ToolName) && !equalFoldTrim(query.ToolName, candidate.ToolName)
}

// compareEnvironment returns the agreement score, the differing fields and the
// number of agreeing fields. Only values both sides provide are compared; the
// score is the agreeing share of the compared fields, or 0 when nothing is
// comparable.
func compareEnvironment(query, candidate Environment) (score float64, differences []EnvironmentDifference, matches int) {
	comparable := 0
	consider := func(field, queryValue, candidateValue string) {
		if queryValue == "" || candidateValue == "" {
			return
		}
		comparable++
		if equalFoldTrim(queryValue, candidateValue) {
			matches++
			return
		}
		differences = append(differences, EnvironmentDifference{Field: field, Query: queryValue, Candidate: candidateValue})
	}
	consider("os", query.OS, candidate.OS)
	consider("shell", query.Shell, candidate.Shell)
	consider("connector", query.Connector, candidate.Connector)

	keys := make([]string, 0, len(query.RuntimeVersions))
	for key := range query.RuntimeVersions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		consider("runtime_versions."+key, query.RuntimeVersions[key], candidate.RuntimeVersions[key])
	}

	if comparable == 0 {
		return 0, differences, matches
	}
	return float64(matches) / float64(comparable), differences, matches
}

func bothNonEmpty(a, b string) bool {
	return strings.TrimSpace(a) != "" && strings.TrimSpace(b) != ""
}

func equalFoldTrim(a, b string) bool {
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}

func boolScore(value bool) float64 {
	if value {
		return 1
	}
	return 0
}
