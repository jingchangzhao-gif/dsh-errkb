package database

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// SearchDimension names one exact retrieval dimension. The database function
// accepts exactly one of them per call (plus an optional project filter).
type SearchDimension string

const (
	SearchByIssueID           SearchDimension = "issue_id"
	SearchByFingerprint       SearchDimension = "fingerprint"
	SearchByErrorCode         SearchDimension = "error_code"
	SearchByNormalizedMessage SearchDimension = "normalized_message"
	SearchByCommand           SearchDimension = "command"
	SearchByToolName          SearchDimension = "tool_name"
)

// Search limits. The exact layer may return a short candidate list; the final
// "at most three" rule belongs to the reranking step (EKB-024).
const (
	DefaultSearchLimit = 10
	MaxSearchLimit     = 100
)

// searchArgument is the position of the dimension in errkb_search()'s
// parameter list: p_issue_id, p_fingerprint, p_error_code,
// p_normalized_message, p_command, p_tool_name, p_project_key, p_limit.
var searchArgument = map[SearchDimension]int{
	SearchByIssueID:           1,
	SearchByFingerprint:       2,
	SearchByErrorCode:         3,
	SearchByNormalizedMessage: 4,
	SearchByCommand:           5,
	SearchByToolName:          6,
}

// ExactSearchQuery is one exact lookup: a single selector, an optional project
// scope and a row limit.
type ExactSearchQuery struct {
	Dimension  SearchDimension
	Value      string
	ProjectKey string
	Limit      int
}

// SearchHit is one row of the shared candidate shape. The exact layer always
// returns Approximate = false and Score = 1.
type SearchHit struct {
	IssueID     string
	Fingerprint string
	Title       string
	ProjectKey  string
	Category    string
	Status      string
	Hits        int
	LastSeen    time.Time
	MatchReason string
	Approximate bool
	Score       float64
}

const exactSearchSQL = `
	select issue_id, fingerprint, title, project_key, category, status,
	       hits, last_seen, match_reason, approximate, score
	from errkb_search($1, $2, $3, $4, $5, $6, $7, $8)`

// ExactSearch runs one exact lookup. An unknown dimension, an empty value or an
// out-of-range limit is an error; a syntactically valid lookup with no match
// returns an empty slice and a nil error.
func ExactSearch(ctx context.Context, conn *pgx.Conn, query ExactSearchQuery) ([]SearchHit, error) {
	position, ok := searchArgument[query.Dimension]
	if !ok {
		return nil, fmt.Errorf("exact search: unknown dimension %q", query.Dimension)
	}
	if query.Value == "" {
		return nil, fmt.Errorf("exact search: dimension %q needs a value", query.Dimension)
	}
	limit := query.Limit
	if limit == 0 {
		limit = DefaultSearchLimit
	}
	if limit < 1 || limit > MaxSearchLimit {
		return nil, fmt.Errorf("exact search: limit %d is outside 1..%d", limit, MaxSearchLimit)
	}

	arguments := make([]any, 8)
	arguments[position-1] = query.Value
	if query.ProjectKey != "" {
		arguments[6] = query.ProjectKey
	}
	arguments[7] = limit

	rows, err := conn.Query(ctx, exactSearchSQL, arguments...)
	if err != nil {
		return nil, translateSearchError(err)
	}
	defer rows.Close()

	hits := make([]SearchHit, 0, limit)
	for rows.Next() {
		var hit SearchHit
		if err := rows.Scan(&hit.IssueID, &hit.Fingerprint, &hit.Title, &hit.ProjectKey,
			&hit.Category, &hit.Status, &hit.Hits, &hit.LastSeen, &hit.MatchReason,
			&hit.Approximate, &hit.Score); err != nil {
			return nil, translateSearchError(err)
		}
		hits = append(hits, hit)
	}
	return hits, translateSearchError(rows.Err())
}

// translateSearchError keeps invalid-argument failures distinguishable from
// transport errors. The database raises 22023 for a malformed request; an empty
// result is never an error.
func translateSearchError(err error) error {
	if err == nil {
		return nil
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "22023" {
		return fmt.Errorf("exact search: %s", pgErr.Message)
	}
	return fmt.Errorf("exact search: %w", err)
}
