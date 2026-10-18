package core

import (
	"sort"
	"strings"
	"unicode"
)

// Tokenize keeps a placeholder whole: splitting `<path>` into `<`, `path` and
// `>` would make unrelated errors look similar just because both mention a
// path. A placeholder name is one or more lowercase ASCII letters.
const minPlaceholderLength = 1

// isCJKRune reports whether r belongs to a script that is tokenized by
// characters, not by whitespace: Han, Hiragana, Katakana and Hangul. The
// TypeScript tokenizer uses the same four scripts.
func isCJKRune(r rune) bool {
	return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana, unicode.Hangul)
}

// isWordRune reports whether r can continue a whitespace-delimited token. CJK
// characters are letters too, so callers must test isCJKRune first.
func isWordRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsNumber(r) || r == '_'
}

// placeholderEnd returns the index one past the closing ">" of a placeholder
// that starts at runes[start] == '<', or 0 when there is none.
func placeholderEnd(runes []rune, start int) int {
	i := start + 1
	for i < len(runes) && runes[i] >= 'a' && runes[i] <= 'z' {
		i++
	}
	if i > start+minPlaceholderLength && i < len(runes) && runes[i] == '>' {
		return i + 1
	}
	return 0
}

// Tokenize splits normalized text into its distinct tokens, sorted and free of
// duplicates.
//
// A Latin word or number is a token as it stands; a placeholder such as
// `<path>` stays one token; a CJK run becomes its character bigrams
// (`拒绝访问` -> `拒绝`, `绝访`, `访问`) and a single CJK character stays a
// unigram. Punctuation and whitespace only separate tokens. The input is
// expected to be Normalize() output: Tokenize does not lowercase.
//
// The result is sorted so that two runs of the same input are byte-identical;
// comparing token lists never depends on map iteration order.
func Tokenize(text string) []string {
	runes := []rune(text)
	seen := make(map[string]struct{})
	var word strings.Builder

	flushWord := func() {
		if word.Len() > 0 {
			seen[word.String()] = struct{}{}
			word.Reset()
		}
	}

	for i := 0; i < len(runes); {
		r := runes[i]
		if r == '<' {
			if end := placeholderEnd(runes, i); end > 0 {
				flushWord()
				seen[string(runes[i:end])] = struct{}{}
				i = end
				continue
			}
			flushWord()
			i++
			continue
		}
		if isCJKRune(r) {
			flushWord()
			start := i
			for i < len(runes) && isCJKRune(runes[i]) {
				i++
			}
			run := runes[start:i]
			if len(run) == 1 {
				seen[string(run)] = struct{}{}
				continue
			}
			// Bigrams are built from runes, never bytes: cutting UTF-8 bytes
			// would produce half a character and a token that cannot match.
			for k := 0; k+1 < len(run); k++ {
				seen[string(run[k:k+2])] = struct{}{}
			}
			continue
		}
		if isWordRune(r) {
			word.WriteRune(r)
			i++
			continue
		}
		flushWord()
		i++
	}
	flushWord()

	tokens := make([]string, 0, len(seen))
	for token := range seen {
		tokens = append(tokens, token)
	}
	sort.Strings(tokens)
	return tokens
}
