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

func profile(ssid string) *wifi.Profile {
	p := &wifi.Profile{}
	p.SetName(ssid)
	p.SetSsid(ssid)
	return p
}

// TestConnectedAndSavedMarkers checks that the active connection and networks
// with a saved profile are flagged in the list, and that the right panel shows a
// connected banner and the matching profile's details.
func TestConnectedAndSavedMarkers(t *testing.T) {
	var m tea.Model = model{}
	m, _ = m.Update(tea.WindowSizeMsg{Width: 100, Height: 24})
	m, _ = m.Update(interfacesLoadedMsg{items: []*wifi.Interface{iface("wlan0")}})

	home := ap("home", "aa:bb:cc:dd:ee:01", 80, 5180, wifi.KeyManagement_KEY_MANAGEMENT_WPA_PSK)
	cafe := ap("cafe", "aa:bb:cc:dd:ee:02", 50, 2412, wifi.KeyManagement_KEY_MANAGEMENT_NONE)
	m, _ = m.Update(scanResultMsg{ifID: "wlan0", items: []*wifi.AccessPoint{home, cafe}})
	m, _ = m.Update(profilesLoadedMsg{items: []*wifi.Profile{profile("home")}})
	m, _ = m.Update(ifaceStatusMsg{ifID: "wlan0", addr: "10.0.0.5", ap: cafe})

	// Both markers appear in the list.
	out := m.View()
	for _, want := range []string{markSaved, markConnected} {
		if !strings.Contains(out, want) {
			t.Errorf("list missing marker %q\n%s", want, out)
		}
	}

	// Strongest is "home", which has a profile: the right panel shows it.
	mm := m.(model)
	if got := mm.selectedAP().GetSsid(); got != "home" {
		t.Fatalf("selected = %q, want home", got)
	}
	if !strings.Contains(out, "Profile") || !strings.Contains(out, "Autoconnect") {
		t.Errorf("right panel missing profile details\n%s", out)
	}
}

func TestProfileSecurity(t *testing.T) {
	psk := &wifi.Security{}
	psk.SetPsk(&wifi.Security_Psk{})
	open := &wifi.Security{}
	open.SetOpen(&wifi.Security_Open{})

	cases := map[*wifi.Security]string{
		nil:  "-",
		psk:  "WPA/PSK",
		open: "open",
	}
	for s, want := range cases {
		if got := profileSecurity(s); got != want {
			t.Errorf("profileSecurity = %q, want %q", got, want)
		}
	}
}

func TestIpMethod(t *testing.T) {
	manual := &wifi.IpConfig{}
	manual.SetMethod(wifi.IpConfig_METHOD_MANUAL)
	manual.SetAddresses([]string{"192.168.1.10/24"})
	disabled := &wifi.IpConfig{}
	disabled.SetMethod(wifi.IpConfig_METHOD_DISABLED)

	cases := map[*wifi.IpConfig]string{
		nil:      "auto",
		manual:   "manual • 192.168.1.10/24",
		disabled: "disabled",
	}
	for c, want := range cases {
		if got := ipMethod(c); got != want {
			t.Errorf("ipMethod = %q, want %q", got, want)
		}
	}
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
