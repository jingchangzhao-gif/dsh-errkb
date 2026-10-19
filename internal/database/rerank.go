package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/jingchangzhao-gif/dsh-errkb/internal/core"
)

// Retrieval orchestration (plan section 5). The five steps are visible in
// Retrieve in order:
//
//  1. exact issue id lookup;
//  2. exact fingerprint lookup plus the deterministic filters (error code,
//     command, tool name);
//  3. pg_trgm candidates (EKB-023);
//  4. the pure Go reranker (EKB-017);
//  5. at most three explained candidates, written to `retrievals`.
//
// This file is the only place where the database layer and the pure core layer
// meet: all SQL stays here, and internal/core stays free of I/O.
const (
	// MaxRetrievalCandidates is the hard top-3 limit from section 5 step 5.
	// core.Rerank enforces the same cap, and the retrievals table checks
	// candidate_rank between 1 and 3.
	MaxRetrievalCandidates = 3

	// retrievalPoolLimit bounds each exact lookup and the candidate pool.
	retrievalPoolLimit = 20
)

// RetrievalRequest is one search. Message is the raw text; callers do not have
// to normalize it first. At least one of IssueID, Message, ErrorCode, Command
// or ToolName is required.
type RetrievalRequest struct {
	IssueID     string
	Category    string
	Message     string
	ErrorCode   string
	Command     string
	ToolName    string
	Fingerprint string
	ProjectKey  string
	Environment core.Environment
	SessionID   string
}

// RetrievalSolution is the verified fix attached to a candidate (EKB-035).
// Only verified versions travel: the pipe contract renders its absence as a
// nil solution, never as an empty body.
type RetrievalSolution struct {
	SolutionVersionID string
	Body              string
	Source            string
	AppliesTo         string
	Trust             float64
}

// RetrievalCandidate is one explained candidate. It deliberately carries no
// merge verdict: only an exact fingerprint match may be reused automatically,
// everything else needs the user's confirmation (section 5).
type RetrievalCandidate struct {
	IssueID         string
	Fingerprint     string
	Title           string
	ProjectKey      string
	Score           float64
	MatchedFields   []core.MatchField
	Approximate     bool
	EnvironmentDiff []core.EnvironmentDifference
	Solution        *RetrievalSolution
}

// AutoReusable reports whether this candidate may be reused without asking the
// user. Section 5 allows that only on an exact fingerprint match; an
// approximate candidate always needs confirmation.
func (c RetrievalCandidate) AutoReusable() bool {
	if c.Approximate {
		return false
	}
	for _, field := range c.MatchedFields {
		if field == core.MatchFieldFingerprint {
			return true
		}
	}
	return false
}

// RetrievalResult is the explained shortlist. Degraded reports that pg_trgm was
// unavailable, so the fuzzy step only contributed bounded exact matches and the
// caller must surface that instead of hiding it.
type RetrievalResult struct {
	Candidates []RetrievalCandidate
	Degraded   bool
}

// Retrieve runs the five-step retrieval and persists the explanation.
func Retrieve(ctx context.Context, conn *pgx.Conn, request RetrievalRequest) (RetrievalResult, error) {
	normalized := core.Normalize(request.Message)
	category := strings.TrimSpace(request.Category)
	issueID := strings.TrimSpace(request.IssueID)
	errorCode := strings.TrimSpace(request.ErrorCode)
	command := strings.TrimSpace(request.Command)
	toolName := strings.TrimSpace(request.ToolName)
	projectKey := strings.TrimSpace(request.ProjectKey)

	var fingerprint string
	if explicit := strings.TrimSpace(request.Fingerprint); explicit != "" {
		fingerprint = explicit
	} else if category != "" && normalized != "" {
		fingerprint = core.Signature(category, normalized)
	}

	if issueID == "" && normalized == "" && errorCode == "" && command == "" && toolName == "" && fingerprint == "" {
		return RetrievalResult{}, errors.New("retrieve: at least one of issue id, fingerprint, message, error code, command or tool name is required")
	}

	pool := make(map[string]struct{})
	add := func(id string) {
		if id != "" {
			pool[id] = struct{}{}
		}
	}
	search := func(dimension SearchDimension, value string) error {
		hits, err := ExactSearch(ctx, conn, ExactSearchQuery{
			Dimension:  dimension,
			Value:      value,
			ProjectKey: projectKey,
			Limit:      retrievalPoolLimit,
		})
		if err != nil {
			return err
		}
		for _, hit := range hits {
			add(hit.IssueID)
		}
		return nil
	}

	// Steps 1 and 2: the exact issue id, the exact fingerprint and the
	// deterministic filters.
	if issueID != "" {
		if err := search(SearchByIssueID, issueID); err != nil {
			return RetrievalResult{}, fmt.Errorf("retrieve: %w", err)
		}
	}
	if fingerprint != "" {
		if err := search(SearchByFingerprint, fingerprint); err != nil {
			return RetrievalResult{}, fmt.Errorf("retrieve: %w", err)
		}
	}
	for _, selector := range []struct {
		dimension SearchDimension
		value     string
	}{
		{SearchByErrorCode, errorCode},
		{SearchByCommand, command},
		{SearchByToolName, toolName},
	} {
		if selector.value == "" {
			continue
		}
		if err := search(selector.dimension, selector.value); err != nil {
			return RetrievalResult{}, fmt.Errorf("retrieve: %w", err)
		}
	}

	// Step 3: fuzzy candidates. They are candidates only; whether one is exact
	// is decided by the reranker in step 4.
	result := RetrievalResult{}
	if normalized != "" {
		set, err := FuzzyCandidates(ctx, conn, CandidateQuery{
			Query:          normalized,
			ProjectKey:     projectKey,
			Threshold:      DefaultCandidateThreshold,
			Limit:          retrievalPoolLimit,
			MessageWeight:  DefaultMessageWeight,
			CommandWeight:  DefaultCommandWeight,
			ToolNameWeight: DefaultToolNameWeight,
		})
		if err != nil {
			return RetrievalResult{}, fmt.Errorf("retrieve: %w", err)
		}
		result.Degraded = set.Degraded
		for _, candidate := range set.Candidates {
			add(candidate.IssueID)
		}
	}

	if len(pool) == 0 {
		return result, nil
	}

	issueIDs := make([]string, 0, len(pool))
	for id := range pool {
		issueIDs = append(issueIDs, id)
	}
	sort.Strings(issueIDs)

	details, err := loadRetrievalCandidates(ctx, conn, issueIDs)
	if err != nil {
		return RetrievalResult{}, fmt.Errorf("retrieve: %w", err)
	}

	// Step 4: the pure reranker. No SQL runs inside internal/core.
	ranked := core.Rerank(core.RerankQuery{
		Fingerprint:       fingerprint,
		Code:              errorCode,
		NormalizedMessage: normalized,
		Command:           command,
		ToolName:          toolName,
		Environment:       request.Environment,
	}, details.rankCandidates())

	// Step 5: at most three, with the explanation persisted for audit.
	candidates := make([]RetrievalCandidate, 0, len(ranked))
	for _, rankedCandidate := range ranked {
		stored := details.byIssue[rankedCandidate.IssueID]
		candidates = append(candidates, RetrievalCandidate{
			IssueID:         rankedCandidate.IssueID,
			Fingerprint:     stored.Fingerprint,
			Title:           stored.Title,
			ProjectKey:      stored.ProjectKey,
			Score:           rankedCandidate.Score,
			MatchedFields:   rankedCandidate.MatchedFields,
			Approximate:     rankedCandidate.Approximate,
			EnvironmentDiff: rankedCandidate.EnvironmentDiff,
			Solution:        stored.Solution,
		})
	}
	if err := persistRetrievals(ctx, conn, request.SessionID, retrievalQueryText(normalized, errorCode, command, toolName, issueID), candidates); err != nil {
		return RetrievalResult{}, fmt.Errorf("retrieve: %w", err)
	}
	result.Candidates = candidates
	return result, nil
}

// retrievalQueryText is the auditable query string stored on each retrieval
// row: the normalized message when there is one, otherwise the other selector.
func retrievalQueryText(normalized, errorCode, command, toolName, issueID string) string {
	for _, value := range []string{normalized, errorCode, command, toolName, issueID} {
		if value != "" {
			return value
		}
	}
	return ""
}

// retrievalDetails holds the fields of the stored issues that the reranker
// compares, plus the display fields for the result.
type retrievalDetails struct {
	byIssue map[string]storedRetrievalCandidate
	order   []string
}

type storedRetrievalCandidate struct {
	core.RerankCandidate
	Title      string
	ProjectKey string
	Solution   *RetrievalSolution
}

func (d retrievalDetails) rankCandidates() []core.RerankCandidate {
	candidates := make([]core.RerankCandidate, 0, len(d.order))
	for _, issueID := range d.order {
		candidates = append(candidates, d.byIssue[issueID].RerankCandidate)
	}
	return candidates
}

// loadRetrievalCandidates loads the latest occurrence's event fields and
// environment for each issue. An issue without occurrences still comes back
// with empty fields, so it can be ranked but every optional comparison is
// skipped.
func loadRetrievalCandidates(ctx context.Context, conn *pgx.Conn, issueIDs []string) (retrievalDetails, error) {
	detail := retrievalDetails{byIssue: make(map[string]storedRetrievalCandidate, len(issueIDs))}
	rows, err := conn.Query(ctx, `
		select i.id::text,
		       i.fingerprint,
		       i.title,
		       i.project_key,
		       coalesce(latest.normalized_message, ''),
		       coalesce(latest.error_code, ''),
		       coalesce(latest.command, ''),
		       coalesce(latest.tool_name, ''),
		       coalesce(latest.environment, '{}'::jsonb),
		       coalesce(fix.version_id, ''),
		       coalesce(fix.body, ''),
		       coalesce(fix.source, ''),
		       coalesce(fix.applies_to, '{}'::jsonb)::text,
		       coalesce(fix.trust, 0)
		from issues i
		left join lateral (
			select e.normalized_message, e.error_code, e.command, e.tool_name, o.environment
			from occurrences o
			join events e on e.event_id = o.event_id
			where o.issue_id = i.id
			order by o.occurred_at desc, o.id desc
			limit 1
		) latest on true
		left join lateral (
			select sv.id::text as version_id,
			       sv.body,
			       sv.source,
			       sv.applies_to,
			       (select t.trust_score from errkb_trust(s.id, '{}'::jsonb) t) as trust
			from solutions s
			join solution_versions sv on sv.solution_id = s.id
			where s.issue_id = i.id and s.status = 'verified'
			order by sv.version desc
			limit 1
		) fix on true
		where i.id = any($1::uuid[])`,
		issueIDs)
	if err != nil {
		return retrievalDetails{}, fmt.Errorf("load candidates: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			issueID      string
			fingerprint  string
			title        string
			projectKey   string
			message      string
			code         string
			command      string
			toolName     string
			environment  []byte
			fixVersionID string
			fixBody      string
			fixSource    string
			fixAppliesTo string
			fixTrust     float64
		)
		if err := rows.Scan(&issueID, &fingerprint, &title, &projectKey, &message, &code, &command, &toolName, &environment,
			&fixVersionID, &fixBody, &fixSource, &fixAppliesTo, &fixTrust); err != nil {
			return retrievalDetails{}, fmt.Errorf("scan candidate: %w", err)
		}
		stored := storedRetrievalCandidate{
			RerankCandidate: core.RerankCandidate{
				IssueID:           issueID,
				Fingerprint:       fingerprint,
				Code:              code,
				NormalizedMessage: message,
				Command:           command,
				ToolName:          toolName,
				Environment:       environmentFromJSON(environment),
			},
			Title:      title,
			ProjectKey: projectKey,
		}
		if fixVersionID != "" {
			stored.Solution = &RetrievalSolution{
				SolutionVersionID: fixVersionID,
				Body:              fixBody,
				Source:            fixSource,
				AppliesTo:         formatAppliesTo([]byte(fixAppliesTo)),
				Trust:             fixTrust,
			}
		}
		detail.byIssue[issueID] = stored
		detail.order = append(detail.order, issueID)
	}
	if err := rows.Err(); err != nil {
		return retrievalDetails{}, fmt.Errorf("load candidates: %w", err)
	}
	return detail, nil
}

// environmentFromJSON decodes the occurrences.environment shape written by
// errkb_record_event(). Unknown keys are ignored and a malformed document is
// treated as "unknown", never as a fatal error: environment data only adjusts
// the score, it never decides a match.
func environmentFromJSON(raw []byte) core.Environment {
	if len(raw) == 0 {
		return core.Environment{}
	}
	var stored struct {
		OS              string            `json:"os"`
		Shell           string            `json:"shell"`
		Connector       string            `json:"connector"`
		RuntimeVersions map[string]string `json:"runtime_versions"`
	}
	if err := json.Unmarshal(raw, &stored); err != nil {
		return core.Environment{}
	}
	return core.Environment{
		OS:              stored.OS,
		Shell:           stored.Shell,
		Connector:       stored.Connector,
		RuntimeVersions: stored.RuntimeVersions,
	}
}

// formatAppliesTo renders a stored environment scope as the one line the
// candidate view shows ("os=windows, shell=cmd, go=1.26.4"). The contract
// treats it as display text; an empty scope stays empty so the caller can tell
// "applies everywhere" from "applies in a specific environment".
func formatAppliesTo(raw []byte) string {
	environment := environmentFromJSON(raw)
	parts := make([]string, 0, 3+len(environment.RuntimeVersions))
	if environment.OS != "" {
		parts = append(parts, "os="+environment.OS)
	}
	if environment.Shell != "" {
		parts = append(parts, "shell="+environment.Shell)
	}
	if environment.Connector != "" {
		parts = append(parts, "connector="+environment.Connector)
	}
	keys := make([]string, 0, len(environment.RuntimeVersions))
	for key := range environment.RuntimeVersions {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		parts = append(parts, key+"="+environment.RuntimeVersions[key])
	}
	return strings.Join(parts, ", ")
}

// persistRetrievals writes one row per returned candidate (section 4:
// "what a search matched and why"). It is part of the search: a failure to
// record the explanation fails the search instead of silently dropping audit
// data.
func persistRetrievals(ctx context.Context, conn *pgx.Conn, sessionID, query string, candidates []RetrievalCandidate) error {
	if len(candidates) == 0 {
		return nil
	}
	var session any
	if trimmed := strings.TrimSpace(sessionID); trimmed != "" {
		session = trimmed
	}
	for index, candidate := range candidates {
		matchedFields := make([]string, len(candidate.MatchedFields))
		for position, field := range candidate.MatchedFields {
			matchedFields[position] = string(field)
		}
		differences := candidate.EnvironmentDiff
		if differences == nil {
			differences = []core.EnvironmentDifference{}
		}
		encodedDifferences, err := json.Marshal(differences)
		if err != nil {
			return fmt.Errorf("encode environment diff: %w", err)
		}
		if _, err := conn.Exec(ctx, `
			insert into retrievals (
				session_id, query, candidate_issue_id, candidate_rank,
				score, match_fields, approximate, environment_diff
			) values ($1, $2, $3, $4, $5, $6, $7, $8)`,
			session, query, candidate.IssueID, index+1, candidate.Score,
			matchedFields, candidate.Approximate, encodedDifferences); err != nil {
			return fmt.Errorf("record retrieval: %w", err)
		}
	}
	return nil
}
