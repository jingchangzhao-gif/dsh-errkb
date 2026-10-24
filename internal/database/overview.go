package database

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// Overview queries back the pipe API's list and stats operations (EKB-035).
// They are read-only and deliberately thin: neither list ordering nor counts
// may influence retrieval or trust.

// ListSummary is one row of a list response.
type ListSummary struct {
	ID        string
	Title     string
	UpdatedAt time.Time
	Hits      int
	Status    string
	Fix       string
}

// StatsSummary is the aggregate view behind errkb_stats.
type StatsSummary struct {
	Events    int64
	Issues    int64
	Solutions int64
	Outcomes  int64
}

// listEnvironmentFilter turns an environment scope into the jsonb containment
// document and the connector name the list queries understand. Only the fields
// the caller set take part in the comparison; an empty scope means "no filter",
// so issues without occurrences still list.
func listEnvironmentFilter(environment Environment) (active bool, document, connector string) {
	document = "{}"
	filter := map[string]any{}
	if environment.OS != "" {
		filter["os"] = environment.OS
	}
	if environment.Shell != "" {
		filter["shell"] = environment.Shell
	}
	if len(environment.RuntimeVersions) > 0 {
		filter["runtime_versions"] = environment.RuntimeVersions
	}
	if len(filter) > 0 {
		encoded, err := json.Marshal(filter)
		if err != nil {
			return false, "{}", ""
		}
		document = string(encoded)
	}
	connector = environment.Connector
	return document != "{}" || connector != "", document, connector
}

// ListIssues pages issues by updated_at desc, id desc. The cursor is opaque
// outside this package: it is the last row of the previous page.
func ListIssues(ctx context.Context, conn *pgx.Conn, project, cursor string, limit int, environment Environment) ([]ListSummary, string, error) {
	if limit < 1 || limit > 100 {
		return nil, "", fmt.Errorf("list issues: limit must be between 1 and 100, got %d", limit)
	}
	cursorTime, cursorID, err := parseListCursor(cursor)
	if err != nil {
		return nil, "", err
	}
	active, environmentJSON, connector := listEnvironmentFilter(environment)
	rows, err := conn.Query(ctx, `
		select id::text, title, updated_at, hits, status, coalesce(fix.body, '')
		from issues
		left join lateral (select sv.body from solutions s join solution_versions sv on sv.solution_id = s.id where s.issue_id = issues.id order by sv.version desc limit 1) fix on true
		where ($1 = '' or project_key = $1)
		  and ($2::timestamptz is null or (updated_at, id) < ($2::timestamptz, $3::uuid))
		  and (not $5 or exists (
			select 1 from occurrences o join connectors c on c.id = o.connector_id
			where o.issue_id = issues.id and o.environment @> $6::jsonb and ($7 = '' or c.name = $7)))
		order by updated_at desc, id desc
		limit $4`,
		strings.TrimSpace(project), cursorTime, cursorID, limit, active, environmentJSON, connector)
	if err != nil {
		return nil, "", fmt.Errorf("list issues: %w", err)
	}
	defer rows.Close()
	items := make([]ListSummary, 0, limit)
	for rows.Next() {
		var item ListSummary
		if err := rows.Scan(&item.ID, &item.Title, &item.UpdatedAt, &item.Hits, &item.Status, &item.Fix); err != nil {
			return nil, "", fmt.Errorf("list issues: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("list issues: %w", err)
	}
	return items, nextListCursor(items, limit), nil
}

// ListSolutions pages solutions by their own updated_at. The title is the
// issue title: a solution has no title of its own in the schema, and inventing
// one here would drift from the stored facts.
func ListSolutions(ctx context.Context, conn *pgx.Conn, project, cursor string, limit int, environment Environment) ([]ListSummary, string, error) {
	if limit < 1 || limit > 100 {
		return nil, "", fmt.Errorf("list solutions: limit must be between 1 and 100, got %d", limit)
	}
	cursorTime, cursorID, err := parseListCursor(cursor)
	if err != nil {
		return nil, "", err
	}
	active, environmentJSON, connector := listEnvironmentFilter(environment)
	rows, err := conn.Query(ctx, `
		select s.id::text, i.title, s.updated_at, i.hits, s.status, coalesce(fix.body, '')
		from solutions s
		left join lateral (select sv.body from solution_versions sv where sv.solution_id = s.id order by sv.version desc limit 1) fix on true
		join issues i on i.id = s.issue_id
		where ($1 = '' or i.project_key = $1)
		  and ($2::timestamptz is null or (s.updated_at, s.id) < ($2::timestamptz, $3::uuid))
		  and (not $5 or exists (
			select 1 from occurrences o join connectors c on c.id = o.connector_id
			where o.issue_id = i.id and o.environment @> $6::jsonb and ($7 = '' or c.name = $7)))
		order by s.updated_at desc, s.id desc
		limit $4`,
		strings.TrimSpace(project), cursorTime, cursorID, limit, active, environmentJSON, connector)
	if err != nil {
		return nil, "", fmt.Errorf("list solutions: %w", err)
	}
	defer rows.Close()
	items := make([]ListSummary, 0, limit)
	for rows.Next() {
		var item ListSummary
		if err := rows.Scan(&item.ID, &item.Title, &item.UpdatedAt, &item.Hits, &item.Status, &item.Fix); err != nil {
			return nil, "", fmt.Errorf("list solutions: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("list solutions: %w", err)
	}
	return items, nextListCursor(items, limit), nil
}

// Stats counts the knowledge base. Retrieval latency and token savings are not
// measured by the store yet, so the caller must leave those at zero instead of
// inventing numbers.
func Stats(ctx context.Context, conn *pgx.Conn, project string) (StatsSummary, error) {
	project = strings.TrimSpace(project)
	var summary StatsSummary
	if err := conn.QueryRow(ctx, `
		select
			(select count(*) from issues where ($1 = '' or project_key = $1)),
			(select count(*) from events e
			 where $1 = '' or exists (
			 	select 1 from occurrences o where o.event_id = e.event_id and o.project_key = $1)),
			(select count(*) from solutions s
			 join issues i on i.id = s.issue_id
			 where $1 = '' or i.project_key = $1),
			(select count(*) from outcomes o
			 join issues i on i.id = o.issue_id
			 where $1 = '' or i.project_key = $1)`,
		project).Scan(&summary.Issues, &summary.Events, &summary.Solutions, &summary.Outcomes); err != nil {
		return StatsSummary{}, fmt.Errorf("stats: %w", err)
	}
	return summary, nil
}

// encodeListCursor stores the last row as "<RFC3339Nano>|<uuid>". The format
// is private to this file; the contract only promises opacity.
func encodeListCursor(item ListSummary) string {
	return fmt.Sprintf("%s|%s", item.UpdatedAt.UTC().Format(time.RFC3339Nano), item.ID)
}

func nextListCursor(items []ListSummary, limit int) string {
	if len(items) == 0 || len(items) < limit {
		return ""
	}
	return encodeListCursor(items[len(items)-1])
}

func parseListCursor(cursor string) (*time.Time, *string, error) {
	cursor = strings.TrimSpace(cursor)
	if cursor == "" {
		return nil, nil, nil
	}
	parts := strings.SplitN(cursor, "|", 2)
	if len(parts) != 2 || parts[1] == "" {
		return nil, nil, fmt.Errorf("list: malformed cursor")
	}
	nanos, err := time.Parse(time.RFC3339Nano, parts[0])
	if err != nil {
		return nil, nil, fmt.Errorf("list: malformed cursor: %w", err)
	}
	return &nanos, &parts[1], nil
}
