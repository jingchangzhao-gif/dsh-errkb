// record_solution / set_status write-backs (EKB-038b). These are the two
// operations a host needs to close the loop by itself: record the fix it just
// found, and correct an issue that was not a real failure. Both stay thin:
// trust is still computed by errkb_trust() from outcomes, and the solution
// state machine is still enforced by the 0003 trigger.
package database

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/audit"
)

// SolutionRecord is the solution and revision RecordSolution appended.
type SolutionRecord struct {
	SolutionID        string
	SolutionVersionID string
	Version           int
}

// RecordSolution stores a fix against an issue in one transaction: it reuses
// the issue's open (candidate/verified) solution, or opens one; appends the
// next version; verifies a still-candidate solution; and marks the issue
// resolved. A missing issue surfaces as pgx.ErrNoRows.
func RecordSolution(
	ctx context.Context,
	conn *pgx.Conn,
	issueID, body, preconditions string,
	appliesTo Environment,
	source, actor string,
) (SolutionRecord, error) {
	if issueID == "" {
		return SolutionRecord{}, errors.New("record solution: issue id is required")
	}
	if body == "" {
		return SolutionRecord{}, errors.New("record solution: body is required")
	}

	tx, err := conn.Begin(ctx)
	if err != nil {
		return SolutionRecord{}, translateSolutionError("record solution", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var issueExists bool
	if err := tx.QueryRow(ctx, `select exists (select 1 from issues where id = $1)`, issueID).Scan(&issueExists); err != nil {
		return SolutionRecord{}, translateSolutionError("record solution", err)
	}
	if !issueExists {
		return SolutionRecord{}, fmt.Errorf("record solution: issue %s: %w", issueID, pgx.ErrNoRows)
	}

	var solutionID string
	var status SolutionStatus
	err = tx.QueryRow(ctx, `
		select id, status from solutions
		where issue_id = $1 and status in ('candidate', 'verified')
		order by created_at desc
		limit 1
	`, issueID).Scan(&solutionID, &status)
	if errors.Is(err, pgx.ErrNoRows) {
		solutionID, err = CreateSolution(ctx, tx, issueID, source, actor)
		if err != nil {
			return SolutionRecord{}, err
		}
		status = SolutionCandidate
	} else if err != nil {
		return SolutionRecord{}, translateSolutionError("record solution", err)
	}

	version, err := AddSolutionVersion(ctx, tx, solutionID, body, preconditions, "", appliesTo, source, actor)
	if err != nil {
		return SolutionRecord{}, err
	}
	if status != SolutionVerified {
		if err := TransitionSolution(ctx, tx, solutionID, SolutionVerified); err != nil {
			return SolutionRecord{}, err
		}
	}

	// Every edit leaves an audit row in the same transaction, so a recorded
	// fix and its trail cannot diverge.
	if err := insertAudit(ctx, tx, "solution.version.added", "solution_version", version.ID, actor, source, map[string]any{
		"solution_id":   solutionID,
		"version":       version.Version,
		"body":          body,
		"preconditions": preconditions,
	}); err != nil {
		return SolutionRecord{}, translateSolutionError("record solution", err)
	}

	tag, err := tx.Exec(ctx, `
		update issues set status = 'resolved', updated_at = now() where id = $1
	`, issueID)
	if err != nil {
		return SolutionRecord{}, translateSolutionError("record solution", err)
	}
	if tag.RowsAffected() == 0 {
		return SolutionRecord{}, fmt.Errorf("record solution: issue %s: %w", issueID, pgx.ErrNoRows)
	}

	if err := tx.Commit(ctx); err != nil {
		return SolutionRecord{}, translateSolutionError("record solution", err)
	}
	return SolutionRecord{
		SolutionID:        solutionID,
		SolutionVersionID: version.ID,
		Version:           version.Version,
	}, nil
}

// SetIssueStatus corrects an issue status. The database check constraint is the
// authority; a missing issue surfaces as pgx.ErrNoRows. The edit and its audit
// row commit together.
func SetIssueStatus(ctx context.Context, conn *pgx.Conn, issueID, status string) error {
	valid := map[string]bool{"open": true, "resolved": true, "wontfix": true, "misjudged": true}
	if issueID == "" {
		return errors.New("set issue status: issue id is required")
	}
	if !valid[status] {
		return fmt.Errorf("set issue status: unknown status %q", status)
	}
	tx, err := conn.Begin(ctx)
	if err != nil {
		return translateSolutionError("set issue status", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var before string
	if err := tx.QueryRow(ctx, `select status from issues where id = $1`, issueID).Scan(&before); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("set issue status: issue %s: %w", issueID, pgx.ErrNoRows)
		}
		return translateSolutionError("set issue status", err)
	}
	if _, err := tx.Exec(ctx, `
		update issues set status = $2, updated_at = now() where id = $1
	`, issueID, status); err != nil {
		return translateSolutionError("set issue status", err)
	}
	if err := insertAudit(ctx, tx, "issue.status.changed", "issue", issueID, "owner", "api", map[string]any{
		"from": before,
		"to":   status,
	}); err != nil {
		return translateSolutionError("set issue status", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return translateSolutionError("set issue status", err)
	}
	return nil
}

// insertAudit appends one audit row inside the caller's transaction, so the
// edit and its trail cannot diverge.
func insertAudit(ctx context.Context, tx pgx.Tx, action, entity, entityID, operator, source string, detail map[string]any) error {
	return audit.Record(ctx, tx, audit.Entry{
		Action:   action,
		Entity:   entity,
		EntityID: entityID,
		Operator: operator,
		Source:   source,
		Detail:   detail,
	})
}
