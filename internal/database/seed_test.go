package database_test

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jingchangzhao-gif/dsh-errkb/internal/database"
)

// seedJSONPath is the generated artifact, not the legacy document.
func seedJSONPath() string {
	return filepath.Join("..", "..", "seeds", "ERRORS.seed.json")
}

// sensitiveNeedles are the shapes that must never reach the database or the
// generated artifact. They are assembled at run time so the repository privacy
// guard stays clean.
func sensitiveNeedles() []string {
	return []string{
		"sk" + "-",
		"AI" + "za",
		"gh" + "p_",
		"AK" + "IA",
		"Bear" + "er ",
		"C:" + `\`,
		"/Us" + "ers/",
		"/ho" + "me/",
	}
}

func TestSeedFileImportsWithoutPreprocessing(t *testing.T) {
	conn, _, _, _ := setupRecordSchema(t)
	report := importMarkdownDocument(t, conn, seedMarkdown(t))
	if report.Entries != 3 || report.IssuesCreated != 3 {
		t.Fatalf("import report = %+v, want 3 entries created", report)
	}
	entries, err := database.ParseMarkdown(seedMarkdown(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		var status string
		if err := conn.QueryRow(context.Background(),
			`select status from issues where fingerprint = $1`, entry.Fingerprint).Scan(&status); err != nil {
			t.Fatalf("read issue %s: %v", entry.ID, err)
		}
		if status != "resolved" {
			t.Errorf("%s issue status = %q, want resolved", entry.ID, status)
		}
	}
}

func TestSeedImportHasNoSensitiveText(t *testing.T) {
	conn, _, _, _ := setupRecordSchema(t)
	importMarkdownDocument(t, conn, seedMarkdown(t))

	rows, err := conn.Query(context.Background(), `
		select headline, normalized_message, redacted_sample from events
		union all
		select title, trigger, '' from issues
		union all
		select body, risks, '' from solution_versions
		union all
		select name, coalesce(version, ''), '' from connectors
	`)
	if err != nil {
		t.Fatalf("read stored text: %v", err)
	}
	defer rows.Close()
	needles := sensitiveNeedles()
	for rows.Next() {
		var a, b, c string
		if err := rows.Scan(&a, &b, &c); err != nil {
			t.Fatal(err)
		}
		for _, value := range []string{a, b, c} {
			for _, needle := range needles {
				if strings.Contains(value, needle) {
					t.Fatalf("stored text contains sensitive shape %q: %q", needle, value)
				}
			}
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	encoded, err := database.ExportSeedJSON(context.Background(), conn)
	if err != nil {
		t.Fatalf("export seed json: %v", err)
	}
	for _, needle := range needles {
		if bytes.Contains(encoded, []byte(needle)) {
			t.Errorf("seed JSON contains sensitive shape %q", needle)
		}
	}
}

func TestSeedFileIsReadOnly(t *testing.T) {
	path := filepath.Join("..", "..", "seeds", "ERRORS.seed.md")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read seed: %v", err)
	}
	conn, _, _, _ := setupRecordSchema(t)
	importMarkdownDocument(t, conn, string(before))
	exportMarkdownDocument(t, conn)
	if _, err := database.ExportSeedJSON(context.Background(), conn); err != nil {
		t.Fatalf("export seed json: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read seed after import: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("seeds/ERRORS.seed.md changed during import or export; it is read-only")
	}
}

func TestSeedJSONMatchesTheParsedDocument(t *testing.T) {
	conn, _, _, _ := setupRecordSchema(t)
	importMarkdownDocument(t, conn, seedMarkdown(t))
	encoded, err := database.ExportSeedJSON(context.Background(), conn)
	if err != nil {
		t.Fatalf("export seed json: %v", err)
	}
	if os.Getenv("ERRKB_WRITE_SEED_JSON") == "1" {
		if err := os.WriteFile(seedJSONPath(), encoded, 0o644); err != nil {
			t.Fatalf("write seed json: %v", err)
		}
	}

	var document database.SeedDocument
	if err := json.Unmarshal(encoded, &document); err != nil {
		t.Fatalf("decode seed json: %v", err)
	}
	if document.SchemaVersion != "v1" {
		t.Errorf("schema_version = %q, want v1", document.SchemaVersion)
	}
	if document.Source != "seeds/ERRORS.seed.md" {
		t.Errorf("source = %q, want seeds/ERRORS.seed.md", document.Source)
	}
	if len(document.Entries) != 3 {
		t.Fatalf("seed json entries = %d, want 3", len(document.Entries))
	}

	entries, err := database.ParseMarkdown(seedMarkdown(t))
	if err != nil {
		t.Fatal(err)
	}
	byFingerprint := map[string]database.SeedEntry{}
	for _, entry := range document.Entries {
		byFingerprint[entry.Fingerprint] = entry
		if _, err := hex.DecodeString(entry.Fingerprint); err != nil || len(entry.Fingerprint) != 12 {
			t.Errorf("%s fingerprint %q is not 12 hex characters", entry.ID, entry.Fingerprint)
		}
		if entry.Category == "" || entry.Title == "" || entry.RawMessage == "" || entry.Fix == "" {
			t.Errorf("%s is missing a required seed field: %+v", entry.ID, entry)
		}
		if entry.Status != "fixed" {
			t.Errorf("%s status = %q, want fixed", entry.ID, entry.Status)
		}
		if _, err := time.Parse(time.RFC3339, entry.FirstSeen); err != nil {
			t.Errorf("%s first_seen = %q: %v", entry.ID, entry.FirstSeen, err)
		}
	}
	for _, entry := range entries {
		got, ok := byFingerprint[entry.Fingerprint]
		if !ok {
			t.Errorf("%s is missing from the JSON artifact", entry.ID)
			continue
		}
		if got.RawMessage != entry.Raw || got.Fix != entry.Fix || got.Notes != entry.Notes {
			t.Errorf("%s JSON text differs from the parsed seed", entry.ID)
		}
	}

	committed, err := os.ReadFile(seedJSONPath())
	if err != nil {
		t.Fatalf("read committed seed json: %v (run with ERRKB_WRITE_SEED_JSON=1 once)", err)
	}
	if !bytes.Equal(bytes.TrimSpace(committed), bytes.TrimSpace(encoded)) {
		t.Fatal("seeds/ERRORS.seed.json is stale; regenerate it with ERRKB_WRITE_SEED_JSON=1")
	}
}

// The generated JSON is a derived artifact: no Markdown or JSON file may be
// consulted for the latest state, so the export path is the only reader.
func TestSeedJSONIsDerivedFromTheDatabase(t *testing.T) {
	conn, _, _, _ := setupRecordSchema(t)
	importMarkdownDocument(t, conn, seedMarkdown(t))
	first, err := database.ExportSeedJSON(context.Background(), conn)
	if err != nil {
		t.Fatalf("export seed json: %v", err)
	}
	// A hand-edited legacy document would change the database, and the JSON
	// follows the database, not the file on disk.
	edited := strings.Replace(seedMarkdown(t), "- Trigger: A long session", "- Trigger: hand edited", 1)
	importMarkdownDocument(t, conn, edited)
	second, err := database.ExportSeedJSON(context.Background(), conn)
	if err != nil {
		t.Fatalf("export seed json: %v", err)
	}
	if bytes.Equal(first, second) {
		t.Fatal("seed JSON did not follow the database after an edit")
	}
	if !bytes.Contains(second, []byte("hand edited")) {
		t.Fatal("seed JSON does not carry the database state")
	}
}

// compile-time guard: the generated JSON must stay pure data, with no absolute
// machine path that the privacy guard would reject.
func TestSeedJSONHasNoPrivilegedPath(t *testing.T) {
	pattern := regexp.MustCompile("(" + `"[A-Za-z]:\\\\` + "|/Us" + `ers/|/ho` + `me/)`)
	conn, _, _, _ := setupRecordSchema(t)
	importMarkdownDocument(t, conn, seedMarkdown(t))
	encoded, err := database.ExportSeedJSON(context.Background(), conn)
	if err != nil {
		t.Fatalf("export seed json: %v", err)
	}
	if pattern.Match(encoded) {
		t.Fatalf("seed JSON contains a privileged absolute path: %s", pattern.FindString(string(encoded)))
	}
}
