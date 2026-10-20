package database_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/core"
	"github.com/jingchangzhao-gif/error-knowledge-base/internal/database"
)

// seedMarkdown reads the read-only compatibility sample. The file never
// changes; the importer has to accept it exactly as it is committed.
func seedMarkdown(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "seeds", "ERRORS.seed.md"))
	if err != nil {
		t.Fatalf("read seed: %v", err)
	}
	return string(data)
}

func importMarkdownDocument(t *testing.T, conn *pgx.Conn, text string) database.ImportReport {
	t.Helper()
	report, err := database.ImportMarkdown(context.Background(), conn, text)
	if err != nil {
		t.Fatalf("import markdown: %v", err)
	}
	return report
}

func exportMarkdownDocument(t *testing.T, conn *pgx.Conn) string {
	t.Helper()
	text, err := database.ExportMarkdown(context.Background(), conn)
	if err != nil {
		t.Fatalf("export markdown: %v", err)
	}
	return text
}

// markdownTestBlock builds one legacy-format entry. The fingerprint comes from
// the same pure function the importer uses, so the machine fields are honest.
func markdownTestBlock(id, status, raw, fix string) string {
	fingerprint := core.Signature("tool", core.Normalize(raw))
	return fmt.Sprintf(
		"## %s · %s\n"+
			"<!-- errkb: sig=%s cat=tool code=EPERM first=2026-10-01T00:00:00Z -->\n"+
			"\n"+
			"- Fingerprint: `%s`\n"+
			"- Category: `tool / EPERM`\n"+
			"- First seen: 2026-10-01 00:00 · Last seen: 2026-10-01 00:00 · Hits: 0\n"+
			"- Trigger: test trigger\n"+
			"- Raw message:\n"+
			"  ```text\n"+
			"  %s\n"+
			"  ```\n"+
			"- Fix:\n"+
			"  %s\n"+
			"- Status: `%s`\n"+
			"- Notes:\n",
		id, raw, fingerprint, fingerprint, raw, fix, status,
	)
}

func markdownTestDocument(blocks ...string) string {
	return "# ERRORS\n\n" + strings.Join(blocks, "\n")
}

func markdownRowCount(t *testing.T, conn *pgx.Conn, table string) int {
	t.Helper()
	var n int
	if err := conn.QueryRow(context.Background(), "select count(*) from "+table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// issueVersionBodies returns every stored solution version for one fingerprint,
// in ascending version order.
func issueVersionBodies(t *testing.T, conn *pgx.Conn, fingerprint string) []string {
	t.Helper()
	rows, err := conn.Query(context.Background(), `
		select v.body
		from solution_versions v
		join solutions s on s.id = v.solution_id
		join issues i on i.id = s.issue_id
		where i.fingerprint = $1
		order by v.version
	`, fingerprint)
	if err != nil {
		t.Fatalf("read versions: %v", err)
	}
	defer rows.Close()
	bodies := []string{}
	for rows.Next() {
		var body string
		if err := rows.Scan(&body); err != nil {
			t.Fatalf("scan version: %v", err)
		}
		bodies = append(bodies, body)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("versions: %v", err)
	}
	return bodies
}

func TestParseMarkdownSeedMatchesTheLegacyFormat(t *testing.T) {
	entries, err := database.ParseMarkdown(seedMarkdown(t))
	if err != nil {
		t.Fatalf("parse seed: %v", err)
	}
	if len(entries) != 3 {
		t.Fatalf("seed entries = %d, want 3", len(entries))
	}
	wantIDs := []string{"E-0001", "E-0002", "E-0003"}
	for i, entry := range entries {
		if entry.ID != wantIDs[i] {
			t.Errorf("entry %d ID = %q, want %q", i, entry.ID, wantIDs[i])
		}
		if entry.Status != "fixed" {
			t.Errorf("%s status = %q, want fixed", entry.ID, entry.Status)
		}
		if entry.Raw == "" || entry.Fix == "" || entry.Title == "" {
			t.Errorf("%s lost a required field: title=%q raw=%q fix=%q", entry.ID, entry.Title, entry.Raw, entry.Fix)
		}
		computed := core.Signature(entry.CategoryName(), core.Normalize(entry.Raw))
		if entry.Fingerprint != computed {
			t.Errorf("%s fingerprint = %q, signature(category, raw) = %q", entry.ID, entry.Fingerprint, computed)
		}
		if len(entry.History) != 0 {
			t.Errorf("%s seed entry has %d history revisions, want 0", entry.ID, len(entry.History))
		}
	}
	if entries[2].CategoryName() != "tool" || entries[2].ErrorCode() != "EPERM" {
		t.Errorf("E-0003 category/code = %q/%q, want tool/EPERM", entries[2].CategoryName(), entries[2].ErrorCode())
	}
}

func TestParseMarkdownAcceptsChineseLabels(t *testing.T) {
	raw := "EACCES: permission denied, open '<path>/cache'"
	doc := "# ERRORS\n\n## E-0001 · [tool] EACCES: permission denied\n" +
		fmt.Sprintf("<!-- errkb: sig=%s cat=tool code=EACCES first=2026-10-01T00:00:00Z -->\n", core.Signature("tool", core.Normalize(raw))) +
		"\n- 指纹: `" + core.Signature("tool", core.Normalize(raw)) + "`\n" +
		"- 分类: `tool / EACCES`\n" +
		"- 首次: 2026-10-01 00:00 · 最近: 2026-10-01 00:00 · 命中: 3\n" +
		"- 触发: 中文标签条目\n" +
		"- 原始信息:\n  ```text\n  " + raw + "\n  ```\n" +
		"- 解法:\n  修好权限后重启。\n" +
		"- 状态: `wontfix`\n" +
		"- 备注:\n  中文备注。\n"
	entries, err := database.ParseMarkdown(doc)
	if err != nil {
		t.Fatalf("parse chinese labels: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("entries = %d, want 1", len(entries))
	}
	entry := entries[0]
	if entry.Raw != raw || entry.Fix != "修好权限后重启。" || entry.Notes != "中文备注。" {
		t.Errorf("chinese fields = raw %q fix %q notes %q", entry.Raw, entry.Fix, entry.Notes)
	}
	if entry.Hits != 3 || entry.Status != "wontfix" {
		t.Errorf("hits/status = %d/%q", entry.Hits, entry.Status)
	}
}

func TestParseMarkdownRejectsMalformedDocuments(t *testing.T) {
	good := markdownTestBlock("E-0001", "open", "EPERM: one", "fix one")
	second := markdownTestBlock("E-0002", "open", "EPERM: two", "fix two")
	cases := map[string]string{
		"conflict marker":  "# ERRORS\n\n<<<<<<< HEAD\n" + good,
		"malformed header": "# ERRORS\n\n## E-0001\n<!-- errkb: -->\n",
		"duplicate id":     markdownTestDocument(good, strings.Replace(second, "E-0002", "E-0001", 1)),
		"missing meta":     strings.Replace(good, "<!-- errkb:", "<!-- not-errkb:", 1),
		"unknown status":   strings.Replace(good, "`open`", "`closed`", 1),
		"unterminated raw": strings.Replace(good, "  ```\n- Fix:", "  ```text\n- Fix:", 1),
	}
	for name, doc := range cases {
		if _, err := database.ParseMarkdown(doc); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestImportSeedPopulatesTheDatabase(t *testing.T) {
	conn, _, _, _ := setupRecordSchema(t)
	report := importMarkdownDocument(t, conn, seedMarkdown(t))
	if report.Entries != 3 || report.IssuesCreated != 3 || report.VersionsAdded != 3 {
		t.Fatalf("import report = %+v, want 3 entries / 3 issues / 3 versions", report)
	}
	for table, want := range map[string]int{
		"issues": 3, "events": 3, "occurrences": 3, "solutions": 3, "solution_versions": 3,
	} {
		if got := markdownRowCount(t, conn, table); got != want {
			t.Errorf("%s rows = %d, want %d", table, got, want)
		}
	}
	entries, err := database.ParseMarkdown(seedMarkdown(t))
	if err != nil {
		t.Fatal(err)
	}
	bodies := issueVersionBodies(t, conn, entries[0].Fingerprint)
	if len(bodies) != 1 || bodies[0] != entries[0].Fix {
		t.Errorf("E-0001 versions = %q, want [%q]", bodies, entries[0].Fix)
	}
}

func TestImportIsIdempotent(t *testing.T) {
	conn, _, _, _ := setupRecordSchema(t)
	seed := seedMarkdown(t)
	importMarkdownDocument(t, conn, seed)
	report := importMarkdownDocument(t, conn, seed)
	if report.IssuesCreated != 0 || report.IssuesUpdated != 3 || report.VersionsAdded != 0 {
		t.Fatalf("second import report = %+v", report)
	}
	for table, want := range map[string]int{
		"issues": 3, "events": 3, "occurrences": 3, "solutions": 3, "solution_versions": 3,
	} {
		if got := markdownRowCount(t, conn, table); got != want {
			t.Errorf("%s rows after re-import = %d, want %d", table, got, want)
		}
	}
}

func TestMarkdownRoundTripPreservesFields(t *testing.T) {
	conn, _, _, _ := setupRecordSchema(t)
	importMarkdownDocument(t, conn, seedMarkdown(t))
	first := exportMarkdownDocument(t, conn)

	original, err := database.ParseMarkdown(seedMarkdown(t))
	if err != nil {
		t.Fatal(err)
	}
	exported, err := database.ParseMarkdown(first)
	if err != nil {
		t.Fatalf("exported document does not parse: %v", err)
	}
	if len(exported) != len(original) {
		t.Fatalf("exported entries = %d, want %d", len(exported), len(original))
	}
	byFingerprint := map[string]database.MarkdownEntry{}
	for _, entry := range exported {
		byFingerprint[entry.Fingerprint] = entry
	}
	for i := range original {
		got, ok := byFingerprint[original[i].Fingerprint]
		if !ok {
			t.Errorf("entry %d fingerprint %q is missing from the export", i, original[i].Fingerprint)
			continue
		}
		if got.Raw != original[i].Raw || got.Fix != original[i].Fix || got.Notes != original[i].Notes {
			t.Errorf("entry %d lost text: raw %q fix %q notes %q", i, got.Raw, got.Fix, got.Notes)
		}
		if got.Status != original[i].Status {
			t.Errorf("entry %d status %q != %q", i, got.Status, original[i].Status)
		}
	}

	// A second schema imports the exported document; exporting again must be
	// byte-identical, which is the field-level round-trip proof.
	conn2, _, _, _ := setupRecordSchema(t)
	importMarkdownDocument(t, conn2, first)
	second := exportMarkdownDocument(t, conn2)
	if second != first {
		t.Fatalf("export after re-import differs from the first export")
	}
}

func TestImportPreservesSolutionHistory(t *testing.T) {
	conn, _, _, _ := setupRecordSchema(t)
	importMarkdownDocument(t, conn, seedMarkdown(t))
	exported := exportMarkdownDocument(t, conn)

	edited := strings.Replace(exported, "Shrink what is sent instead of retrying it unchanged:", "Shrink what is sent, hand edited:", 1)
	if edited == exported {
		t.Fatal("fixture edit did not change the document")
	}
	importMarkdownDocument(t, conn, edited)
	withHistory := exportMarkdownDocument(t, conn)
	if !strings.Contains(withHistory, "<!-- errkb-history: v1 -->") {
		t.Fatalf("export does not carry the older version behind a history marker")
	}
	if !strings.Contains(withHistory, "Shrink what is sent, hand edited:") {
		t.Fatalf("export does not carry the newest fix")
	}

	entries, err := database.ParseMarkdown(exported)
	if err != nil {
		t.Fatal(err)
	}
	_ = entries
	contextFingerprint := core.Signature("llm", core.Normalize("CONTEXT_OVERFLOW: the request exceeds the model's context window"))
	if contextFingerprint != "88fe8fb1a189" {
		t.Fatalf("fixture fingerprint = %q, want the seed fingerprint", contextFingerprint)
	}
	bodies := issueVersionBodies(t, conn, contextFingerprint)
	if len(bodies) != 2 {
		t.Fatalf("context entries versions = %d, want 2", len(bodies))
	}
	if !strings.HasPrefix(bodies[0], "Shrink what is sent instead of retrying it unchanged:") {
		t.Errorf("version 1 = %q, want the original body", bodies[0])
	}
	if !strings.HasPrefix(bodies[1], "Shrink what is sent, hand edited:") {
		t.Errorf("version 2 = %q, want the edited body", bodies[1])
	}

	// The exported document must rebuild the same history in a fresh schema.
	conn2, _, _, _ := setupRecordSchema(t)
	importMarkdownDocument(t, conn2, withHistory)
	rebuilt := issueVersionBodies(t, conn2, contextFingerprint)
	if len(rebuilt) != 2 || rebuilt[0] != bodies[0] || rebuilt[1] != bodies[1] {
		t.Fatalf("rebuilt history = %q, want %q", rebuilt, bodies)
	}

	// A repeated import of the same document is still idempotent.
	report := importMarkdownDocument(t, conn, withHistory)
	if report.VersionsAdded != 0 {
		t.Errorf("re-import added %d versions, want 0", report.VersionsAdded)
	}
}

func TestHandEditWinsOnTheNextImport(t *testing.T) {
	conn, _, _, _ := setupRecordSchema(t)
	importMarkdownDocument(t, conn, seedMarkdown(t))
	exported := exportMarkdownDocument(t, conn)
	edited := strings.Replace(exported, "- Trigger: A long session, or a tool result that pasted a large file into the conversation",
		"- Trigger: hand edited trigger", 1)
	edited = strings.Replace(edited, "Seed entry. The message text is illustrative; providers word this error differently.",
		"Seed entry, hand edited notes.", 1)
	if edited == exported {
		t.Fatal("fixture edit did not change the document")
	}
	importMarkdownDocument(t, conn, edited)

	var trigger, risks, body string
	err := conn.QueryRow(context.Background(), `
		select i.trigger, v.risks, v.body
		from issues i
		join solutions s on s.issue_id = i.id
		join solution_versions v on v.solution_id = s.id
		where i.fingerprint = '88fe8fb1a189'
		order by v.version desc
		limit 1
	`).Scan(&trigger, &risks, &body)
	if err != nil {
		t.Fatalf("read edited entry: %v", err)
	}
	if trigger != "hand edited trigger" {
		t.Errorf("trigger = %q, want the hand edit", trigger)
	}
	if risks != "Seed entry, hand edited notes." {
		t.Errorf("notes/risks = %q, want the hand edit", risks)
	}
}

func TestImportMapsTheThreeLegacyStatuses(t *testing.T) {
	conn, _, _, _ := setupRecordSchema(t)
	doc := markdownTestDocument(
		markdownTestBlock("E-0001", "open", "EPERM: alpha", "fix alpha"),
		markdownTestBlock("E-0002", "fixed", "EPERM: beta", "fix beta"),
		markdownTestBlock("E-0003", "wontfix", "EPERM: gamma", "fix gamma"),
	)
	importMarkdownDocument(t, conn, doc)

	rows, err := conn.Query(context.Background(), `
		select i.status, s.status
		from issues i
		join solutions s on s.issue_id = i.id
		order by i.fingerprint
	`)
	if err != nil {
		t.Fatalf("read statuses: %v", err)
	}
	defer rows.Close()
	gotIssue := []string{}
	gotSolution := []string{}
	for rows.Next() {
		var issueStatus, solutionStatus string
		if err := rows.Scan(&issueStatus, &solutionStatus); err != nil {
			t.Fatal(err)
		}
		gotIssue = append(gotIssue, issueStatus)
		gotSolution = append(gotSolution, solutionStatus)
	}
	sort.Strings(gotIssue)
	sort.Strings(gotSolution)
	wantIssue := []string{"open", "resolved", "wontfix"}
	wantSolution := []string{"blocked", "candidate", "verified"}
	if strings.Join(gotIssue, ",") != strings.Join(wantIssue, ",") {
		t.Errorf("issue statuses = %v, want %v", gotIssue, wantIssue)
	}
	if strings.Join(gotSolution, ",") != strings.Join(wantSolution, ",") {
		t.Errorf("solution statuses = %v, want %v", gotSolution, wantSolution)
	}

	exported := exportMarkdownDocument(t, conn)
	for _, status := range []string{"open", "fixed", "wontfix"} {
		if !strings.Contains(exported, "- Status: `"+status+"`") {
			t.Errorf("export lost the %q status", status)
		}
	}
}

func TestImportRejectsBackwardsStatusChange(t *testing.T) {
	conn, _, _, _ := setupRecordSchema(t)
	importMarkdownDocument(t, conn, markdownTestDocument(markdownTestBlock("E-0001", "fixed", "EPERM: one", "fix one")))
	_, err := database.ImportMarkdown(context.Background(), conn,
		markdownTestDocument(markdownTestBlock("E-0001", "open", "EPERM: one", "fix one")))
	if err == nil {
		t.Fatal("importing a verified solution as open must fail")
	}
	if !strings.Contains(err.Error(), "cannot move a solution backwards") {
		t.Errorf("error = %v, want the backwards-transition explanation", err)
	}
}

func TestImportRedactsBeforeWriting(t *testing.T) {
	conn, _, _, _ := setupRecordSchema(t)
	needle := "sk" + "-" + "abcdefghijklmnopqrstuvwx"
	raw := "request failed with key " + needle + " while opening <path>/cache"
	block := markdownTestBlock("E-0001", "open", raw, "rotate the key "+needle)
	block = strings.Replace(block, "- Trigger: test trigger", "- Trigger: leaked "+needle, 1)
	importMarkdownDocument(t, conn, markdownTestDocument(block))

	rows, err := conn.Query(context.Background(), `
		select headline, normalized_message, redacted_sample from events
		union all
		select title, trigger, '' from issues
		union all
		select body, risks, '' from solution_versions
	`)
	if err != nil {
		t.Fatalf("read stored text: %v", err)
	}
	defer rows.Close()
	sawRedacted := false
	for rows.Next() {
		var a, b, c string
		if err := rows.Scan(&a, &b, &c); err != nil {
			t.Fatal(err)
		}
		for _, value := range []string{a, b, c} {
			if strings.Contains(value, needle) {
				t.Fatalf("stored text still contains the secret: %q", value)
			}
			if strings.Contains(value, "<secret>") {
				sawRedacted = true
			}
		}
	}
	if !sawRedacted {
		t.Error("no stored text carries the <secret> placeholder")
	}
}

func TestExportMarkdownParsesInTypeScriptStore(t *testing.T) {
	conn, _, _, _ := setupRecordSchema(t)
	importMarkdownDocument(t, conn, seedMarkdown(t))
	exported := exportMarkdownDocument(t, conn)

	path := filepath.Join(t.TempDir(), "ERRORS.export.md")
	if err := os.WriteFile(path, []byte(exported), 0o644); err != nil {
		t.Fatalf("write export: %v", err)
	}
	result := runTypeScriptStoreParser(t, path)
	if result.Blocks != 3 {
		t.Errorf("src/store.ts parsed %d blocks, want 3", result.Blocks)
	}
	for i, status := range result.Statuses {
		if status != "fixed" {
			t.Errorf("block %d status = %q, want fixed", i, status)
		}
	}
}

type storeParseResult struct {
	Blocks   int      `json:"blocks"`
	IDs      []string `json:"ids"`
	Statuses []string `json:"statuses"`
}

const storeParserProbe = `import { readFileSync } from "node:fs";
import { parseDocument } from "../../../src/store.ts";

const text = readFileSync(process.argv[2], "utf8");
const document = parseDocument(text);
console.log(JSON.stringify({
	blocks: document.blocks.length,
	ids: document.blocks.map(({ entry }) => entry.id),
	statuses: document.blocks.map(({ entry }) => entry.status),
}));
`

// runTypeScriptStoreParser bundles a probe that imports src/store.ts and runs it
// with node. The check is skipped where pnpm or node is unavailable, because the
// Go CI job has no Node toolchain; the compatibility proof itself is required
// locally wherever the plugin toolchain exists.
func runTypeScriptStoreParser(t *testing.T, markdownPath string) storeParseResult {
	t.Helper()
	pnpm, pnpmErr := exec.LookPath("pnpm")
	if pnpmErr != nil {
		pnpm, pnpmErr = exec.LookPath("pnpm.cmd")
	}
	node, nodeErr := exec.LookPath("node")
	if pnpmErr != nil || nodeErr != nil {
		t.Skip("pnpm or node is unavailable; the TypeScript parser check requires the plugin toolchain")
	}
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}
	workDir := filepath.Join(repoRoot, "node_modules", ".cache", "errkb-md-tscheck")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatalf("probe dir: %v", err)
	}
	probe := filepath.Join(workDir, "probe.ts")
	if err := os.WriteFile(probe, []byte(storeParserProbe), 0o644); err != nil {
		t.Fatalf("write probe: %v", err)
	}
	outDir := filepath.Join(workDir, "out")
	build := exec.Command(pnpm, "exec", "tsdown", probe, "--out-dir", outDir, "--format", "esm", "--no-dts", "--no-config")
	build.Dir = repoRoot
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("bundle src/store.ts probe: %v\n%s", err, output)
	}
	bundles, err := filepath.Glob(filepath.Join(outDir, "probe.*"))
	if err != nil || len(bundles) == 0 {
		t.Fatalf("bundler emitted no probe bundle (err=%v)", err)
	}
	run := exec.Command(node, bundles[0], markdownPath)
	run.Dir = repoRoot
	output, err := run.Output()
	if err != nil {
		t.Fatalf("run src/store.ts probe: %v", err)
	}
	var result storeParseResult
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatalf("parse probe output %q: %v", output, err)
	}
	return result
}

// The machine fields must survive the round trip: the device and project keys
// are part of the contract, not decoration.
func TestRoundTripKeepsDeviceAndProject(t *testing.T) {
	conn, _, _, _ := setupRecordSchema(t)
	raw := "EPERM: operation not permitted, rename '<path>/node_modules'"
	fingerprint := core.Signature("tool", core.Normalize(raw))
	lines := []string{
		"# ERRORS",
		"",
		"## E-0001 · [tool] EPERM: rename",
		fmt.Sprintf("<!-- errkb: sig=%s cat=tool code=EPERM first=2026-10-01T00:00:00Z device=DESKTOP-A proj=proj-alpha -->", fingerprint),
		"",
		"- Fingerprint: `" + fingerprint + "`",
		"- Category: `tool / EPERM`",
		"- First seen: 2026-10-01 00:00 · Last seen: 2026-10-01 00:00 · Hits: 4",
		"- Trigger: t",
		"- Raw message:",
		"  ```text",
		"  " + raw,
		"  ```",
		"- Fix:",
		"  f",
		"- Status: `open`",
		"- Notes:",
		"",
	}
	doc := strings.Join(lines, "\n")
	importMarkdownDocument(t, conn, doc)
	exported := exportMarkdownDocument(t, conn)
	if !strings.Contains(exported, "device=DESKTOP-A") {
		t.Errorf("export lost the device field")
	}
	if !strings.Contains(exported, "proj=proj-alpha") {
		t.Errorf("export lost the project field")
	}
	if !strings.Contains(exported, "Hits: 4") {
		t.Errorf("export lost the hit count")
	}
	entries, err := database.ParseMarkdown(exported)
	if err != nil {
		t.Fatal(err)
	}
	if entries[0].Hits != 4 || entries[0].Meta["proj"] != "proj-alpha" {
		t.Errorf("round-trip entry = hits %d proj %q", entries[0].Hits, entries[0].Meta["proj"])
	}
}

// The exported document must not contain a Markdown-authority marker: the
// database stays the only place that answers "what is the latest state".
func TestExportHasNoReadAuthorityMarker(t *testing.T) {
	conn, _, _, _ := setupRecordSchema(t)
	importMarkdownDocument(t, conn, seedMarkdown(t))
	exported := exportMarkdownDocument(t, conn)
	for _, forbidden := range []string{"authoritative", "read_authority", "source-of-truth"} {
		if strings.Contains(strings.ToLower(exported), forbidden) {
			t.Errorf("export claims authority with %q", forbidden)
		}
	}
	if !regexp.MustCompile(`(?m)^## E-\d{4} · `).MatchString(exported) {
		t.Error("export does not use the legacy entry header shape")
	}
}
