package ui

import "testing"

func sampleResults() []OmniboxResult {
	return []OmniboxResult{
		{IssueID: "2f5d6f43-9c1b-4f0c-a4a4-3a2f0e0f6e1a", Fingerprint: "0123456789ab", Title: "EPERM: operation not permitted, rename", ReasonLabel: "exact fingerprint (same failure)", Approximate: false, Score: 1, Hits: 3, SolutionBody: "Close the file and retry."},
		{IssueID: "3d7e4a11-1111-4222-8333-444455556666", Fingerprint: "aaaaaaaabbbb", Title: "EPERM: operation not permitted, rename (different tool)", ReasonLabel: "same error code", Approximate: true, Score: 0.82, Hits: 1},
		{IssueID: "4e8f5b22-2222-4333-8444-555566667777", Fingerprint: "ccccccccdddd", Title: "Access is denied", ReasonLabel: "text similarity", Approximate: true, Score: 0.61},
	}
}

func TestOmniboxKeyboardNavigation(t *testing.T) {
	state := NewOmniboxState()
	state.SetQuery("EPERM")
	state.SetResults(sampleResults(), false)

	if got := state.SelectedIndex(); got != 0 {
		t.Fatalf("initial selection = %d, want 0", got)
	}
	if action := state.HandleKey(KeyDown, 0); action != ActionMoved {
		t.Fatalf("down action = %v, want ActionMoved", action)
	}
	if got := state.SelectedIndex(); got != 1 {
		t.Fatalf("after one down = %d, want 1", got)
	}
	state.HandleKey(KeyDown, 0)
	state.HandleKey(KeyDown, 0) // clamped at the last row, never wraps
	if got := state.SelectedIndex(); got != 2 {
		t.Fatalf("clamped selection = %d, want 2", got)
	}
	state.HandleKey(KeyUp, 0)
	state.HandleKey(KeyUp, 0)
	state.HandleKey(KeyUp, 0)
	if got := state.SelectedIndex(); got != 0 {
		t.Fatalf("clamped up selection = %d, want 0", got)
	}

	result, ok := state.SelectedResult()
	if !ok || result.Fingerprint != "0123456789ab" {
		t.Fatalf("selected result = %+v, ok=%v, want the first row", result, ok)
	}
	if action := state.HandleKey(KeyEnter, 0); action != ActionOpen {
		t.Fatalf("enter action = %v, want ActionOpen", action)
	}
	if action := state.HandleKey(KeyEscape, 0); action != ActionClose {
		t.Fatalf("escape action = %v, want ActionClose", action)
	}
}

func TestOmniboxEnterOnAnEmptyListExplainsTheEmptyState(t *testing.T) {
	state := NewOmniboxState()
	state.SetQuery("nothing here")
	state.SetResults(nil, false)
	if action := state.HandleKey(KeyEnter, 0); action != ActionNone {
		t.Fatalf("enter on empty action = %v, want ActionNone", action)
	}
	if state.Status() == "" {
		t.Fatal("enter on an empty list left no explanation")
	}
}

func TestOmniboxTypingClearsStaleResults(t *testing.T) {
	state := NewOmniboxState()
	state.SetQuery("EPERM")
	state.SetResults(sampleResults(), false)

	if action := state.HandleKey(KeyRune, 'X'); action != ActionQueryChanged {
		t.Fatalf("typing action = %v, want ActionQueryChanged", action)
	}
	if state.Query() != "EPERMX" {
		t.Fatalf("query = %q, want EPERMX", state.Query())
	}
	if len(state.Results()) != 0 {
		t.Fatalf("typing kept %d stale results", len(state.Results()))
	}
	if !state.Loading() {
		t.Fatal("typing did not mark the next lookup as in flight")
	}

	state.HandleKey(KeyBackspace, 0)
	if state.Query() != "EPERM" {
		t.Fatalf("query after backspace = %q, want EPERM", state.Query())
	}

	before := state.Query()
	state.HandleKey(KeyRune, 24) // control characters never reach the query
	if state.Query() != before {
		t.Fatalf("control rune changed the query to %q", state.Query())
	}
}

func TestOmniboxApproximateResultsAreDistinguishable(t *testing.T) {
	state := NewOmniboxState()
	state.SetResults(sampleResults(), false)
	rows := state.Rows()
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3", len(rows))
	}
	if rows[0].Accent != AccentExact || rows[0].Badge == rows[1].Badge {
		t.Fatalf("exact row = %+v, want the exact accent and a distinct badge", rows[0])
	}
	if rows[1].Accent != AccentApproximate {
		t.Fatalf("approximate row accent = %q, want %q", rows[1].Accent, AccentApproximate)
	}
	if rows[1].Badge == "" || rows[1].Badge == rows[0].Badge {
		t.Fatalf("approximate badge = %q, want the confirmation wording", rows[1].Badge)
	}
	for _, row := range rows {
		if row.Address == "" {
			t.Fatalf("row %+v has no address to open", row)
		}
	}
	if rows[0].Meta == "" || state.Status() != "" {
		t.Fatalf("exact rows should carry a reason and no warning; meta=%q status=%q", rows[0].Meta, state.Status())
	}
}

func TestOmniboxCapsTheListAtThree(t *testing.T) {
	state := NewOmniboxState()
	more := append(sampleResults(), OmniboxResult{IssueID: "fourth"})
	state.SetResults(more, false)
	if got := len(state.Rows()); got != OmniboxMaxResults {
		t.Fatalf("rows = %d, want the hard cap %d", got, OmniboxMaxResults)
	}
}

func TestOmniboxEmptyStateNamesTheNextStep(t *testing.T) {
	state := NewOmniboxState()
	state.SetQuery("EPERM")
	state.SetResults(nil, false)
	status := state.Status()
	if status == "" {
		t.Fatal("empty lookup left no status")
	}
	if !contains(status, "errkb record") && !contains(status, "errkb run") {
		t.Fatalf("empty state %q does not name the next step", status)
	}

	// The initial state (before any search) asks for input instead of claiming
	// an empty result.
	fresh := NewOmniboxState()
	if note := fresh.EmptyText(); note == "" || contains(note, "No matching issue") {
		t.Fatalf("initial empty text = %q, want a prompt, not an empty-result claim", note)
	}
}

func TestOmniboxDegradedNoteIsVisible(t *testing.T) {
	state := NewOmniboxState()
	state.SetResults(sampleResults(), true)
	if !state.Degraded() {
		t.Fatal("degraded flag was dropped")
	}
	if state.Status() == "" {
		t.Fatal("degraded lookup left no explanation")
	}
}

func TestOmniboxDisplayIDNoteWinsOverTheGenericEmptyText(t *testing.T) {
	state := NewOmniboxState()
	state.SetQuery("E-001")
	state.SetNote("display ids such as E-001 exist only in exported Markdown copies")
	if state.Status() != "display ids such as E-001 exist only in exported Markdown copies" {
		t.Fatalf("status = %q, want the display-id explanation", state.Status())
	}
	if len(state.Results()) != 0 {
		t.Fatalf("note kept %d results", len(state.Results()))
	}
}

func TestShortenCutsAtRuneBoundaries(t *testing.T) {
	if got := shorten("错误信息 cannot find", 6); got != "错误信息 …" {
		t.Fatalf("shorten = %q, want a rune-safe clip with an ellipsis", got)
	}
	if got := shorten("error: cannot find", 10); got != "error: ca…" {
		t.Fatalf("shorten = %q, want the ASCII clip value", got)
	}
	if got := shorten("short", 10); got != "short" {
		t.Fatalf("shorten shortened a short string: %q", got)
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
