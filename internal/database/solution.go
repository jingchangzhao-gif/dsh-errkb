// Package database holds the PostgreSQL repository layer. This file wraps the
// solution model of EKB-021: candidate fixes, their version history and the
// per-environment outcomes that feed the authoritative trust calculation.
//
// Trust is never written from Go. errkb_trust() (migration 0003) derives it from
// outcomes, so no connector or client can set a confidence value.
package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// solutionWriter is the subset of *pgx.Conn and pgx.Tx the solution writers
// need, so RecordSolution can run them inside one transaction. Every caller
// that passes a *pgx.Conn keeps working unchanged.
type solutionWriter interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// SolutionStatus is the closed state machine enforced by the database:
// candidate -> verified | deprecated | blocked, verified -> deprecated |
// blocked, blocked -> deprecated, deprecated -> terminal.
type SolutionStatus string

const (
	SolutionCandidate  SolutionStatus = "candidate"
	SolutionVerified   SolutionStatus = "verified"
	SolutionDeprecated SolutionStatus = "deprecated"
	SolutionBlocked    SolutionStatus = "blocked"
)

func (status SolutionStatus) valid() bool {
	switch status {
	case SolutionCandidate, SolutionVerified, SolutionDeprecated, SolutionBlocked:
		return true
	}
	return false
}

// OutcomeKind is what happened when a solution version was tried somewhere.
type OutcomeKind string

const (
	OutcomeSuccess OutcomeKind = "success"
	OutcomeFailure OutcomeKind = "failure"
	OutcomeUnknown OutcomeKind = "unknown"
)

func (kind OutcomeKind) valid() bool {
	switch kind {
	case OutcomeSuccess, OutcomeFailure, OutcomeUnknown:
		return true
	}
	return false
}

// Environment narrows an outcome to the machine it happened on. An empty value
// means "every environment".
type Environment struct {
	OS              string            `json:"os,omitempty"`
	Shell           string            `json:"shell,omitempty"`
	Connector       string            `json:"connector,omitempty"`
	RuntimeVersions map[string]string `json:"runtime_versions,omitempty"`
}

func (environment Environment) json() ([]byte, error) {
	encoded, err := json.Marshal(environment)
	if err != nil {
		return nil, fmt.Errorf("encode environment: %w", err)
	}
	return encoded, nil
}

// SolutionVersion is one revision of a candidate fix.
type SolutionVersion struct {
	ID            string
	SolutionID    string
	Version       int
	Body          string
	Preconditions string
	Risks         string
	AppliesTo     Environment
	EffectiveAt   time.Time
}

// Trust is the result of errkb_trust(): a Laplace-smoothed success ratio with
// the sample counts it was derived from.
type Trust struct {
	Score     float64
	Samples   int
	Successes int
	Failures  int
	Unknowns  int
}

// CreateSolution opens a candidate solution for an issue.
func CreateSolution(ctx context.Context, writer solutionWriter, issueID, source, actor string) (string, error) {
	var id string
	err := writer.QueryRow(ctx, `
		insert into solutions (issue_id, source, actor)
		values ($1, $2, $3)
		returning id
	`, issueID, source, actor).Scan(&id)
	if err != nil {
		return "", translateSolutionError("create solution", err)
	}
	return id, nil
}

// AddSolutionVersion appends the next revision. The version number is derived
// inside the statement so history cannot be overwritten; a concurrent append
// loses to the unique constraint instead of silently reusing a number.
func AddSolutionVersion(
	ctx context.Context,
	writer solutionWriter,
	solutionID, body, preconditions, risks string,
	appliesTo Environment,
	source, actor string,
) (SolutionVersion, error) {
	if body == "" {
		return SolutionVersion{}, fmt.Errorf("add solution version: body is required")
	}
	encoded, err := appliesTo.json()
	if err != nil {
		return SolutionVersion{}, err
	}

	var version SolutionVersion
	var storedAppliesTo []byte
	err = writer.QueryRow(ctx, `
		insert into solution_versions (
			solution_id, version, body, preconditions, risks, applies_to,
			source, actor
		) values (
			$1,
			(select coalesce(max(version), 0) + 1 from solution_versions where solution_id = $1),
			$2, $3, $4, $5::jsonb, $6, $7
		)
		returning id, solution_id, version, body, preconditions, risks, applies_to, effective_at
	`, solutionID, body, preconditions, risks, encoded, source, actor).Scan(
		&version.ID,
		&version.SolutionID,
		&version.Version,
		&version.Body,
		&version.Preconditions,
		&version.Risks,
		&storedAppliesTo,
		&version.EffectiveAt,
	)
	if err != nil {
		return SolutionVersion{}, translateSolutionError("add solution version", err)
	}
	if err := json.Unmarshal(storedAppliesTo, &version.AppliesTo); err != nil {
		return SolutionVersion{}, fmt.Errorf("decode applies_to: %w", err)
	}
	return version, nil
}

// TransitionSolution moves a solution along the state machine. The database
// trigger rejects anything the machine does not define.
func TransitionSolution(ctx context.Context, writer solutionWriter, solutionID string, to SolutionStatus) error {
	if !to.valid() {
		return fmt.Errorf("transition solution: %q is not a solution status", to)
	}
	tag, err := writer.Exec(ctx, `
		update solutions set status = $2 where id = $1
	`, solutionID, string(to))
	if err != nil {
		return translateSolutionError("transition solution", err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("transition solution %s: %w", solutionID, pgx.ErrNoRows)
	}
	return nil
}

// RecordOutcome stores what happened when a version was used. An empty
// environment is filled from the linked occurrence, so outcomes never lose the
// machine they happened on.
func RecordOutcome(
	ctx context.Context,
	conn *pgx.Conn,
	issueID, solutionVersionID string,
	kind OutcomeKind,
	note string,
	environment Environment,
	occurrenceID string,
	source, actor string,
) (string, error) {
	if !kind.valid() {
		return "", fmt.Errorf("record outcome: %q is not an outcome value", kind)
	}
	encoded, err := environment.json()
	if err != nil {
		return "", err
	}

	var occurrence any
	if occurrenceID != "" {
		occurrence = occurrenceID
	}

	var id string
	err = conn.QueryRow(ctx, `
		insert into outcomes (
			issue_id, solution_version_id, occurrence_id, outcome, note,
			environment, source, actor
		) values (
			$1, $2, $3, $4, $5,
			case
				when $6::jsonb = '{}'::jsonb and $3::uuid is not null
					then coalesce((select environment from occurrences where id = $3), '{}'::jsonb)
				else $6::jsonb
			end,
			$7, $8
		)
		returning id
	`, issueID, solutionVersionID, occurrence, string(kind), note, encoded, source, actor).Scan(&id)
	if err != nil {
		return "", translateSolutionError("record outcome", err)
	}
	return id, nil
}

// SolutionTrust asks the database for the authoritative value. There is no Go
// arithmetic here on purpose: the formula lives in errkb_trust().
func SolutionTrust(ctx context.Context, conn *pgx.Conn, solutionID string, environment Environment) (Trust, error) {
	encoded, err := environment.json()
	if err != nil {
		return Trust{}, err
	}

	var trust Trust
	err = conn.QueryRow(ctx, `
		select trust_score, samples, successes, failures, unknowns
		from errkb_trust($1, $2::jsonb)
	`, solutionID, encoded).Scan(&trust.Score, &trust.Samples, &trust.Successes, &trust.Failures, &trust.Unknowns)
	if err != nil {
		return Trust{}, fmt.Errorf("solution trust: %w", err)
	}
	return trust, nil
}

// translateSolutionError turns the database's constraint failures into messages
// a caller can act on, while leaving unexpected errors wrapped.
func translateSolutionError(operation string, err error) error {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return fmt.Errorf("%s: %w", operation, err)
	}
	switch pgErr.Code {
	case "23503":
		return fmt.Errorf("%s: the issue, solution or solution version does not exist: %w", operation, err)
	case "23514":
		return fmt.Errorf("%s: %s", operation, pgErr.Message)
	case "23505":
		return fmt.Errorf("%s: the version number is already taken: %w", operation, err)
	}
	return fmt.Errorf("%s: %w", operation, err)
}
