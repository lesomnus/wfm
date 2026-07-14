package tui

import (
	"fmt"
	"strings"
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

func TestPickAddr(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{nil, ""},
		{[]string{"10.0.0.5/24"}, "10.0.0.5"},
		{[]string{"fe80::1/64", "10.0.0.5/24"}, "10.0.0.5"}, // prefer IPv4
		{[]string{"fe80::1/64"}, "fe80::1"},                 // fall back to first
	}
	for _, c := range cases {
		if got := pickAddr(c.in); got != c.want {
			t.Errorf("pickAddr(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestIfaceStatusInHeader checks the resolved IP appears in the header, and that
// a status for a non-selected interface is ignored.
func TestIfaceStatusInHeader(t *testing.T) {
	it := &wifi.Interface{}
	it.SetId("wlan0")
	it.SetMac("3C:9C:0F:AB:CD:12")

	var m tea.Model = model{}
	m, _ = m.Update(tea.WindowSizeMsg{Width: 90, Height: 24})
	m, _ = m.Update(interfacesLoadedMsg{items: []*wifi.Interface{it}})

	m, _ = m.Update(ifaceStatusMsg{ifID: "wlan1", addr: "10.0.0.9"}) // wrong iface
	if mm := m.(model); mm.ifAddr != "" {
		t.Fatalf("status for non-selected interface applied: %q", mm.ifAddr)
	}

	m, _ = m.Update(ifaceStatusMsg{ifID: "wlan0", addr: "192.168.0.42"})
	out := m.View()
	for _, want := range []string{"3C:9C:0F:AB:CD:12", "•", "192.168.0.42"} {
		if !strings.Contains(out, want) {
			t.Errorf("header missing %q\n%s", want, out)
		}
	}
}

func TestSignalColorBuckets(t *testing.T) {
	cases := map[int32]int{0: 0, 5: 0, 19: 1, 55: 5, 64: 6, 99: 9, 100: 9}
	for sig, bucket := range cases {
		if got := signalColor(sig); got != signalPalette[bucket] {
			t.Errorf("signalColor(%d) = %q, want bucket %d (%q)", sig, got, bucket, signalPalette[bucket])
		}
	}
}

// TestConnectionShownDuringScan checks that while a scan is loading the active
// connection appears immediately, and that the full scan result then replaces it
// while keeping the cursor on that same AP.
func TestConnectionShownDuringScan(t *testing.T) {
	var m tea.Model = model{}
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m, _ = m.Update(interfacesLoadedMsg{items: []*wifi.Interface{iface("wlan0")}})
	if mm := m.(model); !mm.scanning || len(mm.aps) != 0 {
		t.Fatalf("precondition: want scanning with empty list, got scanning=%v aps=%d", mm.scanning, len(mm.aps))
	}

	conn := ap("home", "00:00:00:00:00:02", 60, 5180, wifi.KeyManagement_KEY_MANAGEMENT_WPA_PSK)
	m, _ = m.Update(ifaceStatusMsg{ifID: "wlan0", addr: "10.0.0.2", ap: conn})
	mm := m.(model)
	if len(mm.aps) != 1 || mm.aps[0].GetId() != conn.GetId() {
		t.Fatalf("connection not shown during scan: aps=%d", len(mm.aps))
	}
	if !mm.scanning {
		t.Error("should still be scanning while showing the connection")
	}

	// Full scan result arrives and replaces the placeholder, selection preserved.
	m, _ = m.Update(scanResultMsg{ifID: "wlan0", items: []*wifi.AccessPoint{
		ap("other", "00:00:00:00:00:01", 90, 2412),
		conn,
		ap("weak", "00:00:00:00:00:03", 20, 2412),
	}})
	mm = m.(model)
	if mm.scanning {
		t.Error("scan should be done")
	}
	if got := mm.selectedAP().GetId(); got != conn.GetId() {
		t.Errorf("selection after scan = %q, want connected AP %q", got, conn.GetId())
	}
}

// TestScanResultBeforeStatusNotClobbered checks that a connection status
// arriving after the scan already populated the list does not shrink it back to
// the single connected AP.
func TestScanResultBeforeStatusNotClobbered(t *testing.T) {
	var m tea.Model = model{}
	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m, _ = m.Update(interfacesLoadedMsg{items: []*wifi.Interface{iface("wlan0")}})
	conn := ap("home", "00:00:00:00:00:02", 60, 5180)
	m, _ = m.Update(scanResultMsg{ifID: "wlan0", items: []*wifi.AccessPoint{
		ap("other", "00:00:00:00:00:01", 90, 2412), conn,
	}})
	m, _ = m.Update(ifaceStatusMsg{ifID: "wlan0", addr: "10.0.0.2", ap: conn})
	if mm := m.(model); len(mm.aps) != 2 {
		t.Errorf("aps after late status = %d, want 2 (list must not be clobbered)", len(mm.aps))
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
