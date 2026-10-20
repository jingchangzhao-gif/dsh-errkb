package service

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jingchangzhao-gif/dsh-errkb/internal/contract"
	"github.com/jingchangzhao-gif/dsh-errkb/internal/core"
	"github.com/jingchangzhao-gif/dsh-errkb/internal/database"
)

// searchTestPool builds a pool whose connections see a throwaway schema. It is
// the same isolation the database tests use, recreated here because the search
// API is the unit under test.
func searchTestPool(t *testing.T) (*pgxpool.Pool, *pgx.Conn, func()) {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv("ERRKB_TEST_DATABASE_URL"))
	if dsn == "" {
		t.Skip("ERRKB_TEST_DATABASE_URL is not set; the search integration test needs PostgreSQL")
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse test DSN: %v", err)
	}
	schema := fmt.Sprintf("errkb_search_%d", time.Now().UnixNano())
	connect := func(ctx context.Context) (*pgx.Conn, error) {
		connConfig := config.ConnConfig.Copy()
		connConfig.RuntimeParams["search_path"] = schema + ",public"
		return pgx.ConnectConfig(ctx, connConfig)
	}
	setup, err := connect(context.Background())
	if err != nil {
		t.Fatalf("connect test database: %v", err)
	}
	if _, err := setup.Exec(context.Background(), "create schema "+schema); err != nil {
		t.Fatalf("create test schema: %v", err)
	}
	if _, err := database.Migrate(context.Background(), setup, "../../db/migrations"); err != nil {
		t.Fatalf("migrate test schema: %v", err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema + ",public"
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("open search pool: %v", err)
	}
	return pool, setup, func() {
		pool.Close()
		_, _ = setup.Exec(context.Background(), "drop schema "+schema+" cascade")
		_ = setup.Close(context.Background())
	}
}

func insertSearchConnector(t *testing.T, conn *pgx.Conn) (string, string) {
	t.Helper()
	ctx := context.Background()
	connectorID, err := database.EnsureConnector(ctx, conn, "search-test", "B", "test")
	if err != nil {
		t.Fatalf("ensure connector: %v", err)
	}
	sessionID, err := database.EnsureSession(ctx, conn, connectorID, "search-session", "search-test")
	if err != nil {
		t.Fatalf("ensure session: %v", err)
	}
	return connectorID, sessionID
}

var searchEventNumber int

func recordSearchEvent(t *testing.T, conn *pgx.Conn, connectorID, sessionID, message, code, command string) contract.ErrorEvent {
	t.Helper()
	searchEventNumber++
	eventID := fmt.Sprintf("00000000-0000-4000-8000-%012d", searchEventNumber)
	message = strings.TrimSpace(message)
	normalized := core.Normalize(message)
	headline := core.ExtractHeadline(message)
	transient := false
	confidence := 0.5
	event := contract.ErrorEvent{
		SchemaVersion:     contract.SchemaVersion,
		EventID:           eventID,
		ConnectorID:       connectorID,
		SessionID:         sessionID,
		ObservedAt:        time.Now().UTC().Format(time.RFC3339),
		Channel:           "B",
		Category:          "tool",
		Severity:          "error",
		Transient:         &transient,
		Headline:          headline.Line,
		NormalizedMessage: normalized,
		RedactedSample:    message,
		ErrorCode:         code,
		Command:           command,
		ToolName:          "search-test",
		OS:                "windows",
		ProjectKey:        "search-test",
		Fingerprint:       core.Signature("tool", normalized),
		Evidence:          "search integration test",
		Confidence:        &confidence,
	}
	if _, err := database.RecordEvent(context.Background(), conn, event); err != nil {
		t.Fatalf("record search event: %v", err)
	}
	return event
}

func TestSearchFindsTheFourInputKinds(t *testing.T) {
	pool, setup, cleanup := searchTestPool(t)
	defer cleanup()
	connectorID, sessionID := insertSearchConnector(t, setup)

	first := recordSearchEvent(t, setup, connectorID, sessionID,
		"EPERM: operation not permitted, rename 'C:/repo/package.json'", "EPERM", "pnpm install")
	recordSearchEvent(t, setup, connectorID, sessionID,
		"ENOENT: no such file or directory, open 'C:/repo/missing.ts'", "ENOENT", "node build.js")

	search := NewSearchService(pool)
	ctx := context.Background()

	// Kind 1: fingerprint. Exact, not approximate, and the reason says so.
	results, err := search.Search(ctx, first.Fingerprint)
	if err != nil {
		t.Fatalf("search by fingerprint: %v", err)
	}
	if results.Kind != SearchInputIssue {
		t.Fatalf("fingerprint kind = %q, want %q", results.Kind, SearchInputIssue)
	}
	if len(results.Hits) == 0 {
		t.Fatal("fingerprint lookup returned no hits")
	}
	if results.Hits[0].Approximate || results.Hits[0].ReasonLabel != "exact fingerprint (same failure)" {
		t.Fatalf("fingerprint hit = %+v, want an exact same-failure reason", results.Hits[0])
	}
	if results.Hits[0].IssueID == "" {
		t.Fatal("fingerprint hit has no issue id")
	}

	// Kind 2: the issue UUID that the fingerprint lookup just showed.
	byID, err := search.Search(ctx, results.Hits[0].IssueID)
	if err != nil {
		t.Fatalf("search by issue id: %v", err)
	}
	if len(byID.Hits) != 1 || byID.Hits[0].IssueID != results.Hits[0].IssueID {
		t.Fatalf("issue lookup hits = %+v, want exactly the one issue", byID.Hits)
	}

	// Kind 3: error code. It is a shared code, so the row must stay
	// approximate even though the code matches exactly.
	byCode, err := search.Search(ctx, "EPERM")
	if err != nil {
		t.Fatalf("search by error code: %v", err)
	}
	if byCode.Kind != SearchInputErrorCode {
		t.Fatalf("error code kind = %q, want %q", byCode.Kind, SearchInputErrorCode)
	}
	if len(byCode.Hits) == 0 {
		t.Fatal("error code lookup returned no hits")
	}
	if !byCode.Hits[0].Approximate {
		t.Fatalf("error code hit = %+v, want approximate=true", byCode.Hits[0])
	}

	// Kind 4: free text finds the same issue through the token shortlist.
	byText, err := search.Search(ctx, "operation not permitted")
	if err != nil {
		t.Fatalf("search by text: %v", err)
	}
	if byText.Kind != SearchInputText {
		t.Fatalf("text kind = %q, want %q", byText.Kind, SearchInputText)
	}
	if len(byText.Hits) == 0 {
		t.Fatal("text lookup returned no hits")
	}

	// Kind 5: a command-shaped query is classified as a command and still
	// resolves to the issue that recorded it.
	byCommand, err := search.Search(ctx, "pnpm install")
	if err != nil {
		t.Fatalf("search by command: %v", err)
	}
	if byCommand.Kind != SearchInputCommand {
		t.Fatalf("command kind = %q, want %q", byCommand.Kind, SearchInputCommand)
	}
	if len(byCommand.Hits) == 0 {
		t.Fatal("command lookup returned no hits")
	}
	if len(byCommand.Hits) > MaxOmniboxHits {
		t.Fatalf("command hits = %d, want at most %d", len(byCommand.Hits), MaxOmniboxHits)
	}
}

func TestSearchEmptyStateExplainsItself(t *testing.T) {
	pool, setup, cleanup := searchTestPool(t)
	defer cleanup()
	connectorID, sessionID := insertSearchConnector(t, setup)
	recordSearchEvent(t, setup, connectorID, sessionID,
		"EPERM: operation not permitted, rename 'C:/repo/package.json'", "EPERM", "")

	search := NewSearchService(pool)
	results, err := search.Search(context.Background(), "something nobody recorded")
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results.Hits) != 0 {
		t.Fatalf("unexpected hits: %+v", results.Hits)
	}
}
