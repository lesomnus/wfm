package nmdbus

import (
	"reflect"
	"testing"

	"github.com/lesomnus/wfm/internal/wnet"
)

// TestSettingsRoundtripDNS builds an NM settings dict from a spec and parses it
// back, covering ipv4 'au' and ipv6 'aay' DNS encoding (no D-Bus needed).
func TestSettingsRoundtripDNS(t *testing.T) {
	spec := wnet.ProfileSpec{
		SSID:        "Net",
		Autoconnect: true,
		Security:    wnet.Security{Kind: wnet.SecPSK, Passphrase: "pw"},
		IPv4: &wnet.IPConfig{
			Method:    wnet.IPManual,
			Addresses: []string{"192.168.1.10/24"},
			Gateway:   "192.168.1.1",
			DNS:       []string{"8.8.8.8", "1.1.1.1"},
		},
		IPv6: &wnet.IPConfig{
			Method:    wnet.IPManual,
			Addresses: []string{"2001:db8::10/64"},
			DNS:       []string{"2001:4860:4860::8888"},
		},
	}
	p := profileFromSettings(buildSettings(spec))

	if p.Security.Kind != wnet.SecPSK || !p.Autoconnect || p.SSID != "Net" {
		t.Errorf("basics wrong: %+v", p)
	}
	if p.IPv4.Method != wnet.IPManual || p.IPv6.Method != wnet.IPManual {
		t.Errorf("methods: v4=%v v6=%v", p.IPv4.Method, p.IPv6.Method)
	}
	if !reflect.DeepEqual(p.IPv4.Addresses, []string{"192.168.1.10/24"}) {
		t.Errorf("ipv4 addr = %v", p.IPv4.Addresses)
	}
	if !reflect.DeepEqual(p.IPv4.DNS, []string{"8.8.8.8", "1.1.1.1"}) {
		t.Errorf("ipv4 dns = %v", p.IPv4.DNS)
	}
	if !reflect.DeepEqual(p.IPv6.DNS, []string{"2001:4860:4860::8888"}) {
		t.Errorf("ipv6 dns = %v (aay roundtrip)", p.IPv6.DNS)
	}
}

func TestSettingsDHCP(t *testing.T) {
	spec := wnet.ProfileSpec{SSID: "Net", Security: wnet.Security{Kind: wnet.SecOpen}}
	p := profileFromSettings(buildSettings(spec))
	if p.IPv4.Method != wnet.IPAuto || p.IPv6.Method != wnet.IPAuto {
		t.Errorf("expected auto/DHCP, got v4=%v v6=%v", p.IPv4.Method, p.IPv6.Method)
	}
	if p.Security.Kind != wnet.SecOpen {
		t.Errorf("expected open, got %v", p.Security.Kind)
	}
}
