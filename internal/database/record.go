// Recording of one redacted ErrorEvent. The database function
// errkb_record_event() owns idempotency and aggregation; this file only binds
// parameters and translates errors, so no Go code can introduce a
// check-then-insert race.
package database

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/jingchangzhao-gif/dsh-errkb/internal/contract"
)

// RecordResult is what errkb_record_event() reports back. Created is false when
// the event_id was already recorded: the stored event is left untouched and the
// new occurrence was still appended.
type RecordResult struct {
	EventID      string
	OccurrenceID string
	IssueID      string
	Fingerprint  string
	Created      bool
}

const recordEventSQL = `
	select event_id, occurrence_id, issue_id, fingerprint, created
	from errkb_record_event($1::jsonb, $2, $3)
`

// RecordEvent records one ErrorEvent in a single database call. The function
// runs inside the caller's transaction; with a plain connection that is a
// short implicit transaction, so an event and its occurrence are never split.
func RecordEvent(ctx context.Context, conn *pgx.Conn, event contract.ErrorEvent) (RecordResult, error) {
	if err := event.Validate(); err != nil {
		return RecordResult{}, fmt.Errorf("record event %s: %w", event.EventID, err)
	}
	payload, err := json.Marshal(event)
	if err != nil {
		return RecordResult{}, fmt.Errorf("record event %s: encode the event: %w", event.EventID, err)
	}

	var result RecordResult
	err = conn.QueryRow(ctx, recordEventSQL, payload, "connector", event.ConnectorID).Scan(
		&result.EventID,
		&result.OccurrenceID,
		&result.IssueID,
		&result.Fingerprint,
		&result.Created,
	)
	if err != nil {
		return RecordResult{}, translateRecordError(event.EventID, err)
	}
	return result, nil
}

// translateRecordError turns the PostgreSQL errors a caller can act on into
// messages that name the cause; everything else keeps the original error.
func translateRecordError(eventID string, err error) error {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		switch pgErr.Code {
		case "23503": // foreign_key_violation
			return fmt.Errorf("record event %s: the connector or session does not exist: %w", eventID, err)
		case "23505": // unique_violation
			return fmt.Errorf("record event %s: %w", eventID, err)
		case "23514": // check_violation
			return fmt.Errorf("record event %s: a field violates a database constraint: %w", eventID, err)
		case "22023": // invalid_parameter_value raised by errkb_record_event()
			return fmt.Errorf("record event %s: %w", eventID, err)
		}
	}
	return fmt.Errorf("record event %s: %w", eventID, err)
}
