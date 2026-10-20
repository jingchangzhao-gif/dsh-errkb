//go:build windows

package ui

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"

	"github.com/jingchangzhao-gif/dsh-errkb/internal/service"
)

// OmniboxSearcher is how the window gets answers. The command layer supplies
// it from the running daemon's pool; the window never talks to PostgreSQL.
type OmniboxSearcher func(ctx context.Context, query string) (service.SearchResults, error)

// TrayOptions configures the tray. Interval is the refresh cadence; Search, when
// set, enables the search row (EKB-045).
type TrayOptions struct {
	Interval time.Duration
	Search   OmniboxSearcher
}

// Omnibox window constants.
const (
	omniboxClassName = "errkbOmniboxWindow"
	wmSearchDone     = wmApp + 30

	wsPopup     = 0x80000000
	wsCaption   = 0x00C00000
	wsSysMenu   = 0x00080000
	wsBorder    = 0x00800000
	wsVScroll   = 0x00200000
	swHide      = 0
	swShow      = 5
	wmKeyDown   = 0x0100
	wmChar      = 0x0102
	wmPaintMsg  = 0x000F
	wmErase     = 0x0014
	wmFocus     = 0x0007
	vkBack      = 0x08
	vkReturn    = 0x0D
	vkEscape    = 0x1B
	vkUp        = 0x26
	vkDown      = 0x28
	dtLeft      = 0x00000000
	dtCenter    = 0x00000001
	dtVcenter   = 0x00000004
	dtSingle    = 0x00000020
	dtNoPrefix  = 0x00000800
	transparent = 1
)

var (
	procGetDC               = modUser32.NewProc("GetDC")
	procReleaseDC           = modUser32.NewProc("ReleaseDC")
	procBeginPaint          = modUser32.NewProc("BeginPaint")
	procEndPaint            = modUser32.NewProc("EndPaint")
	procFillRect            = modUser32.NewProc("FillRect")
	procGetClientRect       = modUser32.NewProc("GetClientRect")
	procGetSystemMetrics    = modUser32.NewProc("GetSystemMetrics")
	procSetWindowPos        = modUser32.NewProc("SetWindowPos")
	procShowWindow          = modUser32.NewProc("ShowWindow")
	procSetFocus            = modUser32.NewProc("SetFocus")
	procInvalidateRect      = modUser32.NewProc("InvalidateRect")
	procUpdateWindow        = modUser32.NewProc("UpdateWindow")
	procDrawTextW           = modUser32.NewProc("DrawTextW")
	procSetTextColor        = modGdi32.NewProc("SetTextColor")
	procSetBkMode           = modGdi32.NewProc("SetBkMode")
	procCreateSolidBrush    = modGdi32.NewProc("CreateSolidBrush")
	procGetStockObject      = modGdi32.NewProc("GetStockObject")
	procSelectObject        = modGdi32.NewProc("SelectObject")
	omniboxClassRegistered  bool
	omniboxWindowProcHandle = windows.NewCallback(omniboxWindowProc)
)

const (
	defaultGUIFont = 17 // DEFAULT_GUI_FONT in GetStockObject
	uiBackground   = 0x00171f27
	uiPanel        = 0x00202931
	uiSelection    = 0x00353f4b
	uiText         = 0x00f6f4f3
	uiMuted        = 0x00af9b9c
	uiExact        = 0x005ed222
	uiApproximate  = 0x000677d9
	uiAccent       = 0x00ffd166
)

type rect struct {
	Left, Top, Right, Bottom int32
}

type paintStruct struct {
	Hdc       windows.Handle
	Erase     int32
	Paint     rect
	Restore   int32
	IncUpdate int32
	Reserved  [32]byte
}

// omniboxWindow is the address-bar window. A Win32 callback cannot carry a Go
// pointer, so the instance is reached through a registry keyed by window.
type omniboxWindow struct {
	hwnd    windows.Handle
	state   *OmniboxState
	search  OmniboxSearcher
	baseCtx context.Context

	mu      sync.Mutex
	latest  *omniboxOutcome
	current int
	font    windows.Handle
}

type omniboxOutcome struct {
	number  int
	query   string
	results service.SearchResults
	err     error
}

var (
	omniboxRegistryMu sync.Mutex
	omniboxRegistry   = map[windows.Handle]*omniboxWindow{}
)

func registerOmnibox(window *omniboxWindow) {
	omniboxRegistryMu.Lock()
	defer omniboxRegistryMu.Unlock()
	omniboxRegistry[window.hwnd] = window
}

func unregisterOmnibox(hwnd windows.Handle) {
	omniboxRegistryMu.Lock()
	defer omniboxRegistryMu.Unlock()
	delete(omniboxRegistry, hwnd)
}

func lookupOmnibox(hwnd windows.Handle) *omniboxWindow {
	omniboxRegistryMu.Lock()
	defer omniboxRegistryMu.Unlock()
	return omniboxRegistry[hwnd]
}

// newOmniboxWindow creates the address bar hidden. Failures are returned so the
// tray can keep running without it: a search bug must never take capture down.
func newOmniboxWindow(ctx context.Context, instance windows.Handle, search OmniboxSearcher) (*omniboxWindow, error) {
	if search == nil {
		return nil, errors.New("errkb ui: no search backend is configured")
	}
	if err := ensureOmniboxClass(instance); err != nil {
		return nil, err
	}
	className, _ := windows.UTF16PtrFromString(omniboxClassName)
	windowName, _ := windows.UTF16PtrFromString("errkb search")
	style := uintptr(wsPopup | wsCaption | wsSysMenu | wsBorder | wsVScroll)
	hwnd, _, callErr := procCreateWindowExW.Call(
		0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(windowName)),
		style, 0, 0, 720, 470, 0, 0, uintptr(instance), 0,
	)
	if hwnd == 0 {
		return nil, fmt.Errorf("errkb ui: create the search window: %v", callErr)
	}
	window := &omniboxWindow{
		hwnd:    windows.Handle(hwnd),
		state:   NewOmniboxState(),
		search:  search,
		baseCtx: ctx,
	}
	if font, _, _ := procGetStockObject.Call(defaultGUIFont); font != 0 {
		window.font = windows.Handle(font)
	}
	registerOmnibox(window)
	return window, nil
}

// ensureOmniboxClass registers the window class once per process.
func ensureOmniboxClass(instance windows.Handle) error {
	omniboxRegistryMu.Lock()
	defer omniboxRegistryMu.Unlock()
	if omniboxClassRegistered {
		return nil
	}
	className, err := windows.UTF16PtrFromString(omniboxClassName)
	if err != nil {
		return err
	}
	atom, _, callErr := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wndClassEx{
		Size:      uint32(unsafe.Sizeof(wndClassEx{})),
		WndProc:   omniboxWindowProcHandle,
		Instance:  instance,
		ClassName: className,
	})))
	if atom == 0 {
		return fmt.Errorf("errkb ui: register the search window class: %v", callErr)
	}
	omniboxClassRegistered = true
	return nil
}

// show positions the window in the middle of the screen and focuses the input.
func (w *omniboxWindow) show() {
	screenWidth, _, _ := procGetSystemMetrics.Call(0)
	screenHeight, _, _ := procGetSystemMetrics.Call(1)
	x := (int(screenWidth) - 720) / 2
	y := (int(screenHeight) - 470) / 3
	procSetWindowPos.Call(uintptr(w.hwnd), 0, uintptr(x), uintptr(y), 0, 0, 0x0001|0x0004)
	procShowWindow.Call(uintptr(w.hwnd), swShow)
	procSetFocus.Call(uintptr(w.hwnd))
}

// hide stays alive so reopening keeps the typed query.
func (w *omniboxWindow) hide() {
	w.mu.Lock()
	w.current++
	w.mu.Unlock()
	procShowWindow.Call(uintptr(w.hwnd), swHide)
}

// destroy removes the window and its registry entry.
func (w *omniboxWindow) destroy() {
	unregisterOmnibox(w.hwnd)
	procDestroyWindow.Call(uintptr(w.hwnd))
}

// requestSearch fans the query out to the searcher. The generation counter wins
// the race: only the newest answer is painted.
func (w *omniboxWindow) requestSearch() {
	query := w.state.Query()
	w.mu.Lock()
	w.current++
	number := w.current
	w.mu.Unlock()
	if query == "" {
		w.state.SetResults(nil, false)
		return
	}
	w.state.SetLoading(true)
	go func() {
		ctx, cancel := context.WithTimeout(w.baseCtx, 10*time.Second)
		defer cancel()
		results, err := w.search(ctx, query)
		w.mu.Lock()
		w.latest = &omniboxOutcome{number: number, query: query, results: results, err: err}
		w.mu.Unlock()
		procPostMessageW.Call(uintptr(w.hwnd), wmSearchDone, 0, 0)
	}()
}

// applyLatest installs the newest finished search into the model.
func (w *omniboxWindow) applyLatest() {
	w.mu.Lock()
	outcome := w.latest
	current := w.current
	w.mu.Unlock()
	if outcome == nil || outcome.number != current {
		return
	}
	if outcome.err != nil {
		w.state.SetLoading(false)
		w.state.SetNote(readableSearchError(outcome.err))
		return
	}
	w.state.SetKind(string(outcome.results.Kind))
	hits := make([]OmniboxResult, 0, len(outcome.results.Hits))
	for _, hit := range outcome.results.Hits {
		hits = append(hits, OmniboxResult{
			IssueID:      hit.IssueID,
			Fingerprint:  hit.Fingerprint,
			Title:        hit.Title,
			ReasonLabel:  hit.ReasonLabel,
			MatchDetail:  hit.MatchDetail,
			Approximate:  hit.Approximate,
			Score:        hit.Score,
			Hits:         hit.Hits,
			SolutionBody: hit.SolutionBody,
		})
	}
	w.state.SetResults(hits, outcome.results.Degraded)
	if outcome.results.Note != "" {
		w.state.SetNote(outcome.results.Note)
	}
}

// readableSearchError turns a transport failure into a line the person can act
// on instead of a raw error chain.
func readableSearchError(err error) string {
	if errors.Is(err, service.ErrSearchUnavailable) {
		return "The daemon is not running, so search is unavailable. Start errkb ui or errkb daemon first."
	}
	return "Search failed: " + err.Error()
}

// omniboxWindowProc is the Win32 callback. It recovers panics so a UI defect can
// never take the capture service down (acceptance 11 keeps parity with EKB-044).
func omniboxWindowProc(hwnd, msg, wparam, lparam uintptr) uintptr {
	defer func() { _ = recover() }()
	window := lookupOmnibox(windows.Handle(hwnd))
	if window != nil {
		switch uint32(msg) {
		case wmKeyDown:
			window.onKey(OmniboxKeyFromVirtual(uint32(wparam)))
			return 0
		case wmChar:
			if wparam >= 32 && wparam != 127 {
				window.onRune(rune(wparam))
			}
			return 0
		case wmSearchDone:
			window.applyLatest()
			window.repaint()
			return 0
		case wmPaintMsg:
			window.paint()
			return 0
		case wmErase:
			return 1
		case wmFocus:
			window.repaint()
			return 0
		case wmClose:
			window.hide()
			return 0
		case wmDestroy:
			unregisterOmnibox(windows.Handle(hwnd))
			return 0
		}
	}
	result, _, _ := procDefWindowProcW.Call(hwnd, msg, wparam, lparam)
	return result
}

// OmniboxKeyFromVirtual maps the Win32 virtual keys the address bar understands.
func OmniboxKeyFromVirtual(virtual uint32) OmniboxKey {
	switch virtual {
	case vkUp:
		return KeyUp
	case vkDown:
		return KeyDown
	case vkReturn:
		return KeyEnter
	case vkEscape:
		return KeyEscape
	case vkBack:
		return KeyBackspace
	}
	return KeyNone
}

func (w *omniboxWindow) onKey(key OmniboxKey) {
	switch w.state.HandleKey(key, 0) {
	case ActionQueryChanged:
		w.requestSearch()
		w.repaint()
	case ActionMoved:
		w.repaint()
	case ActionOpen:
		if result, ok := w.state.SelectedResult(); ok {
			w.state.SetStatus("selected " + result.Title + " — the detail page arrives with EKB-046")
		}
		w.repaint()
	case ActionClose:
		w.hide()
	}
}

func (w *omniboxWindow) onRune(r rune) {
	if w.state.HandleKey(KeyRune, r) == ActionQueryChanged {
		w.requestSearch()
		w.repaint()
	}
}

func (w *omniboxWindow) repaint() {
	procInvalidateRect.Call(uintptr(w.hwnd), 0, 0)
}

// paint draws the bar, the input line, the status and up to three rows.
func (w *omniboxWindow) paint() {
	var paint paintStruct
	hdc, _, _ := procBeginPaint.Call(uintptr(w.hwnd), uintptr(unsafe.Pointer(&paint)))
	if hdc == 0 {
		return
	}
	dc := windows.Handle(hdc)
	procGetStockObject.Call(defaultGUIFont)
	if w.font != 0 {
		procSelectObject.Call(uintptr(dc), uintptr(w.font))
	}
	procSetBkMode.Call(uintptr(dc), transparent)

	var client rect
	procGetClientRect.Call(uintptr(w.hwnd), uintptr(unsafe.Pointer(&client)))

	fill(dc, rect{0, 0, client.Right, client.Bottom}, uiBackground)
	drawText(dc, "errkb search", rect{20, 12, 400, 36}, uiAccent, dtLeft|dtSingle|dtVcenter)
	drawText(dc, "Esc closes", rect{client.Right - 140, 12, client.Right - 20, 36}, uiMuted, dtLeft|dtSingle|dtVcenter)

	input := rect{20, 44, client.Right - 20, 84}
	fill(dc, input, uiPanel)
	query := w.state.Query()
	if query == "" {
		drawText(dc, "type an issue id, fingerprint, error code, command or message…", rect{34, 48, client.Right - 34, 80}, uiMuted, dtLeft|dtSingle|dtVcenter)
	} else {
		drawText(dc, shorten(query, 96), rect{34, 48, client.Right - 34, 80}, uiText, dtLeft|dtSingle|dtVcenter)
	}

	statusKind := w.state.Kind()
	if statusKind == "" {
		statusKind = "text"
	}
	summary := statusKind
	if w.state.Loading() {
		summary += " · searching…"
	}
	if len(w.state.Results()) > 0 {
		summary += fmt.Sprintf(" · %d result(s)", len(w.state.Results()))
	}
	if w.state.Degraded() {
		summary += " · degraded"
	}
	drawText(dc, shorten(summary, 110), rect{22, 92, client.Right - 22, 112}, uiMuted, dtLeft|dtSingle|dtVcenter)

	top := int32(120)
	for index, row := range w.state.Rows() {
		rowTop := top + int32(index)*68
		rowRect := rect{16, rowTop, client.Right - 16, rowTop + 60}
		if index == w.state.SelectedIndex() {
			fill(dc, rowRect, uiSelection)
		}
		drawText(dc, row.Index+".", rect{rowRect.Left + 10, rowRect.Top + 4, rowRect.Left + 36, rowRect.Top + 28}, uiMuted, dtLeft|dtSingle|dtVcenter)
		drawText(dc, row.Title, rect{rowRect.Left + 38, rowRect.Top + 4, rowRect.Right - 240, rowRect.Top + 28}, uiText, dtLeft|dtSingle|dtVcenter)
		badgeColor := uint32(uiExact)
		if row.Accent == AccentApproximate {
			badgeColor = uiApproximate
		}
		drawText(dc, row.Badge, rect{rowRect.Right - 232, rowRect.Top + 4, rowRect.Right - 10, rowRect.Top + 28}, badgeColor, dtLeft|dtSingle|dtVcenter)
		meta := row.Meta
		if selectedAt := w.state.SelectedIndex(); selectedAt == index && w.state.Status() != "" {
			meta = w.state.Status()
		}
		drawText(dc, shorten(meta, 120), rect{rowRect.Left + 38, rowRect.Top + 30, rowRect.Right - 10, rowRect.Top + 56}, uiMuted, dtLeft|dtSingle|dtVcenter)
	}

	footer := w.state.EmptyText()
	switch {
	case w.state.Status() != "":
		footer = w.state.Status()
	case len(w.state.Rows()) > 0:
		footer = "↑↓ move · Enter open · Esc close"
	}
	drawText(dc, shorten(footer, 150), rect{22, client.Bottom - 40, client.Right - 22, client.Bottom - 10}, uiMuted, dtLeft|dtSingle|dtVcenter)

	procEndPaint.Call(uintptr(w.hwnd), uintptr(unsafe.Pointer(&paint)))
}

// fill paints a solid rectangle with a fresh brush.
func fill(dc windows.Handle, area rect, color uint32) {
	brush, _, _ := procCreateSolidBrush.Call(uintptr(color))
	if brush == 0 {
		return
	}
	procFillRect.Call(uintptr(dc), uintptr(unsafe.Pointer(&area)), brush)
	procDeleteObject.Call(brush)
}

// drawText writes one line, clipping to area.
func drawText(dc windows.Handle, text string, area rect, color uint32, format uintptr) {
	encoded, err := windows.UTF16PtrFromString(text)
	if err != nil {
		return
	}
	procSetTextColor.Call(uintptr(dc), uintptr(color))
	procDrawTextW.Call(uintptr(dc), uintptr(unsafe.Pointer(encoded)), ^uintptr(0),
		uintptr(unsafe.Pointer(&area)), format)
}
