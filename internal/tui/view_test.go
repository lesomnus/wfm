package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lesomnus/wfm/internal/wifi"
)

func ap(ssid, bssid string, sig int32, freq uint32, km ...wifi.KeyManagement) *wifi.AccessPoint {
	a := &wifi.AccessPoint{}
	a.SetId(bssid)
	a.SetSsid(ssid)
	a.SetBssid(bssid)
	a.SetSignal(sig)
	a.SetFrequency(freq)
	a.SetKeyManagement(km)
	return a
}

func iface(id string) *wifi.Interface {
	it := &wifi.Interface{}
	it.SetId(id)
	return it
}

// TestViewRenders exercises the layout and navigation without a terminal: it
// feeds a size, an interface list, and a scan result, then checks the rendered
// frame contains the expected header, list, and detail fields.
func TestViewRenders(t *testing.T) {
	var m tea.Model = model{}

	m, _ = m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m, _ = m.Update(interfacesLoadedMsg{items: []*wifi.Interface{iface("wlan0"), iface("wlan1")}})
	m, _ = m.Update(scanResultMsg{
		ifID: "wlan0",
		items: []*wifi.AccessPoint{
			ap("home-net", "aa:bb:cc:dd:ee:ff", 72, 5180, wifi.KeyManagement_KEY_MANAGEMENT_WPA_PSK),
			ap("cafe", "11:22:33:44:55:66", 40, 2412, wifi.KeyManagement_KEY_MANAGEMENT_NONE),
		},
	})

	out := m.View()
	for _, want := range []string{"wlan0", "1/2", "home-net", "cafe", "aa:bb:cc:dd:ee:ff", "wpa-psk", "5180 MHz"} {
		if !strings.Contains(out, want) {
			t.Errorf("view missing %q\n---\n%s", want, out)
		}
	}

	// Down then Tab: selection moves, Tab switches interface and clears APs.
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyDown})
	if got := m.(model).apIndex; got != 1 {
		t.Fatalf("apIndex after down = %d, want 1", got)
	}
	m, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	mm := m.(model)
	if mm.ifIndex != 1 {
		t.Fatalf("ifIndex after tab = %d, want 1", mm.ifIndex)
	}
	if mm.apIndex != 0 || len(mm.aps) != 0 || !mm.scanning {
		t.Fatalf("after tab: apIndex=%d aps=%d scanning=%v, want 0/0/true", mm.apIndex, len(mm.aps), mm.scanning)
	}
	if !strings.Contains(m.View(), "2/2") {
		t.Errorf("view after tab missing counter 2/2\n%s", m.View())
	}
}
