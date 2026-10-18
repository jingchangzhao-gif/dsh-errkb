// Package redact is the single redaction engine: credential and personal-data
// text is replaced before it reaches any storage boundary (database row, log
// line or offline spool), never after. The rules themselves are generated from
// spec/redact.cue into rules_gen.go; this file is the pure engine.
//
// The package has no I/O and no global state: it is a function of its input,
// so a write path cannot accidentally "store first, redact later".
package redact

import (
	"regexp"
	"strings"

	"github.com/jingchangzhao-gif/dsh-errkb/internal/core"
)

// Share is the redaction policy. Public text may leave the machine and loses
// whole absolute paths; private text stays with the user and only loses the
// user name in a home-directory path.
type Share string

const (
	// Public redacts for text that may be shared outside the machine.
	Public Share = "public"
	// Private redacts for text that stays on the machine.
	Private Share = "private"
)

// SampleMaxChars is the cap applied by Sample, matching the TypeScript engine.
const SampleMaxChars = 500

const truncationMark = "…"

var (
	upperPattern = regexp.MustCompile(`[A-Z]`)
	lowerPattern = regexp.MustCompile(`[a-z]`)
	digitPattern = regexp.MustCompile(`[0-9]`)
)

// Text applies the full redaction policy to text.
//
// Public mode collapses absolute paths first, then applies every generated
// rule in order: a path can hide a credential, and the rules must see the
// collapsed form. Private mode first replaces a home-directory prefix with "~"
// and then applies the same rules, matching src/redact.ts exactly.
func Text(text string, share Share) string {
	out := text
	if share == Private {
		for _, rule := range homePrefixRules {
			out = rule.Pattern.ReplaceAllString(out, rule.Replacement)
		}
	} else {
		out = core.CollapseAbsolutePaths(out)
	}
	for _, rule := range rules {
		out = applyRule(out, rule)
	}
	return out
}

// Sample redacts text and then caps it at SampleMaxChars characters the way
// src/redact.ts does: private samples keep their length, public samples are
// capped with a trailing ellipsis.
func Sample(text string, share Share) string {
	redacted := Text(text, share)
	if share == Private {
		return redacted
	}
	return CapSample(redacted, SampleMaxChars)
}

// CapSample shortens text to at most max characters, counting code points and
// ending the result with an ellipsis when it was cut.
func CapSample(text string, max int) string {
	chars := []rune(text)
	if len(chars) <= max {
		return text
	}
	return string(chars[:max-1]) + truncationMark
}

// applyRule replaces every match of one generated rule. Long base64 tokens are
// the only family whose Go regular expression cannot carry the lookaheads the
// JavaScript side uses, so their mixed-case-and-digit requirement is enforced
// here, on the match itself.
func applyRule(text string, rule Rule) string {
	if !rule.MixedCaseDigit {
		return rule.Pattern.ReplaceAllString(text, rule.Replacement)
	}
	return rule.Pattern.ReplaceAllStringFunc(text, func(match string) string {
		if !mixedCaseDigit(strings.TrimRight(match, "=")) {
			return match
		}
		return rule.Replacement
	})
}

func mixedCaseDigit(token string) bool {
	return upperPattern.MatchString(token) &&
		lowerPattern.MatchString(token) &&
		digitPattern.MatchString(token)
}
