package service

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/core"
	"github.com/jingchangzhao-gif/error-knowledge-base/internal/database"
)

// SearchInputKind is what the address bar decided the text is. The four kinds
// are fixed by EKB-045: a number, an error code, a command, and free text.
// Exact shapes never reach the fuzzy path, so a display id such as E-001 can
// never be answered with a lookalike such as E-0012.
type SearchInputKind string

// The four input kinds.
const (
	SearchInputIssue     SearchInputKind = "issue"
	SearchInputErrorCode SearchInputKind = "error-code"
	SearchInputCommand   SearchInputKind = "command"
	SearchInputText      SearchInputKind = "text"
)

// MaxOmniboxHits is the hard cap the address bar shows. Retrieval already caps
// the shortlist at three; this is the same number stated once for the UI.
const MaxOmniboxHits = 3

// ErrSearchUnavailable is returned when the daemon (and therefore the pool) is
// not running. The omnibox turns it into a readable empty state.
var ErrSearchUnavailable = errors.New("the errkb daemon is not running")

// SearchHit is one row of the address bar's shortlist. It is display data only:
// the reason is worded for a person, and nothing here decides reuse.
type SearchHit struct {
	IssueID      string
	Fingerprint  string
	Title        string
	ReasonLabel  string // "exact fingerprint (same failure)", "same error code", ...
	MatchDetail  string // "score 0.82 · fingerprint, error_code"
	Score        float64
	Approximate  bool
	Hits         int
	SolutionBody string
}

// SearchResults is what one address-bar lookup returned.
type SearchResults struct {
	Kind     SearchInputKind
	Hits     []SearchHit
	Degraded bool   // pg_trgm unavailable: fuzzy candidates may be missing
	Note     string // an honest explanation for an empty exact lookup
}

// SearchService answers omnibox queries from the same pool the daemon uses. It
// adds no retrieval of its own: every kind resolves through database.Retrieve,
// which is the EKB-024 contract.
type SearchService struct {
	pool *pgxpool.Pool
}

// NewSearchService wraps a pool. A nil pool is a valid value whose Search
// reports ErrSearchUnavailable.
func NewSearchService(pool *pgxpool.Pool) *SearchService {
	return &SearchService{pool: pool}
}

var (
	searchUUIDPattern      = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	searchFingerprint      = regexp.MustCompile(`^[0-9a-f]{12}$`)
	searchDisplayID        = regexp.MustCompile(`(?i)^[a-z]{1,3}-\d{3,}$`)
	searchErrorCode        = regexp.MustCompile(`^(?:ERR_[A-Z0-9_]+|E[A-Z]{2,}|[A-Z][A-Z0-9_]{2,})$`)
	searchPathLike         = regexp.MustCompile(`^(?:[A-Za-z]:[\\/]|\.{0,2}[\\/]|\\\\)`)
	searchCommandWithSpace = regexp.MustCompile(`^\S+\s+(?:--?[A-Za-z]|["'./\\]|[A-Za-z0-9_]+\.(?:ts|js|go|ps1|exe|json|yaml|yml)\b)`)
	searchKnownCommands    = commandFirstTokens()
)

// ClassifySearchInput decides the kind from the text alone. The order matters:
// an id-shaped or code-shaped token must be claimed before the free-text path
// gets a chance to answer with a fuzzy lookalike.
func ClassifySearchInput(query string) SearchInputKind {
	query = strings.TrimSpace(query)
	switch {
	case query == "":
		return SearchInputText
	case searchDisplayID.MatchString(query):
		return SearchInputIssue
	case searchUUIDPattern.MatchString(query):
		return SearchInputIssue
	case searchFingerprint.MatchString(query):
		return SearchInputIssue
	case searchErrorCode.MatchString(query):
		return SearchInputErrorCode
	case searchPathLike.MatchString(query):
		return SearchInputCommand
	}
	fields := strings.Fields(query)
	if len(fields) > 0 && searchKnownCommands[strings.ToLower(fields[0])] {
		return SearchInputCommand
	}
	if searchCommandWithSpace.MatchString(query) {
		return SearchInputCommand
	}
	return SearchInputText
}

// Search runs one address-bar lookup. It never borrows another retrieval path:
// the kind only decides which selector database.Retrieve receives.
func (s *SearchService) Search(ctx context.Context, query string) (SearchResults, error) {
	query = strings.TrimSpace(query)
	kind := ClassifySearchInput(query)
	results := SearchResults{Kind: kind}
	if query == "" {
		return results, nil
	}
	if searchDisplayID.MatchString(query) {
		// The daemon stores issues under their UUID; the E-nnnn display id is
		// produced only when exporting Markdown. Answer exactly and explain,
		// never fall through to text similarity (EKB-045 acceptance 2). This
		// branch needs no database, so it answers even while the daemon is down.
		results.Note = fmt.Sprintf("display ids such as %s exist only in exported Markdown copies; paste the issue id or a message instead", query)
		return results, nil
	}
	if s == nil || s.pool == nil {
		return results, ErrSearchUnavailable
	}

	request := database.RetrievalRequest{}
	switch kind {
	case SearchInputIssue:
		if searchUUIDPattern.MatchString(query) {
			request.IssueID = query
		} else {
			request.Fingerprint = query
		}
	case SearchInputErrorCode:
		request.ErrorCode = query
	case SearchInputCommand:
		request.Command = query
	default:
		request.Message = query
	}

	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return results, fmt.Errorf("search: connect to PostgreSQL: %w", err)
	}
	defer conn.Release()

	retrieval, err := database.Retrieve(ctx, conn.Conn(), request)
	if err != nil {
		return results, fmt.Errorf("search: %w", err)
	}
	results.Degraded = retrieval.Degraded
	candidates := retrieval.Candidates
	if len(candidates) > MaxOmniboxHits {
		candidates = candidates[:MaxOmniboxHits]
	}
	results.Hits = make([]SearchHit, 0, len(candidates))
	for _, candidate := range candidates {
		hit := SearchHit{
			IssueID:     candidate.IssueID,
			Fingerprint: candidate.Fingerprint,
			Title:       candidate.Title,
			ReasonLabel: displayReason(kind, candidate),
			MatchDetail: matchDetail(candidate),
			Score:       candidate.Score,
			Approximate: candidate.Approximate,
			Hits:        candidate.Hits,
		}
		if candidate.Solution != nil {
			hit.SolutionBody = candidate.Solution.Body
		}
		results.Hits = append(results.Hits, hit)
	}
	return results, nil
}

// displayReason words the retrieval explanation for a person. It reads the
// same matched fields the core reranker produced; the approximate badge stays a
// separate signal so a shared code never looks like a proven same error.
func displayReason(kind SearchInputKind, candidate database.RetrievalCandidate) string {
	if !candidate.Approximate {
		for _, field := range candidate.MatchedFields {
			if field == core.MatchFieldFingerprint {
				return "exact fingerprint (same failure)"
			}
		}
		if kind == SearchInputIssue {
			return "issue id"
		}
		return "exact match"
	}
	switch kind {
	case SearchInputErrorCode:
		return "same error code"
	case SearchInputCommand:
		return "same command"
	case SearchInputIssue:
		return "text similarity"
	}
	for _, field := range candidate.MatchedFields {
		switch field {
		case core.MatchFieldErrorCode:
			return "shared error code"
		case core.MatchFieldCommand:
			return "shared command"
		}
	}
	return "text similarity"
}

// commandFirstTokens lists the shell commands an address bar should read as a
// command rather than as a sentence. It is deliberately small and boring; a
// missed command only means the text path runs, which is still a safe answer.
func commandFirstTokens() map[string]bool {
	tokens := []string{
		"aws", "az", "bash", "bun", "cargo", "cat", "cd", "chmod", "chown",
		"cmake", "cmd", "copy", "cp", "curl", "deno", "dir", "docker", "dotnet",
		"del", "eslint", "find", "gcloud", "git", "go", "gofmt", "grep", "helm",
		"java", "jest", "kubectl", "ls", "make", "mingw32-make", "move", "mv",
		"node", "npm", "npx", "openssl", "oxlint", "pg_dump", "pip", "pnpm",
		"powershell", "prettier", "psql", "pytest", "pwsh", "python", "python3",
		"rg", "rm", "rustc", "scp", "sh", "ssh", "systemctl", "terraform", "tsc",
		"tsdown", "type", "vitest", "wget", "yarn", "zsh",
	}
	known := make(map[string]bool, len(tokens))
	for _, token := range tokens {
		known[token] = true
	}
	return known
}
