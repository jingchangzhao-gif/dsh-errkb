// Package service is the contract between hosts and the errkb core. This file
// is the notice service behind the desktop notifications (EKB-048): it turns one
// retrieval into the content a host may show, estimates the size, records the
// delivery in the notices table and suppresses repeats of the same issue.
package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/jingchangzhao-gif/error-knowledge-base/internal/core"
	"github.com/jingchangzhao-gif/error-knowledge-base/internal/database"
)

const (
	// NoticeKindDesktop marks a notice that was offered as a desktop
	// notification. EKB-049 counts these rows.
	NoticeKindDesktop = "desktop"

	// DefaultNoticeLimit is the plan §5 hard cap: at most three candidates, no
	// matter how many the retrieval returned.
	DefaultNoticeLimit = 3

	// DefaultNoticeSuppressWindow is how long the same issue stays quiet after
	// one notification. Repeated errors must not turn into a popup storm.
	DefaultNoticeSuppressWindow = 2 * time.Minute

	noticeSuppressCapacity = 256
)

// NoticeQuery is one failure to look up. At least one selector (IssueID,
// Fingerprint, ErrorCode, Command or Message) is required, exactly like the
// retrieval behind it.
type NoticeQuery struct {
	SessionID   string
	ProjectKey  string
	Category    string
	Message     string
	ErrorCode   string
	Command     string
	ToolName    string
	Fingerprint string
	IssueID     string
	Environment core.Environment
}

// NoticeCandidate is one candidate fix as the notification shows it. It keeps
// the explanation and the applicability data so both the notification and the
// copy action can state where the fix came from and where it may not apply.
type NoticeCandidate struct {
	Rank        int
	IssueID     string
	Fingerprint string
	Title       string

	MatchReason string
	MatchDetail string
	Approximate bool

	Trust float64
	Hits  int

	SolutionVersionID string
	SolutionBody      string
	SolutionSource    string
	AppliesTo         string
	Risk              string

	EnvironmentDiff []core.EnvironmentDifference
}

// Badge names the match strength in plain language. Approximate rows say
// "confirm" on purpose: §5 says a near match is only a candidate.
func (c NoticeCandidate) Badge() string {
	if c.Approximate {
		return "approximate — confirm before reuse"
	}
	return "exact match"
}

// TrustLine always shows the sample size next to the number, so a bare 0.8
// cannot be mistaken for a certainty.
func (c NoticeCandidate) TrustLine() string {
	return fmt.Sprintf("trust %.2f (based on %d recorded occurrence(s))", c.Trust, c.Hits)
}

// ApplicabilityNote turns a recorded environment difference into a warning.
// Returning "" means the stored environment matched (or was not recorded) and
// no warning is shown.
func (c NoticeCandidate) ApplicabilityNote() string {
	if len(c.EnvironmentDiff) == 0 {
		return ""
	}
	parts := make([]string, 0, len(c.EnvironmentDiff))
	for _, difference := range c.EnvironmentDiff {
		parts = append(parts, fmt.Sprintf("%s: this host %s, recorded %s",
			difference.Field, difference.Query, difference.Candidate))
	}
	return "WARNING: may not apply here — " + strings.Join(parts, "; ")
}

// CopyText is what the one-click copy action puts on the clipboard: the fix
// body plus the three elements §6 requires (source, applicable environment and
// risk), and the issue id so the pasted text can be traced back.
func (c NoticeCandidate) CopyText() string {
	return fmt.Sprintf("%s\n\nsource: %s\napplies to: %s\nrisk: %s\nissue: %s",
		strings.TrimSpace(c.SolutionBody), sourceOrUnknown(c.SolutionSource),
		appliesOrAny(c.AppliesTo), riskOrUnrecorded(c.Risk), c.IssueID)
}

// Notice is one composed desktop notification. Body is what a host may render;
// TokenEstimate is always an estimate and the wording in Body says so.
type Notice struct {
	Kind          string
	Title         string
	Body          string
	Candidates    []NoticeCandidate
	TokenEstimate int
	Suppressed    bool
	IssueID       string
	CreatedAt     time.Time
}

// Visible reports whether a host should show this notice. A suppressed notice
// and an empty result stay silent.
func (n Notice) Visible() bool { return len(n.Candidates) > 0 && !n.Suppressed }

// NoticeService retrieves candidate fixes, composes the notification text,
// records it in the notices table and suppresses repeats.
type NoticeService struct {
	pool           *pgxpool.Pool
	limit          int
	suppressWindow time.Duration
	now            func() time.Time

	mu     sync.Mutex
	recent map[string]time.Time
}

// NewNoticeService builds the service over a PostgreSQL pool.
func NewNoticeService(pool *pgxpool.Pool) *NoticeService {
	return &NoticeService{
		pool:           pool,
		limit:          DefaultNoticeLimit,
		suppressWindow: DefaultNoticeSuppressWindow,
		now:            time.Now,
		recent:         make(map[string]time.Time),
	}
}

// Notice runs one retrieval and returns the composed notification. It writes a
// notices row only when a notification is actually shown: a suppressed repeat
// or a failure without a known fix records nothing.
func (s *NoticeService) Notice(ctx context.Context, query NoticeQuery) (Notice, error) {
	if s == nil || s.pool == nil {
		return Notice{}, errors.New("notify: the database is not available")
	}
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return Notice{}, fmt.Errorf("notify: connect to PostgreSQL: %w", err)
	}
	defer conn.Release()

	result, err := database.Retrieve(ctx, conn.Conn(), database.RetrievalRequest{
		IssueID:     query.IssueID,
		Category:    query.Category,
		Message:     query.Message,
		ErrorCode:   query.ErrorCode,
		Command:     query.Command,
		ToolName:    query.ToolName,
		Fingerprint: query.Fingerprint,
		ProjectKey:  query.ProjectKey,
		Environment: query.Environment,
		SessionID:   query.SessionID,
	})
	if err != nil {
		return Notice{}, fmt.Errorf("notify: %w", err)
	}

	candidates := s.candidates(ctx, conn.Conn(), result.Candidates)
	now := s.now()
	notice := Notice{
		Kind:       NoticeKindDesktop,
		Candidates: candidates,
		CreatedAt:  now,
	}
	if len(candidates) == 0 {
		return notice, nil
	}
	notice.Title, notice.Body = composeNotice(candidates)
	notice.TokenEstimate = EstimateNoticeTokens(notice.Body)
	notice.Body = fmt.Sprintf("%s\n\n(estimated size: about %d tokens — an estimate, not an exact count)",
		notice.Body, notice.TokenEstimate)
	notice.IssueID = candidates[0].IssueID

	key := "issue:" + candidates[0].IssueID
	if s.recentlyShown(key, now) {
		notice.Suppressed = true
		return notice, nil
	}
	if err := s.rememberNotice(ctx, conn.Conn(), notice, query.SessionID); err != nil {
		return Notice{}, err
	}
	s.rememberShown(key, now)
	return notice, nil
}

// candidates keeps only candidates with a solution (a notice about a failure
// without a fix would be noise), applies the §5 cap and fetches the recorded
// risk text for each version.
func (s *NoticeService) candidates(ctx context.Context, conn *pgx.Conn, retrieved []database.RetrievalCandidate) []NoticeCandidate {
	limit := s.limit
	if limit <= 0 || limit > DefaultNoticeLimit {
		limit = DefaultNoticeLimit
	}
	candidates := make([]NoticeCandidate, 0, limit)
	versionIDs := make([]string, 0, limit)
	for _, candidate := range retrieved {
		if candidate.Solution == nil {
			continue
		}
		if len(candidates) == limit {
			break
		}
		candidates = append(candidates, NoticeCandidate{
			Rank:              len(candidates) + 1,
			IssueID:           candidate.IssueID,
			Fingerprint:       candidate.Fingerprint,
			Title:             candidate.Title,
			MatchReason:       matchReason(candidate),
			MatchDetail:       matchDetail(candidate),
			Approximate:       candidate.Approximate,
			Trust:             candidate.Solution.Trust,
			Hits:              candidate.Hits,
			SolutionVersionID: candidate.Solution.SolutionVersionID,
			SolutionBody:      candidate.Solution.Body,
			SolutionSource:    candidate.Solution.Source,
			AppliesTo:         candidate.Solution.AppliesTo,
			EnvironmentDiff:   candidate.EnvironmentDiff,
		})
		versionIDs = append(versionIDs, candidate.Solution.SolutionVersionID)
	}
	if len(candidates) == 0 {
		return candidates
	}
	risks, err := solutionRisks(ctx, conn, versionIDs)
	if err == nil {
		for index := range candidates {
			candidates[index].Risk = risks[candidates[index].SolutionVersionID]
		}
	}
	return candidates
}

func solutionRisks(ctx context.Context, conn *pgx.Conn, versionIDs []string) (map[string]string, error) {
	risks := make(map[string]string, len(versionIDs))
	rows, err := conn.Query(ctx,
		"select id::text, coalesce(risks, '') from solution_versions where id = any($1::uuid[])",
		versionIDs)
	if err != nil {
		return risks, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, risk string
		if err := rows.Scan(&id, &risk); err != nil {
			return risks, err
		}
		risks[id] = risk
	}
	return risks, rows.Err()
}

func (s *NoticeService) rememberNotice(ctx context.Context, conn *pgx.Conn, notice Notice, sessionID string) error {
	var issueID any
	if notice.IssueID != "" {
		issueID = notice.IssueID
	}
	var session any
	if sessionID != "" {
		session = sessionID
	}
	_, err := conn.Exec(ctx, `
		insert into notices (session_id, issue_id, kind, body, estimated_tokens)
		values ($1, $2, $3, $4, $5)`,
		session, issueID, notice.Kind, notice.Body, notice.TokenEstimate)
	if err != nil {
		return fmt.Errorf("notify: record the notice: %w", err)
	}
	return nil
}

// recentlyShown reports whether the key was shown inside the window.
func (s *NoticeService) recentlyShown(key string, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	shown, ok := s.recent[key]
	return ok && now.Sub(shown) < s.suppressWindow
}

// rememberShown records a delivery and keeps the map bounded: entries older
// than twice the window are dropped, and the map is cleared if that is not
// enough.
func (s *NoticeService) rememberShown(key string, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.recent) >= noticeSuppressCapacity {
		cutoff := now.Add(-2 * s.suppressWindow)
		for existing, shown := range s.recent {
			if shown.Before(cutoff) {
				delete(s.recent, existing)
			}
		}
		if len(s.recent) >= noticeSuppressCapacity {
			s.recent = make(map[string]time.Time)
		}
	}
	s.recent[key] = now
}

// composeNotice builds the title and body. The token line is appended by the
// caller once the estimate over the body is known.
func composeNotice(candidates []NoticeCandidate) (string, string) {
	title := "errkb: known fix for this failure"
	if len(candidates) > 1 {
		title = fmt.Sprintf("errkb: %d possible fixes for this failure", len(candidates))
	}
	var body strings.Builder
	body.WriteString("The errkb knowledge base already recorded this failure.\n")
	for _, candidate := range candidates {
		fmt.Fprintf(&body, "\n%d) %s\n", candidate.Rank, oneLineText(candidate.Title))
		fmt.Fprintf(&body, "   [%s]\n", candidate.Badge())
		fmt.Fprintf(&body, "   fix: %s\n", oneLineText(candidate.SolutionBody))
		fmt.Fprintf(&body, "   source: %s; applies to: %s; risk: %s\n",
			sourceOrUnknown(candidate.SolutionSource), appliesOrAny(candidate.AppliesTo),
			riskOrUnrecorded(candidate.Risk))
		fmt.Fprintf(&body, "   %s\n", candidate.TrustLine())
		if note := candidate.ApplicabilityNote(); note != "" {
			fmt.Fprintf(&body, "   %s\n", note)
		}
	}
	return title, strings.TrimRight(body.String(), "\n")
}

// EstimateNoticeTokens converts text to an approximate token count. It is a
// rough characters/4 estimate on purpose — the UI must call it an estimate.
func EstimateNoticeTokens(text string) int {
	runes := utf8.RuneCountInString(strings.TrimSpace(text))
	if runes == 0 {
		return 0
	}
	return (runes + 3) / 4
}

func oneLineText(text string) string {
	return strings.Join(strings.Fields(text), " ")
}

func sourceOrUnknown(source string) string {
	if source == "" {
		return "unknown"
	}
	return source
}

func appliesOrAny(appliesTo string) string {
	if appliesTo == "" {
		return "no recorded environment"
	}
	return appliesTo
}

func riskOrUnrecorded(risk string) string {
	if risk == "" {
		return "not recorded"
	}
	return risk
}
