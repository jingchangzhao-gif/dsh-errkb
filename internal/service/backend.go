// SQLBackend is the PostgreSQL-backed implementation of the local API's
// Backend interface (EKB-035). It is the only place that turns a validated
// contract payload into a database call and back; the pipe layer keeps no
// business logic.
//
// The file is portable (no build tag): the daemon serves it on Windows, and
// tests drive it directly.
package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jingchangzhao-gif/dsh-errkb/internal/contract"
	"github.com/jingchangzhao-gif/dsh-errkb/internal/core"
	"github.com/jingchangzhao-gif/dsh-errkb/internal/database"
)

// DefaultListLimit is the page size errkb_list uses when the caller omits
// limit. The contract still caps it at 100.
const DefaultListLimit = 20

// SQLBackend answers the six local API operations from PostgreSQL. A nil or
// closed pool fails every call with CodeUnavailable instead of panicking.
type SQLBackend struct {
	pool *pgxpool.Pool
}

// NewSQLBackend wires a pool. The pool is not owned: the daemon closes it.
func NewSQLBackend(pool *pgxpool.Pool) *SQLBackend {
	return &SQLBackend{pool: pool}
}

func (b *SQLBackend) withConn(ctx context.Context, call func(context.Context, *pgx.Conn) error) error {
	if b == nil || b.pool == nil {
		return APIErrorf(CodeUnavailable, "the database is not available")
	}
	conn, err := b.pool.Acquire(ctx)
	if err != nil {
		return APIErrorf(CodeUnavailable, "acquire a database connection: %v", err)
	}
	defer conn.Release()
	return call(ctx, conn.Conn())
}

// Lookup answers "I know what this error is". A fingerprint-only lookup runs
// the same five-step retrieval and therefore still records a retrieval row.
func (b *SQLBackend) Lookup(ctx context.Context, request contract.LookupRequest) (contract.LookupResponse, error) {
	if err := request.Validate(); err != nil {
		return contract.LookupResponse{}, APIErrorf(CodeInvalidRequest, "%v", err)
	}
	var candidates []contract.Candidate
	var degraded bool
	err := b.withConn(ctx, func(ctx context.Context, conn *pgx.Conn) error {
		result, err := database.Retrieve(ctx, conn, database.RetrievalRequest{
			IssueID:     request.Issue,
			Fingerprint: request.Fingerprint,
			Message:     request.NormalizedMessage,
			ErrorCode:   request.ErrorCode,
			ProjectKey:  request.ProjectKey,
		})
		if err != nil {
			return err
		}
		candidates = mapCandidates(result.Candidates)
		degraded = result.Degraded
		return nil
	})
	if err != nil {
		return contract.LookupResponse{}, translateBackendError("lookup", err)
	}
	_ = degraded // degraded is surfaced by the CLI, not by the contract.
	return contract.LookupResponse{SchemaVersion: contract.ToolSchemaVersion, Candidates: candidates}, nil
}

// Search is the free-text lookup. It is a separate operation on purpose.
func (b *SQLBackend) Search(ctx context.Context, request contract.SearchRequest) (contract.SearchResponse, error) {
	if err := request.Validate(); err != nil {
		return contract.SearchResponse{}, APIErrorf(CodeInvalidRequest, "%v", err)
	}
	var candidates []contract.Candidate
	err := b.withConn(ctx, func(ctx context.Context, conn *pgx.Conn) error {
		result, err := database.Retrieve(ctx, conn, database.RetrievalRequest{
			Message:    request.Query,
			ProjectKey: request.ProjectKey,
		})
		if err != nil {
			return err
		}
		candidates = mapCandidates(result.Candidates)
		return nil
	})
	if err != nil {
		return contract.SearchResponse{}, translateBackendError("search", err)
	}
	return contract.SearchResponse{SchemaVersion: contract.ToolSchemaVersion, Candidates: candidates}, nil
}

// Record stores one already redacted failure and answers the issue identity.
func (b *SQLBackend) Record(ctx context.Context, request contract.RecordRequest) (contract.RecordResponse, error) {
	if err := request.Validate(); err != nil {
		return contract.RecordResponse{}, APIErrorf(CodeInvalidRequest, "%v", err)
	}
	var stored database.RecordResult
	err := b.withConn(ctx, func(ctx context.Context, conn *pgx.Conn) error {
		result, err := database.RecordEvent(ctx, conn, request.Event)
		if err != nil {
			return err
		}
		stored = result
		return nil
	})
	if err != nil {
		return contract.RecordResponse{}, translateBackendError("record", err)
	}
	return contract.RecordResponse{
		SchemaVersion: contract.ToolSchemaVersion,
		EventID:       stored.EventID,
		OccurrenceID:  stored.OccurrenceID,
		IssueID:       stored.IssueID,
		Fingerprint:   stored.Fingerprint,
		Created:       stored.Created,
	}, nil
}

// Feedback records an outcome. It never sets trust: errkb_trust() derives it.
func (b *SQLBackend) Feedback(ctx context.Context, request contract.FeedbackRequest) (contract.FeedbackResponse, error) {
	if err := request.Validate(); err != nil {
		return contract.FeedbackResponse{}, APIErrorf(CodeInvalidRequest, "%v", err)
	}
	var outcomeID string
	err := b.withConn(ctx, func(ctx context.Context, conn *pgx.Conn) error {
		id, err := database.RecordOutcome(ctx, conn, request.IssueID, request.SolutionVersionID,
			database.OutcomeKind(request.Outcome), request.Note, database.Environment{}, "", "cli", "cli")
		if err != nil {
			return err
		}
		outcomeID = id
		return nil
	})
	if err != nil {
		return contract.FeedbackResponse{}, translateBackendError("feedback", err)
	}
	return contract.FeedbackResponse{SchemaVersion: contract.ToolSchemaVersion, OutcomeID: outcomeID, Recorded: true}, nil
}

// List pages issues or solutions. An empty page is not an error.
func (b *SQLBackend) List(ctx context.Context, request contract.ListRequest) (contract.ListResponse, error) {
	if err := request.Validate(); err != nil {
		return contract.ListResponse{}, APIErrorf(CodeInvalidRequest, "%v", err)
	}
	limit := DefaultListLimit
	if request.Limit != nil {
		limit = *request.Limit
	}
	var items []database.ListSummary
	var next string
	err := b.withConn(ctx, func(ctx context.Context, conn *pgx.Conn) error {
		var err error
		if request.Kind == "solution" {
			items, next, err = database.ListSolutions(ctx, conn, request.ProjectKey, request.Cursor, limit)
		} else {
			items, next, err = database.ListIssues(ctx, conn, request.ProjectKey, request.Cursor, limit)
		}
		return err
	})
	if err != nil {
		if strings.Contains(err.Error(), "malformed cursor") {
			return contract.ListResponse{}, APIErrorf(CodeInvalidRequest, "%v", err)
		}
		return contract.ListResponse{}, translateBackendError("list", err)
	}
	mapped := make([]contract.ListItem, 0, len(items))
	for _, item := range items {
		mapped = append(mapped, contract.ListItem{
			ID:        item.ID,
			Title:     item.Title,
			UpdatedAt: item.UpdatedAt.UTC().Format(time.RFC3339),
		})
	}
	return contract.ListResponse{SchemaVersion: contract.ToolSchemaVersion, Items: mapped, NextCursor: next}, nil
}

// Stats reports totals. Latency and token savings are not measured by the
// store yet, so they stay zero instead of being invented.
func (b *SQLBackend) Stats(ctx context.Context, request contract.StatsRequest) (contract.StatsResponse, error) {
	if err := request.Validate(); err != nil {
		return contract.StatsResponse{}, APIErrorf(CodeInvalidRequest, "%v", err)
	}
	var summary database.StatsSummary
	err := b.withConn(ctx, func(ctx context.Context, conn *pgx.Conn) error {
		result, err := database.Stats(ctx, conn, request.ProjectKey)
		if err != nil {
			return err
		}
		summary = result
		return nil
	})
	if err != nil {
		return contract.StatsResponse{}, translateBackendError("stats", err)
	}
	return contract.StatsResponse{
		SchemaVersion: contract.ToolSchemaVersion,
		Totals: contract.StatsTotals{
			Events:    int(summary.Events),
			Issues:    int(summary.Issues),
			Solutions: int(summary.Solutions),
			Outcomes:  int(summary.Outcomes),
		},
	}, nil
}

// mapCandidates converts retrieval rows into the reason-carrying contract
// shape. The mapping is intentionally mechanical: the reason names the step
// that produced the hit, and approximate follows the reranker's verdict.
func mapCandidates(candidates []database.RetrievalCandidate) []contract.Candidate {
	mapped := make([]contract.Candidate, 0, len(candidates))
	for _, candidate := range candidates {
		score := candidate.Score
		reason := matchReason(candidate)
		detail := matchDetail(candidate)
		entry := contract.Candidate{
			IssueID:     candidate.IssueID,
			Fingerprint: candidate.Fingerprint,
			Title:       candidate.Title,
			MatchReason: reason,
			MatchDetail: detail,
			Approximate: candidate.Approximate,
			Confidence:  &score,
		}
		if candidate.Solution != nil {
			entry.Solution = &contract.CandidateSolution{
				SolutionVersionID: candidate.Solution.SolutionVersionID,
				Body:              candidate.Solution.Body,
				Source:            candidate.Solution.Source,
				AppliesTo:         candidate.Solution.AppliesTo,
				Trust:             candidate.Solution.Trust,
			}
		}
		mapped = append(mapped, entry)
	}
	return mapped
}

func matchReason(candidate database.RetrievalCandidate) string {
	for _, field := range candidate.MatchedFields {
		switch field {
		case core.MatchFieldFingerprint:
			return "exact_fingerprint"
		case core.MatchFieldErrorCode:
			return "error_code"
		}
	}
	if candidate.Approximate {
		return "jaccard_rerank"
	}
	return "deterministic_filter"
}

func matchDetail(candidate database.RetrievalCandidate) string {
	fields := make([]string, 0, len(candidate.MatchedFields))
	for _, field := range candidate.MatchedFields {
		fields = append(fields, string(field))
	}
	detail := fmt.Sprintf("score %.2f", candidate.Score)
	if len(fields) > 0 {
		detail += "; matched: " + strings.Join(fields, ", ")
	}
	if len(candidate.EnvironmentDiff) > 0 {
		differences := make([]string, 0, len(candidate.EnvironmentDiff))
		for _, difference := range candidate.EnvironmentDiff {
			differences = append(differences, difference.Field)
		}
		detail += "; environment differs: " + strings.Join(differences, ", ")
	}
	return detail
}

// translateBackendError maps storage failures onto the closed wire codes. A
// backend that returns anything else becomes CodeInternal in encodeResult.
func translateBackendError(operation string, err error) error {
	var apiError *APIError
	if errors.As(err, &apiError) {
		return err
	}
	var pgError *pgconn.PgError
	if errors.As(err, &pgError) {
		switch pgError.Code {
		case "23505":
			return APIErrorf(CodeConflict, "%s: %s", operation, pgError.Message)
		case "23503", "23514", "22023", "22P02":
			return APIErrorf(CodeInvalidRequest, "%s: %s", operation, pgError.Message)
		}
	}
	return APIErrorf(CodeInternal, "%s: %v", operation, err)
}
