package redact

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type redactCorpus struct {
	Cases []redactCase `json:"cases"`
}

type redactCase struct {
	CaseID   string `json:"case_id"`
	Input    string `json:"input"`
	Expected string `json:"expected"`
	Options  struct {
		Share string `json:"share"`
	} `json:"options"`
}

func loadRedactCorpus(t *testing.T) redactCorpus {
	t.Helper()
	path := filepath.Join("..", "..", "spec", "corpus", "redact.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read corpus: %v", err)
	}
	var corpus redactCorpus
	if err := json.Unmarshal(data, &corpus); err != nil {
		t.Fatalf("parse corpus: %v", err)
	}
	if len(corpus.Cases) == 0 {
		t.Fatal("corpus has no cases")
	}
	return corpus
}

// TestTextCorpus runs the same 20 cases the TypeScript engine runs. The corpus
// is the contract: a rule that diverges between the two engines fails here.
func TestTextCorpus(t *testing.T) {
	corpus := loadRedactCorpus(t)
	for _, testCase := range corpus.Cases {
		t.Run(testCase.CaseID, func(t *testing.T) {
			share := Public
			if testCase.Options.Share == string(Private) {
				share = Private
			}
			got := Text(testCase.Input, share)
			if got != testCase.Expected {
				t.Errorf("Text(%q, %q) = %q, want %q", testCase.Input, share, got, testCase.Expected)
			}
			// Determinism and idempotence: redacting redacted text changes nothing.
			if again := Text(testCase.Input, share); again != got {
				t.Errorf("second call differs: %q vs %q", again, got)
			}
			if twice := Text(got, share); twice != got {
				t.Errorf("not idempotent: %q vs %q", twice, got)
			}
		})
	}
}

// TestEveryRuleHasAHit proves every generated family is reachable: its own
// example must change when the engine runs. The example is assembled from
// segments at generation time, so no credential-shaped literal is committed.
func TestEveryRuleHasAHit(t *testing.T) {
	for _, rule := range rules {
		if got := Text(rule.Example, Public); got == rule.Example {
			t.Errorf("rule %q did not match its own example %q", rule.Name, rule.Example)
		}
	}
	for _, rule := range homePrefixRules {
		if got := Text(rule.Example, Private); got == rule.Example {
			t.Errorf("home rule %q did not match its own example %q", rule.Name, rule.Example)
		}
	}
}

// TestBoundaries covers inputs the corpus does not: empty text, long text with
// nothing to redact, and multibyte text at the sample cap.
func TestBoundaries(t *testing.T) {
	if got := Text("", Public); got != "" {
		t.Errorf("Text empty = %q, want empty", got)
	}

	long := strings.Repeat("错", 400)
	if got := Text(long, Public); got != long {
		t.Errorf("multibyte text changed: got %d runes, want %d", len([]rune(got)), len([]rune(long)))
	}

	if got := CapSample("abc", 5); got != "abc" {
		t.Errorf("CapSample short = %q, want unchanged", got)
	}
	capped := CapSample(strings.Repeat("x", 501), 500)
	if len([]rune(capped)) != 500 || !strings.HasSuffix(capped, "…") {
		t.Errorf("CapSample long = %d runes ending %q, want 500 runes ending in an ellipsis", len([]rune(capped)), capped[len(capped)-3:])
	}
	if got := CapSample(strings.Repeat("错", 500), 500); got != strings.Repeat("错", 500) {
		t.Errorf("CapSample must count runes, not bytes")
	}
}

// TestSamplePolicy mirrors src/redact.ts: private samples are not capped,
// public samples are.
func TestSamplePolicy(t *testing.T) {
	long := strings.Repeat("x", 600)
	if got := Sample(long, Private); got != long {
		t.Errorf("private sample changed: %d runes, want %d", len([]rune(got)), len([]rune(long)))
	}
	if got := Sample(long, Public); len([]rune(got)) != SampleMaxChars {
		t.Errorf("public sample = %d runes, want %d", len([]rune(got)), SampleMaxChars)
	}
}
