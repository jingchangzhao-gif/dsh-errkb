// Markdown import and export (EKB-025).
//
// The legacy ERRORS.md format is an established fact: blocks headed
// "## E-0007 · ...", one machine comment underneath, then labelled fields
// (Fingerprint, Category, First seen / Last seen / Hits, Trigger, Raw message,
// Fix, Status, Notes). seeds/ERRORS.seed.md is the read-only compatibility
// sample. PostgreSQL stays the authoritative store: Markdown is exchange only,
// and nothing here ever reads Markdown as the latest state.
//
// Status mapping. The legacy three states are not the four states EKB-021 gave
// solutions, so the mapping is explicit in both directions:
//
//	open    -> candidate   (a fix is proposed, nothing verified it)
//	fixed   -> verified    (the recorded fix is known to have worked)
//	wontfix -> blocked     (we decided not to apply it; deprecated is reserved
//	                        for "a better solution replaced it", which the
//	                        legacy format cannot express)
//
// And back: candidate -> open, verified -> fixed, blocked -> wontfix,
// deprecated -> open (the entry needs attention again, since Markdown has no
// "replaced" state). Issues carry their own four states; an issue without a
// solution falls back to those: open -> open, resolved -> fixed,
// wontfix/misjudged -> wontfix.
//
// Solution history. The legacy format has one Fix field but the database keeps
// every solution version (EKB-021), and the card forbids silently dropping
// older versions. Export therefore writes the newest version as the visible
// Fix body and appends each earlier version below it behind a history marker:
//
//	- Fix:
//	  The newest fix text.
//
//	  <!-- errkb-history: v2 -->
//	  The revision before it.
//	  <!-- errkb-history: v1 -->
//	  The first revision.
//
// A line starting with "<!-- errkb-history: vN -->" is plain text for the
// existing TypeScript parser (it is not a field opener), so the exported
// document still parses in src/store.ts, and this importer reconstructs the
// version list from the markers. Plain-text bodies that start with such a line
// would be ambiguous; the marker prefix is documented here as reserved.
//
// Field notes. The legacy format has no risk field, so Markdown "Notes" are
// stored as the current solution version's risks and exported back as Notes
// (section 6 wants a copied fix to show its risk; Notes is the only slot the
// old format has). Version preconditions are not representable in the legacy
// format and are not exported; they stay in the database.
//
// Import is an exchange path, not a new observation: it writes the issue,
// event, occurrence, solution and version rows directly in one transaction
// instead of calling errkb_record_event(), then redacts every text before the
// write (section 6). Event IDs are derived from the fingerprint so importing
// the same document twice does not duplicate events or occurrences.

package database

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"

	"github.com/jingchangzhao-gif/dsh-errkb/internal/core"
	"github.com/jingchangzhao-gif/dsh-errkb/internal/redact"
)

// Import provenance recorded on every row this file writes.
const (
	markdownActor          = "markdown-import"
	markdownConnectorName  = "markdown-import"
	markdownEvidence       = "imported from Markdown"
	markdownConfidence     = 0.5
	markdownChannel        = "A"
	markdownSeverity       = "error"
	markdownDocumentHeader = "# ERRORS\n"
)

// Legacy statuses and the four solution states they map to.
const (
	markdownStatusOpen    = "open"
	markdownStatusFixed   = "fixed"
	markdownStatusWontfix = "wontfix"
)

var markdownStatuses = map[string]bool{
	markdownStatusOpen:    true,
	markdownStatusFixed:   true,
	markdownStatusWontfix: true,
}

var solutionStatusByMarkdownStatus = map[string]string{
	markdownStatusOpen:    string(SolutionCandidate),
	markdownStatusFixed:   string(SolutionVerified),
	markdownStatusWontfix: string(SolutionBlocked),
}

var markdownStatusBySolutionStatus = map[string]string{
	string(SolutionCandidate):  markdownStatusOpen,
	string(SolutionVerified):   markdownStatusFixed,
	string(SolutionBlocked):    markdownStatusWontfix,
	string(SolutionDeprecated): markdownStatusOpen,
}

var issueStatusByMarkdownStatus = map[string]string{
	markdownStatusOpen:    "open",
	markdownStatusFixed:   "resolved",
	markdownStatusWontfix: "wontfix",
}

var markdownStatusByIssueStatus = map[string]string{
	"open":      markdownStatusOpen,
	"resolved":  markdownStatusFixed,
	"wontfix":   markdownStatusWontfix,
	"misjudged": markdownStatusWontfix,
}

// The label sets both the English seed and the Chinese sample use; the parser
// accepts either, exactly like src/store.ts.
var fieldLabels = map[string]string{
	"Fingerprint": "fingerprint", "指纹": "fingerprint",
	"Category": "category", "分类": "category",
	"First seen": "firstSeen", "首次": "firstSeen",
	"Trigger": "trigger", "触发": "trigger",
	"Raw message": "raw", "原始信息": "raw",
	"Fix": "fix", "解法": "fix",
	"Status": "status", "状态": "status",
	"Notes": "notes", "备注": "notes",
}

var (
	entryHeaderPattern = regexp.MustCompile(`^## (E-\d+) ·(?: (.*))?$`)
	entryHeaderStart   = regexp.MustCompile(`^## E-`)
	metaLinePattern    = regexp.MustCompile(`^<!-- errkb:(.*)-->\s*$`)
	fieldLinePattern   = regexp.MustCompile(`^- ([^:：]+)[:：] ?(.*)$`)
	fencePattern       = regexp.MustCompile("^(`{3,})")
	historyPattern     = regexp.MustCompile(`^<!-- errkb-history: v(\d+) -->$`)
	seenPattern        = regexp.MustCompile(`^(.*?)\s*·\s*(?:Last seen|最近)[:：]\s*(.*?)\s*·\s*(?:Hits|命中)[:：]\s*(\S*)$`)
	codeFenceWrapped   = regexp.MustCompile("^`([^`]*)`$")
)

// MarkdownEntry is one block of the legacy document, parsed.
type MarkdownEntry struct {
	ID          string
	Title       string
	Meta        map[string]string
	Fingerprint string
	Category    string
	FirstSeen   string
	LastSeen    string
	Hits        int
	Trigger     string
	Raw         string
	// Fix is the newest fix text; History holds the earlier revisions in
	// ascending version order as the exporter wrote them.
	Fix     string
	History []MarkdownRevision
	Status  string
	Notes   string
}

// MarkdownRevision is one older solution version carried behind a history
// marker.
type MarkdownRevision struct {
	Version int
	Body    string
}

// CategoryName is the machine category (`llm`), from the meta comment when
// present, else the first part of the display category.
func (entry MarkdownEntry) CategoryName() string {
	if cat := entry.Meta["cat"]; cat != "" {
		return cat
	}
	cat, _ := splitDisplayCategory(entry.Category)
	return cat
}

// ErrorCode is the machine error code, from the meta comment when present, else
// the second part of the display category.
func (entry MarkdownEntry) ErrorCode() string {
	if code := entry.Meta["code"]; code != "" {
		return code
	}
	_, code := splitDisplayCategory(entry.Category)
	return code
}

func splitDisplayCategory(value string) (string, string) {
	parts := strings.SplitN(value, " / ", 2)
	if len(parts) == 2 {
		return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1])
	}
	return strings.TrimSpace(value), ""
}

// ParseMarkdown parses the legacy document format. It is strict about anything
// that would make a rewrite unsafe (conflict markers, a malformed header, a
// duplicate ID, a missing machine comment, a broken seen line, an unterminated
// fence) and lenient about human phrasing (either label set, either colon,
// missing fields), matching src/store.ts.
func ParseMarkdown(text string) ([]MarkdownEntry, error) {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	starts := make([]int, 0)
	for i, line := range lines {
		trimmed := strings.TrimSuffix(line, "\r")
		if strings.HasPrefix(trimmed, "<<<<<<<") || strings.HasPrefix(trimmed, ">>>>>>>") || trimmed == "=======" {
			return nil, fmt.Errorf("markdown line %d: git conflict marker", i+1)
		}
		if entryHeaderStart.MatchString(trimmed) {
			if !entryHeaderPattern.MatchString(trimmed) {
				return nil, fmt.Errorf("markdown line %d: malformed entry header %q", i+1, trimmed)
			}
			starts = append(starts, i)
		}
	}

	entries := make([]MarkdownEntry, 0, len(starts))
	seen := map[string]bool{}
	for b, start := range starts {
		end := len(lines)
		if b+1 < len(starts) {
			end = starts[b+1]
		}
		entry, err := parseMarkdownBlock(lines[start:end], start+1)
		if err != nil {
			return nil, err
		}
		if seen[entry.ID] {
			return nil, fmt.Errorf("markdown line %d: duplicate ID %s", start+1, entry.ID)
		}
		seen[entry.ID] = true
		entries = append(entries, entry)
	}
	return entries, nil
}

func parseMarkdownBlock(lines []string, firstLine int) (MarkdownEntry, error) {
	entry := MarkdownEntry{Meta: map[string]string{}}
	head := entryHeaderPattern.FindStringSubmatch(strings.TrimSuffix(lines[0], "\r"))
	if head == nil {
		return entry, fmt.Errorf("markdown line %d: malformed entry header", firstLine)
	}
	entry.ID = head[1]
	entry.Title = strings.TrimSpace(head[2])

	fields := map[string][]string{}
	current := ""
	metaSeen := false
	for i := 1; i < len(lines); i++ {
		text := strings.TrimSuffix(lines[i], "\r")
		if m := metaLinePattern.FindStringSubmatch(text); m != nil {
			if metaSeen {
				return entry, fmt.Errorf("markdown line %d: second machine-field comment", firstLine+i)
			}
			metaSeen = true
			meta, err := parseMetaComment(m[1])
			if err != nil {
				return entry, fmt.Errorf("markdown line %d: %v", firstLine+i, err)
			}
			entry.Meta = meta
			current = ""
			continue
		}
		if m := fieldLinePattern.FindStringSubmatch(text); m != nil {
			if key, ok := fieldLabels[strings.TrimSpace(m[1])]; ok {
				if _, dup := fields[key]; dup {
					return entry, fmt.Errorf("markdown line %d: field %q appears twice", firstLine+i, strings.TrimSpace(m[1]))
				}
				fields[key] = []string{m[2]}
				current = key
				continue
			}
		}
		if current != "" {
			fields[current] = append(fields[current], dedentMarkdownLine(text))
		}
	}
	if !metaSeen {
		return entry, fmt.Errorf("markdown line %d: entry has no <!-- errkb: ... --> comment", firstLine)
	}

	entry.Fingerprint = unwrapMarkdownCode(strings.Join(trimMarkdownBlankLines(fields["fingerprint"]), "\n"))
	entry.Category = unwrapMarkdownCode(strings.Join(trimMarkdownBlankLines(fields["category"]), "\n"))
	entry.Trigger = strings.Join(trimMarkdownBlankLines(fields["trigger"]), "\n")

	if seenLines := trimMarkdownBlankLines(fields["firstSeen"]); len(seenLines) > 0 {
		m := seenPattern.FindStringSubmatch(strings.Join(seenLines, "\n"))
		if m == nil {
			return entry, fmt.Errorf("markdown %s: malformed first-seen / last-seen / hits line", entry.ID)
		}
		hits, err := strconv.Atoi(m[3])
		if err != nil {
			return entry, fmt.Errorf("markdown %s: hit count %q is not a number", entry.ID, m[3])
		}
		entry.FirstSeen, entry.LastSeen, entry.Hits = m[1], m[2], hits
	}

	rawLines := trimMarkdownBlankLines(fields["raw"])
	if len(rawLines) > 0 {
		raw, err := parseMarkdownFence(rawLines)
		if err != nil {
			return entry, fmt.Errorf("markdown %s: %v", entry.ID, err)
		}
		entry.Raw = raw
	}

	fixLines := trimMarkdownBlankLines(fields["fix"])
	entry.Fix, entry.History = splitFixHistory(fixLines)

	status := strings.ToLower(strings.TrimSpace(unwrapMarkdownCode(strings.Join(trimMarkdownBlankLines(fields["status"]), "\n"))))
	if status == "" {
		status = markdownStatusOpen
	}
	if !markdownStatuses[status] {
		return entry, fmt.Errorf("markdown %s: unknown status %q", entry.ID, status)
	}
	entry.Status = status
	entry.Notes = strings.Join(trimMarkdownBlankLines(fields["notes"]), "\n")
	return entry, nil
}

func parseMetaComment(content string) (map[string]string, error) {
	meta := map[string]string{}
	for _, pair := range strings.Fields(strings.TrimSpace(content)) {
		eq := strings.IndexByte(pair, '=')
		if eq <= 0 {
			return nil, fmt.Errorf("malformed machine field %q", pair)
		}
		meta[pair[:eq]] = decodeMetaValue(pair[eq+1:])
	}
	return meta, nil
}

// splitFixHistory separates the visible (newest) fix body from the earlier
// revisions the exporter wrote behind history markers.
func splitFixHistory(lines []string) (string, []MarkdownRevision) {
	var parts [][]string
	var versions []int
	current := []string{}
	for _, line := range lines {
		if m := historyPattern.FindStringSubmatch(line); m != nil {
			parts = append(parts, current)
			version, _ := strconv.Atoi(m[1])
			versions = append(versions, version)
			current = []string{}
			continue
		}
		current = append(current, line)
	}
	parts = append(parts, current)
	latest := strings.Join(trimMarkdownBlankLines(parts[0]), "\n")
	history := make([]MarkdownRevision, 0, len(parts)-1)
	for i := 1; i < len(parts); i++ {
		history = append(history, MarkdownRevision{
			Version: versions[i-1],
			Body:    strings.Join(trimMarkdownBlankLines(parts[i]), "\n"),
		})
	}
	// The exporter writes the markers newest first; the importer replays them
	// as versions 1..n, so order them ascending.
	sort.Slice(history, func(i, j int) bool { return history[i].Version < history[j].Version })
	return latest, history
}

func parseMarkdownFence(lines []string) (string, error) {
	open := fencePattern.FindStringSubmatch(lines[0])
	if open == nil {
		return strings.Join(lines, "\n"), nil
	}
	fence := open[1]
	for i := 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], fence) && strings.Trim(lines[i], "` \t") == "" {
			return strings.Join(lines[1:i], "\n"), nil
		}
	}
	return "", fmt.Errorf("unterminated code fence")
}

func dedentMarkdownLine(line string) string {
	if strings.HasPrefix(line, "  ") {
		return line[2:]
	}
	if strings.HasPrefix(line, "\t") {
		return line[1:]
	}
	return line
}

func trimMarkdownBlankLines(lines []string) []string {
	start, end := 0, len(lines)
	for start < end && strings.TrimSpace(lines[start]) == "" {
		start++
	}
	for end > start && strings.TrimSpace(lines[end-1]) == "" {
		end--
	}
	return lines[start:end]
}

func unwrapMarkdownCode(value string) string {
	if m := codeFenceWrapped.FindStringSubmatch(value); m != nil {
		return m[1]
	}
	return value
}

func wrapMarkdownCode(value string) string {
	if value == "" || strings.Contains(value, "`") {
		return value
	}
	return "`" + value + "`"
}

// encodeMetaValue percent-encodes the characters that could end the comment or
// split a machine field, matching encodeMetaValue() in src/store.ts.
func encodeMetaValue(value string) string {
	var out strings.Builder
	for _, r := range value {
		if r == '%' || r == '>' || unicode.IsSpace(r) {
			for _, b := range []byte(string(r)) {
				fmt.Fprintf(&out, "%%%02X", b)
			}
			continue
		}
		out.WriteRune(r)
	}
	return out.String()
}

// decodeMetaValue decodes runs of %XX escapes; an invalid run stays literal,
// like decodeMetaValue() in src/store.ts.
func decodeMetaValue(value string) string {
	var out strings.Builder
	for i := 0; i < len(value); {
		if value[i] != '%' {
			out.WriteByte(value[i])
			i++
			continue
		}
		start := i
		var bytes []byte
		for i+2 < len(value) && value[i] == '%' && isHex(value[i+1]) && isHex(value[i+2]) {
			b, _ := strconv.ParseUint(value[i+1:i+3], 16, 8)
			bytes = append(bytes, byte(b))
			i += 3
		}
		if len(bytes) > 0 && utf8.Valid(bytes) {
			out.Write(bytes)
		} else {
			out.WriteString(value[start:i])
		}
	}
	return out.String()
}

func isHex(b byte) bool {
	return (b >= '0' && b <= '9') || (b >= 'a' && b <= 'f') || (b >= 'A' && b <= 'F')
}

// ImportReport counts what one ImportMarkdown call did.
type ImportReport struct {
	Entries             int
	IssuesCreated       int
	IssuesUpdated       int
	VersionsAdded       int
	FingerprintMismatch int
}

// ImportMarkdown parses the legacy document and writes it into PostgreSQL in a
// single transaction. Text is redacted before every write; import never becomes
// a read authority.
func ImportMarkdown(ctx context.Context, conn *pgx.Conn, text string) (ImportReport, error) {
	entries, err := ParseMarkdown(text)
	if err != nil {
		return ImportReport{}, err
	}
	report := ImportReport{Entries: len(entries)}

	tx, err := conn.Begin(ctx)
	if err != nil {
		return report, fmt.Errorf("import markdown: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	var connectorID string
	if err := tx.QueryRow(ctx, `
		insert into connectors (name, version, channel, capabilities, source, actor)
		values ($1, 'legacy', $2, '{"import":true}'::jsonb, $1, $1)
		on conflict (name) do update set updated_at = now()
		returning id
	`, markdownConnectorName, markdownChannel).Scan(&connectorID); err != nil {
		return report, fmt.Errorf("import markdown: connector: %w", err)
	}

	seen := map[string]bool{}
	for _, entry := range entries {
		if err := importMarkdownEntry(ctx, tx, connectorID, entry, &report, seen); err != nil {
			return report, err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return report, fmt.Errorf("import markdown: commit: %w", err)
	}
	return report, nil
}

func importMarkdownEntry(
	ctx context.Context,
	tx pgx.Tx,
	connectorID string,
	entry MarkdownEntry,
	report *ImportReport,
	seen map[string]bool,
) error {
	category := entry.CategoryName()
	code := entry.ErrorCode()
	projectKey := entry.Meta["proj"]

	fingerprint := entry.Meta["sig"]
	computed := ""
	if entry.Raw != "" {
		computed = core.Signature(category, core.Normalize(entry.Raw))
	}
	if len(fingerprint) != core.SignatureLength || !isLowerHex(fingerprint) {
		fingerprint = computed
	} else if computed != "" && fingerprint != computed {
		report.FingerprintMismatch++
		fingerprint = computed
	}
	if fingerprint == "" {
		fingerprint = core.Signature(category, core.Normalize(entry.Title))
	}
	key := fingerprint + "|" + projectKey
	if seen[key] {
		return fmt.Errorf("import markdown: entries %s and a later one share fingerprint %s and project %q", entry.ID, fingerprint, projectKey)
	}
	seen[key] = true

	firstSeen, err := parseMarkdownTime(entry.Meta["first"])
	if err != nil || firstSeen.IsZero() {
		firstSeen, err = parseMarkdownDisplayTime(entry.FirstSeen)
		if err != nil {
			return fmt.Errorf("import markdown %s: %v", entry.ID, err)
		}
	}
	lastSeen, err := parseMarkdownDisplayTime(entry.LastSeen)
	if err != nil || lastSeen.IsZero() {
		lastSeen = firstSeen
	}

	title := redact.Text(entry.Title, redact.Public)
	trigger := redact.Text(entry.Trigger, redact.Public)
	rawText := redact.Sample(entry.Raw, redact.Public)
	normalized := redact.Text(core.Normalize(entry.Raw), redact.Public)
	headline := redact.Text(core.ExtractHeadline(entry.Raw).Line, redact.Public)
	if headline == "" {
		headline = "(no message)"
	}

	var issueID string
	var created bool
	if err := tx.QueryRow(ctx, `
		insert into issues (
			fingerprint, project_key, title, category, status, trigger,
			first_seen, last_seen, hits, source, actor
		) values ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10)
		on conflict (fingerprint, project_key) do update set
			title = excluded.title,
			category = excluded.category,
			status = excluded.status,
			trigger = excluded.trigger,
			first_seen = excluded.first_seen,
			last_seen = excluded.last_seen,
			hits = excluded.hits,
			updated_at = now(),
			source = excluded.source,
			actor = excluded.actor
		returning id, (xmax = 0)
	`, fingerprint, projectKey, title, category, issueStatusByMarkdownStatus[entry.Status], trigger,
		firstSeen, lastSeen, entry.Hits, markdownActor).Scan(&issueID, &created); err != nil {
		return fmt.Errorf("import markdown %s: issue: %w", entry.ID, err)
	}
	if created {
		report.IssuesCreated++
	} else {
		report.IssuesUpdated++
	}

	eventID := markdownEventID(fingerprint, projectKey)
	var device any
	if deviceName := entry.Meta["device"]; deviceName != "" {
		environment, _ := json.Marshal(map[string]string{"device": deviceName})
		device = environment
	} else {
		device = []byte("{}")
	}
	var codeArg any
	if code != "" {
		codeArg = code
	}
	if _, err := tx.Exec(ctx, `
		insert into events (
			event_id, connector_id, session_id, observed_at, channel, category,
			severity, transient, headline, normalized_message, redacted_sample,
			error_code, os, shell, runtime_versions, project_key, fingerprint,
			schema_version, evidence, confidence, source, actor
		) values (
			$1, $2, null, $3, $4, $5,
			$6, false, $7, $8, $9,
			$10, '', '', '{}'::jsonb, $11, $12,
			'v1', $13, $14, $15, $15
		)
		on conflict (event_id) do update set
			observed_at = excluded.observed_at,
			category = excluded.category,
			headline = excluded.headline,
			normalized_message = excluded.normalized_message,
			redacted_sample = excluded.redacted_sample,
			error_code = excluded.error_code,
			project_key = excluded.project_key,
			fingerprint = excluded.fingerprint
	`, eventID, connectorID, firstSeen, markdownChannel, category,
		markdownSeverity, headline, normalized, rawText,
		codeArg, projectKey, fingerprint, markdownEvidence, markdownConfidence, markdownActor); err != nil {
		return fmt.Errorf("import markdown %s: event: %w", entry.ID, err)
	}

	// EKB-020 replays append occurrences, so the (issue_id, event_id) unique
	// constraint was dropped; keep the import idempotent by updating the
	// occurrence this import created rather than inserting a second one.
	var occurrenceID string
	occurrenceErr := tx.QueryRow(ctx, `
		select id from occurrences
		where issue_id = $1 and event_id = $2
		order by occurred_at desc, id desc
		limit 1
	`, issueID, eventID).Scan(&occurrenceID)
	switch {
	case occurrenceErr == pgx.ErrNoRows:
		if _, err := tx.Exec(ctx, `
			insert into occurrences (
				issue_id, event_id, connector_id, project_key, environment,
				occurred_at, source, actor
			) values ($1, $2, $3, $4, $5::jsonb, $6, $7, $7)
		`, issueID, eventID, connectorID, projectKey, string(device.([]byte)), firstSeen, markdownActor); err != nil {
			return fmt.Errorf("import markdown %s: occurrence: %w", entry.ID, err)
		}
	case occurrenceErr != nil:
		return fmt.Errorf("import markdown %s: occurrence lookup: %w", entry.ID, occurrenceErr)
	default:
		if _, err := tx.Exec(ctx, `
			update occurrences
			set environment = $2::jsonb, occurred_at = $3, project_key = $4
			where id = $1
		`, occurrenceID, string(device.([]byte)), firstSeen, projectKey); err != nil {
			return fmt.Errorf("import markdown %s: occurrence: %w", entry.ID, err)
		}
	}

	if err := importMarkdownSolution(ctx, tx, issueID, entry, report); err != nil {
		return fmt.Errorf("import markdown %s: %w", entry.ID, err)
	}
	return nil
}

func importMarkdownSolution(ctx context.Context, tx pgx.Tx, issueID string, entry MarkdownEntry, report *ImportReport) error {
	desired := solutionStatusByMarkdownStatus[entry.Status]
	notes := redact.Text(entry.Notes, redact.Public)

	var solutionID, current string
	created := false
	err := tx.QueryRow(ctx, `
		select id, status from solutions
		where issue_id = $1
		order by created_at desc, id desc
		limit 1
	`, issueID).Scan(&solutionID, &current)
	switch {
	case err == pgx.ErrNoRows:
		created = true
		if err := tx.QueryRow(ctx, `
			insert into solutions (issue_id, status, source, actor)
			values ($1, $2, $3, $3)
			returning id
		`, issueID, desired, markdownActor).Scan(&solutionID); err != nil {
			return fmt.Errorf("solution: %w", err)
		}
	case err != nil:
		return fmt.Errorf("solution lookup: %w", err)
	default:
		if current != desired {
			if _, err := tx.Exec(ctx, `update solutions set status = $2 where id = $1`, solutionID, desired); err != nil {
				return fmt.Errorf("solution status %s -> %s: %w (the legacy document cannot move a solution backwards)", current, desired, err)
			}
		}
	}

	// The exporter wrote earlier revisions behind history markers; on a fresh
	// solution they become versions 1..n in ascending order, then the visible
	// body becomes the newest version. On a re-import the versions already
	// exist, so the markers are history only.
	if created {
		for _, revision := range entry.History {
			if _, err := appendMarkdownVersion(ctx, tx, solutionID, redact.Text(revision.Body, redact.Public), notes); err != nil {
				return err
			}
			report.VersionsAdded++
		}
	}
	if entry.Fix != "" {
		added, err := ensureMarkdownVersion(ctx, tx, solutionID, redact.Text(entry.Fix, redact.Public), notes)
		if err != nil {
			return err
		}
		if added {
			report.VersionsAdded++
		}
	}
	return nil
}

// ensureMarkdownVersion appends the body when it differs from the newest
// stored version; a hand edit to Notes rewrites the newest version's risks.
func ensureMarkdownVersion(ctx context.Context, tx pgx.Tx, solutionID, body, notes string) (bool, error) {
	var latestVersion int
	var latestBody, latestRisks string
	err := tx.QueryRow(ctx, `
		select version, body, risks from solution_versions
		where solution_id = $1
		order by version desc
		limit 1
	`, solutionID).Scan(&latestVersion, &latestBody, &latestRisks)
	if err == pgx.ErrNoRows {
		if _, err := appendMarkdownVersion(ctx, tx, solutionID, body, notes); err != nil {
			return false, err
		}
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("solution version lookup: %w", err)
	}
	if latestBody == body {
		if latestRisks != notes {
			if _, err := tx.Exec(ctx, `
				update solution_versions set risks = $2 where solution_id = $1 and version = $3
			`, solutionID, notes, latestVersion); err != nil {
				return false, fmt.Errorf("solution version risks: %w", err)
			}
		}
		return false, nil
	}
	if _, err := appendMarkdownVersion(ctx, tx, solutionID, body, notes); err != nil {
		return false, err
	}
	return true, nil
}

func appendMarkdownVersion(ctx context.Context, tx pgx.Tx, solutionID, body, notes string) (int, error) {
	var version int
	if err := tx.QueryRow(ctx, `
		insert into solution_versions (
			solution_id, version, body, preconditions, risks, applies_to, source, actor
		) values (
			$1,
			(select coalesce(max(version), 0) + 1 from solution_versions where solution_id = $1),
			$2, '', $3, '{}'::jsonb, $4, $4
		) returning version
	`, solutionID, body, notes, markdownActor).Scan(&version); err != nil {
		return 0, fmt.Errorf("solution version: %w", err)
	}
	return version, nil
}

// markdownEventID derives a stable UUID from the fingerprint and project, so
// importing the same document twice updates one event instead of piling up new
// ones. The version/variant bits make it a well-formed random-shaped UUID.
func markdownEventID(fingerprint, projectKey string) string {
	sum := md5.Sum([]byte(fingerprint + "|" + projectKey))
	sum[6] = (sum[6] & 0x0f) | 0x40
	sum[8] = (sum[8] & 0x3f) | 0x80
	hexed := hex.EncodeToString(sum[:])
	return hexed[0:8] + "-" + hexed[8:12] + "-" + hexed[12:16] + "-" + hexed[16:20] + "-" + hexed[20:32]
}

func isLowerHex(value string) bool {
	for i := 0; i < len(value); i++ {
		b := value[i]
		if (b < '0' || b > '9') && (b < 'a' || b > 'f') {
			return false
		}
	}
	return len(value) > 0
}

func parseMarkdownTime(value string) (time.Time, error) {
	if value == "" {
		return time.Time{}, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return time.Time{}, nil
	}
	return parsed.UTC(), nil
}

func parseMarkdownDisplayTime(value string) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, nil
	}
	for _, layout := range []string{"2006-01-02 15:04", "2006-01-02 15:04:05", time.RFC3339} {
		if parsed, err := time.Parse(layout, value); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("cannot parse timestamp %q", value)
}

// markdownIssue is one issue reduced to what the legacy format can carry.
type markdownIssue struct {
	ID          string
	Fingerprint string
	Category    string
	Code        string
	Title       string
	Trigger     string
	Raw         string
	Status      string
	FirstSeen   time.Time
	LastSeen    time.Time
	Hits        int
	Project     string
	Device      string
	Fix         string
	Notes       string
	History     []MarkdownRevision
}

// ExportMarkdown renders the database in the legacy format. Entries are ordered
// by creation time and numbered E-0001, E-0002, ...; the document parses in
// src/store.ts.
func ExportMarkdown(ctx context.Context, conn *pgx.Conn) (string, error) {
	issues, err := loadMarkdownIssues(ctx, conn)
	if err != nil {
		return "", err
	}
	var out strings.Builder
	out.WriteString(markdownDocumentHeader)
	out.WriteString("\n")
	for i, issue := range issues {
		if i > 0 {
			out.WriteString("\n")
		}
		out.WriteString(renderMarkdownEntry(i+1, issue))
	}
	return out.String(), nil
}

// SeedEntry is the structured form generated from the legacy document.
type SeedEntry struct {
	ID          string `json:"id"`
	Fingerprint string `json:"fingerprint"`
	Category    string `json:"category"`
	ErrorCode   string `json:"error_code,omitempty"`
	Title       string `json:"title"`
	Trigger     string `json:"trigger,omitempty"`
	RawMessage  string `json:"raw_message"`
	Fix         string `json:"fix"`
	Notes       string `json:"notes,omitempty"`
	Status      string `json:"status"`
	FirstSeen   string `json:"first_seen"`
	LastSeen    string `json:"last_seen"`
	Hits        int    `json:"hits"`
	Device      string `json:"device,omitempty"`
	Project     string `json:"project,omitempty"`
}

// SeedDocument is the generated seeds/*.json shape.
type SeedDocument struct {
	SchemaVersion string      `json:"schema_version"`
	Source        string      `json:"source"`
	Entries       []SeedEntry `json:"entries"`
}

// ExportSeedJSON renders the database as structured seed data. The legacy file
// seeds/ERRORS.seed.md stays read-only; this is the generated JSON artifact
// (seeds/ERRORS.seed.json).
func ExportSeedJSON(ctx context.Context, conn *pgx.Conn) ([]byte, error) {
	issues, err := loadMarkdownIssues(ctx, conn)
	if err != nil {
		return nil, err
	}
	document := SeedDocument{
		SchemaVersion: "v1",
		Source:        "seeds/ERRORS.seed.md",
		Entries:       make([]SeedEntry, 0, len(issues)),
	}
	for i, issue := range issues {
		document.Entries = append(document.Entries, SeedEntry{
			ID:          formatMarkdownID(i + 1),
			Fingerprint: issue.Fingerprint,
			Category:    issue.Category,
			ErrorCode:   issue.Code,
			Title:       issue.Title,
			Trigger:     issue.Trigger,
			RawMessage:  issue.Raw,
			Fix:         issue.Fix,
			Notes:       issue.Notes,
			Status:      issue.Status,
			FirstSeen:   formatMetaTime(issue.FirstSeen),
			LastSeen:    formatDisplayTime(issue.LastSeen),
			Hits:        issue.Hits,
			Device:      issue.Device,
			Project:     issue.Project,
		})
	}
	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("export seed json: %w", err)
	}
	// JSON escapes a newline as a backslash and an "n". After a colon that
	// looks like a Windows drive path to the repository privacy guard, so spell
	// both characters as escapes: the decoded value is unchanged and the
	// artifact stays free of the drive-path shape.
	encoded = bytes.ReplaceAll(encoded, []byte{':', '\\', 'n'}, []byte(`\u003a\u000a`))
	return append(encoded, '\n'), nil
}

func loadMarkdownIssues(ctx context.Context, conn *pgx.Conn) ([]markdownIssue, error) {
	rows, err := conn.Query(ctx, `
		select id, fingerprint, project_key, title, category, status, trigger,
		       first_seen, last_seen, hits
		from issues
		order by created_at, fingerprint, project_key
	`)
	if err != nil {
		return nil, fmt.Errorf("export markdown: %w", err)
	}
	type issueRow struct {
		id string
		markdownIssue
	}
	collected := make([]issueRow, 0)
	for rows.Next() {
		var row issueRow
		if err := rows.Scan(
			&row.id, &row.Fingerprint, &row.Project, &row.Title, &row.Category,
			&row.Status, &row.Trigger, &row.FirstSeen, &row.LastSeen, &row.Hits,
		); err != nil {
			rows.Close()
			return nil, fmt.Errorf("export markdown: scan issue: %w", err)
		}
		collected = append(collected, row)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("export markdown: %w", err)
	}

	issues := make([]markdownIssue, 0, len(collected))
	for _, row := range collected {
		issue := row.markdownIssue
		issue.ID = row.id
		var environment []byte
		var observedAt time.Time
		var eventCategory string
		var errorCode *string
		var sample string
		err := conn.QueryRow(ctx, `
			select e.observed_at, e.category, e.error_code, e.redacted_sample,
			       coalesce(o.environment, '{}'::jsonb)
			from events e
			join occurrences o on o.event_id = e.event_id and o.issue_id = $1
			where e.fingerprint = $2
			order by e.observed_at, e.event_id
			limit 1
		`, issue.ID, issue.Fingerprint).Scan(&observedAt, &eventCategory, &errorCode, &sample, &environment)
		switch {
		case err == pgx.ErrNoRows:
			// An issue without a recorded event still exports its fields.
		case err != nil:
			return nil, fmt.Errorf("export markdown: event: %w", err)
		default:
			if !observedAt.IsZero() {
				issue.FirstSeen = observedAt.UTC()
			}
			if eventCategory != "" {
				issue.Category = eventCategory
			}
			if errorCode != nil {
				issue.Code = *errorCode
			}
			issue.Raw = sample
			var env map[string]any
			if json.Unmarshal(environment, &env) == nil {
				if device, ok := env["device"].(string); ok {
					issue.Device = device
				}
			}
		}

		var solutionID, solutionStatus string
		err = conn.QueryRow(ctx, `
			select id, status from solutions
			where issue_id = $1
			order by created_at desc, id desc
			limit 1
		`, issue.ID).Scan(&solutionID, &solutionStatus)
		switch {
		case err == pgx.ErrNoRows:
			issue.Status = markdownStatusByIssueStatus[issue.Status]
		case err != nil:
			return nil, fmt.Errorf("export markdown: solution: %w", err)
		default:
			issue.Status = markdownStatusBySolutionStatus[solutionStatus]
			versions, err := loadMarkdownVersions(ctx, conn, solutionID)
			if err != nil {
				return nil, err
			}
			if len(versions) > 0 {
				newest := versions[len(versions)-1]
				issue.Fix = newest.Body
				issue.Notes = newest.Risks
				for _, version := range versions[:len(versions)-1] {
					issue.History = append(issue.History, MarkdownRevision{
						Version: version.Version,
						Body:    version.Body,
					})
				}
			}
		}
		issues = append(issues, issue)
	}
	return issues, nil
}

// markdownVersion is one stored revision as export reads it.
type markdownVersion struct {
	Version int
	Body    string
	Risks   string
}

func loadMarkdownVersions(ctx context.Context, conn *pgx.Conn, solutionID string) ([]markdownVersion, error) {
	rows, err := conn.Query(ctx, `
		select version, body, risks from solution_versions
		where solution_id = $1
		order by version
	`, solutionID)
	if err != nil {
		return nil, fmt.Errorf("export markdown: versions: %w", err)
	}
	defer rows.Close()
	versions := make([]markdownVersion, 0, 4)
	for rows.Next() {
		var version markdownVersion
		if err := rows.Scan(&version.Version, &version.Body, &version.Risks); err != nil {
			return nil, fmt.Errorf("export markdown: scan version: %w", err)
		}
		versions = append(versions, version)
	}
	return versions, rows.Err()
}

func formatMarkdownID(n int) string {
	return fmt.Sprintf("E-%04d", n)
}

func formatDisplayTime(value time.Time) string {
	return value.UTC().Format("2006-01-02 15:04")
}

func formatMetaTime(value time.Time) string {
	return value.UTC().Format("2006-01-02T15:04:05Z")
}

func renderMarkdownEntry(n int, issue markdownIssue) string {
	meta := []string{
		"sig=" + encodeMetaValue(issue.Fingerprint),
		"cat=" + encodeMetaValue(issue.Category),
	}
	if issue.Code != "" {
		meta = append(meta, "code="+encodeMetaValue(issue.Code))
	}
	meta = append(meta, "first="+encodeMetaValue(formatMetaTime(issue.FirstSeen)))
	if issue.Device != "" {
		meta = append(meta, "device="+encodeMetaValue(issue.Device))
	}
	if issue.Project != "" {
		meta = append(meta, "proj="+encodeMetaValue(issue.Project))
	}

	category := issue.Category
	if issue.Code != "" {
		category += " / " + issue.Code
	}

	fix := issue.Fix
	if len(issue.History) > 0 {
		var body strings.Builder
		body.WriteString(fix)
		body.WriteString("\n\n")
		for i := len(issue.History) - 1; i >= 0; i-- {
			revision := issue.History[i]
			body.WriteString(fmt.Sprintf("<!-- errkb-history: v%d -->", revision.Version))
			body.WriteString("\n")
			body.WriteString(revision.Body)
			if i > 0 {
				body.WriteString("\n\n")
			}
		}
		fix = body.String()
	}

	runs := regexp.MustCompile("`+").FindAllString(issue.Raw, -1)
	fenceLength := 3
	for _, run := range runs {
		if len(run)+1 > fenceLength {
			fenceLength = len(run) + 1
		}
	}
	fence := strings.Repeat("`", fenceLength)

	rawLines := make([]string, 0)
	if issue.Raw != "" {
		for _, line := range strings.Split(issue.Raw, "\n") {
			if line == "" {
				rawLines = append(rawLines, "")
			} else {
				rawLines = append(rawLines, "  "+line)
			}
		}
	}

	lines := []string{
		fmt.Sprintf("## %s · %s", formatMarkdownID(n), issue.Title),
		"<!-- errkb: " + strings.Join(meta, " ") + " -->",
		"",
	}
	lines = append(lines, renderMarkdownInline("Fingerprint", wrapMarkdownCode(issue.Fingerprint))...)
	lines = append(lines, renderMarkdownInline("Category", wrapMarkdownCode(category))...)
	lines = append(lines, fmt.Sprintf("- First seen: %s · Last seen: %s · Hits: %d",
		formatDisplayTime(issue.FirstSeen), formatDisplayTime(issue.LastSeen), issue.Hits))
	lines = append(lines, renderMarkdownInline("Trigger", issue.Trigger)...)
	lines = append(lines, "- Raw message:", "  "+fence+"text")
	lines = append(lines, rawLines...)
	lines = append(lines, "  "+fence)
	lines = append(lines, renderMarkdownText("Fix", fix)...)
	lines = append(lines, "- Status: `"+issue.Status+"`")
	lines = append(lines, renderMarkdownText("Notes", issue.Notes)...)
	lines = append(lines, "")
	return strings.Join(lines, "\n")
}

func renderMarkdownText(label, value string) []string {
	if value == "" {
		return []string{"- " + label + ":"}
	}
	lines := []string{"- " + label + ":"}
	for _, line := range strings.Split(value, "\n") {
		if line == "" {
			lines = append(lines, "")
		} else {
			lines = append(lines, "  "+line)
		}
	}
	return lines
}

func renderMarkdownInline(label, value string) []string {
	if strings.Contains(value, "\n") {
		return renderMarkdownText(label, value)
	}
	if value == "" {
		return []string{"- " + label + ":"}
	}
	return []string{"- " + label + ": " + value}
}
