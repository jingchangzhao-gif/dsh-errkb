package core

import (
	"crypto/sha256"
	"encoding/hex"
)

// SignatureLength is the number of hexadecimal characters a fingerprint keeps.
// It is a hard requirement, not a default: ErrorEvent.fingerprint storage and
// indexing assume exactly this length.
const SignatureLength = 12

// Signature returns the fingerprint of an error message that has already been
// run through Normalize.
//
// The algorithm is byte-for-byte reproducible and matches src/signature.ts:
//
//	fingerprint = hex(sha256(category + "\x00" + normalized))[:12]
//
// Categories are hashed as the UTF-8 bytes the caller passed (the capture
// categories are ASCII: "tool", "command-exit", "llm", "agent"). The single
// NUL separator keeps ("a", "bc") and ("ab", "c") apart; the normalized text
// is hashed as-is, without a second lowercasing or trimming. The hash is
// SHA-256; the returned value is the first 12 lowercase hex characters, i.e.
// the first 6 bytes of the digest.
//
// Signature does no text replacement: normalization stays in Normalize, so a
// change in either side is visible on its own.
func Signature(category, normalized string) string {
	sum := sha256.Sum256([]byte(category + "\x00" + normalized))
	return hex.EncodeToString(sum[:])[:SignatureLength]
}
