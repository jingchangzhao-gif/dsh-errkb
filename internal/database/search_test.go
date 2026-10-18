package database_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/jingchangzhao-gif/dsh-errkb/internal/contract"
	"github.com/jingchangzhao-gif/dsh-errkb/internal/core"
	"github.com/jingchangzhao-gif/dsh-errkb/internal/database"
	"github.com/jingchangzhao-gif/dsh-errkb/internal/redact"
)

// searchEventCounter keeps synthetic event ids apart inside one test binary.
var searchEventCounter atomic.Int64

// searchSpec is the subset of event fields the exact layer can search.
type searchSpec struct {
	message string
	code    string
	command string
	tool    string
	project string
}

// recordSearchEvent records one event and returns the issue it landed on.
func recordSearchEvent(t *testing.T, conn *pgx.Conn, connector, session string, spec searchSpec) database.RecordResult {
	t.Helper()
	normalized := core.Normalize(spec.message)
	transient := false
	confidence := 0.9
	event := contract.ErrorEvent{
		SchemaVersion:     "v1",
		EventID:           eventUUID(int(searchEventCounter.Add(1)) + 100000),
		ConnectorID:       connector,
		SessionID:         session,
		ObservedAt:        time.Now().UTC().Format(time.RFC3339),
		Channel:           "A",
		Category:          "tool",
		Severity:          "error",
		Transient:         &transient,
		Headline:          "headline for " + spec.message,
		NormalizedMessage: normalized,
		RedactedSample:    redact.Sample(spec.message, redact.Public),
		ErrorCode:         spec.code,
		Command:           spec.command,
		ToolName:          spec.tool,
		OS:                "windows",
		Shell:             "powershell",
		ProjectKey:        spec.project,
		Fingerprint:       core.Signature("tool", normalized),
		Evidence:          "test fixture",
		Confidence:        &confidence,
	}
	result, err := database.RecordEvent(context.Background(), conn, event)
	if err != nil {
		t.Fatalf("record search fixture: %v", err)
	}
	return result
}

func TestExactSearchHitsEveryDimension(t *testing.T) {
	ctx := context.Background()
	conn, _, connector, session := setupRecordSchema(t)

	const message = "EPERM renaming the package directory"
	result := recordSearchEvent(t, conn, connector, session, searchSpec{
		message: message,
		code:    "EPERM",
		command: "pnpm build",
		tool:    "bash",
		project: "proj",
	})
	normalized := core.Normalize(message)

	cases := []struct {
		name      string
		dimension database.SearchDimension
		value     string
		reason    string
	}{
		{"issue id", database.SearchByIssueID, result.IssueID, "issue_id"},
		{"fingerprint", database.SearchByFingerprint, result.Fingerprint, "exact_fingerprint"},
		{"error code", database.SearchByErrorCode, "EPERM", "error_code"},
		{"normalized message", database.SearchByNormalizedMessage, normalized, "deterministic_filter"},
		{"command", database.SearchByCommand, "pnpm build", "deterministic_filter"},
		{"tool name", database.SearchByToolName, "bash", "deterministic_filter"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			hits, err := database.ExactSearch(ctx, conn, database.ExactSearchQuery{
				Dimension: testCase.dimension,
				Value:     testCase.value,
			})
			if err != nil {
				t.Fatalf("exact search: %v", err)
			}
			if len(hits) != 1 {
				t.Fatalf("hits = %d, want 1", len(hits))
			}
			hit := hits[0]
			if hit.IssueID != result.IssueID {
				t.Errorf("issue id = %s, want %s", hit.IssueID, result.IssueID)
			}
			if hit.Fingerprint != result.Fingerprint {
				t.Errorf("fingerprint = %s, want %s", hit.Fingerprint, result.Fingerprint)
			}
			if hit.MatchReason != testCase.reason {
				t.Errorf("match reason = %q, want %q", hit.MatchReason, testCase.reason)
			}
			if hit.Approximate {
				t.Error("an exact hit must not be approximate")
			}
			if hit.Score != 1 {
				t.Errorf("score = %v, want 1", hit.Score)
			}
			if hit.Title == "" || hit.Status == "" {
				t.Errorf("hit is missing fields: %+v", hit)
			}
		})
	}
}

func TestExactSearchMissesEveryDimension(t *testing.T) {
	ctx := context.Background()
	conn, _, connector, session := setupRecordSchema(t)
	recordSearchEvent(t, conn, connector, session, searchSpec{
		message: "EPERM renaming the package directory",
		code:    "EPERM",
		command: "pnpm build",
		tool:    "bash",
		project: "proj",
	})

	cases := []struct {
		dimension database.SearchDimension
		value     string
	}{
		{database.SearchByIssueID, "11111111-1111-4111-8111-111111111111"},
		{database.SearchByFingerprint, "ffffffffffff"},
		{database.SearchByErrorCode, "ENOENT"},
		{database.SearchByNormalizedMessage, "eperm renaming another package"},
		{database.SearchByCommand, "pnpm test"},
		{database.SearchByToolName, "zsh"},
	}
	for _, testCase := range cases {
		t.Run(string(testCase.dimension), func(t *testing.T) {
			hits, err := database.ExactSearch(ctx, conn, database.ExactSearchQuery{
				Dimension: testCase.dimension,
				Value:     testCase.value,
			})
			if err != nil {
				t.Fatalf("a valid but unknown value must not be an error: %v", err)
			}
			if len(hits) != 0 {
				t.Fatalf("hits = %d, want an empty set", len(hits))
			}
		})
	}
}

// TestExactSearchIdentifiersAreExact pins the "no prefix" rule. Database ids
// are UUIDs and fingerprints are 12 hex characters, so a prefix is not a valid
// identifier at all: it is rejected as a bad argument instead of matching a
// different row (the "E-001 must not match E-0012" case). A substring of a
// message is treated the same way: no match, no error.
func TestExactSearchIdentifiersAreExact(t *testing.T) {
	ctx := context.Background()
	conn, _, connector, session := setupRecordSchema(t)
	const message = "EPERM renaming the package directory"
	result := recordSearchEvent(t, conn, connector, session, searchSpec{
		message: message, code: "EPERM", command: "pnpm build", tool: "bash", project: "proj",
	})

	_, err := database.ExactSearch(ctx, conn, database.ExactSearchQuery{
		Dimension: database.SearchByIssueID,
		Value:     result.IssueID[:18],
	})
	if err == nil || !strings.Contains(err.Error(), "not a UUID") {
		t.Fatalf("truncated issue id error = %v, want a rejected UUID", err)
	}

	_, err = database.ExactSearch(ctx, conn, database.ExactSearchQuery{
		Dimension: database.SearchByFingerprint,
		Value:     result.Fingerprint[:6],
	})
	if err == nil || !strings.Contains(err.Error(), "12 lowercase hex") {
		t.Fatalf("truncated fingerprint error = %v, want a rejected fingerprint", err)
	}

	hits, err := database.ExactSearch(ctx, conn, database.ExactSearchQuery{
		Dimension: database.SearchByNormalizedMessage,
		Value:     "eprem renaming",
	})
	if err != nil {
		t.Fatalf("a substring-shaped message must stay a normal lookup: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("a substring matched %d rows; the layer must compare whole values", len(hits))
	}
}

func TestExactSearchRejectsMalformedRequests(t *testing.T) {
	ctx := context.Background()
	conn, _, _, _ := setupRecordSchema(t)

	cases := []struct {
		name  string
		query database.ExactSearchQuery
		want  string
	}{
		{"unknown dimension", database.ExactSearchQuery{Dimension: "title", Value: "x"}, "unknown dimension"},
		{"empty value", database.ExactSearchQuery{Dimension: database.SearchByErrorCode}, "needs a value"},
		{"limit too high", database.ExactSearchQuery{Dimension: database.SearchByErrorCode, Value: "EPERM", Limit: 101}, "outside 1.."},
		{"limit negative", database.ExactSearchQuery{Dimension: database.SearchByErrorCode, Value: "EPERM", Limit: -1}, "outside 1.."},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := database.ExactSearch(ctx, conn, testCase.query)
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("error = %v, want it to contain %q", err, testCase.want)
			}
		})
	}

	sqlCases := []struct {
		name string
		sql  string
		want string
	}{
		{"no selector", `select 1 from errkb_search()`, "one lookup dimension is required"},
		{"two selectors", `select 1 from errkb_search(p_fingerprint := 'ffffffffffff', p_error_code := 'EPERM')`, "exactly one lookup dimension"},
		{"zero limit", `select 1 from errkb_search(p_fingerprint := 'ffffffffffff', p_limit := 0)`, "between 1 and 100"},
		{"null limit", `select 1 from errkb_search(p_fingerprint := 'ffffffffffff', p_limit := null::integer)`, "between 1 and 100"},
	}
	for _, testCase := range sqlCases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := conn.Exec(ctx, testCase.sql)
			if err == nil || !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("error = %v, want it to contain %q", err, testCase.want)
			}
		})
	}
}

func TestExactSearchProjectScopeAndLimit(t *testing.T) {
	ctx := context.Background()
	conn, _, connector, session := setupRecordSchema(t)

	first := recordSearchEvent(t, conn, connector, session, searchSpec{
		message: "first shared failure", code: "E_SHARED", command: "npm ci", tool: "npm", project: "alpha",
	})
	second := recordSearchEvent(t, conn, connector, session, searchSpec{
		message: "second shared failure", code: "E_SHARED", command: "npm ci", tool: "npm", project: "beta",
	})

	all, err := database.ExactSearch(ctx, conn, database.ExactSearchQuery{
		Dimension: database.SearchByErrorCode, Value: "E_SHARED", Limit: 100,
	})
	if err != nil {
		t.Fatalf("search by error code: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("hits = %d, want 2", len(all))
	}

	scoped, err := database.ExactSearch(ctx, conn, database.ExactSearchQuery{
		Dimension: database.SearchByErrorCode, Value: "E_SHARED", ProjectKey: "alpha",
	})
	if err != nil {
		t.Fatalf("search with project scope: %v", err)
	}
	if len(scoped) != 1 || scoped[0].IssueID != first.IssueID {
		t.Fatalf("scoped hits = %+v, want only %s", scoped, first.IssueID)
	}

	scoped, err = database.ExactSearch(ctx, conn, database.ExactSearchQuery{
		Dimension: database.SearchByCommand, Value: "npm ci", ProjectKey: "beta",
	})
	if err != nil {
		t.Fatalf("search by command with project scope: %v", err)
	}
	if len(scoped) != 1 || scoped[0].IssueID != second.IssueID {
		t.Fatalf("scoped hits = %+v, want only %s", scoped, second.IssueID)
	}

	limited, err := database.ExactSearch(ctx, conn, database.ExactSearchQuery{
		Dimension: database.SearchByErrorCode, Value: "E_SHARED", Limit: 1,
	})
	if err != nil {
		t.Fatalf("limited search: %v", err)
	}
	if len(limited) != 1 {
		t.Fatalf("limited hits = %d, want 1", len(limited))
	}
	if limited[0].IssueID != all[0].IssueID {
		t.Errorf("the limit must truncate the head of the ordered set: %s vs %s", limited[0].IssueID, all[0].IssueID)
	}
}

// TestExactSearchOrderIsDeterministic proves that equal inputs return the same
// sequence: the total order is hits desc, last_seen desc, id asc, and the id
// tiebreaker is unique.
func TestExactSearchOrderIsDeterministic(t *testing.T) {
	ctx := context.Background()
	conn, _, connector, session := setupRecordSchema(t)

	for i := 0; i < 5; i++ {
		recordSearchEvent(t, conn, connector, session, searchSpec{
			message: fmt.Sprintf("deterministic failure %d", i),
			code:    "E_STABLE",
			command: "make test",
			tool:    "make",
			project: "proj",
		})
	}

	query := database.ExactSearchQuery{
		Dimension: database.SearchByErrorCode, Value: "E_STABLE", Limit: 100,
	}
	all, err := database.ExactSearch(ctx, conn, query)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(all) != 5 {
		t.Fatalf("hits = %d, want 5", len(all))
	}
	sorted := append([]database.SearchHit(nil), all...)
	sort.Slice(sorted, func(a, b int) bool {
		if sorted[a].Hits != sorted[b].Hits {
			return sorted[a].Hits > sorted[b].Hits
		}
		if !sorted[a].LastSeen.Equal(sorted[b].LastSeen) {
			return sorted[a].LastSeen.After(sorted[b].LastSeen)
		}
		return sorted[a].IssueID < sorted[b].IssueID
	})
	for i := range all {
		if all[i].IssueID != sorted[i].IssueID {
			t.Fatalf("row %d = %s, want %s (order is not hits/last_seen/id)", i, all[i].IssueID, sorted[i].IssueID)
		}
	}

	limited := query
	limited.Limit = 2
	first, err := database.ExactSearch(ctx, conn, limited)
	if err != nil {
		t.Fatalf("first limited search: %v", err)
	}
	second, err := database.ExactSearch(ctx, conn, limited)
	if err != nil {
		t.Fatalf("second limited search: %v", err)
	}
	if len(first) != 2 || len(second) != 2 {
		t.Fatalf("limited hits = %d and %d, want 2", len(first), len(second))
	}
	for i := range first {
		if first[i].IssueID != second[i].IssueID {
			t.Fatalf("repeat call changed row %d: %s vs %s", i, first[i].IssueID, second[i].IssueID)
		}
		if first[i].IssueID != all[i].IssueID {
			t.Fatalf("limit row %d = %s, want %s", i, first[i].IssueID, all[i].IssueID)
		}
	}
}

type planNode struct {
	NodeType string     `json:"Node Type"`
	Relation string     `json:"Relation Name"`
	Plans    []planNode `json:"Plans"`
}

func (node planNode) walk(visit func(planNode)) {
	visit(node)
	for _, child := range node.Plans {
		child.walk(visit)
	}
}

// TestExactSearchUsesIndexes seeds enough rows for the planner to prefer
// indexes and checks every dimension's plan. The predicates are mirrored here
// and also checked against the live function text, so the migration cannot
// drift into an unindexed shape without failing this test.
func TestExactSearchUsesIndexes(t *testing.T) {
	ctx := context.Background()
	conn, _, connector, _ := setupRecordSchema(t)

	const fixedIssueID = "00000000-0000-4000-8000-000000000001"
	const fixedFingerprint = "000000000001"
	if _, err := conn.Exec(ctx, `
		insert into issues (id, fingerprint, project_key, title, category, source, actor)
		values ($1, $2, 'fixed', 'fixed issue', 'tool', 'test', 'test')`,
		fixedIssueID, fixedFingerprint); err != nil {
		t.Fatalf("insert fixed issue: %v", err)
	}
	if _, err := conn.Exec(ctx, `
		insert into issues (fingerprint, project_key, title, category, source, actor)
		select lpad(to_hex(g), 12, '0'), 'p', 'bulk issue ' || g, 'tool', 'test', 'test'
		from generate_series(2, 3000) g`); err != nil {
		t.Fatalf("insert bulk issues: %v", err)
	}
	if _, err := conn.Exec(ctx, `
		with inserted as (
			insert into events (connector_id, observed_at, channel, category, severity,
			                    headline, normalized_message, redacted_sample, error_code,
			                    command, tool_name, os, evidence, confidence, project_key,
			                    fingerprint, source, actor)
			select $1::uuid, now(), 'A', 'tool', 'error',
			       'headline ' || g, 'message ' || g, 'sample ' || g, 'ECODE-' || g,
			       'cmd ' || g, 'tool-' || g, 'windows', 'evidence', 0.9, 'p',
			       lpad(to_hex(g), 12, '0'), 'test', 'test'
			from generate_series(2, 3000) g
			returning event_id, fingerprint
		)
		insert into occurrences (issue_id, event_id, occurred_at, source, actor)
		select i.id, inserted.event_id, now(), 'test', 'test'
		from inserted
		join issues i on i.fingerprint = inserted.fingerprint and i.project_key = 'p'`,
		connector); err != nil {
		t.Fatalf("seed events: %v", err)
	}
	if _, err := conn.Exec(ctx, `analyze issues; analyze events; analyze occurrences`); err != nil {
		t.Fatalf("analyze: %v", err)
	}

	queries := map[string]string{
		"issue id":    `select id from issues where id = '` + fixedIssueID + `'`,
		"fingerprint": `select id from issues where fingerprint = '000000000002'`,
		"error code": `select i.id from events e join occurrences o on o.event_id = e.event_id
			join issues i on i.id = o.issue_id where e.error_code = 'ECODE-42'`,
		"normalized message": `select i.id from events e join occurrences o on o.event_id = e.event_id
			join issues i on i.id = o.issue_id where e.normalized_message = 'message 42'`,
		"command": `select i.id from events e join occurrences o on o.event_id = e.event_id
			join issues i on i.id = o.issue_id where e.command = 'cmd 42'`,
		"tool name": `select i.id from events e join occurrences o on o.event_id = e.event_id
			join issues i on i.id = o.issue_id where e.tool_name = 'tool-42'`,
	}
	sensitiveRelations := map[string]bool{"issues": true, "events": true, "occurrences": true}
	for name, query := range queries {
		t.Run(name, func(t *testing.T) {
			var raw string
			if err := conn.QueryRow(ctx, "explain (format json) "+query).Scan(&raw); err != nil {
				t.Fatalf("explain: %v", err)
			}
			var plans []struct {
				Plan planNode `json:"Plan"`
			}
			if err := json.Unmarshal([]byte(raw), &plans); err != nil {
				t.Fatalf("decode plan: %v", err)
			}
			if len(plans) == 0 {
				t.Fatal("explain returned no plan")
			}
			indexNodes := 0
			plans[0].Plan.walk(func(node planNode) {
				if strings.Contains(node.NodeType, "Index") {
					indexNodes++
				}
				if node.NodeType == "Seq Scan" && sensitiveRelations[node.Relation] {
					t.Errorf("the plan scans %q sequentially: %s", node.Relation, raw)
				}
			})
			if indexNodes == 0 {
				t.Errorf("the plan uses no index: %s", raw)
			}
		})
	}

	var definition string
	if err := conn.QueryRow(ctx,
		`select pg_get_functiondef('errkb_search(text,text,text,text,text,text,text,integer)'::regprocedure)`,
	).Scan(&definition); err != nil {
		t.Fatalf("read the function definition: %v", err)
	}
	for _, predicate := range []string{
		"i.id = p_issue_id::uuid",
		"i.fingerprint = p_fingerprint",
		"e.error_code = p_error_code",
		"e.normalized_message = p_normalized_message",
		"e.command = p_command",
		"e.tool_name = p_tool_name",
	} {
		if !strings.Contains(definition, predicate) {
			t.Errorf("the function no longer contains %q; update the EXPLAIN mirror", predicate)
		}
	}
	lowered := strings.ToLower(definition)
	for _, forbidden := range []string{" like ", "similarity(", "pg_trgm"} {
		if strings.Contains(lowered, forbidden) {
			t.Errorf("the exact layer contains fuzzy matching: %q", forbidden)
		}
	}
}
