package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

type fingerprintCorpus struct {
	Cases []fingerprintCase `json:"cases"`
}

type fingerprintCase struct {
	CaseID string `json:"case_id"`
	Input  struct {
		Category string `json:"category"`
		Message  string `json:"message"`
	} `json:"input"`
	Expected string `json:"expected"`
}

func loadFingerprintCorpus(t *testing.T) fingerprintCorpus {
	t.Helper()
	path := filepath.Join("..", "..", "spec", "corpus", "fingerprint.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read corpus: %v", err)
	}
	var corpus fingerprintCorpus
	if err := json.Unmarshal(data, &corpus); err != nil {
		t.Fatalf("parse corpus: %v", err)
	}
	if len(corpus.Cases) == 0 {
		t.Fatal("corpus has no cases")
	}
	return corpus
}

var hexFingerprint = regexp.MustCompile(`^[0-9a-f]{12}$`)

func TestFingerprintCorpus(t *testing.T) {
	corpus := loadFingerprintCorpus(t)
	for _, testCase := range corpus.Cases {
		t.Run(testCase.CaseID, func(t *testing.T) {
			got := Signature(testCase.Input.Category, Normalize(testCase.Input.Message))
			if got != testCase.Expected {
				t.Errorf("signature(%q, %q) = %q, want %q",
					testCase.Input.Category, testCase.Input.Message, got, testCase.Expected)
			}
			if len(got) != SignatureLength {
				t.Errorf("length = %d, want %d", len(got), SignatureLength)
			}
			if !hexFingerprint.MatchString(got) {
				t.Errorf("%q is not twelve lowercase hex characters", got)
			}
			if again := Signature(testCase.Input.Category, Normalize(testCase.Input.Message)); again != got {
				t.Errorf("second call differs: %q vs %q", again, got)
			}
		})
	}
}

// TestFingerprintBoundaries covers inputs the corpus does not: an empty
// message, a category change, and the separation between normalization and
// hashing. None of these are corpus cases.
func TestFingerprintBoundaries(t *testing.T) {
	empty := Signature("tool", "")
	if !hexFingerprint.MatchString(empty) {
		t.Errorf("empty message: %q is not a fingerprint", empty)
	}

	// The category is part of the hash: the same message under another
	// category must not share a fingerprint.
	if Signature("tool", "boom") == Signature("llm", "boom") {
		t.Error("different categories produced the same fingerprint")
	}

	// Signature hashes its input as is: normalizing inside it would make a
	// normalization change invisible.
	raw := Signature("tool", "Error:  Boom")
	normalized := Signature("tool", Normalize("Error:  Boom"))
	if raw == normalized {
		t.Error("Signature already normalized its input")
	}
}
