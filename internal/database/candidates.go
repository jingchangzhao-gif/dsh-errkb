package database

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

// Fuzzy candidate generation is retrieval step 3 (plan section 5): pg_trgm
// similarity produces candidates for the Go reranker (EKB-024). It never
// decides that two issues are the same, so Candidate carries a similarity and
// the matched fields but no merge verdict.
const (
	DefaultCandidateThreshold = 0.30
	DefaultCandidateLimit     = 20
	MaxCandidateLimit         = 100

	DefaultMessageWeight  = 1.00
	DefaultCommandWeight  = 0.40
	DefaultToolNameWeight = 0.20

	// TrigramQueryMaxChars caps the text handed to the pg_trgm stage.
	//
	// The GIN trigram scan cost grows with the number of trigrams in the query:
	// on the EKB-027 100k-issue dataset a 100-character title spends 82 ms in
	// the events_normalized_message_trgm_idx scan while the same query cut to
	// 64 characters costs 47-49 ms. The fuzzy stage only produces candidates
	// for the Go reranker, which reads the full message and therefore still
	// ranks with the complete text (EKB-024); capping the candidate query keeps
	// the database work bounded without changing what is compared at rerank
	// time. The cap is counted in runes, so a multi-byte character is never
	// split.
	TrigramQueryMaxChars = 64
)

// CandidateQuery describes one fuzzy candidate lookup. Build it with
// DefaultCandidateQuery and override the fields as needed.
type CandidateQuery struct {
	Query          string
	ProjectKey     string
	Threshold      float64
	Limit          int
	MessageWeight  float64
	CommandWeight  float64
	ToolNameWeight float64
}

// DefaultCandidateQuery returns the section 5 defaults for a query.
func DefaultCandidateQuery(query string) CandidateQuery {
	return CandidateQuery{
		Query:          query,
		Threshold:      DefaultCandidateThreshold,
		Limit:          DefaultCandidateLimit,
		MessageWeight:  DefaultMessageWeight,
		CommandWeight:  DefaultCommandWeight,
		ToolNameWeight: DefaultToolNameWeight,
	}
}

// Candidate is one fuzzy candidate issue. Similarity and Score are nil in the
// degraded mode, where only exact matches are available.
type Candidate struct {
	IssueID     string
	Fingerprint string
	Title       string
	ProjectKey  string
	MatchFields []string
	Similarity  *float64
	Score       *float64
	Approximate bool
	Degraded    bool
}

// CandidateSet is the result of a fuzzy lookup. Degraded reports that the
// pg_trgm extension is unavailable and the candidates came from the bounded
// exact-match fallback; callers must surface that instead of treating an
// empty set as "no fuzzy candidates".
type CandidateSet struct {
	Candidates []Candidate
	Degraded   bool
}

// TrigramAvailable reports whether the pg_trgm extension is installed in the
// database, and therefore whether fuzzy candidates are available at all.
func TrigramAvailable(ctx context.Context, conn *pgx.Conn) (bool, error) {
	var available bool
	if err := conn.QueryRow(ctx, "select errkb_trigram_available()").Scan(&available); err != nil {
		return false, fmt.Errorf("check pg_trgm availability: %w", err)
	}
	return available, nil
}

// FuzzyCandidates returns up to query.Limit candidate issues ordered by
// weighted trigram score. It performs no merging: the caller reranks with
// Jaccard and environment differences (EKB-024).
func FuzzyCandidates(ctx context.Context, conn *pgx.Conn, query CandidateQuery) (CandidateSet, error) {
	normalized, err := normalizeCandidateQuery(query)
	if err != nil {
		return CandidateSet{}, err
	}

	available, err := TrigramAvailable(ctx, conn)
	if err != nil {
		return CandidateSet{}, err
	}

	var project *string
	if normalized.ProjectKey != "" {
		project = &normalized.ProjectKey
	}

	rows, err := conn.Query(ctx, `
		select issue_id, fingerprint, title, project_key, match_fields,
		       similarity, score, approximate, degraded
		from errkb_candidates($1, $2, $3, $4, $5, $6, $7)`,
		normalized.Query, project, normalized.Threshold, normalized.Limit,
		normalized.MessageWeight, normalized.CommandWeight, normalized.ToolNameWeight)
	if err != nil {
		return CandidateSet{}, translateCandidateError(err)
	}
	defer rows.Close()

	result := CandidateSet{Degraded: !available, Candidates: []Candidate{}}
	for rows.Next() {
		var (
			candidate  Candidate
			similarity pgtype.Float8
			score      pgtype.Float8
		)
		if err := rows.Scan(&candidate.IssueID, &candidate.Fingerprint, &candidate.Title,
			&candidate.ProjectKey, &candidate.MatchFields, &similarity, &score,
			&candidate.Approximate, &candidate.Degraded); err != nil {
			return CandidateSet{}, fmt.Errorf("scan candidate: %w", err)
		}
		if similarity.Valid {
			value := similarity.Float64
			candidate.Similarity = &value
		}
		if score.Valid {
			value := score.Float64
			candidate.Score = &value
		}
		result.Candidates = append(result.Candidates, candidate)
	}
	if err := rows.Err(); err != nil {
		return CandidateSet{}, fmt.Errorf("read candidates: %w", err)
	}
	return result, nil
}

func normalizeCandidateQuery(query CandidateQuery) (CandidateQuery, error) {
	if strings.TrimSpace(query.Query) == "" {
		return CandidateQuery{}, fmt.Errorf("fuzzy candidates: a non-empty query is required")
	}
	if query.Threshold <= 0 || query.Threshold > 1 {
		return CandidateQuery{}, fmt.Errorf("fuzzy candidates: threshold must be greater than 0 and at most 1")
	}
	if query.Limit < 1 || query.Limit > MaxCandidateLimit {
		return CandidateQuery{}, fmt.Errorf("fuzzy candidates: limit must be between 1 and %d", MaxCandidateLimit)
	}
	if query.MessageWeight < 0 || query.CommandWeight < 0 || query.ToolNameWeight < 0 {
		return CandidateQuery{}, fmt.Errorf("fuzzy candidates: weights must be non-negative")
	}
	if query.MessageWeight+query.CommandWeight+query.ToolNameWeight <= 0 {
		return CandidateQuery{}, fmt.Errorf("fuzzy candidates: at least one weight must be positive")
	}
	query.Query = truncateTrigramQuery(query.Query)
	return query, nil
}

// truncateTrigramQuery keeps the first TrigramQueryMaxChars runes of the query.
// The cap exists for the GIN scan cost (see TrigramQueryMaxChars); the reranker
// still receives and compares the full text.
func truncateTrigramQuery(query string) string {
	runes := []rune(query)
	if len(runes) <= TrigramQueryMaxChars {
		return query
	}
	return string(runes[:TrigramQueryMaxChars])
}

func translateCandidateError(err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "22023" {
		return fmt.Errorf("fuzzy candidates: %s", pgErr.Message)
	}
	return fmt.Errorf("fuzzy candidates: %w", err)
}
