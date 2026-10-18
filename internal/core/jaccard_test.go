package core

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// jaccardTolerance is how close a computed similarity must be to the corpus
// value. The corpus numbers are rational (71/100 and so on); comparing floats
// with == would be a coin flip, so every boundary assertion uses this tolerance.
const jaccardTolerance = 1e-9

type matchCorpus struct {
	Cases []matchCase `json:"cases"`
}

type matchCase struct {
	CaseID string `json:"case_id"`
	Note   string `json:"note"`
	Input  struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
		Entries []struct {
			Message string `json:"message"`
		} `json:"entries"`
	} `json:"input"`
	Expected struct {
		Matched    bool    `json:"matched"`
		Similarity float64 `json:"similarity"`
	} `json:"expected"`
}

func loadMatchCorpus(t *testing.T) matchCorpus {
	t.Helper()
	path := filepath.Join("..", "..", "spec", "corpus", "match.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read corpus: %v", err)
	}
	var corpus matchCorpus
	if err := json.Unmarshal(data, &corpus); err != nil {
		t.Fatalf("parse corpus: %v", err)
	}
	if len(corpus.Cases) == 0 {
		t.Fatal("corpus has no cases")
	}
	return corpus
}

// TestJaccardBoundaries is the 0.71/0.72/0.73 probe EKB-017's threshold rests
// on. The three match.json cases are the same construction as
// tests/match.test.ts: an entry of 100 distinct words, an error that repeats the
// first k of them, so the similarity is exactly k/100.
func TestJaccardBoundaries(t *testing.T) {
	corpus := loadMatchCorpus(t)
	want := map[string]float64{
		"match-001": 0.71,
		"match-002": 0.72,
		"match-003": 0.73,
	}
	for _, testCase := range corpus.Cases {
		expected, ok := want[testCase.CaseID]
		if !ok {
			continue
		}
		got := Jaccard(
			Tokenize(testCase.Input.Error.Message),
			Tokenize(testCase.Input.Entries[0].Message),
		)
		if math.Abs(got-expected) > jaccardTolerance {
			t.Errorf("%s: Jaccard = %.17g, want %.2f (+-%g)", testCase.CaseID, got, expected, jaccardTolerance)
		}
		// The corpus states the same number in both places; keep them tied.
		if testCase.Expected.Matched && math.Abs(testCase.Expected.Similarity-expected) > jaccardTolerance {
			t.Errorf("%s: corpus similarity %.17g disagrees with the note %.2f", testCase.CaseID, testCase.Expected.Similarity, expected)
		}
	}
}

// TestJaccardProperties covers symmetry, reflexivity and the empty-set rule.
func TestJaccardProperties(t *testing.T) {
	a := Tokenize("failed to open <path>: permission denied")
	b := Tokenize("permission denied opening <path>")

	if got := Jaccard(a, a); got != 1 {
		t.Errorf("Jaccard(a, a) = %v, want 1", got)
	}
	if Jaccard(a, b) != Jaccard(b, a) {
		t.Errorf("Jaccard is not symmetric: %v vs %v", Jaccard(a, b), Jaccard(b, a))
	}
	if got := Jaccard(nil, nil); got != 0 {
		t.Errorf("Jaccard(empty, empty) = %v, want 0", got)
	}
	if got := Jaccard(a, nil); got != 0 {
		t.Errorf("Jaccard(a, empty) = %v, want 0", got)
	}
	if got := Jaccard(Tokenize("alpha beta"), Tokenize("gamma delta")); got != 0 {
		t.Errorf("Jaccard(disjoint) = %v, want 0", got)
	}
	// Duplicates count once, exactly like the corpus token sets.
	if got := Jaccard([]string{"a", "a", "b"}, []string{"a", "b", "b"}); got != 1 {
		t.Errorf("Jaccard(duplicates) = %v, want 1", got)
	}
}
