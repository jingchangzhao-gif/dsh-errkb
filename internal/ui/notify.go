package ui

import (
	"fmt"

	"github.com/jingchangzhao-gif/dsh-errkb/internal/service"
)

// Notification is the non-modal desktop notification model for one failure.
//
// It never takes focus: the host shows it beside the user's work and leaves the
// keyboard focus where it was. It never executes a fix either; copying the fix
// and opening the issue are the only actions, and both are explicit user
// gestures. The view layer renders Rows and forwards a copy or open request.
type Notification struct {
	notice   service.Notice
	selected int
}

// NotificationRow is one line of the notification list: a fix, its match badge,
// the trust with its sample size, and an applicability warning when the fix was
// recorded on a different environment.
type NotificationRow struct {
	Index       int
	Title       string
	Badge       string
	Trust       string
	Warning     string
	Approximate bool
	CopyHint    string
}

// NewNotification wraps one composed notice. The model shows at most three
// candidates no matter what the caller passed in (plan §5).
func NewNotification(notice service.Notice) *Notification {
	notification := &Notification{notice: notice}
	notification.notice.Candidates = notification.visibleCandidates()
	return notification
}

// Visible reports whether the host should show anything at all. A suppressed
// repeat and a failure without a known fix stay silent.
func (n *Notification) Visible() bool {
	if n == nil {
		return false
	}
	return n.notice.Visible()
}

// Modal is always false: a desktop notification must not block or take over the
// user's window.
func (n *Notification) Modal() bool { return false }

// StealsFocus is always false. The comment is part of the contract: a failing
// error must not move the keyboard focus away from what the user is reading.
func (n *Notification) StealsFocus() bool { return false }

// Executes is always false: V1 never applies a fix automatically (plan §6).
func (n *Notification) Executes() bool { return false }

// Title is the notification title.
func (n *Notification) Title() string {
	if n == nil {
		return ""
	}
	return n.notice.Title
}

// Body is the composed text with the match badges, trust lines and any
// applicability warning. It ends with the estimated token count.
func (n *Notification) Body() string {
	if n == nil {
		return ""
	}
	return n.notice.Body
}

// TokenNote states the token count is an estimate. The wording may not imply a
// precise number (plan §13); this is the whole text the host shows.
func (n *Notification) TokenNote() string {
	if n == nil {
		return ""
	}
	return fmt.Sprintf("about %d tokens (estimated, not an exact count)", n.notice.TokenEstimate)
}

// Candidates returns the shown candidates, capped at three.
func (n *Notification) Candidates() []service.NoticeCandidate {
	if n == nil {
		return nil
	}
	return n.visibleCandidates()
}

// MoveSelection moves the highlighted row and clamps at both ends.
func (n *Notification) MoveSelection(delta int) {
	if n == nil {
		return
	}
	candidates := n.visibleCandidates()
	if len(candidates) == 0 {
		n.selected = 0
		return
	}
	n.selected += delta
	if n.selected < 0 {
		n.selected = 0
	}
	if n.selected >= len(candidates) {
		n.selected = len(candidates) - 1
	}
}

// Select moves the highlight to an explicit row, if it exists.
func (n *Notification) Select(index int) {
	if n == nil {
		return
	}
	if index < 0 || index >= len(n.visibleCandidates()) {
		return
	}
	n.selected = index
}

// SelectedCandidate returns the highlighted fix, if any.
func (n *Notification) SelectedCandidate() (service.NoticeCandidate, bool) {
	if n == nil {
		return service.NoticeCandidate{}, false
	}
	candidates := n.visibleCandidates()
	if n.selected < 0 || n.selected >= len(candidates) {
		return service.NoticeCandidate{}, false
	}
	return candidates[n.selected], true
}

// CopySelected returns the fix text with its source, applicable environment and
// risk (plan §6, card EKB-046's three elements). It is only a string; the host
// puts it on the clipboard. Nothing is applied.
func (n *Notification) CopySelected() (string, bool) {
	candidate, ok := n.SelectedCandidate()
	if !ok {
		return "", false
	}
	return candidate.CopyText(), true
}

// OpenSelectedIssue returns the issue id behind the highlighted fix so the host
// can open the detail page. It does not load anything by itself.
func (n *Notification) OpenSelectedIssue() (string, bool) {
	candidate, ok := n.SelectedCandidate()
	if !ok || candidate.IssueID == "" {
		return "", false
	}
	return candidate.IssueID, true
}

// Rows is the render-ready view of the notification.
func (n *Notification) Rows() []NotificationRow {
	if n == nil {
		return nil
	}
	candidates := n.visibleCandidates()
	rows := make([]NotificationRow, 0, len(candidates))
	for index, candidate := range candidates {
		rows = append(rows, NotificationRow{
			Index:       index,
			Title:       candidate.Title,
			Badge:       candidate.Badge(),
			Trust:       candidate.TrustLine(),
			Warning:     candidate.ApplicabilityNote(),
			Approximate: candidate.Approximate,
			CopyHint:    "copy",
		})
	}
	return rows
}

// visibleCandidates caps the list at three and keeps non-suppressed notices
// meaningful.
func (n *Notification) visibleCandidates() []service.NoticeCandidate {
	candidates := n.notice.Candidates
	if len(candidates) > service.DefaultNoticeLimit {
		candidates = candidates[:service.DefaultNoticeLimit]
	}
	if candidates == nil {
		return nil
	}
	return candidates
}
