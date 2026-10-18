package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type tokenizeCorpus struct {
	Cases []tokenizeCase `json:"cases"`
}

type tokenizeCase struct {
	CaseID   string   `json:"case_id"`
	Input    string   `json:"input"`
	Expected []string `json:"expected"`
}

func loadTokenizeCorpus(t *testing.T) tokenizeCorpus {
	t.Helper()
	path := filepath.Join("..", "..", "spec", "corpus", "tokenize.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read corpus: %v", err)
	}
	var corpus tokenizeCorpus
	if err := json.Unmarshal(data, &corpus); err != nil {
		t.Fatalf("parse corpus: %v", err)
	}
	if len(corpus.Cases) == 0 {
		t.Fatal("corpus has no cases")
	}
	return corpus
}

func TestTokenizeCorpus(t *testing.T) {
	corpus := loadTokenizeCorpus(t)
	for _, testCase := range corpus.Cases {
		t.Run(testCase.CaseID, func(t *testing.T) {
			got := Tokenize(testCase.Input)
			if !reflect.DeepEqual(got, testCase.Expected) {
				t.Errorf("Tokenize(%q) = %v, want %v", testCase.Input, got, testCase.Expected)
			}
			// Stability: a second call returns the same sequence, not a
			// different map iteration order.
			if again := Tokenize(testCase.Input); !reflect.DeepEqual(again, got) {
				t.Errorf("second call differs: %v vs %v", again, got)
			}
		})
	}
}

// TestTokenizeBoundaries covers inputs the corpus does not.
func TestTokenizeBoundaries(t *testing.T) {
	if got := Tokenize(""); got != nil && len(got) != 0 {
		t.Errorf("Tokenize empty = %v, want no tokens", got)
	}

	if got := Tokenize(" ,.:;!? "); len(got) != 0 {
		t.Errorf("Tokenize punctuation = %v, want no tokens", got)
	}

	// A single CJK character stays a unigram; a run of them becomes bigrams.
	if got, want := Tokenize("错"), []string{"错"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Tokenize single CJK = %v, want %v", got, want)
	}

	// A Latin word and a CJK run that touch are still separate tokens: the
	// word ends at the script boundary.
	if got, want := Tokenize("eperm拒绝访问"), []string{"eperm", "拒绝", "绝访", "访问"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Tokenize adjacent scripts = %v, want %v", got, want)
	}

	// Placeholders are one token and are not split around the brackets.
	if got, want := Tokenize("open <path> failed"), []string{"<path>", "failed", "open"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Tokenize placeholder = %v, want %v", got, want)
	}

	// Duplicates collapse: the corpus stores sets.
	if got, want := Tokenize("a a a"), []string{"a"}; !reflect.DeepEqual(got, want) {
		t.Errorf("Tokenize duplicates = %v, want %v", got, want)
	}

	// A long word is one token; nothing panics or truncates.
	long := make([]rune, 0, 100000)
	for i := 0; i < 100000; i++ {
		long = append(long, 'x')
	}
	if got := Tokenize(string(long)); len(got) != 1 {
		t.Errorf("Tokenize long word = %d tokens, want 1", len(got))
	}
}
