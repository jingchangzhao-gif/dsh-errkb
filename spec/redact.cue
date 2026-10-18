package spec

// Redaction rules: the single source for the Go engine, the TypeScript engine
// and the CI privacy guard (§6, EKB-014).
//
// Every credential and personal-data family lives here exactly once. The
// generator `tests/gen-redact-rules.ts` turns this file into three artifacts —
// `internal/redact/rules_gen.go`, `src/redact-patterns.ts` and the PATTERN line
// of `.github/workflows/privacy-guard.yml` — so a rule cannot exist in one
// output and be missing in another.
//
// Two encodings per rule, one meaning. JavaScript regular expressions support
// lookbehind and lookahead; Go's RE2 dialect supports neither. Where the two
// dialects cannot share one expression the file carries both forms and the
// tests prove they agree on the conformance corpus:
//
//   - `long-hex` and `long-base64`: the JavaScript form keeps its boundary
//     lookarounds; the Go form matches the maximal run and lets the engine
//     decide (`mixed_case_digit`) whether the run is a random token.
//   - the home-prefix family: JavaScript keeps one lookbehind expression, Go
//     uses a capture form whose replacement restores the consumed character.
//
// Rules that match are applied in list order. `replacement` is a
// JavaScript/Go replacement template: `$1`-style references keep the matched
// key name, so `api_key=abc` becomes `api_key=<secret>` rather than losing the
// name that makes the message readable.
//
// The examples are assembled from segments at code generation time: a literal
// credential-shaped string in a committed file would trip the very guard these
// rules power.

// #RedactVersion is the version of this rules document. Raising it is a
// compatibility statement, exactly like the wire contracts (see compat.cue).
#RedactVersion: "v1"

// #Rule is one text rule applied to every stored text.
#Rule: {
	// Stable name, used in tests, the conformance corpus (`family: <name>`)
	// and the generated artifacts.
	name: string & !=""
	// One line: what the family is and why it is redacted.
	purpose: string & !=""
	// Replacement template, `$1`-style references allowed.
	replacement: string
	// The privacy-guard alternative this family covers, if the CI guard needs
	// one. The guard runs under `grep -E`, so this is POSIX ERE, not RE2/JS.
	guard?: string
	// JavaScript encoding: a `RegExp` source and flags emitted by the generator.
	js: {
		source: string & !=""
		flags:  string & =~"^[gi]*$"
	}
	// RE2 encoding: the same rule without lookbehind/lookahead.
	go: {
		source:           string & !=""
		mixed_case_digit: bool | *false
	}
	// A text that must be redacted by this rule, split into segments so the
	// committed artifacts cannot match the privacy guard themselves.
	example: [string, ...string]
}

// #PathRule is one private-mode home-directory rule (the Go encoding).
#PathRule: {
	name:        string & !=""
	purpose:     string & !=""
	pattern:     string & !=""
	replacement: string
	example: [string, ...string]
}

// #Redact is the whole rules document.
#HomePrefix: {
	name:        string & !=""
	purpose:     string & !=""
	replacement: string
	js: {source: string & !="", flags: string & =~"^[gi]*$"}
	go: [...#PathRule]
}

#Redact: {
	schema_version: #RedactVersion
	placeholders: {secret: "<secret>", email: "<email>", request_id: "<request-id>", hex: "<hex>", base64: "<base64>"}
	rules: [...#Rule]
	home_prefix: [...#HomePrefix]
	path_guards: [...string]
}

// redact is the concrete document the generator exports.
redact: #Redact & {
	rules: [
		{
			name:        "authorization-header"
			purpose:     "Drops the credential behind an Authorization or Proxy-Authorization header, including the Basic/Bearer scheme word."
			replacement: "$1$2<secret>"
			js: {source: "\\b((?:proxy-)?authorization)([\"']?\\s*[:=]\\s*[\"']?)(?:(?:basic|bearer|token|digest)\\s+)?[^\\s\"',;]+", flags: "gi"}
			go: {source: "\\b((?:proxy-)?authorization)([\"']?\\s*[:=]\\s*[\"']?)(?:(?:basic|bearer|token|digest)\\s+)?[^\\s\"',;]+", mixed_case_digit: false}
			example: ["401 Unauthorized; Authorization: Basic ", "dXNlcjpw", "YXNz"]
		},
		{
			name:        "api-key-assignment"
			purpose:     "Drops the value of an api_key, api-key or x-api-key assignment while keeping the key name."
			replacement: "$1$2<secret>"
			js: {source: "\\b((?:x-)?api[_-]?key)([\"']?\\s*[:=]\\s*[\"']?)[^\\s\"'&,;]+", flags: "gi"}
			go: {source: "\\b((?:x-)?api[_-]?key)([\"']?\\s*[:=]\\s*[\"']?)[^\\s\"'&,;]+", mixed_case_digit: false}
			example: ["GET /v1/models?api", "_key=k3y-Value-77 failed"]
		},
		{
			name:        "secret-assignment"
			purpose:     "Drops token, password, passwd, client_secret and secret values in `key=value` or quoted JSON form."
			replacement: "$1$2<secret>"
			js: {source: "\\b((?:access_|refresh_|id_|auth_)?token|password|passwd|client_secret|secret)([\"']?\\s*=\\s*[\"']?|[\"']\\s*:\\s*[\"']?)[^\\s\"'&,;]+", flags: "gi"}
			go: {source: "\\b((?:access_|refresh_|id_|auth_)?token|password|passwd|client_secret|secret)([\"']?\\s*=\\s*[\"']?|[\"']\\s*:\\s*[\"']?)[^\\s\"'&,;]+", mixed_case_digit: false}
			example: ["callback?token=", "tok-Value-99", "&state=1"]
		},
		{
			name:        "bearer-token"
			purpose:     "Drops a bare Bearer token that no header rule caught."
			replacement: "$1 <secret>"
			guard:       "Bearer [A-Za-z0-9._~+/-]{20,}"
			js: {source: "\\b(Bearer)\\s+[A-Za-z0-9._~+/=-]{8,}", flags: "gi"}
			go: {source: "\\b(Bearer)\\s+[A-Za-z0-9._~+/=-]{8,}", mixed_case_digit: false}
			example: ["header Bearer ", "eyJhbGciOi", "Jfoo.bar.baz", " expired"]
		},
		{
			name:        "openai-style-key"
			purpose:     "Drops an OpenAI-style sk- key."
			replacement: "<secret>"
			guard:       "sk-[A-Za-z0-9]{16,}"
			js: {source: "\\bsk-[A-Za-z0-9_-]{16,}", flags: "g"}
			go: {source: "\\bsk-[A-Za-z0-9_-]{16,}", mixed_case_digit: false}
			example: ["Incorrect API key provided: sk-", "proj-", "Zq7Xw2Lp9Rt4Vb8Nm3Kd6Hs1", "."]
		},
		{
			name:        "xai-key"
			purpose:     "Drops an xAI-style xai- key that the CI guard does not know."
			replacement: "<secret>"
			js: {source: "\\bxai-[A-Za-z0-9_-]{16,}", flags: "g"}
			go: {source: "\\bxai-[A-Za-z0-9_-]{16,}", mixed_case_digit: false}
			example: ["invalid key xai-", "Zq7Xw2Lp9Rt4Vb8Nm3Kd6Hs1"]
		},
		{
			name:        "google-api-key"
			purpose:     "Drops a Google API key (AIza prefix) that the CI guard does not know."
			replacement: "<secret>"
			js: {source: "\\bAIza[0-9A-Za-z_-]{30,}", flags: "g"}
			go: {source: "\\bAIza[0-9A-Za-z_-]{30,}", mixed_case_digit: false}
			example: ["key=AIza", "Zq7Xw2Lp9Rt4Vb8Nm3Kd6Hs1", "AbCdEfGhIjK", " is invalid"]
		},
		{
			name:        "github-token"
			purpose:     "Drops every classic GitHub token prefix (ghp_, gho_, ghu_, ghs_, ghr_)."
			replacement: "<secret>"
			guard:       "ghp_[A-Za-z0-9]{20,}"
			js: {source: "\\bgh[pousr]_[A-Za-z0-9]{20,}", flags: "g"}
			go: {source: "\\bgh[pousr]_[A-Za-z0-9]{20,}", mixed_case_digit: false}
			example: ["remote: token ghp_", "Zq7Xw2Lp9Rt4Vb8Nm3Kd6Hs1", " revoked"]
		},
		{
			name:        "github-fine-grained-token"
			purpose:     "Drops a fine-grained GitHub token (github_pat_)."
			replacement: "<secret>"
			guard:       "github_pat_[A-Za-z0-9_]{20,}"
			js: {source: "\\bgithub_pat_[A-Za-z0-9_]{20,}", flags: "g"}
			go: {source: "\\bgithub_pat_[A-Za-z0-9_]{20,}", mixed_case_digit: false}
			example: ["auth github_pat_", "Zq7Xw2Lp9Rt4Vb8Nm3Kd6Hs1", "_x denied"]
		},
		{
			name:        "aws-access-key"
			purpose:     "Drops an AWS access key ID (AKIA or ASIA prefix)."
			replacement: "<secret>"
			guard:       "AKIA[0-9A-Z]{16}"
			js: {source: "\\b(?:AKIA|ASIA)[0-9A-Z]{16}", flags: "g"}
			go: {source: "\\b(?:AKIA|ASIA)[0-9A-Z]{16}", mixed_case_digit: false}
			example: ["The AWS Access Key Id AKIA", "IOSFODNN7EXAMPLE", " does not exist"]
		},
		{
			name:        "email"
			purpose:     "Drops every e-mail address, not only the personal domains the CI guard lists: an address at a company domain identifies a person just as well."
			replacement: "<email>"
			guard:       "[A-Za-z0-9._%+-]+@([A-Za-z0-9-]+\\.)*(edu|edu\\.[a-z]{2}|ac\\.[a-z]{2}|gmail\\.com|outlook\\.com|hotmail\\.com|qq\\.com|163\\.com|126\\.com)"
			js: {source: "[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(?:\\.[A-Za-z0-9-]+)*\\.[A-Za-z]{2,}", flags: "g"}
			go: {source: "[A-Za-z0-9._%+-]+@[A-Za-z0-9-]+(?:\\.[A-Za-z0-9-]+)*\\.[A-Za-z]{2,}", mixed_case_digit: false}
			example: ["commit author kim.lee", "@", "gmail.com rejected"]
		},
		{
			name:        "request-id"
			purpose:     "Drops a raw request ID assigned to request_id, request-id or x-request-id: a provider can map it back to an account."
			replacement: "$1$2<request-id>"
			js: {source: "\\b((?:x-)?request[_-]?id)([\"']?\\s*[:=]\\s*[\"']?)[A-Za-z0-9._-]+", flags: "gi"}
			go: {source: "\\b((?:x-)?request[_-]?id)([\"']?\\s*[:=]\\s*[\"']?)[A-Za-z0-9._-]+", mixed_case_digit: false}
			example: ["server error (request_id: ", "abc-123-def", ")"]
		},
		{
			name:        "request-id-token"
			purpose:     "Drops a req_-prefixed raw request identifier."
			replacement: "<request-id>"
			js: {source: "\\breq_[A-Za-z0-9]{16,}", flags: "g"}
			go: {source: "\\breq_[A-Za-z0-9]{16,}", mixed_case_digit: false}
			example: ["overloaded req_", "Zq7Xw2Lp9Rt4Vb8Nm3Kd6Hs1"]
		},
		{
			name:        "long-hex"
			purpose:     "Replaces any 32-or-more character hexadecimal run: tokens, digests and session IDs."
			replacement: "<hex>"
			js: {source: "(?<![0-9A-Fa-f])[0-9A-Fa-f]{32,}(?![0-9A-Fa-f])", flags: "g"}
			go: {source: "[0-9A-Fa-f]{32,}", mixed_case_digit: false}
			example: ["session ", "0123456789abcdef0123456789ABCDEF", " expired"]
		},
		{
			name:        "long-base64"
			purpose:     "Replaces a 40-or-more character base64-like run that mixes upper case, lower case and a digit — the mix is what separates a random token from a long identifier or a word."
			replacement: "<base64>"
			js: {source: "(?<![A-Za-z0-9+/_=-])(?=[A-Za-z0-9+/_-]*[A-Z])(?=[A-Za-z0-9+/_-]*[a-z])(?=[A-Za-z0-9+/_-]*\\d)[A-Za-z0-9+/_-]{40,}={0,2}", flags: "g"}
			go: {source: "[A-Za-z0-9+/_-]{40,}={0,2}", mixed_case_digit: true}
			example: ["blob ", "Zq7Xw2Lp9Rt4Vb8Nm3Kd6Hs1", "Zq7Xw2Lp9Rt4Vb8Nm3Kd6Hs1", "== rejected"]
		},
	]
	home_prefix: [
		{
			name:        "home-prefix"
			purpose:     "In private mode paths are kept, but the per-user home directory still becomes ~ so a user name never reaches the disk in either mode."
			replacement: "~"
			js: {source: "(?:\\b[A-Za-z]:[\\\\/]Users|(?<![\\w.~-])\\/Users|(?<![\\w.~-])\\/home)[\\\\/][^\\\\/\\s'\"`<>|:*?]+", flags: "g"}
			go: [
				{
					name:        "home-prefix-drive"
					purpose:     "A Windows drive path under Users."
					pattern:     "\\b[A-Za-z]:[\\\\/]Users[\\\\/][^\\\\/\\s'\"\\x60<>|:*?]+"
					replacement: "~"
					example: ["open C:", "\\", "Users", "\\", "kim", "\\", "proj", "\\", "a.ts"]
				},
				{
					name:        "home-prefix-home"
					purpose:     "A POSIX home directory under /Users or /home; the capture restores the character that the RE2 dialect cannot look behind."
					pattern:     "(^|[^\\w.~-])(?:/Users|/home)[\\\\/][^\\\\/\\s'\"\\x60<>|:*?]+"
					replacement: "$1~"
					example: ["ENOENT '", "/", "home/", "kim/proj/a.ts", "'"]
				},
			]
		},
	]
	path_guards: [
		"[A-Za-z]:\\\\[^\\\\`]+\\\\",
		"/Users/[A-Za-z0-9._-]+/",
		"/home/[A-Za-z0-9._-]+/",
	]
}
