package core

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// The placeholders normalization writes, one per class of run-to-run noise.
// They are declared here and nowhere else: the corpus in
// spec/corpus/normalize.json is generated with the identical strings, so an
// extra literal in the implementation would silently diverge from it.
const (
	PlaceholderTimestamp = "<ts>"
	PlaceholderPID       = "<pid>"
	PlaceholderPort      = "<port>"
	PlaceholderPosition  = "<pos>"
	PlaceholderUUID      = "<uuid>"
	PlaceholderHash      = "<hash>"
	PlaceholderTemp      = "<tmp>"
	PlaceholderPath      = "<path>"
)

var (
	// The ANSI pattern is shared with headline extraction (headline.go): both
	// strip the CSI and OSC sequences terminals and test runners emit.

	// spaceRun collapses every whitespace run to one space. Unicode space
	// separators are included so this matches JavaScript's `\s`.
	spaceRun = regexp.MustCompile(`[\s\p{Z}]+`)

	uuidPattern = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)

	// longHexPattern finds runs of at least 32 hexadecimal characters. The
	// TypeScript expression adds lookarounds that only exist to make sure the
	// run is maximal; matching the maximal run directly is equivalent.
	longHexPattern = regexp.MustCompile(`(?i)[0-9a-f]{32,}`)

	// edgePunctuation is Unicode punctuation, space separators and whitespace
	// at either end. `<` and `>` are symbols, not punctuation, so a trailing
	// placeholder survives.
	edgePunctuation = regexp.MustCompile(`^[\p{P}\p{Z}\s]+|[\p{P}\p{Z}\s]+$`)
)

// noisePatterns are applied in this order, which matters in three places:
// UUIDs and hashes go before timestamps so a date-shaped run inside a hash is
// never split, ports go before positions so `127.0.0.1:3000` is a port and not
// a line number, and temp-directory names go before path collapsing so a temp
// directory that is the last segment is still replaced.
var noisePatterns = []struct {
	pattern     *regexp.Regexp
	replacement string
}{
	// A date, optionally with a time, a fractional part and a zone.
	{
		regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}(?:[T ]\d{2}:\d{2}(?::\d{2}(?:[.,]\d+)?)?(?:Z|[+-]\d{2}:?\d{2})?)?\b`),
		PlaceholderTimestamp,
	},
	{regexp.MustCompile(`\b\d{1,2}:\d{2}:\d{2}(?:[.,]\d+)?\b`), PlaceholderTimestamp},
	// Durations: `123ms`, `1.5s`, `30 ms`, `耗时 123ms`, `123 毫秒`.
	{regexp.MustCompile(`\b\d+(?:\.\d+)?\s?(?:ms|s)\b|\d+(?:\.\d+)?\s?毫?秒`), PlaceholderTimestamp},
	{regexp.MustCompile(`(?i)\bpid\s*[:=#]?\s*\d+`), PlaceholderPID},
	{regexp.MustCompile(`(?i)\bport\s*[:=#]?\s*\d+`), PlaceholderPort},
	// host:port, including IPv4, `localhost`, `[::1]` and `::`.
	{regexp.MustCompile(`(?i)(localhost|\b\d{1,3}(?:\.\d{1,3}){3}|\]|::):\d{1,5}\b`), `$1:` + PlaceholderPort},
	{regexp.MustCompile(`(?i)\bline\s+\d+(?:\s*[:,]\s*\d+)?`), PlaceholderPosition},
	{regexp.MustCompile(`(?i)\bcol(?:umn)?\s+\d+`), PlaceholderPosition},
	{regexp.MustCompile(`(\.[A-Za-z]\w*):\d+(?::\d+)?\b`), `$1:` + PlaceholderPosition},
	{regexp.MustCompile(`:\d+:\d+\b`), ":" + PlaceholderPosition},
	{regexp.MustCompile(`\(\d+,\s?\d+\)`), "(" + PlaceholderPosition + ")"},
	// The segment right after a temp root.
	{regexp.MustCompile(`([\\/](?:tmp|temp)[\\/])[^\\/\s'"` + "`" + `]+`), `$1` + PlaceholderTemp},
	// `tmp-…` / `tmp_…` names that mkdtemp-style helpers create anywhere.
	{regexp.MustCompile(`(?i)\btmp[-_][\w-]+`), PlaceholderTemp},
}

// segmentPattern matches one path segment: anything a message does not use to
// delimit a path. A placeholder written by an earlier step counts as a
// segment character too, so `/tmp/<tmp>/a.ts` stays one path.
const segmentPattern = `(?:<[a-z-]+>|[^\s'"` + "`" + `<>|:*?\\/()\[\]{},;])+`

// pathCandidate matches the four absolute-path shapes src/signature.ts defines:
// a Windows drive path (optionally behind the `\\?\` long-path prefix), a UNC
// share, a `~` home path, and a POSIX path with at least two segments. The
// TypeScript expression also has lookbehind guards; Go has no lookbehind, so
// the alternatives are grouped here and pathAllowed checks the preceding rune
// instead.
var pathCandidate = regexp.MustCompile(
	`(?P<drive>(?:\\\\\?\\)?[A-Za-z]:[\\/]` + segmentPattern + `(?:[\\/]` + segmentPattern + `)*[\\/]?)` +
		`|(?P<unc>\\\\` + segmentPattern + `(?:\\` + segmentPattern + `)+\\?)` +
		`|(?P<home>~[\\/]` + segmentPattern + `(?:[\\/]` + segmentPattern + `)*[\\/]?)` +
		`|(?P<posix>/` + segmentPattern + `(?:/` + segmentPattern + `)+/?)`,
)

// Alternative indexes inside pathCandidate.
const (
	pathDrive = iota
	pathUNC
	pathHome
	pathPOSIX
)

// pathAllowed reports whether a path match starting at start may fold, i.e.
// whether the character before it satisfies the TypeScript lookbehind: neither
// a word character nor a backslash for Windows and UNC paths, and additionally
// none of `. : / \ < > ~ -` for home and POSIX paths. Those guards keep
// `https://host/a/b`, `a/b/c` and `<path>/x` from being rewritten.
func pathAllowed(text string, start, alternative int) bool {
	if start == 0 {
		return true
	}
	previous, _ := utf8.DecodeLastRuneInString(text[:start])
	if isASCIIWord(previous) {
		return false
	}
	if alternative == pathDrive || alternative == pathUNC {
		return previous != '\\'
	}
	return !strings.ContainsRune(".:/\\<>~-", previous)
}

func isASCIIWord(r rune) bool {
	return r == '_' ||
		(r >= '0' && r <= '9') ||
		(r >= 'a' && r <= 'z') ||
		(r >= 'A' && r <= 'Z')
}

// collapseAbsolutePaths replaces every absolute path with its collapsed form,
// left to right, and rescans one byte after a rejected candidate the way a
// regular-expression engine without lookbehind would.
func collapseAbsolutePaths(text string) string {
	var out strings.Builder
	out.Grow(len(text))
	last, pos := 0, 0
	for pos < len(text) {
		match := pathCandidate.FindStringSubmatchIndex(text[pos:])
		if match == nil {
			break
		}
		start, end := pos+match[0], pos+match[1]
		alternative := -1
		for candidate, group := range []int{1, 2, 3, 4} {
			if match[2*group] >= 0 {
				alternative = candidate
				break
			}
		}
		if alternative < 0 || !pathAllowed(text, start, alternative) {
			pos = start + 1
			continue
		}
		out.WriteString(text[last:start])
		out.WriteString(collapsePath(text[start:end]))
		last = end
		pos = end
	}
	out.WriteString(text[last:])
	return out.String()
}

var (
	trailingDots   = regexp.MustCompile(`\.+$`)
	pathSeparators = regexp.MustCompile(`[\\/]+`)
	driveLetter    = regexp.MustCompile(`^[A-Za-z]:$`)
)

// homeRoots is the parent directory of a per-user home. A path that ends at
// `<root>/<user>` keeps no last segment, because that segment is the user name.
var homeRoots = map[string]bool{"users": true, "home": true}

// collapsePath collapses one absolute path to PlaceholderPath plus its last
// segment, written with the separator the path used. The last segment is what
// tells `ENOENT ... package.json` from `ENOENT ... tsconfig.json`. Two
// exceptions drop it: a path that is only a directory (trailing separator), and
// a bare home directory such as `C:/Users/<name>`. Sentence punctuation the
// segment class swallowed is handed back outside the placeholder.
func collapsePath(path string) string {
	trailing := trailingDots.FindString(path)
	body := path[:len(path)-len(trailing)]
	segments := make([]string, 0, 6)
	for _, segment := range pathSeparators.Split(body, -1) {
		if segment == "" || segment == "?" || segment == "~" {
			continue
		}
		if driveLetter.MatchString(segment) {
			continue
		}
		segments = append(segments, segment)
	}
	if strings.HasSuffix(body, "/") || strings.HasSuffix(body, "\\") {
		return PlaceholderPath + trailing
	}
	root := ""
	if len(segments) > 0 {
		root = segments[0]
	}
	rest := segments[1:]
	if len(rest) == 1 && homeRoots[strings.ToLower(root)] {
		return PlaceholderPath + trailing
	}
	cut := strings.LastIndex(body, "/")
	if index := strings.LastIndex(body, "\\"); index > cut {
		cut = index
	}
	if cut < 0 {
		return PlaceholderPath + trailing
	}
	return PlaceholderPath + body[cut:] + trailing
}

// replaceLongHashes replaces every run of at least 32 hexadecimal characters
// with PlaceholderHash. The TypeScript expression uses lookarounds to require
// that the run is maximal; matching the maximal run is the same condition.
func replaceLongHashes(text string) string {
	var out strings.Builder
	out.Grow(len(text))
	last := 0
	for _, match := range longHexPattern.FindAllStringIndex(text, -1) {
		out.WriteString(text[last:match[0]])
		out.WriteString(PlaceholderHash)
		last = match[1]
	}
	out.WriteString(text[last:])
	return out.String()
}

// Normalize turns one raw error message into the form used for comparison and
// signatures:
//
//  1. strip ANSI escapes and `\r`, collapse whitespace;
//  2. replace UUIDs, hashes, timestamps, durations, PIDs, ports,
//     line/column positions and temp-directory names with placeholders;
//  3. collapse absolute paths to `<path>`, keeping the last segment;
//  4. lowercase, and trim punctuation from both ends.
//
// It is pure and idempotent: no I/O, no clock, no random source.
func Normalize(raw string) string {
	text := ansiPattern.ReplaceAllString(raw, "")
	text = strings.ReplaceAll(text, "\r", "")
	text = spaceRun.ReplaceAllString(text, " ")
	text = uuidPattern.ReplaceAllString(text, PlaceholderUUID)
	text = replaceLongHashes(text)
	for _, noise := range noisePatterns {
		text = noise.pattern.ReplaceAllString(text, noise.replacement)
	}
	text = collapseAbsolutePaths(text)
	text = strings.ToLower(text)
	return edgePunctuation.ReplaceAllString(text, "")
}
