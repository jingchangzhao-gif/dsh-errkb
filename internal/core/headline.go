package core

import (
	"regexp"
	"strings"
	"unicode"
)

// HeadlineMaxChars is the longest headline kept as is. Longer lines are cut to
// HeadlineMaxChars-1 runes and end with an ellipsis, which keeps the result
// inside the length the ErrorEvent contract expects for a title line.
const HeadlineMaxChars = 200

// ansiPattern matches the escape sequences terminals and test runners emit:
// CSI sequences (colours, cursor movement) and OSC sequences (hyperlinks,
// window titles). It is the same expression spec/corpus/headline.json was
// generated with, so the two implementations strip the same bytes.
var ansiPattern = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(\x07|\x1b\\)`)

// tracebackMarker starts a Python traceback. The marker is looked for in the
// raw text, before ANSI stripping, so a coloured traceback still counts.
const tracebackMarker = "Traceback (most recent call last):"

// codePattern finds a line that names its error: a Node/pnpm code
// (ERR_PNPM_FETCH_404), an errno (EPERM), an exception class
// (ModuleNotFoundError) or a TypeScript error (error TS2307).
var codePattern = regexp.MustCompile(`\b(ERR_[A-Z0-9_]+|E[A-Z]{2,}|[A-Z]\w*Error|error TS\d+)\b`)

// notACode holds log-level words that fit E[A-Z]{2,} but name no error:
// "npm ERR! code ENOENT" must yield ENOENT, not ERR.
var notACode = map[string]struct{}{
	"ERR":    {},
	"ERROR":  {},
	"ERRORS": {},
	"EXIT":   {},
}

// Headline is one readable line that identifies an error, plus the code the
// line names. Code is empty when the line names none.
type Headline struct {
	Line string
	Code string
}

// ExtractHeadline picks the one line of a possibly multi-line error text that
// identifies it:
//
//  1. a Python traceback gives its last non-empty line, the exception itself;
//  2. otherwise the first line naming a code (see codePattern);
//  3. otherwise the last non-empty line.
//
// The traceback rule runs first because a traceback quotes source lines, and
// a quoted "raise ValueError(...)" would otherwise win rule 2. ANSI escapes
// are stripped, every line is trimmed, empty lines are dropped and the result
// is capped at HeadlineMaxChars. The function is pure: no I/O, no time, no
// random source, so the same input always yields the same headline.
func ExtractHeadline(raw string) Headline {
	clean := ansiPattern.ReplaceAllString(raw, "")
	lines := make([]string, 0, 8)
	for _, line := range strings.FieldsFunc(clean, func(r rune) bool {
		return r == '\n' || r == '\r'
	}) {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			lines = append(lines, trimmed)
		}
	}
	last := ""
	if len(lines) > 0 {
		last = lines[len(lines)-1]
	}
	if strings.Contains(raw, tracebackMarker) {
		return pickHeadline(last)
	}
	for _, line := range lines {
		if codeIn(line) != "" {
			return pickHeadline(line)
		}
	}
	return pickHeadline(last)
}

// codeIn returns the first real code in a line, as stored:
// "error TS2307" yields TS2307.
func codeIn(line string) string {
	for _, match := range codePattern.FindAllStringSubmatch(line, -1) {
		found := match[1]
		if _, skip := notACode[found]; skip {
			continue
		}
		return strings.TrimPrefix(found, "error ")
	}
	return ""
}

func pickHeadline(line string) Headline {
	return Headline{Line: capHeadline(line), Code: codeIn(line)}
}

// capHeadline cuts a line to HeadlineMaxChars runes, the last one an ellipsis.
func capHeadline(line string) string {
	runes := []rune(line)
	if len(runes) <= HeadlineMaxChars {
		return line
	}
	cut := strings.TrimRightFunc(string(runes[:HeadlineMaxChars-1]), unicode.IsSpace)
	return cut + "…"
}
