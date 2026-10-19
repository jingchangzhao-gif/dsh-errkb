// The connector capture identity helpers (EKB-038 rollback). A connector knows
// its own name and session key, not the core's uuids, so the core resolves both
// in one place. Kept here instead of in internal/capture so the SQL stays in the
// database package, next to the other storage entry points.
package database

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// StoredEvent is what a connector capture stores: the issue identity plus one
// occurrence, and whether this call created the issue.
type StoredEvent struct {
	EventID      string
	OccurrenceID string
	IssueID      string
	Fingerprint  string
	Created      bool
}

// StoredEventByEventID answers with the stored identity when the event already
// exists, and nil otherwise. The connector capture pipeline calls it before
// recording: EKB-020 deliberately counts every record call as one appearance,
// so a capture-level replay after a lost acknowledgement must be absorbed here
// instead of reaching the function a second time.
func StoredEventByEventID(ctx context.Context, conn *pgx.Conn, eventID string) (*StoredEvent, error) {
	var stored StoredEvent
	err := conn.QueryRow(ctx, `
		select events.event_id::text,
		       coalesce((
		           select occurrences.id::text from occurrences
		           where occurrences.event_id = events.event_id
		           order by occurrences.id
		           limit 1), ''),
		       coalesce((
		           select issues.id::text from issues
		           where issues.fingerprint = events.fingerprint
		             and issues.project_key = events.project_key), ''),
		       events.fingerprint
		from events
		where events.event_id = $1::uuid`, eventID,
	).Scan(&stored.EventID, &stored.OccurrenceID, &stored.IssueID, &stored.Fingerprint)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("look up event %s: %w", eventID, err)
	}
	return &stored, nil
}

// EnsureConnector resolves the connector name an adapter knows to the row's
// uuid, creating or refreshing the row. The channel records which capture level
// the adapter can reach (A–E, plan section 3.1).
func EnsureConnector(ctx context.Context, conn *pgx.Conn, name, channel, version string) (string, error) {
	if name == "" {
		return "", errors.New("connector: name is required")
	}
	if channel == "" {
		channel = "A"
	}
	var id string
	err := conn.QueryRow(ctx, `
		insert into connectors (name, version, channel, capabilities, source, actor)
		values ($1, $2, $3, '{}'::jsonb, 'connector', $1)
		on conflict (name) do update
			set channel = excluded.channel,
			    version = excluded.version,
			    updated_at = now()
		returning id::text`, name, version, channel).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("connector %q: %w", name, err)
	}
	return id, nil
}

// EnsureSession resolves the adapter's session key for one connector. Sessions
// are per connector by schema, so two adapters cannot collide on the same key.
func EnsureSession(ctx context.Context, conn *pgx.Conn, connectorID, externalID, projectKey string) (string, error) {
	if connectorID == "" || externalID == "" {
		return "", errors.New("session: connector id and external id are required")
	}
	var id string
	err := conn.QueryRow(ctx, `
		insert into sessions (connector_id, external_id, project_key, started_at, source, actor)
		values ($1::uuid, $2, $3, now(), 'connector', 'connector')
		on conflict (connector_id, external_id) do update
			set project_key = excluded.project_key,
			    updated_at = now()
		returning id::text`, connectorID, externalID, projectKey).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("session %q: %w", externalID, err)
	}
	return id, nil
}
