//go:build windows

package ui

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Tray menu commands.
const (
	menuPause    = 1
	menuSearch   = 2
	menuSettings = 3
	menuExit     = 4
)

// Window messages and tray constants.
const (
	trayClassName = "errkbTrayWindow"
	wmDestroy     = 0x0002
	wmClose       = 0x0010
	wmTimer       = 0x0113

	wmLButtonUp  = 0x0202
	wmLButtonDbl = 0x0203
	wmRButtonUp  = 0x0205

	wmApp         = 0x8000
	wmTrayMessage = wmApp + 1

	nimAdd    = 0
	nimModify = 1
	nimDelete = 2

	nifMessage = 0x00000001
	nifIcon    = 0x00000002
	nifTip     = 0x00000004

	mfString    = 0x00000000
	mfGrayed    = 0x00000001
	mfSeparator = 0x00000800

	tpmReturnCmd = 0x0100
	tpmNonotify  = 0x0080

	trayTimerID = 1
	trayID      = 1
)

var (
	modKernel32 = windows.NewLazySystemDLL("kernel32.dll")
	modUser32   = windows.NewLazySystemDLL("user32.dll")
	modShell32  = windows.NewLazySystemDLL("shell32.dll")
	modGdi32    = windows.NewLazySystemDLL("gdi32.dll")

	procGetModuleHandleW    = modKernel32.NewProc("GetModuleHandleW")
	procRegisterClassExW    = modUser32.NewProc("RegisterClassExW")
	procCreateWindowExW     = modUser32.NewProc("CreateWindowExW")
	procDefWindowProcW      = modUser32.NewProc("DefWindowProcW")
	procDestroyWindow       = modUser32.NewProc("DestroyWindow")
	procPostMessageW        = modUser32.NewProc("PostMessageW")
	procPostQuitMessage     = modUser32.NewProc("PostQuitMessage")
	procGetMessageW         = modUser32.NewProc("GetMessageW")
	procTranslateMessage    = modUser32.NewProc("TranslateMessage")
	procDispatchMessageW    = modUser32.NewProc("DispatchMessageW")
	procSetTimer            = modUser32.NewProc("SetTimer")
	procKillTimer           = modUser32.NewProc("KillTimer")
	procCreatePopupMenu     = modUser32.NewProc("CreatePopupMenu")
	procAppendMenuW         = modUser32.NewProc("AppendMenuW")
	procDestroyMenu         = modUser32.NewProc("DestroyMenu")
	procTrackPopupMenu      = modUser32.NewProc("TrackPopupMenu")
	procSetForegroundWindow = modUser32.NewProc("SetForegroundWindow")
	procGetCursorPos        = modUser32.NewProc("GetCursorPos")
	procCreateIconIndirect  = modUser32.NewProc("CreateIconIndirect")
	procDestroyIcon         = modUser32.NewProc("DestroyIcon")
	procShellNotifyIconW    = modShell32.NewProc("Shell_NotifyIconW")
	procCreateDIBSection    = modGdi32.NewProc("CreateDIBSection")
	procCreateBitmap        = modGdi32.NewProc("CreateBitmap")
	procDeleteObject        = modGdi32.NewProc("DeleteObject")
)

type wndClassEx struct {
	Size       uint32
	Style      uint32
	WndProc    uintptr
	ClsExtra   int32
	WndExtra   int32
	Instance   windows.Handle
	Icon       windows.Handle
	Cursor     windows.Handle
	Background windows.Handle
	MenuName   *uint16
	ClassName  *uint16
	IconSmall  windows.Handle
}

type message struct {
	Hwnd     windows.Handle
	Message  uint32
	WParam   uintptr
	LParam   uintptr
	Time     uint32
	Pt       point
	LPrivate uint32
}

type point struct {
	X, Y int32
}

type notifyIconData struct {
	Size             uint32
	Hwnd             windows.Handle
	ID               uint32
	Flags            uint32
	CallbackMessage  uint32
	Icon             windows.Handle
	Tip              [128]uint16
	State            uint32
	StateMask        uint32
	Info             [256]uint16
	TimeoutOrVersion uint32
	InfoTitle        [64]uint16
	InfoFlags        uint32
	Guid             [16]byte
	BalloonIcon      windows.Handle
}

type iconInfo struct {
	Icon     int32
	XHotspot uint32
	YHotspot uint32
	Mask     windows.Handle
	Color    windows.Handle
}

type bitmapInfoHeader struct {
	Size          uint32
	Width         int32
	Height        int32
	Planes        uint16
	BitCount      uint16
	Compression   uint32
	SizeImage     uint32
	XPelsPerMeter int32
	YPelsPerMeter int32
	ClrUsed       uint32
	ClrImportant  uint32
}

type bitmapInfo struct {
	Header bitmapInfoHeader
	Colors [1]uint32
}

// tray is the live icon. The window procedure reaches it through the registry
// because Win32 callbacks cannot carry a Go pointer.
type tray struct {
	hwnd     windows.Handle
	instance windows.Handle
	host     Controller
	options  TrayOptions
	ctx      context.Context
	interval time.Duration
	icons    map[State]windows.Handle
	snapshot Snapshot
	added    bool
	omnibox  *omniboxWindow
}

var (
	trayRegistryMu sync.Mutex
	trayRegistry   = map[windows.Handle]*tray{}
)

func lookupTray(hwnd windows.Handle) *tray {
	trayRegistryMu.Lock()
	defer trayRegistryMu.Unlock()
	return trayRegistry[hwnd]
}

func registerTray(t *tray) {
	trayRegistryMu.Lock()
	defer trayRegistryMu.Unlock()
	trayRegistry[t.hwnd] = t
}

func unregisterTray(hwnd windows.Handle) {
	trayRegistryMu.Lock()
	defer trayRegistryMu.Unlock()
	delete(trayRegistry, hwnd)
}

// menuItem is one row of the tray menu. It is pure data so the state-to-menu
// mapping is testable without Win32.
type menuItem struct {
	Label     string
	Enabled   bool
	Separator bool
	Command   int
}

// menuFor renders the menu for a snapshot. Pause/resume is always reachable
// (acceptance 8); search opens the address bar once EKB-045 is wired; settings
// stays disabled until EKB-047.
func menuFor(snapshot Snapshot, searchEnabled bool) []menuItem {
	action := pauseAction(snapshot)
	return []menuItem{
		{Label: snapshot.Title, Enabled: false},
		{Label: shorten(snapshot.Detail, 64), Enabled: false},
		{Separator: true},
		{Label: action.label, Enabled: true, Command: action.command},
		{Label: "Search…", Enabled: searchEnabled, Command: menuSearch},
		{Label: "Settings (EKB-047)", Enabled: false},
		{Separator: true},
		{Label: "Exit", Enabled: true, Command: menuExit},
	}
}

type trayAction struct {
	label   string
	command int
}

// pauseAction decides what the second menu row does. A paused tray resumes; a
// faulted tray retries; a live tray pauses.
func pauseAction(snapshot Snapshot) trayAction {
	switch snapshot.State {
	case StatePaused:
		return trayAction{label: "Resume capture", command: menuPause}
	case StateRunning, StateQueueBacklog:
		return trayAction{label: "Pause capture", command: menuPause}
	default:
		return trayAction{label: "Retry", command: menuPause}
	}
}

// trayTip is the tooltip: the title plus the readable reason (acceptance 5).
func trayTip(snapshot Snapshot) string {
	tip := snapshot.Title
	if snapshot.Detail != "" {
		tip += " — " + shorten(snapshot.Detail, 100)
	}
	return tip
}

// stateColor is the icon color for a state: green, grey, red, amber.
func stateColor(state State) uint32 {
	switch state {
	case StatePaused:
		return rgb(107, 114, 128)
	case StateDatabaseFault:
		return rgb(220, 38, 38)
	case StateQueueBacklog:
		return rgb(217, 119, 6)
	default:
		return rgb(22, 163, 74)
	}
}

func rgb(r, g, b uint32) uint32 {
	return 0xFF000000 | r<<16 | g<<8 | b
}

// RunTray shows the icon and blocks until the user exits or ctx is cancelled.
// The caller owes the host a final Exit; RunTray only closes the UI.
func RunTray(ctx context.Context, host Controller, options TrayOptions) error {
	interval := options.Interval
	if interval <= 0 {
		interval = time.Second
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	instance, _, callErr := procGetModuleHandleW.Call(0)
	if instance == 0 {
		return fmt.Errorf("errkb ui: get the module handle: %v", callErr)
	}
	className, err := windows.UTF16PtrFromString(trayClassName)
	if err != nil {
		return err
	}
	windowName, err := windows.UTF16PtrFromString("errkb")
	if err != nil {
		return err
	}

	atom, _, callErr := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wndClassEx{
		Size:      uint32(unsafe.Sizeof(wndClassEx{})),
		WndProc:   windows.NewCallback(trayWindowProc),
		Instance:  windows.Handle(instance),
		ClassName: className,
	})))
	if atom == 0 {
		return fmt.Errorf("errkb ui: register the tray window class: %v", callErr)
	}

	hwnd, _, callErr := procCreateWindowExW.Call(
		0, uintptr(unsafe.Pointer(className)), uintptr(unsafe.Pointer(windowName)),
		0, 0, 0, 0, 0, 0, 0, uintptr(instance), 0,
	)
	if hwnd == 0 {
		return fmt.Errorf("errkb ui: create the tray window: %v", callErr)
	}

	t := &tray{
		hwnd:     windows.Handle(hwnd),
		instance: windows.Handle(instance),
		host:     host,
		options:  options,
		ctx:      ctx,
		interval: interval,
		icons:    map[State]windows.Handle{},
	}
	for _, state := range []State{StateRunning, StatePaused, StateDatabaseFault, StateQueueBacklog} {
		icon, iconErr := newColorIcon(stateColor(state))
		if iconErr != nil {
			t.destroyIcons()
			procDestroyWindow.Call(hwnd)
			return iconErr
		}
		t.icons[state] = icon
	}
	registerTray(t)
	t.snapshot = host.Snapshot(ctx)
	t.addIcon()
	t.refresh(ctx)
	procSetTimer.Call(hwnd, trayTimerID, uintptr(interval/time.Millisecond), 0)

	go func() {
		<-ctx.Done()
		procPostMessageW.Call(hwnd, wmClose, 0, 0)
	}()

	var loopErr error
	for {
		var msg message
		ret, _, callErr := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(ret) == -1 {
			loopErr = fmt.Errorf("errkb ui: read the window message: %v", callErr)
			break
		}
		if ret == 0 {
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
	}
	procKillTimer.Call(hwnd, trayTimerID)
	return loopErr
}

// trayWindowProc is the Win32 callback. It recovers panics so a UI bug can
// never take the capture service down (acceptance 7).
func trayWindowProc(hwnd, msg, wparam, lparam uintptr) uintptr {
	defer func() { _ = recover() }()
	t := lookupTray(windows.Handle(hwnd))
	if t != nil {
		switch uint32(msg) {
		case wmTrayMessage:
			switch uint32(lparam) & 0xFFFF {
			case wmRButtonUp, wmLButtonUp, wmLButtonDbl:
				t.showMenu()
			}
			return 0
		case wmTimer:
			t.refresh(t.ctx)
			return 0
		case wmClose:
			procDestroyWindow.Call(hwnd)
			return 0
		case wmDestroy:
			t.removeIcon()
			t.destroyIcons()
			if t.omnibox != nil {
				t.omnibox.destroy()
				t.omnibox = nil
			}
			unregisterTray(windows.Handle(hwnd))
			procPostQuitMessage.Call(0)
			return 0
		}
	}
	result, _, _ := procDefWindowProcW.Call(hwnd, msg, wparam, lparam)
	return result
}

// refresh measures the host and updates the icon, tooltip and menu source data.
func (t *tray) refresh(ctx context.Context) {
	snapshot := t.host.Snapshot(ctx)
	t.snapshot = snapshot
	t.modifyIcon(snapshot)
}

// showMenu opens the tray menu at the cursor and dispatches the selection.
func (t *tray) showMenu() {
	snapshot := t.host.Snapshot(t.ctx)
	menu, _, _ := procCreatePopupMenu.Call()
	if menu == 0 {
		return
	}
	for _, item := range menuFor(snapshot, t.options.Search != nil) {
		if item.Separator {
			procAppendMenuW.Call(menu, mfSeparator, 0, 0)
			continue
		}
		flags := uintptr(mfString)
		if !item.Enabled {
			flags |= mfGrayed
		}
		label, err := windows.UTF16PtrFromString(item.Label)
		if err != nil {
			continue
		}
		procAppendMenuW.Call(menu, flags, uintptr(item.Command), uintptr(unsafe.Pointer(label)))
	}
	var cursor point
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&cursor)))
	procSetForegroundWindow.Call(uintptr(t.hwnd))
	command, _, _ := procTrackPopupMenu.Call(menu, tpmReturnCmd|tpmNonotify,
		uintptr(cursor.X), uintptr(cursor.Y), 0, uintptr(t.hwnd), 0)
	procDestroyMenu.Call(menu)

	switch command {
	case menuPause:
		if pauseAction(snapshot).label == "Pause capture" {
			_ = t.host.Pause(t.ctx)
		} else {
			_ = t.host.Resume(t.ctx)
		}
		t.refresh(t.ctx)
	case menuSearch:
		t.openSearch()
	case menuExit:
		_ = t.host.Exit(t.ctx)
		procDestroyWindow.Call(uintptr(t.hwnd))
	}
}

// openSearch lazily creates the address bar. A creation failure becomes a
// readable menu item the next time the menu opens, never a process exit.
func (t *tray) openSearch() {
	if t.options.Search == nil {
		return
	}
	if t.omnibox == nil {
		window, err := newOmniboxWindow(t.ctx, t.instance, t.options.Search)
		if err != nil {
			t.snapshot.Detail = shorten(err.Error(), 100)
			t.modifyIcon(t.snapshot)
			return
		}
		t.omnibox = window
	}
	t.omnibox.show()
}

func (t *tray) addIcon() {
	data := notifyIconData{
		Size:            uint32(unsafe.Sizeof(notifyIconData{})),
		Hwnd:            t.hwnd,
		ID:              trayID,
		Flags:           nifMessage | nifIcon | nifTip,
		CallbackMessage: wmTrayMessage,
		Icon:            t.icons[t.snapshot.State],
	}
	copyUTF16(data.Tip[:], trayTip(t.snapshot))
	procShellNotifyIconW.Call(nimAdd, uintptr(unsafe.Pointer(&data)))
	t.added = true
}

func (t *tray) modifyIcon(snapshot Snapshot) {
	if !t.added {
		return
	}
	data := notifyIconData{
		Size:  uint32(unsafe.Sizeof(notifyIconData{})),
		Hwnd:  t.hwnd,
		ID:    trayID,
		Flags: nifIcon | nifTip,
		Icon:  t.icons[snapshot.State],
	}
	copyUTF16(data.Tip[:], trayTip(snapshot))
	procShellNotifyIconW.Call(nimModify, uintptr(unsafe.Pointer(&data)))
}

func (t *tray) removeIcon() {
	if !t.added {
		return
	}
	data := notifyIconData{
		Size: uint32(unsafe.Sizeof(notifyIconData{})),
		Hwnd: t.hwnd,
		ID:   trayID,
	}
	procShellNotifyIconW.Call(nimDelete, uintptr(unsafe.Pointer(&data)))
	t.added = false
}

func (t *tray) destroyIcons() {
	for _, icon := range t.icons {
		procDestroyIcon.Call(uintptr(icon))
	}
	t.icons = map[State]windows.Handle{}
}

// newColorIcon draws a filled circle in the given 0xAARRGGBB color.
func newColorIcon(color uint32) (windows.Handle, error) {
	const size = 16
	info := bitmapInfo{Header: bitmapInfoHeader{
		Size:     uint32(unsafe.Sizeof(bitmapInfoHeader{})),
		Width:    size,
		Height:   size,
		Planes:   1,
		BitCount: 32,
	}}
	var bits unsafe.Pointer
	hbmColor, _, callErr := procCreateDIBSection.Call(0, uintptr(unsafe.Pointer(&info)), 0,
		uintptr(unsafe.Pointer(&bits)), 0, 0)
	if hbmColor == 0 || bits == nil {
		return 0, fmt.Errorf("errkb ui: create the tray icon bitmap: %v", callErr)
	}
	pixels := unsafe.Slice((*uint32)(bits), size*size)
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			dx, dy := x*2-(size-1), y*2-(size-1)
			if dx*dx+dy*dy <= 13*13 {
				pixels[y*size+x] = color
			}
		}
	}
	hbmMask, _, _ := procCreateBitmap.Call(size, size, 1, 1, 0)
	icon, _, callErr := procCreateIconIndirect.Call(uintptr(unsafe.Pointer(&iconInfo{
		Icon:  1,
		Mask:  windows.Handle(hbmMask),
		Color: windows.Handle(hbmColor),
	})))
	procDeleteObject.Call(hbmColor)
	procDeleteObject.Call(hbmMask)
	if icon == 0 {
		return 0, fmt.Errorf("errkb ui: create the tray icon: %v", callErr)
	}
	return windows.Handle(icon), nil
}

// copyUTF16 copies text into a fixed UTF-16 buffer with a terminating NUL.
func copyUTF16(destination []uint16, text string) {
	encoded := windows.StringToUTF16(text)
	if len(encoded) >= len(destination) {
		encoded = encoded[:len(destination)-1]
	}
	copy(destination, encoded)
	destination[len(encoded)] = 0
}

// ensure the build keeps the imported message helpers honest.
var _ = windows.NewCallback
