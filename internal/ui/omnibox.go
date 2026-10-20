package ui

import (
	"strings"
)

// OmniboxMaxResults is the hard cap the address bar renders. It matches the
// service cap and exists so a paint loop can never show a fourth row.
const OmniboxMaxResults = 3

// Accent is how a row paints. Approximate rows are amber and exact rows are
// green, which is the visual part of the "approximate must be distinguishable"
// rule (EKB-045 acceptance 5).
type Accent string

// The row accents.
const (
	AccentExact       Accent = "exact"
	AccentApproximate Accent = "approximate"
	AccentMuted       Accent = "muted"
)

// OmniboxResult is one row the model shows. It is pure data so the keyboard,
// badge and empty-state rules are testable without Win32.
type OmniboxResult struct {
	IssueID      string
	Fingerprint  string
	Title        string
	ReasonLabel  string
	MatchDetail  string
	Approximate  bool
	Score        float64
	Hits         int
	SolutionBody string
}

// Accent reports the row's semantic color.
func (r OmniboxResult) Accent() Accent {
	if r.Approximate {
		return AccentApproximate
	}
	return AccentExact
}

// Badge is the short tag printed after the title. The approximate tag spells
// out the confirmation rule; a person must never mistake it for a known fix.
func (r OmniboxResult) Badge() string {
	if r.Approximate {
		return "APPROXIMATE — confirm before reuse"
	}
	return "exact"
}

// OmniboxRow is one rendered line of the model.
type OmniboxRow struct {
	Index   string
	Title   string
	Meta    string
	Badge   string
	Accent  Accent
	Address string // "issue id or fingerprint" the detail page will open
}

// OmniboxKey is the keyboard vocabulary the window maps Win32 virtual keys to.
type OmniboxKey int

// The address-bar keys.
const (
	KeyNone OmniboxKey = iota
	KeyUp
	KeyDown
	KeyEnter
	KeyEscape
	KeyBackspace
	KeyRune
)

// OmniboxAction is what the window should do after a key.
type OmniboxAction int

// The address-bar actions.
const (
	ActionNone OmniboxAction = iota
	ActionQueryChanged
	ActionMoved
	ActionOpen
	ActionClose
)

// OmniboxState is the whole address bar without any Win32. The window owns one
// and paints Rows(); tests drive it with HandleKey.
type OmniboxState struct {
	query     []rune
	kind      string
	results   []OmniboxResult
	selected  int
	loading   bool
	status    string
	degraded  bool
	didSearch bool
}

// NewOmniboxState returns an empty address bar.
func NewOmniboxState() *OmniboxState {
	return &OmniboxState{kind: "text"}
}

// Query returns what the person has typed.
func (o *OmniboxState) Query() string {
	return string(o.query)
}

// Kind returns the input-kind label ("issue", "error code", ...).
func (o *OmniboxState) Kind() string {
	return o.kind
}

// SetKind updates the label when a search starts.
func (o *OmniboxState) SetKind(kind string) {
	o.kind = kind
}

// Loading reports that a lookup is in flight.
func (o *OmniboxState) Loading() bool {
	return o.loading
}

// SetLoading toggles the in-flight marker.
func (o *OmniboxState) SetLoading(loading bool) {
	o.loading = loading
}

// Degraded reports that pg_trgm was unavailable during the last lookup.
func (o *OmniboxState) Degraded() bool {
	return o.degraded
}

// Status is the footer text (an empty-state explanation, an error, or a
// selection notice).
func (o *OmniboxState) Status() string {
	return o.status
}

// SetStatus replaces the footer text.
func (o *OmniboxState) SetStatus(status string) {
	o.status = status
}

// Results returns the current shortlist.
func (o *OmniboxState) Results() []OmniboxResult {
	return o.results
}

// SelectedIndex returns the highlighted row, or -1 when the list is empty.
func (o *OmniboxState) SelectedIndex() int {
	if len(o.results) == 0 {
		return -1
	}
	return o.selected
}

// SetResults installs a shortlist, caps it at OmniboxMaxResults, resets the
// highlight and derives the empty/degraded status.
func (o *OmniboxState) SetResults(results []OmniboxResult, degraded bool) {
	o.didSearch = true
	o.loading = false
	o.degraded = degraded
	if len(results) > OmniboxMaxResults {
		results = results[:OmniboxMaxResults]
	}
	o.results = results
	o.selected = 0
	switch {
	case len(results) == 0:
		o.status = o.EmptyText()
	case degraded:
		o.status = "fuzzy search is degraded (pg_trgm unavailable); exact matches only"
	default:
		o.status = ""
	}
}

// SetNote installs an explanation for an exact-shaped lookup that found
// nothing (the E-nnnn display id path).
func (o *OmniboxState) SetNote(note string) {
	o.didSearch = true
	o.loading = false
	o.results = nil
	o.selected = 0
	o.status = strings.TrimSpace(note)
	if o.status == "" {
		o.status = o.EmptyText()
	}
}

// SelectedResult returns the highlighted row, or false when there is none.
func (o *OmniboxState) SelectedResult() (OmniboxResult, bool) {
	index := o.SelectedIndex()
	if index < 0 {
		return OmniboxResult{}, false
	}
	return o.results[index], true
}

// MoveSelection moves the highlight by delta, clamped to the list. A clamp (not
// a wrap) keeps Down at the last row from jumping to the first.
func (o *OmniboxState) MoveSelection(delta int) {
	if len(o.results) == 0 {
		return
	}
	next := o.selected + delta
	if next < 0 {
		next = 0
	}
	if next >= len(o.results) {
		next = len(o.results) - 1
	}
	o.selected = next
}

// HandleKey applies one key. The returned action tells the window whether to
// search again, repaint, open the selection or close.
func (o *OmniboxState) HandleKey(key OmniboxKey, r rune) OmniboxAction {
	switch key {
	case KeyUp:
		o.MoveSelection(-1)
		return ActionMoved
	case KeyDown:
		o.MoveSelection(1)
		return ActionMoved
	case KeyEnter:
		if _, ok := o.SelectedResult(); ok {
			return ActionOpen
		}
		o.status = o.EmptyText()
		return ActionNone
	case KeyEscape:
		return ActionClose
	case KeyBackspace:
		if len(o.query) == 0 {
			return ActionNone
		}
		o.query = o.query[:len(o.query)-1]
		o.markQueryChanged()
		return ActionQueryChanged
	case KeyRune:
		if r < 32 || r == 127 {
			return ActionNone
		}
		o.query = append(o.query, r)
		o.markQueryChanged()
		return ActionQueryChanged
	}
	return ActionNone
}

// markQueryChanged clears stale results so the bar never shows answers to a
// query the person already edited away.
func (o *OmniboxState) markQueryChanged() {
	o.results = nil
	o.selected = 0
	o.status = ""
	o.loading = true
}

// SetQuery replaces the query text (used by tests and by host-driven edits).
func (o *OmniboxState) SetQuery(query string) {
	o.query = []rune(query)
	o.markQueryChanged()
}

// Rows renders the shortlist. Each row carries its accent and badge so the
// Win32 paint and the keyboard tests read the same data.
func (o *OmniboxState) Rows() []OmniboxRow {
	rows := make([]OmniboxRow, 0, len(o.results))
	for index, result := range o.results {
		meta := result.Title
		if result.Hits > 0 {
			meta += " · hit " + itoa(result.Hits) + "×"
		}
		if result.ReasonLabel != "" {
			meta += " · " + result.ReasonLabel
		}
		rows = append(rows, OmniboxRow{
			Index:   itoa(index + 1),
			Title:   shorten(result.Title, 72),
			Meta:    meta,
			Badge:   result.Badge(),
			Accent:  result.Accent(),
			Address: result.IssueID,
		})
	}
	return rows
}

// EmptyText explains the empty state and the next step (acceptance 8).
func (o *OmniboxState) EmptyText() string {
	if o.didSearch && o.Query() != "" {
		return "No matching issue yet. Record it first: errkb record — or capture the failure with errkb run -- <command>."
	}
	return "Search by issue id, fingerprint, error code, command or message."
}

// shorten cuts a string to at most n runes. The tray tooltip and the address
// bar share it so both clip at a rune boundary, never inside a character.
func shorten(text string, n int) string {
	runes := []rune(text)
	if len(runes) <= n {
		return text
	}
	if n <= 1 {
		return string(runes[:n])
	}
	return string(runes[:n-1]) + "…"
}

// itoa is a tiny positive-integer formatter; the address bar only ever prints
// small row numbers and hit counts.
func itoa(value int) string {
	if value == 0 {
		return "0"
	}
	digits := make([]byte, 0, 8)
	for value > 0 {
		digits = append(digits, byte('0'+value%10))
		value /= 10
	}
	for left, right := 0, len(digits)-1; left < right; left, right = left+1, right-1 {
		digits[left], digits[right] = digits[right], digits[left]
	}
	return string(digits)
}
