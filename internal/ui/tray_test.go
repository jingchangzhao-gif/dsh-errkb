//go:build windows

package ui

import (
	"strings"
	"testing"
)

func TestMenuForRunning(t *testing.T) {
	items := menuFor(Snapshot{State: StateRunning, Title: "errkb: running", Detail: "capturing"}, true)
	if len(items) != 8 {
		t.Fatalf("items = %d, want 8", len(items))
	}
	if items[3].Label != "Pause capture" || !items[3].Enabled {
		t.Fatalf("pause row = %+v", items[3])
	}
	if items[4].Label != "Search…" || !items[4].Enabled || items[4].Command != menuSearch {
		t.Fatalf("search row = %+v, want an enabled search command", items[4])
	}
	if items[5].Label != "Settings (EKB-047)" || items[5].Enabled {
		t.Fatalf("settings row = %+v, want disabled", items[5])
	}
	if items[7].Label != "Exit" || !items[7].Enabled || items[7].Command != menuExit {
		t.Fatalf("exit row = %+v", items[7])
	}
}

func TestMenuForPaused(t *testing.T) {
	items := menuFor(Snapshot{State: StatePaused, Title: "errkb: capture paused"}, true)
	if items[3].Label != "Resume capture" || items[3].Command != menuPause {
		t.Fatalf("pause row = %+v, want resume", items[3])
	}
}

func TestMenuForFaultOffersRetry(t *testing.T) {
	items := menuFor(Snapshot{State: StateDatabaseFault, Detail: "connection refused"}, false)
	if items[3].Label != "Retry" {
		t.Fatalf("pause row = %+v, want retry", items[3])
	}
	if items[4].Enabled {
		t.Fatalf("search row = %+v, want disabled without a backend", items[4])
	}
	if !strings.Contains(items[1].Label, "connection refused") {
		t.Fatalf("detail row = %q, want the reason", items[1].Label)
	}
}

func TestPauseActionForEachState(t *testing.T) {
	cases := map[State]string{
		StateRunning:       "Pause capture",
		StatePaused:        "Resume capture",
		StateDatabaseFault: "Retry",
		StateQueueBacklog:  "Pause capture",
	}
	for state, want := range cases {
		if got := pauseAction(Snapshot{State: state}).label; got != want {
			t.Fatalf("state %q: label = %q, want %q", state, got, want)
		}
	}
}

func TestTrayTipCarriesTheReason(t *testing.T) {
	tip := trayTip(Snapshot{State: StateDatabaseFault, Title: "errkb: database unavailable", Detail: "connection refused"})
	if !strings.Contains(tip, "database unavailable") || !strings.Contains(tip, "connection refused") {
		t.Fatalf("tip = %q", tip)
	}
}

func TestShortenCountsRunes(t *testing.T) {
	if got := shorten("abcdef", 4); got != "abc…" {
		t.Fatalf("shorten = %q", got)
	}
	if got := shorten("数据库连接失败", 3); got != "数据…" {
		t.Fatalf("shorten CJK = %q", got)
	}
	if got := shorten("ok", 10); got != "ok" {
		t.Fatalf("shorten short = %q", got)
	}
}

func TestStateColorsDiffer(t *testing.T) {
	seen := map[uint32]State{}
	for _, state := range []State{StateRunning, StatePaused, StateDatabaseFault, StateQueueBacklog} {
		color := stateColor(state)
		if other, exists := seen[color]; exists {
			t.Fatalf("%q and %q share the color %#x", state, other, color)
		}
		seen[color] = state
		if color>>24 != 0xFF {
			t.Fatalf("state %q: alpha = %#x, want opaque", state, color>>24)
		}
	}
}

func TestColorIconCreates(t *testing.T) {
	icon, err := newColorIcon(stateColor(StateRunning))
	if err != nil {
		t.Fatalf("newColorIcon: %v", err)
	}
	if icon == 0 {
		t.Fatal("icon handle is zero")
	}
	procDestroyIcon.Call(uintptr(icon))
}
