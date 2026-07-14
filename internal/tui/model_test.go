package tui

import (
	"fmt"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lesomnus/wfm/internal/wifi"
)

// seeded returns a model showing `n` access points on one interface, with the
// given viewport height, sorted as the UI would sort them.
func seeded(t *testing.T, height, n int) model {
	t.Helper()
	var m tea.Model = model{}
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: height})
	m, _ = m.Update(interfacesLoadedMsg{items: []*wifi.Interface{iface("wlan0")}})

	aps := make([]*wifi.AccessPoint, n)
	for i := range aps {
		// Descending signal so index i is the i-th strongest after sorting.
		aps[i] = ap(fmt.Sprintf("net-%02d", i), fmt.Sprintf("00:00:00:00:00:%02d", i),
			int32(100-i), uint32(5180+i), wifi.KeyManagement_KEY_MANAGEMENT_WPA_PSK)
	}
	m, _ = m.Update(scanResultMsg{ifID: "wlan0", items: aps})
	return m.(model)
}

func TestSortsBySignalDescending(t *testing.T) {
	var m tea.Model = model{}
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m, _ = m.Update(interfacesLoadedMsg{items: []*wifi.Interface{iface("wlan0")}})
	m, _ = m.Update(scanResultMsg{ifID: "wlan0", items: []*wifi.AccessPoint{
		ap("weak", "00:00:00:00:00:01", 20, 2412),
		ap("strong", "00:00:00:00:00:02", 90, 2412),
		ap("mid", "00:00:00:00:00:03", 55, 2412),
	}})

	mm := m.(model)
	got := []string{mm.aps[0].GetSsid(), mm.aps[1].GetSsid(), mm.aps[2].GetSsid()}
	want := []string{"strong", "mid", "weak"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sorted order = %v, want %v", got, want)
		}
	}
	if mm.selectedAP().GetSsid() != "strong" {
		t.Errorf("initial selection = %q, want strongest", mm.selectedAP().GetSsid())
	}
}

// TestScrollOffAhead checks the scroll invariant after every keypress while
// walking the cursor all the way down and back up: whenever there are at least
// scrollOff items ahead of the cursor in the direction of travel, that many rows
// must be visible ahead of it. Near a list boundary fewer is expected.
func TestScrollOffAhead(t *testing.T) {
	n := 20
	m := seeded(t, 14, n) // visibleRows == 10
	rows := m.visibleRows()
	var tm tea.Model = m

	check := func(dir int) {
		mm := tm.(model)
		end := mm.top + rows
		if end > len(mm.aps) {
			end = len(mm.aps)
		}
		if dir > 0 { // rows below the cursor
			if want := min(scrollOff, n-1-mm.apIndex); end-1-mm.apIndex < want {
				t.Fatalf("down at idx=%d top=%d: %d rows below, want %d", mm.apIndex, mm.top, end-1-mm.apIndex, want)
			}
		} else { // rows above the cursor
			if want := min(scrollOff, mm.apIndex); mm.apIndex-mm.top < want {
				t.Fatalf("up at idx=%d top=%d: %d rows above, want %d", mm.apIndex, mm.top, mm.apIndex-mm.top, want)
			}
		}
	}

	for i := 0; i < n; i++ {
		tm, _ = tm.Update(tea.KeyMsg{Type: tea.KeyDown})
		check(+1)
	}
	for i := 0; i < n; i++ {
		tm, _ = tm.Update(tea.KeyMsg{Type: tea.KeyUp})
		check(-1)
	}
}

// TestSelectionPreservedAcrossRescan checks that a periodic refresh keeps the
// cursor on the same access point even when the ordering changes.
func TestSelectionPreservedAcrossRescan(t *testing.T) {
	m := seeded(t, 24, 5)
	var tm tea.Model = m
	tm, _ = tm.Update(tea.KeyMsg{Type: tea.KeyDown})
	tm, _ = tm.Update(tea.KeyMsg{Type: tea.KeyDown})
	selID := tm.(model).selectedAP().GetId()

	// Re-scan with the same APs in a different order and shuffled signals.
	prev := tm.(model).aps
	reordered := []*wifi.AccessPoint{prev[4], prev[0], prev[2], prev[1], prev[3]}
	tm, _ = tm.Update(scanResultMsg{ifID: "wlan0", items: reordered})

	if got := tm.(model).selectedAP().GetId(); got != selID {
		t.Errorf("selection after rescan = %q, want %q", got, selID)
	}
}

// TestManualRescanKey checks that pressing r starts a scan of the current
// interface.
func TestManualRescanKey(t *testing.T) {
	m := seeded(t, 24, 3)
	if m.scanning {
		t.Fatal("precondition: should be idle after scan result")
	}
	var tm tea.Model = m
	tm, cmd := tm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	if !tm.(model).scanning {
		t.Error("model should be scanning after pressing r")
	}
	if cmd == nil {
		t.Error("pressing r should return a scan command")
	}
}
