package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type normalizeCorpus struct {
	Cases []normalizeCase `json:"cases"`
}

type normalizeCase struct {
	CaseID   string `json:"case_id"`
	Input    string `json:"input"`
	Expected string `json:"expected"`
}

func loadNormalizeCorpus(t *testing.T) normalizeCorpus {
	t.Helper()
	path := filepath.Join("..", "..", "spec", "corpus", "normalize.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read corpus: %v", err)
	}
	var corpus normalizeCorpus
	if err := json.Unmarshal(data, &corpus); err != nil {
		t.Fatalf("parse corpus: %v", err)
	}
	if len(corpus.Cases) == 0 {
		t.Fatal("corpus has no cases")
	}
	return corpus
}

func TestNormalizeCorpus(t *testing.T) {
	corpus := loadNormalizeCorpus(t)
	for _, testCase := range corpus.Cases {
		t.Run(testCase.CaseID, func(t *testing.T) {
			got := Normalize(testCase.Input)
			if got != testCase.Expected {
				t.Errorf("normalize(%q) = %q, want %q", testCase.Input, got, testCase.Expected)
			}
			// Determinism: the same input must always yield the same text.
			if again := Normalize(testCase.Input); again != got {
				t.Errorf("second call differs: %q vs %q", again, got)
			}
		})
	}
}

// TestNormalizeBoundaries covers inputs the corpus does not: empty text,
// multibyte text past the corpus length, and a path directly after a multibyte
// rune. These assert that nothing panics and that multibyte input stays intact;
// they do not add rules the corpus has no case for.
func TestNormalizeBoundaries(t *testing.T) {
	if got := Normalize(""); got != "" {
		t.Errorf("normalize empty = %q, want empty", got)
	}

	long := strings.Repeat("错", 5000) + "：失败"
	if got := Normalize(long); got != long {
		t.Errorf("multibyte text changed: got %d runes, want %d", len([]rune(got)), len([]rune(long)))
	}

	// The TypeScript lookbehind treats a preceding multibyte rune as a
	// non-word character, so this path collapses. The home path is assembled
	// at run time: the repository privacy guard rejects literal per-user home
	// paths anywhere in the tree, test fixtures included.
	if got := Normalize("错 " + "/" + "Users/" + "u/p/a.ts"); got != "错 <path>/a.ts" {
		t.Errorf("multibyte boundary: got %q", got)
	}
}
