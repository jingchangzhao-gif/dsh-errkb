// Package audit owns the audit trail: writing controlled actions and reading
// them back for the audit view. The trail itself is the `audit_log` table from
// EKB-019; EKB-026 gives it a retention window and delete/truncate guards, and
// EKB-046/EKB-047 already write their edits through the same columns. This file
// is the read/write API the statistics and audit views use (EKB-049).
package audit

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// DefaultQueryLimit and MaxQueryLimit bound the audit page size. The page is a
// UI list, not an export, so 200 rows is plenty for one screen and keeps the
// query cheap.
const (
	DefaultQueryLimit = 50
	MaxQueryLimit     = 200
)

// Writer is the subset of pgx a caller needs to record an audit entry, so a
// caller inside a transaction can pass its pgx.Tx (the same shape the edit API
// uses).
type Writer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Entry is one controlled action to record.
type Entry struct {
	Action   string
	Entity   string
	EntityID string
	Operator string
	Source   string
	Detail   map[string]any
}

// Validate rejects entries that would pollute the trail.
func (e Entry) Validate() error {
	if strings.TrimSpace(e.Action) == "" {
		return errors.New("audit: action is required")
	}
	if strings.TrimSpace(e.Entity) == "" {
		return errors.New("audit: entity is required")
	}
	if strings.TrimSpace(e.Operator) == "" {
		return errors.New("audit: operator is required")
	}
	if strings.TrimSpace(e.Source) == "" {
		return errors.New("audit: source is required")
	}
	return nil
}

// Record appends one audit row. Detail is stored as jsonb; nil means {}.
func Record(ctx context.Context, writer Writer, entry Entry) error {
	if err := entry.Validate(); err != nil {
		return err
	}
	detail := entry.Detail
	if detail == nil {
		detail = map[string]any{}
	}
	encoded, err := json.Marshal(detail)
	if err != nil {
		return fmt.Errorf("audit: encode detail: %w", err)
	}
	if _, err := writer.Exec(ctx, `
		insert into audit_log (action, entity, entity_id, operator, source, detail)
		values ($1, $2, $3, $4, $5, $6::jsonb)
	`, entry.Action, entry.Entity, entry.EntityID, entry.Operator, entry.Source, encoded); err != nil {
		return fmt.Errorf("audit: record %q: %w", entry.Action, err)
	}
	return nil
}

// Filter is the audit view filter. Zero time fields mean unbounded, and empty
// strings mean "any". A caller may filter by time, action, entity and object id —
// the card requires all three of time, operation type and target object.
type Filter struct {
	From     time.Time
	To       time.Time
	Action   string
	Entity   string
	EntityID string
	Limit    int
	Offset   int
}

// Normalized clamps the paging values and trims the string filters.
func (f Filter) Normalized() Filter {
	f.Action = strings.TrimSpace(f.Action)
	f.Entity = strings.TrimSpace(f.Entity)
	f.EntityID = strings.TrimSpace(f.EntityID)
	if f.Limit <= 0 {
		f.Limit = DefaultQueryLimit
	}
	if f.Limit > MaxQueryLimit {
		f.Limit = MaxQueryLimit
	}
	if f.Offset < 0 {
		f.Offset = 0
	}
	return f
}

// Row is one audit row as the view shows it.
type Row struct {
	ID         int64
	OccurredAt time.Time
	Action     string
	Entity     string
	EntityID   string
	Operator   string
	Source     string
	Detail     json.RawMessage
}

// Page is one page of the audit trail plus the unpaged total.
type Page struct {
	Records []Row
	Total   int
	Limit   int
	Offset  int
}

// Query reads a page of the audit trail, newest first.
func Query(ctx context.Context, conn *pgx.Conn, filter Filter) (Page, error) {
	filter = filter.Normalized()
	conditions := make([]string, 0, 3)
	arguments := make([]any, 0, 5)
	if !filter.From.IsZero() {
		arguments = append(arguments, filter.From)
		conditions = append(conditions, fmt.Sprintf("occurred_at >= $%d", len(arguments)))
	}
	if !filter.To.IsZero() {
		arguments = append(arguments, filter.To)
		conditions = append(conditions, fmt.Sprintf("occurred_at < $%d", len(arguments)))
	}
	if filter.Action != "" {
		arguments = append(arguments, filter.Action)
		conditions = append(conditions, fmt.Sprintf("action = $%d", len(arguments)))
	}
	if filter.Entity != "" {
		arguments = append(arguments, filter.Entity)
		conditions = append(conditions, fmt.Sprintf("entity = $%d", len(arguments)))
	}
	if filter.EntityID != "" {
		arguments = append(arguments, filter.EntityID)
		conditions = append(conditions, fmt.Sprintf("entity_id = $%d", len(arguments)))
	}
	where := ""
	if len(conditions) > 0 {
		where = " where " + strings.Join(conditions, " and ")
	}

	var total int
	if err := conn.QueryRow(ctx, "select count(*) from audit_log"+where, arguments...).Scan(&total); err != nil {
		return Page{}, fmt.Errorf("audit: count the trail: %w", err)
	}

	arguments = append(arguments, filter.Limit, filter.Offset)
	rows, err := conn.Query(ctx, `
		select id, occurred_at, action, entity, entity_id, operator, source, detail
		from audit_log`+where+fmt.Sprintf(` order by occurred_at desc, id desc limit $%d offset $%d`,
		len(arguments)-1, len(arguments)), arguments...)
	if err != nil {
		return Page{}, fmt.Errorf("audit: read the trail: %w", err)
	}
	defer rows.Close()

	page := Page{Total: total, Limit: filter.Limit, Offset: filter.Offset}
	for rows.Next() {
		var record Row
		var detail []byte
		if err := rows.Scan(&record.ID, &record.OccurredAt, &record.Action, &record.Entity,
			&record.EntityID, &record.Operator, &record.Source, &detail); err != nil {
			return Page{}, fmt.Errorf("audit: scan a trail row: %w", err)
		}
		if len(detail) > 0 {
			record.Detail = append(json.RawMessage(nil), detail...)
		}
		page.Records = append(page.Records, record)
	}
	if err := rows.Err(); err != nil {
		return Page{}, fmt.Errorf("audit: read the trail: %w", err)
	}
	return page, nil
}

// Actions lists the distinct actions in the trail, for the audit view filter.
func Actions(ctx context.Context, conn *pgx.Conn) ([]string, error) {
	rows, err := conn.Query(ctx, "select distinct action from audit_log order by action")
	if err != nil {
		return nil, fmt.Errorf("audit: list actions: %w", err)
	}
	defer rows.Close()
	actions := make([]string, 0, 8)
	for rows.Next() {
		var action string
		if err := rows.Scan(&action); err != nil {
			return nil, fmt.Errorf("audit: scan an action: %w", err)
		}
		actions = append(actions, action)
	}
	return actions, rows.Err()
}
