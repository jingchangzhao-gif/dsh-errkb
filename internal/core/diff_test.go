package core_test

// Cross-language differential suite (EKB-018).
//
// The TypeScript oracle is the committed corpus under spec/corpus: it is
// generated from the real TypeScript implementation by tests/gen-corpus.ts
// (EKB-009), so comparing the Go implementation against each case's expected
// value is the field-by-field Go-versus-TypeScript comparison. The generator's
// --check mode keeps the corpus in step with the TypeScript side.
//
// Authority comes from the plan's section 8.2 status column:
//   - src/signature.ts and src/redact.ts are Keep (strong alignment, the Go side
//     must reproduce them exactly);
//   - src/match.ts and src/store.ts are downgraded (no authority, differences are
//     recorded in spec/corpus/divergences.md instead of being chased);
//   - src/inject.ts has no Go counterpart at all (DSH connector).
//
// The judgement for this card is "no undocumented divergence", not "zero
// divergence": every intentional gap is registered in divergences.md and locked
// by a test here so that a change to the gap fails the suite until the registry
// is updated.

import (
	"encoding/json"
	"go/parser"
	"go/token"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/jingchangzhao-gif/dsh-errkb/internal/core"
	"github.com/jingchangzhao-gif/dsh-errkb/internal/redact"
)

// divergence is one registered gap between the two implementations.
type divergence struct {
	Corpus string
	Fields []string
	Class  string // "downgraded-ts" or "intentional-absence"
	Lock   string // test function name that locks the gap's shape
}

// registeredDivergences mirrors spec/corpus/divergences.md rows one to one.
// TestDiffDivergenceRegistry fails when the two drift apart.
var registeredDivergences = []divergence{
	{
		Corpus: "match",
		Fields: []string{"approximate", "id", "matched", "via"},
		Class:  "downgraded-ts",
		Lock:   "TestDiffMatchSemanticsAreNotPorted",
	},
	{
		Corpus: "match",
		Fields: []string{"similarity"},
		Class:  "downgraded-ts",
		Lock:   "TestDiffMatchSimilarity",
	},
	{
		Corpus: "notice",
		Fields: []string{"kind", "text"},
		Class:  "intentional-absence",
		Lock:   "TestDiffNoticeIsTypeScriptOnly",
	},
}

const floatTolerance = 1e-9

func diffError(t *testing.T, corpus, caseID, field string, goValue, tsValue any) {
	t.Helper()
	t.Errorf("%s %s: field %q: go=%v ts=%v", corpus, caseID, field, goValue, tsValue)
}

func loadDiffCorpus[T any](t *testing.T, fileName string) []T {
	t.Helper()
	path := filepath.Join("..", "..", "spec", "corpus", fileName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read corpus %s: %v", fileName, err)
	}
	var document struct {
		Cases []T `json:"cases"`
	}
	if err := json.Unmarshal(data, &document); err != nil {
		t.Fatalf("parse corpus %s: %v", fileName, err)
	}
	if len(document.Cases) == 0 {
		t.Fatalf("corpus %s has no cases", fileName)
	}
	return document.Cases
}

// The corpus shapes below mirror tests/gen-corpus.ts. Corpus expected values are
// the TypeScript implementation's output for the same input.

type diffNormalizeCase struct {
	CaseID   string `json:"case_id"`
	Input    string `json:"input"`
	Expected string `json:"expected"`
}

type diffFingerprintCase struct {
	CaseID string `json:"case_id"`
	Input  struct {
		Category string `json:"category"`
		Message  string `json:"message"`
	} `json:"input"`
	Expected string `json:"expected"`
}

type diffHeadlineCase struct {
	CaseID   string `json:"case_id"`
	Input    string `json:"input"`
	Expected struct {
		Line string `json:"line"`
		Code string `json:"code"`
	} `json:"expected"`
}

type diffRedactCase struct {
	CaseID  string `json:"case_id"`
	Input   string `json:"input"`
	Options *struct {
		Share string `json:"share"`
	} `json:"options"`
	Expected string `json:"expected"`
}

type diffTokenizeCase struct {
	CaseID   string   `json:"case_id"`
	Input    string   `json:"input"`
	Expected []string `json:"expected"`
}

type diffMatchCase struct {
	CaseID string `json:"case_id"`
	Input  struct {
		Error struct {
			Category string `json:"category"`
			Code     string `json:"code"`
			Message  string `json:"message"`
		} `json:"error"`
		Entries []struct {
			ID       string `json:"id"`
			Category string `json:"category"`
			Code     string `json:"code"`
			Message  string `json:"message"`
		} `json:"entries"`
	} `json:"input"`
	Expected struct {
		Matched     bool     `json:"matched"`
		ID          string   `json:"id"`
		Via         string   `json:"via"`
		Approximate *bool    `json:"approximate"`
		Similarity  *float64 `json:"similarity"`
	} `json:"expected"`
}

type diffNoticeCase struct {
	CaseID   string          `json:"case_id"`
	Input    json.RawMessage `json:"input"`
	Expected struct {
		Kind string `json:"kind"`
		Text string `json:"text"`
	} `json:"expected"`
}

// TestDiffNormalize compares normalized text case by case. Authority: signature
// is a Keep implementation, so any difference is a real problem on the Go side.
func TestDiffNormalize(t *testing.T) {
	for _, testCase := range loadDiffCorpus[diffNormalizeCase](t, "normalize.json") {
		got := core.Normalize(testCase.Input)
		if got != testCase.Expected {
			diffError(t, "normalize.json", testCase.CaseID, "normalized", got, testCase.Expected)
		}
	}
}

// TestDiffFingerprint compares the 12-character fingerprints. Authority: Keep.
func TestDiffFingerprint(t *testing.T) {
	for _, testCase := range loadDiffCorpus[diffFingerprintCase](t, "fingerprint.json") {
		got := core.Signature(testCase.Input.Category, core.Normalize(testCase.Input.Message))
		if got != testCase.Expected {
			diffError(t, "fingerprint.json", testCase.CaseID, "fingerprint", got, testCase.Expected)
		}
	}
}

// TestDiffHeadline compares the extracted headline line and code. Authority: Keep.
func TestDiffHeadline(t *testing.T) {
	for _, testCase := range loadDiffCorpus[diffHeadlineCase](t, "headline.json") {
		got := core.ExtractHeadline(testCase.Input)
		if got.Line != testCase.Expected.Line {
			diffError(t, "headline.json", testCase.CaseID, "line", got.Line, testCase.Expected.Line)
		}
		if got.Code != testCase.Expected.Code {
			diffError(t, "headline.json", testCase.CaseID, "code", got.Code, testCase.Expected.Code)
		}
	}
}

// TestDiffRedact compares redacted text. Authority: Keep, and both sides read the
// rules from spec/redact.cue, so they are expected to agree exactly.
func TestDiffRedact(t *testing.T) {
	for _, testCase := range loadDiffCorpus[diffRedactCase](t, "redact.json") {
		share := redact.Public
		if testCase.Options != nil && testCase.Options.Share == "private" {
			share = redact.Private
		}
		got := redact.Text(testCase.Input, share)
		if got != testCase.Expected {
			diffError(t, "redact.json", testCase.CaseID, "redacted", got, testCase.Expected)
		}
	}
}

// TestDiffTokenize compares sorted token sets. Authority: Keep.
func TestDiffTokenize(t *testing.T) {
	for _, testCase := range loadDiffCorpus[diffTokenizeCase](t, "tokenize.json") {
		got := core.Tokenize(testCase.Input)
		sort.Strings(got)
		want := append([]string(nil), testCase.Expected...)
		sort.Strings(want)
		// A missing token list and an empty one are the same result.
		if len(got) == 0 && len(want) == 0 {
			continue
		}
		if !reflect.DeepEqual(got, want) {
			diffError(t, "tokenize.json", testCase.CaseID, "tokens", got, want)
		}
	}
}

// TestDiffMatchSimilarity compares the score field of the match corpus. The
// TypeScript `similarity` is route-defined (1 for an exact fingerprint hit, 0
// for the code fallback, token overlap for a fuzzy hit), so the comparison is
// per route; the exact route is a registered divergence locked here.
func TestDiffMatchSimilarity(t *testing.T) {
	for _, testCase := range loadDiffCorpus[diffMatchCase](t, "match.json") {
		var got float64
		if len(testCase.Input.Entries) > 0 {
			got = core.Jaccard(core.Tokenize(testCase.Input.Error.Message), core.Tokenize(testCase.Input.Entries[0].Message))
		}
		if !testCase.Expected.Matched {
			// Unmatched cases carry no score on the TypeScript side; the Go score
			// must stay below the fuzzy threshold that made the TypeScript side miss.
			if got >= 0.72 {
				diffError(t, "match.json", testCase.CaseID, "similarity", got, "< 0.72 (unmatched)")
			}
			continue
		}
		if testCase.Expected.Similarity == nil {
			t.Fatalf("match.json %s: matched case without a similarity", testCase.CaseID)
		}
		tsSimilarity := *testCase.Expected.Similarity
		switch testCase.Expected.Via {
		case "fuzzy":
			// Both sides compute the same token overlap here.
			if math.Abs(got-tsSimilarity) > floatTolerance {
				diffError(t, "match.json", testCase.CaseID, "similarity", got, tsSimilarity)
			}
		case "exact":
			// Registered divergence: the exact route reports 1 regardless of token
			// overlap, while Go has no verdict and only the overlap. Lock the shape:
			// if the two values ever coincide, the registry row must go.
			if tsSimilarity != 1 {
				t.Errorf("match.json %s: exact route similarity = %v, want 1 (TypeScript rule)", testCase.CaseID, tsSimilarity)
			}
			if math.Abs(got-1) <= floatTolerance {
				t.Errorf("match.json %s: the exact-route divergence vanished (go=%v); remove the similarity row from divergences.md", testCase.CaseID, got)
			}
		case "code":
			// The code fallback reports 0 and the token sets of these unrelated
			// messages do not overlap either, so both sides agree.
			if tsSimilarity != 0 {
				t.Errorf("match.json %s: code route similarity = %v, want 0 (TypeScript rule)", testCase.CaseID, tsSimilarity)
			}
			if got != 0 {
				diffError(t, "match.json", testCase.CaseID, "similarity", got, tsSimilarity)
			}
		default:
			t.Fatalf("match.json %s: unknown via %q", testCase.CaseID, testCase.Expected.Via)
		}
	}
}

// TestDiffNoticeIsTypeScriptOnly locks the registered absence of a Go notice
// implementation: notice.json stays TypeScript-owned because src/inject.ts is a
// DSH connector with no Go counterpart (plan section 8.2/8.3). If a notice
// implementation lands in internal/core, this test fails and the registry must
// be updated in the same change.
func TestDiffNoticeIsTypeScriptOnly(t *testing.T) {
	cases := loadDiffCorpus[diffNoticeCase](t, "notice.json")
	for _, testCase := range cases {
		if testCase.Expected.Kind == "" || testCase.Expected.Text == "" {
			t.Errorf("notice.json %s: expected kind/text missing", testCase.CaseID)
		}
	}

	noticeSymbol := regexp.MustCompile(`(?m)^(?:func\s+[A-Za-z_]*Notice|type\s+[A-Za-z_]*Notice)`)
	for _, file := range goSourceFiles(t, ".") {
		source, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		if match := noticeSymbol.FindString(string(source)); match != "" {
			t.Errorf("%s: found %q; a core notice implementation means the recorded divergence must be removed or rewritten", file, match)
		}
	}
}

// TestDiffMatchSemanticsAreNotPorted locks the downgraded match verdict: Go has
// no Match function because src/match.ts lost authority. If one appears, the
// registry must change with it.
func TestDiffMatchSemanticsAreNotPorted(t *testing.T) {
	matchSymbol := regexp.MustCompile(`(?m)^func\s+(?:\([^)]*\)\s*)?Match[A-Za-z_]*\s*\(`)
	for _, file := range goSourceFiles(t, ".") {
		source, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s: %v", file, err)
		}
		if match := matchSymbol.FindString(string(source)); match != "" {
			t.Errorf("%s: found %q; the match verdict is registered as downgraded, update divergences.md if the Go side now owns it", file, match)
		}
	}
}

// TestDiffDivergenceRegistry keeps spec/corpus/divergences.md and
// registeredDivergences in sync, and checks that every lock is a real test in
// this file. Changing one side without the other fails here.
func TestDiffDivergenceRegistry(t *testing.T) {
	path := filepath.Join("..", "..", "spec", "corpus", "divergences.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read divergences.md: %v", err)
	}
	rows := parseDivergenceRows(t, string(data))

	if len(rows) != len(registeredDivergences) {
		t.Fatalf("divergences.md has %d rows, registry has %d", len(rows), len(registeredDivergences))
	}
	for index, entry := range registeredDivergences {
		row := rows[index]
		if row.corpus != entry.Corpus {
			t.Errorf("divergences.md row %d corpus = %q, registry = %q", index+1, row.corpus, entry.Corpus)
		}
		if !reflect.DeepEqual(row.fields, entry.Fields) {
			t.Errorf("divergences.md %s fields = %v, registry = %v", row.corpus, row.fields, entry.Fields)
		}
		if row.class != entry.Class {
			t.Errorf("divergences.md %s class = %q, registry = %q", row.corpus, row.class, entry.Class)
		}
		if row.lock != entry.Lock {
			t.Errorf("divergences.md %s lock = %q, registry = %q", row.corpus, row.lock, entry.Lock)
		}
	}

	source, err := os.ReadFile("diff_test.go")
	if err != nil {
		t.Fatalf("read diff_test.go: %v", err)
	}
	text := string(source)
	for _, entry := range registeredDivergences {
		pattern := regexp.MustCompile(`(?m)^func\s+` + regexp.QuoteMeta(entry.Lock) + `\s*\(`)
		if !pattern.MatchString(text) {
			t.Errorf("locking test %q is not defined in diff_test.go", entry.Lock)
		}
	}
}

type divergenceRow struct {
	corpus string
	fields []string
	class  string
	lock   string
}

// parseDivergenceRows reads the markdown table whose columns are
// corpus | fields | go behaviour | ts behaviour | class | locking test.
func parseDivergenceRows(t *testing.T, markdown string) []divergenceRow {
	t.Helper()
	rows := make([]divergenceRow, 0, len(registeredDivergences))
	for _, line := range strings.Split(markdown, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "| `") {
			continue
		}
		cells := strings.Split(line, "|")
		if len(cells) < 8 {
			t.Fatalf("divergence row has %d columns, want 6: %q", len(cells)-2, line)
		}
		corpus := strings.Trim(strings.TrimSpace(cells[1]), "`")
		fields := make([]string, 0, 4)
		for _, field := range strings.Split(strings.TrimSpace(cells[2]), ",") {
			fields = append(fields, strings.Trim(strings.TrimSpace(field), "`"))
		}
		sort.Strings(fields)
		class := strings.TrimSpace(cells[5])
		lock := strings.Trim(strings.TrimSpace(cells[6]), "`")
		rows = append(rows, divergenceRow{corpus: corpus, fields: fields, class: class, lock: lock})
	}
	return rows
}

// TestDiffNoIOImports is the P2 closure evidence for "the core packages do no
// I/O": it parses imports of every non-test Go file in internal/core and
// internal/redact and rejects the I/O, process and network packages. Tests are
// excluded because fixtures are read from spec/corpus by design.
func TestDiffNoIOImports(t *testing.T) {
	forbidden := map[string]bool{
		"os":          true,
		"net":         true,
		"net/http":    true,
		"os/exec":     true,
		"io/ioutil":   true,
		"math/rand":   true,
		"crypto/rand": true,
	}
	for _, directory := range []string{".", "../redact"} {
		for _, file := range goSourceFiles(t, directory) {
			parsed, err := parser.ParseFile(token.NewFileSet(), file, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatalf("parse %s: %v", file, err)
			}
			for _, spec := range parsed.Imports {
				path := strings.Trim(spec.Path.Value, `"`)
				if forbidden[path] {
					t.Errorf("%s imports %q; core packages must stay I/O-free", file, path)
				}
			}
		}
	}
}

// goSourceFiles returns the non-test .go files of a directory.
func goSourceFiles(t *testing.T, directory string) []string {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("read directory %s: %v", directory, err)
	}
	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		files = append(files, filepath.Join(directory, name))
	}
	sort.Strings(files)
	if len(files) == 0 {
		t.Fatalf("no Go source files in %s", directory)
	}
	return files
}

// TestDiffLabelledSmoke is the labelled-corpus smoke from EKB-010/EKB-017: no
// distinct-lookalike pair may come back as an exact hit.
func TestDiffLabelledSmoke(t *testing.T) {
	path := filepath.Join("..", "..", "spec", "corpus", "labelled", "distinct-lookalike.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read labelled corpus: %v", err)
	}
	var corpus struct {
		Cases []struct {
			CaseID string `json:"case_id"`
			Kind   string `json:"lookalike_kind"`
			Pair   struct {
				A struct {
					Category string `json:"category"`
					Code     string `json:"code"`
					Message  string `json:"message"`
					Command  string `json:"command"`
				} `json:"a"`
				B struct {
					Category string `json:"category"`
					Code     string `json:"code"`
					Message  string `json:"message"`
					Command  string `json:"command"`
				} `json:"b"`
			} `json:"pair"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &corpus); err != nil {
		t.Fatalf("parse labelled corpus: %v", err)
	}
	if len(corpus.Cases) == 0 {
		t.Fatal("labelled corpus has no cases")
	}
	for _, testCase := range corpus.Cases {
		query := core.RerankQuery{
			Fingerprint:       core.Signature(testCase.Pair.A.Category, core.Normalize(testCase.Pair.A.Message)),
			Code:              testCase.Pair.A.Code,
			NormalizedMessage: core.Normalize(testCase.Pair.A.Message),
			Command:           testCase.Pair.A.Command,
		}
		candidate := core.RerankCandidate{
			IssueID:           testCase.CaseID,
			Fingerprint:       core.Signature(testCase.Pair.B.Category, core.Normalize(testCase.Pair.B.Message)),
			Code:              testCase.Pair.B.Code,
			NormalizedMessage: core.Normalize(testCase.Pair.B.Message),
			Command:           testCase.Pair.B.Command,
		}
		results := core.Rerank(query, []core.RerankCandidate{candidate})
		if len(results) != 1 {
			t.Fatalf("%s: got %d results, want 1", testCase.CaseID, len(results))
		}
		if !results[0].Approximate {
			t.Errorf("%s (%s) was reported as an exact hit", testCase.CaseID, testCase.Kind)
		}
	}
}
