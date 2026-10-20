// Package service hosts the read and edit paths behind the UI. This file is
// the EKB-046 detail page and solution editor: it loads one issue with its
// occurrences, solution versions, outcomes and trust, and every write path is
// audited in the same transaction as the change.
//
// Trust is read-only here. The authoritative value comes from errkb_trust()
// (EKB-021) and no method in this file writes it.
package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/database"
)

// AuditOperator and AuditSource label the rows this API writes. V1 is a local
// single-owner install, so the operator is the owner and the source is the UI.
const (
	AuditOperator = "owner"
	AuditSource   = "ui"
)

var (
	// ErrConfirmationRequired is returned when a state change that affects
	// injection is not confirmed (acceptance 7).
	ErrConfirmationRequired = errors.New("this change needs an explicit confirmation")
	// ErrDetailNotFound is returned for an unknown issue id.
	ErrDetailNotFound = errors.New("the issue does not exist")
)

// IssueInfo is the summary shown at the top of the detail page.
type IssueInfo struct {
	ID          string
	Title       string
	Category    string
	Status      string
	Fingerprint string
	ProjectKey  string
	Trigger     string
	Hits        int
	FirstSeen   time.Time
	LastSeen    time.Time
}

// OccurrenceInfo is one real appearance of the issue, with the environment it
// happened in so two rows are distinguishable (acceptance 6).
type OccurrenceInfo struct {
	ID              string
	EventID         string
	OccurredAt      time.Time
	OS              string
	Shell           string
	Connector       string
	RuntimeVersions map[string]string
	Headline        string
	Command         string
	ExitCode        *int
}

// VersionInfo is one revision of a fix. Versions are never overwritten, so the
// slice is the history (acceptance 1).
type VersionInfo struct {
	ID            string
	SolutionID    string
	Version       int
	Body          string
	Preconditions string
	Risks         string
	AppliesTo     database.Environment
	Source        string
	Actor         string
	EffectiveAt   time.Time
}

// OutcomeInfo is one recorded use of a version.
type OutcomeInfo struct {
	ID                string
	SolutionVersionID string
	Kind              string
	Note              string
	Environment       database.Environment
	ObservedAt        time.Time
}

// SolutionInfo is one candidate fix with its whole version history, its
// outcomes, and the trust the database computed. Trust has no setter here.
type SolutionInfo struct {
	ID        string
	Status    string
	CreatedAt time.Time
	Trust     database.Trust
	Versions  []VersionInfo
	Outcomes  []OutcomeInfo
}

// IssueDetail is everything the detail page renders.
type IssueDetail struct {
	Issue       IssueInfo
	Occurrences []OccurrenceInfo
	Solutions   []SolutionInfo
}

// DetailService reads one issue and performs audited edits. A nil pool is a
// valid value whose methods report an error.
type DetailService struct {
	pool *pgxpool.Pool
}

// NewDetailService wraps the daemon's pool.
func NewDetailService(pool *pgxpool.Pool) *DetailService {
	return &DetailService{pool: pool}
}

// Load reads the detail page. Occurrences and outcomes are capped at 100 rows
// each; the page is a human view, not an export.
func (s *DetailService) Load(ctx context.Context, issueID string) (IssueDetail, error) {
	var detail IssueDetail
	if s == nil || s.pool == nil {
		return detail, errors.New("detail: the database is not available")
	}
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return detail, fmt.Errorf("detail: connect to PostgreSQL: %w", err)
	}
	defer conn.Release()

	row := conn.QueryRow(ctx, `
		select id, title, category, status, fingerprint, project_key, trigger,
		       hits, first_seen, last_seen
		from issues where id = $1
	`, issueID)
	var issue IssueInfo
	if err := row.Scan(&issue.ID, &issue.Title, &issue.Category, &issue.Status,
		&issue.Fingerprint, &issue.ProjectKey, &issue.Trigger, &issue.Hits,
		&issue.FirstSeen, &issue.LastSeen); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return detail, ErrDetailNotFound
		}
		return detail, fmt.Errorf("detail: load issue: %w", err)
	}
	detail.Issue = issue

	occurrences, err := loadOccurrences(ctx, conn, issueID)
	if err != nil {
		return detail, err
	}
	detail.Occurrences = occurrences

	solutions, err := loadSolutions(ctx, conn.Conn(), issueID)
	if err != nil {
		return detail, err
	}
	detail.Solutions = solutions
	return detail, nil
}

func loadOccurrences(ctx context.Context, conn *pgxpool.Conn, issueID string) ([]OccurrenceInfo, error) {
	rows, err := conn.Query(ctx, `
		select o.id, o.event_id, o.occurred_at, o.environment,
		       e.headline, e.command, e.exit_code
		from occurrences o
		join events e on e.event_id = o.event_id
		where o.issue_id = $1
		order by o.occurred_at desc, o.id
		limit 100
	`, issueID)
	if err != nil {
		return nil, fmt.Errorf("detail: load occurrences: %w", err)
	}
	defer rows.Close()
	occurrences := make([]OccurrenceInfo, 0, 16)
	for rows.Next() {
		var occurrence OccurrenceInfo
		var environment []byte
		if err := rows.Scan(&occurrence.ID, &occurrence.EventID, &occurrence.OccurredAt,
			&environment, &occurrence.Headline, &occurrence.Command,
			&occurrence.ExitCode); err != nil {
			return nil, fmt.Errorf("detail: scan occurrence: %w", err)
		}
		var decoded database.Environment
		if err := json.Unmarshal(environment, &decoded); err != nil {
			return nil, fmt.Errorf("detail: decode occurrence environment: %w", err)
		}
		occurrence.OS = decoded.OS
		occurrence.Shell = decoded.Shell
		occurrence.Connector = decoded.Connector
		occurrence.RuntimeVersions = decoded.RuntimeVersions
		occurrences = append(occurrences, occurrence)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("detail: read occurrences: %w", err)
	}
	return occurrences, nil
}

func loadSolutions(ctx context.Context, conn *pgx.Conn, issueID string) ([]SolutionInfo, error) {
	rows, err := conn.Query(ctx, `
		select id, status, created_at from solutions
		where issue_id = $1 order by created_at desc, id
	`, issueID)
	if err != nil {
		return nil, fmt.Errorf("detail: load solutions: %w", err)
	}
	defer rows.Close()
	solutions := make([]SolutionInfo, 0, 4)
	for rows.Next() {
		var solution SolutionInfo
		if err := rows.Scan(&solution.ID, &solution.Status, &solution.CreatedAt); err != nil {
			return nil, fmt.Errorf("detail: scan solution: %w", err)
		}
		solutions = append(solutions, solution)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("detail: read solutions: %w", err)
	}

	for index := range solutions {
		versions, err := loadVersions(ctx, conn, solutions[index].ID)
		if err != nil {
			return nil, err
		}
		solutions[index].Versions = versions
		outcomes, err := loadOutcomes(ctx, conn, issueID, solutions[index].ID)
		if err != nil {
			return nil, err
		}
		solutions[index].Outcomes = outcomes
		trust, err := database.SolutionTrust(ctx, conn, solutions[index].ID, database.Environment{})
		if err != nil {
			return nil, fmt.Errorf("detail: load trust: %w", err)
		}
		solutions[index].Trust = trust
	}
	return solutions, nil
}

func loadVersions(ctx context.Context, conn *pgx.Conn, solutionID string) ([]VersionInfo, error) {
	rows, err := conn.Query(ctx, `
		select id, solution_id, version, body, preconditions, risks, applies_to,
		       source, actor, effective_at
		from solution_versions where solution_id = $1
		order by version desc
	`, solutionID)
	if err != nil {
		return nil, fmt.Errorf("detail: load versions: %w", err)
	}
	defer rows.Close()
	versions := make([]VersionInfo, 0, 4)
	for rows.Next() {
		var version VersionInfo
		var appliesTo []byte
		if err := rows.Scan(&version.ID, &version.SolutionID, &version.Version, &version.Body,
			&version.Preconditions, &version.Risks, &appliesTo,
			&version.Source, &version.Actor, &version.EffectiveAt); err != nil {
			return nil, fmt.Errorf("detail: scan version: %w", err)
		}
		if err := json.Unmarshal(appliesTo, &version.AppliesTo); err != nil {
			return nil, fmt.Errorf("detail: decode applies_to: %w", err)
		}
		versions = append(versions, version)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("detail: read versions: %w", err)
	}
	return versions, nil
}

func loadOutcomes(ctx context.Context, conn *pgx.Conn, issueID, solutionID string) ([]OutcomeInfo, error) {
	rows, err := conn.Query(ctx, `
		select o.id, o.solution_version_id, o.outcome, o.note,
		       o.environment, o.observed_at
		from outcomes o
		join solution_versions v on v.id = o.solution_version_id
		where o.issue_id = $1 and v.solution_id = $2
		order by o.observed_at desc, o.id
		limit 100
	`, issueID, solutionID)
	if err != nil {
		return nil, fmt.Errorf("detail: load outcomes: %w", err)
	}
	defer rows.Close()
	outcomes := make([]OutcomeInfo, 0, 8)
	for rows.Next() {
		var outcome OutcomeInfo
		var environment []byte
		if err := rows.Scan(&outcome.ID, &outcome.SolutionVersionID, &outcome.Kind,
			&outcome.Note, &environment, &outcome.ObservedAt); err != nil {
			return nil, fmt.Errorf("detail: scan outcome: %w", err)
		}
		if err := json.Unmarshal(environment, &outcome.Environment); err != nil {
			return nil, fmt.Errorf("detail: decode outcome environment: %w", err)
		}
		outcomes = append(outcomes, outcome)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("detail: read outcomes: %w", err)
	}
	return outcomes, nil
}

// VersionInput is one edit of the solution text. A new row is appended; the
// current version is never updated in place.
type VersionInput struct {
	IssueID       string
	SolutionID    string // empty: open a new candidate solution for the issue
	Body          string
	Preconditions string
	Risks         string
	AppliesTo     database.Environment
}

// AddVersion appends a revision and records one audit row, in one transaction.
func (s *DetailService) AddVersion(ctx context.Context, input VersionInput) (VersionInfo, error) {
	if input.IssueID == "" || input.Body == "" {
		return VersionInfo{}, errors.New("edit: an issue id and a body are required")
	}
	if s == nil || s.pool == nil {
		return VersionInfo{}, errors.New("edit: the database is not available")
	}
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return VersionInfo{}, fmt.Errorf("edit: connect to PostgreSQL: %w", err)
	}
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	if err != nil {
		return VersionInfo{}, fmt.Errorf("edit: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	solutionID := input.SolutionID
	if solutionID == "" {
		solutionID, err = database.CreateSolution(ctx, tx, input.IssueID, AuditSource, AuditOperator)
		if err != nil {
			return VersionInfo{}, fmt.Errorf("edit: %w", err)
		}
	}
	version, err := database.AddSolutionVersion(ctx, tx, solutionID, input.Body,
		input.Preconditions, input.Risks, input.AppliesTo, AuditSource, AuditOperator)
	if err != nil {
		return VersionInfo{}, fmt.Errorf("edit: %w", err)
	}
	if err := writeAudit(ctx, tx, "solution.version.added", "solution_version", version.ID, map[string]any{
		"issue_id":    input.IssueID,
		"solution_id": solutionID,
		"version":     version.Version,
	}); err != nil {
		return VersionInfo{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return VersionInfo{}, fmt.Errorf("edit: commit: %w", err)
	}
	return VersionInfo{
		ID:            version.ID,
		SolutionID:    solutionID,
		Version:       version.Version,
		Body:          version.Body,
		Preconditions: version.Preconditions,
		Risks:         version.Risks,
		AppliesTo:     version.AppliesTo,
		Source:        AuditSource,
		Actor:         AuditOperator,
		EffectiveAt:   version.EffectiveAt,
	}, nil
}

// ChangeSolutionStatus moves a solution along the EKB-021 state machine and
// audits the transition. Deprecating or blocking a fix affects injection, so
// those transitions require the caller to pass confirmed=true.
func (s *DetailService) ChangeSolutionStatus(ctx context.Context, solutionID, status string, confirmed bool) error {
	target := database.SolutionStatus(status)
	if solutionID == "" {
		return errors.New("edit: a solution id is required")
	}
	if target != database.SolutionCandidate && target != database.SolutionVerified &&
		target != database.SolutionDeprecated && target != database.SolutionBlocked {
		return fmt.Errorf("edit: %q is not a solution status", status)
	}
	if (target == database.SolutionDeprecated || target == database.SolutionBlocked) && !confirmed {
		return ErrConfirmationRequired
	}
	if s == nil || s.pool == nil {
		return errors.New("edit: the database is not available")
	}
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("edit: connect to PostgreSQL: %w", err)
	}
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("edit: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var from string
	if err := tx.QueryRow(ctx, `select status from solutions where id = $1`, solutionID).Scan(&from); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrDetailNotFound
		}
		return fmt.Errorf("edit: read solution status: %w", err)
	}
	if err := database.TransitionSolution(ctx, tx, solutionID, target); err != nil {
		return fmt.Errorf("edit: %w", err)
	}
	if err := writeAudit(ctx, tx, "solution.status.changed", "solution", solutionID, map[string]any{
		"from": from,
		"to":   string(target),
	}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("edit: commit: %w", err)
	}
	return nil
}

// MarkIssueMisjudged marks the issue itself as a misjudgement. It affects
// injection, so it requires confirmation and writes an audit row.
func (s *DetailService) MarkIssueMisjudged(ctx context.Context, issueID string, confirmed bool) error {
	if issueID == "" {
		return errors.New("edit: an issue id is required")
	}
	if !confirmed {
		return ErrConfirmationRequired
	}
	if s == nil || s.pool == nil {
		return errors.New("edit: the database is not available")
	}
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("edit: connect to PostgreSQL: %w", err)
	}
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("edit: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var from string
	if err := tx.QueryRow(ctx, `select status from issues where id = $1`, issueID).Scan(&from); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrDetailNotFound
		}
		return fmt.Errorf("edit: read issue status: %w", err)
	}
	if _, err := tx.Exec(ctx, `update issues set status = 'misjudged', updated_at = now() where id = $1`, issueID); err != nil {
		return fmt.Errorf("edit: mark misjudged: %w", err)
	}
	if err := writeAudit(ctx, tx, "issue.status.changed", "issue", issueID, map[string]any{
		"from": from,
		"to":   "misjudged",
	}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("edit: commit: %w", err)
	}
	return nil
}

// DeleteSolution removes a candidate fix and its history. It is irreversible,
// so it requires confirmation and writes the deletion to the audit log.
func (s *DetailService) DeleteSolution(ctx context.Context, solutionID string, confirmed bool) error {
	if solutionID == "" {
		return errors.New("edit: a solution id is required")
	}
	if !confirmed {
		return ErrConfirmationRequired
	}
	if s == nil || s.pool == nil {
		return errors.New("edit: the database is not available")
	}
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return fmt.Errorf("edit: connect to PostgreSQL: %w", err)
	}
	defer conn.Release()

	tx, err := conn.Begin(ctx)
	if err != nil {
		return fmt.Errorf("edit: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var issueID string
	var versions int
	if err := tx.QueryRow(ctx, `
		select s.issue_id,
		       (select count(*) from solution_versions v where v.solution_id = s.id)
		from solutions s where s.id = $1
	`, solutionID).Scan(&issueID, &versions); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("edit: solution %s: %w", solutionID, pgx.ErrNoRows)
		}
		return fmt.Errorf("edit: read solution: %w", err)
	}
	if _, err := tx.Exec(ctx, `delete from solutions where id = $1`, solutionID); err != nil {
		return fmt.Errorf("edit: delete solution: %w", err)
	}
	if err := writeAudit(ctx, tx, "solution.deleted", "solution", solutionID, map[string]any{
		"issue_id":         issueID,
		"versions_deleted": versions,
	}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("edit: commit: %w", err)
	}
	return nil
}

// writeAudit appends the audit row inside the caller's transaction, so the
// change and its record commit or roll back together (acceptance 2).
func writeAudit(ctx context.Context, tx pgx.Tx, action, entity, entityID string, detail map[string]any) error {
	encoded, err := json.Marshal(detail)
	if err != nil {
		return fmt.Errorf("edit: encode audit detail: %w", err)
	}
	if _, err := tx.Exec(ctx, `
		insert into audit_log (action, entity, entity_id, operator, source, detail)
		values ($1, $2, $3, $4, $5, $6::jsonb)
	`, action, entity, entityID, AuditOperator, AuditSource, encoded); err != nil {
		return fmt.Errorf("edit: write audit row: %w", err)
	}
	return nil
}
