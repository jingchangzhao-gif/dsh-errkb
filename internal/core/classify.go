package core

import (
	"regexp"
	"strings"
)

// Category is the technical error class from §1 of the plan. The four classes
// are the whole domain: anything the classifier cannot place gets Unknown, and
// that is visible on purpose, so a growing Unknown bucket shows a gap in the
// rules instead of hiding one.
//
// This is a different axis from the capture channel (§3.1 A–E, how an error was
// observed) and from an ErrorEvent's fingerprint category. The connector maps
// the classifier result onto the event; the capture channel never becomes a
// category.
type Category string

const (
	// CategoryEnvironment: environment, toolchain and runtime errors. The
	// machine or the runtime is wrong, not the code under test.
	CategoryEnvironment Category = "environment"
	// CategoryDependency: dependency, version, configuration, authentication,
	// permission and network errors. Something outside the local code is wrong.
	CategoryDependency Category = "dependency"
	// CategoryBuild: compile, test, code-run and command-exit errors. The code
	// or command under test failed.
	CategoryBuild Category = "build"
	// CategoryToolCall: tool call, plugin, hook, MCP and protocol errors.
	CategoryToolCall Category = "tool-call"
	// CategoryUnknown: no rule matched. Explicit, never a silent fallback.
	CategoryUnknown Category = "unknown"
)

// Severity is how much the error demands attention. It has exactly two values
// in v1: a transient error is noise that heals by itself and is only counted
// until it crosses a connector threshold, anything else records at once.
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

// ClassifyInput is the observable evidence a connector can supply. Every field
// is optional; an empty input classifies as unknown and permanent.
type ClassifyInput struct {
	// Source is the capture source: "tool", "command", "llm" or "agent". It is
	// evidence, not a category: the category rules below still decide.
	Source string
	// Code is a tool or protocol error code, such as ENOENT or RATE_LIMIT.
	Code string
	// Message is the error text after the headline steps (headline and code may
	// repeat here).
	Message string
	// Command is the command that produced the error, when it was a command.
	Command string
	// Tool is the tool name for a tool or agent failure.
	Tool string
	// ExitCode is the process exit code, when one was observed.
	ExitCode *int
}

// Classification is the three values the caller stores: category, severity and
// the transient flag. The classifier decides nothing else: whether to record,
// count or promote is the capture layer's rule (EKB-031/EKB-032), and the
// threshold lives in the connector configuration (EKB-007).
type Classification struct {
	Category  Category
	Severity  Severity
	Transient bool
}

// Permanent and transient LLM failure codes. They are the same list the
// TypeScript capture uses: a permanent code records at once, a transient code
// is noise that a per-session counter promotes after its threshold.
var (
	permanentCodes = map[string]struct{}{
		"AUTH":             {},
		"QUOTA":            {},
		"INVALID_REQUEST":  {},
		"CONTEXT_OVERFLOW": {},
		"NO_ADAPTER":       {},
		"UNKNOWN":          {},
	}
	transientCodes = map[string]struct{}{
		"RATE_LIMIT":     {},
		"SERVER":         {},
		"TIMEOUT":        {},
		"TRANSPORT":      {},
		"EMPTY_RESPONSE": {},
	}
)

// Tool, hook, plugin, MCP and protocol evidence. Checked first because a tool
// failure often carries another layer's message ("... failed: permission
// denied"), and the tool boundary is the fact worth recording.
var toolCallPatterns = compileAll(
	`(?i)\b(?:mcp|jsonrpc|json-rpc)\b`,
	`(?i)\b(?:hook|plugin)\b[^\n]{0,40}\b(?:error|failed|failure|exception)\b`,
	`(?i)\b(?:tool|method)\b[^\n]{0,40}\b(?:failed|error|not found|invalid|unknown)\b`,
	`(?i)\b(?:invalid|unknown)\s+(?:tool|method|params)\b`,
	`(?i)\bprotocol\s+(?:error|violation)\b`,
	`(?i)\bno such tool\b`,
)

// Environment, toolchain and runtime evidence. It outranks build errors because
// a missing binary or a crashed runtime also shows up as a non-zero exit code,
// but installing the toolchain, not changing the code, is the fix.
var environmentPatterns = compileAll(
	`(?i)\bcommand not found\b`,
	`(?i)is not recognized as an internal or external command\b`,
	`(?i)\b(?:executable|binary|toolchain|interpreter)\b[^\n]{0,40}\bnot found\b`,
	`(?i)\b(?:spawn|exec)\b[^\n]{0,40}\bENOENT\b`,
	`(?i)\bENOENT\b[^\n]{0,40}\b(?:spawn|exec)\b`,
	`(?i)\b(?:go|node|python3?|npm|pnpm|yarn|cargo|rustc|dotnet|java|javac|psql|sqlite3|git|cue|docker)\b[^\n]{0,20}\bnot found\b`,
	`(?i)\bunsupported\s+(?:platform|operating system|os|architecture|arch)\b`,
	`(?i)\bTraceback \(most recent call last\):`,
	`(?i)\bpanic:\s`,
)

// Dependency, version, configuration, authentication, permission and network
// evidence. Checked before build evidence so an auth or DNS failure that exits
// non-zero is filed as what it is.
var dependencyPatterns = compileAll(
	`(?i)\b(?:unauthori[sz]ed|forbidden|permission denied|access denied)\b`,
	`\bE(?:ACCES|PERM)\b`,
	`(?i)\b(?:invalid|missing|expired|bad)\s+(?:api[ _-]?key|token|credential|credentials|password|secret)\b`,
	`(?i)\b(?:authentication|authorization)\s+(?:failed|error|required|denied)\b`,
	`\b(?:401|403)\b`,
	`(?i)\b(?:certificate|tls|ssl)\b[^\n]{0,40}\b(?:error|failed|verify|verification|expired|untrusted)\b`,
	`(?i)\b(?:ECONNREFUSED|ECONNRESET|ETIMEDOUT|EAI_AGAIN|ENOTFOUND|EPIPE|ECONNABORTED|EHOSTUNREACH|ENETUNREACH)\b`,
	`(?i)\b(?:network|connection|connect)\s+(?:error|failed|failure|refused|reset|timed out|closed|unreachable)\b`,
	`(?i)\bsocket hang up\b`,
	`(?i)\b(?:rate ?limit(?:ed|ing)?|too many requests)\b`,
	`(?i)\b(?:timed? ?out|timeout)\b`,
	`(?i)\b(?:dependency|dependencies)\b[^\n]{0,40}\b(?:error|conflict|failed|missing|not found)\b`,
	`(?i)\bERESOLVE\b`,
	`(?i)cannot find module\b`,
	`(?i)\b(?:invalid|malformed|missing)\s+(?:config|configuration|setting|settings)\b`,
)

// Compile, test, code-run and command-exit evidence. Last of the specific rules:
// by here no tool, environment or dependency fact explains the failure.
var buildPatterns = compileAll(
	`\bTS\d{4}\b`,
	`(?i)\b(?:compile|compilation|build)\s+(?:error|failed|failure)\b`,
	`(?i)\bsyntax error\b`,
	`(?i)\b(?:test|tests)\s+(?:failed|failure)\b`,
	`(?i)\bAssertionError\b`,
	`(?i)\bexit (?:code|status)\s+[1-9]\d*\b`,
	`(?i)\bcommand (?:failed|exited)\b`,
	`(?m)^\s*make(?:\[\d+\])?: \*\*\*`,
)

// Transient classification is a two-sided evidence list. Permanent patterns win
// over transient ones: "AUTH ... timed out" is a credential problem that will
// repeat, and a DNS name that does not exist will not start existing on retry.
var (
	permanentTransientPatterns = compileAll(
		`(?i)\b(?:unauthori[sz]ed|forbidden|permission denied|access denied)\b`,
		`\bE(?:ACCES|PERM)\b`,
		`(?i)\bENOTFOUND\b`,
		`(?i)\b(?:invalid|missing|expired|bad)\s+(?:api[ _-]?key|token|credential|credentials|password|secret)\b`,
		`(?i)\b(?:quota|context (?:length|window)|maximum context)\b`,
		`(?i)\b(?:certificate|tls|ssl)\b[^\n]{0,40}\b(?:expired|untrusted)\b`,
		`(?i)\binvalid request\b`,
		`(?i)\bunsupported\s+(?:model|platform|protocol version)\b`,
	)
	transientPatterns = compileAll(
		`(?i)\b(?:rate ?limit(?:ed|ing)?|too many requests)\b`,
		`\b429\b`,
		`(?i)\b(?:timed? ?out|timeout)\b`,
		`\bE(?:TIMEDOUT|CONNRESET|PIPE|CONNABORTED|AI_AGAIN)\b`,
		`(?i)\b(?:connection|network)\s+(?:reset|closed|refused|error|failed)\b`,
		`(?i)\bsocket hang up\b`,
		`(?i)\bempty\s+(?:response|reply|result)\b`,
		`(?i)\bno response\b`,
		`(?i)\b5xx\b`,
		`(?i)\binternal server error\b`,
		`(?i)\bbad gateway\b`,
		`(?i)\bservice unavailable\b`,
		`(?i)\bgateway time-?out\b`,
		`(?i)\btemporar(?:ily|y) unavailable\b`,
		`(?i)\btransport (?:error|interrupted)\b`,
		`(?i)\b(?:http|status|server|error|code)\b\D{0,16}\b(?:500|502|503|504)\b`,
	)
)

// Classify assigns one category, one severity and the transient flag from the
// observable fields. It is a pure function: no I/O, no clock, no counters.
//
// Category priority, first match wins:
//
//  1. tool call (tool/agent source, a tool name, or MCP/hook/plugin/protocol text)
//  2. environment (missing toolchain/binary, unsupported platform, traceback, panic)
//  3. dependency (auth/permission/network/config/dependency text, LLM codes, llm source)
//  4. build (compile/test text, non-zero exit code, command source)
//  5. unknown
//
// Transient priority, first match wins: permanent code, transient code, permanent
// text, transient text, otherwise permanent.
//
// Coverage: the rules recognize the codes listed above, the text families named
// in the pattern comments above, non-zero exit codes and the four capture
// sources. Known gaps: severity has no grade above "error"; Windows HRESULT and
// errno numbers are not decoded (the message must carry the name); compiler
// errors from languages without a recognizable marker (only TypeScript's TS####
// is matched); an unknown code in an empty message stays unknown.
func Classify(input ClassifyInput) Classification {
	transient := isTransient(input.Code, input.Message)
	severity := SeverityError
	if transient {
		severity = SeverityWarning
	}
	return Classification{
		Category:  classifyCategory(input),
		Severity:  severity,
		Transient: transient,
	}
}

func isTransient(code, message string) bool {
	normalizedCode := normalizeCode(code)
	if _, ok := permanentCodes[normalizedCode]; ok {
		return false
	}
	if _, ok := transientCodes[normalizedCode]; ok {
		return true
	}
	text := code + "\n" + message
	if anyMatch(permanentTransientPatterns, text) {
		return false
	}
	return anyMatch(transientPatterns, text)
}

func classifyCategory(input ClassifyInput) Category {
	text := input.Code + "\n" + input.Message
	source := strings.ToLower(strings.TrimSpace(input.Source))

	if source == "tool" || source == "agent" || strings.TrimSpace(input.Tool) != "" {
		return CategoryToolCall
	}
	if anyMatch(toolCallPatterns, text) {
		return CategoryToolCall
	}
	if anyMatch(environmentPatterns, text) {
		return CategoryEnvironment
	}
	if source == "llm" || isLLMFailureCode(input.Code) {
		return CategoryDependency
	}
	if anyMatch(dependencyPatterns, text) {
		return CategoryDependency
	}
	if anyMatch(buildPatterns, text) {
		return CategoryBuild
	}
	if input.ExitCode != nil && *input.ExitCode != 0 {
		return CategoryBuild
	}
	if source == "command" {
		return CategoryBuild
	}
	return CategoryUnknown
}

// isLLMFailureCode reports whether the code is one the LLM adapters emit. Those
// failures are dependency errors (auth, quota, service, transport), not tool
// errors: the tool did its job, the remote service did not.
func isLLMFailureCode(code string) bool {
	normalizedCode := normalizeCode(code)
	if normalizedCode == "UNKNOWN" {
		return false
	}
	if _, ok := permanentCodes[normalizedCode]; ok {
		return true
	}
	_, ok := transientCodes[normalizedCode]
	return ok
}

func normalizeCode(code string) string {
	return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(code), "-", "_"))
}

func compileAll(patterns ...string) []*regexp.Regexp {
	compiled := make([]*regexp.Regexp, 0, len(patterns))
	for _, pattern := range patterns {
		compiled = append(compiled, regexp.MustCompile(pattern))
	}
	return compiled
}

func anyMatch(patterns []*regexp.Regexp, text string) bool {
	for _, pattern := range patterns {
		if pattern.MatchString(text) {
			return true
		}
	}
	return false
}
