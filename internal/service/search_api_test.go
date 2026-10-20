package service

import (
	"context"
	"testing"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/core"
	"github.com/jingchangzhao-gif/error-knowledge-base/internal/database"
)

func TestClassifySearchInput(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  SearchInputKind
	}{
		{"uuid", "2f5d6f43-9c1b-4f0c-a4a4-3a2f0e0f6e1a", SearchInputIssue},
		{"uuid upper", "2F5D6F43-9C1B-4F0C-A4A4-3A2F0E0F6E1A", SearchInputIssue},
		{"fingerprint", "0123456789ab", SearchInputIssue},
		{"display id", "E-0001", SearchInputIssue},
		{"short display id", "E-001", SearchInputIssue},
		{"different display id", "E-0012", SearchInputIssue},
		{"error code", "EPERM", SearchInputErrorCode},
		{"error code with underscore", "ERR_ABORTED", SearchInputErrorCode},
		{"node error code", "MODULE_NOT_FOUND", SearchInputErrorCode},
		{"command", "git status", SearchInputCommand},
		{"command with flag", "pnpm build --filter web", SearchInputCommand},
		{"absolute windows path", "C:" + `\tools\run.exe --help`, SearchInputCommand},
		{"relative path", "./scripts/build.sh failed", SearchInputCommand},
		{"sentence", "cannot find the file", SearchInputText},
		{"sentence with colon", "error: cannot find the file", SearchInputText},
		{"empty", "   ", SearchInputText},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifySearchInput(tc.input); got != tc.want {
				t.Fatalf("ClassifySearchInput(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestSearchDisplayIDAnswersExactlyWithoutADatabase(t *testing.T) {
	// A nil pool proves the branch never reaches the fuzzy path: the daemon can
	// be down and a display id still gets its honest answer.
	service := NewSearchService(nil)
	for _, query := range []string{"E-001", "E-0012"} {
		results, err := service.Search(context.Background(), query)
		if err != nil {
			t.Fatalf("Search(%q) error = %v, want an exact empty answer", query, err)
		}
		if results.Kind != SearchInputIssue {
			t.Fatalf("Search(%q) kind = %q, want %q", query, results.Kind, SearchInputIssue)
		}
		if len(results.Hits) != 0 {
			t.Fatalf("Search(%q) hits = %d, want 0", query, len(results.Hits))
		}
		if results.Note == "" {
			t.Fatalf("Search(%q) note is empty, want the display-id explanation", query)
		}
	}
}

func TestSearchWithoutAPoolReportsUnavailable(t *testing.T) {
	service := NewSearchService(nil)
	_, err := service.Search(context.Background(), "cannot find the file")
	if err != ErrSearchUnavailable {
		t.Fatalf("error = %v, want ErrSearchUnavailable", err)
	}
}

func TestSearchEmptyQueryIsQuiet(t *testing.T) {
	service := NewSearchService(nil)
	results, err := service.Search(context.Background(), "  ")
	if err != nil {
		t.Fatalf("empty query error = %v", err)
	}
	if len(results.Hits) != 0 {
		t.Fatalf("empty query hits = %d, want 0", len(results.Hits))
	}
}

func TestDisplayReason(t *testing.T) {
	exact := database.RetrievalCandidate{MatchedFields: []core.MatchField{core.MatchFieldFingerprint}}
	approximate := database.RetrievalCandidate{Approximate: true, MatchedFields: []core.MatchField{core.MatchFieldErrorCode}}
	cases := []struct {
		name      string
		kind      SearchInputKind
		candidate database.RetrievalCandidate
		want      string
	}{
		{"exact fingerprint", SearchInputText, exact, "exact fingerprint (same failure)"},
		{"issue id", SearchInputIssue, database.RetrievalCandidate{}, "issue id"},
		{"text lookup by code", SearchInputErrorCode, approximate, "same error code"},
		{"text lookup by command", SearchInputCommand, approximate, "same command"},
		{"text similarity", SearchInputText, approximate, "shared error code"},
		{"nothing matched", SearchInputText, database.RetrievalCandidate{Approximate: true}, "text similarity"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := displayReason(tc.kind, tc.candidate); got != tc.want {
				t.Fatalf("displayReason(%q) = %q, want %q", tc.kind, got, tc.want)
			}
		})
	}
}
