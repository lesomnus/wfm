package iwd

import (
	"testing"

	"github.com/lesomnus/wfm/internal/wnet"
)

func TestSSIDFilenameRoundtrip(t *testing.T) {
	cases := []struct {
		ssid string
		typ  string
	}{
		{"HomeNet", "psk"},
		{"My WiFi", "psk"},
		{"with-dash_and", "open"},
		{"Café", "psk"},
		{"emoji😀net", "psk"},
	}
	for _, c := range cases {
		file := profileFilename(c.ssid, c.typ)
		ssid, typ, ok := parseProfileFilename(file)
		if !ok {
			t.Errorf("parseProfileFilename(%q) failed", file)
			continue
		}
		if ssid != c.ssid || typ != c.typ {
			t.Errorf("roundtrip %q: got (%q,%q), want (%q,%q)", file, ssid, typ, c.ssid, c.typ)
		}
	}
}

func TestEncodeSSIDSpecial(t *testing.T) {
	// Café = bytes 43 61 66 c3 a9 => "=436166c3a9".
	if got := encodeSSID("Café"); got != "=436166c3a9" {
		t.Errorf("encodeSSID(Café) = %q, want =436166c3a9", got)
	}
	if encodeSSID("HomeNet") != "HomeNet" {
		t.Errorf("simple SSID should be verbatim")
	}
	if !simpleSSID("ok_Name-1 2") {
		t.Errorf("simpleSSID should accept alnum/space/_/-")
	}
	if simpleSSID("has.dot") {
		t.Errorf("simpleSSID should reject '.'")
	}
}

func TestParseProfileFilenameRejects(t *testing.T) {
	for _, name := range []string{"noext", "ssid.txt", "ssid.wpa"} {
		if _, _, ok := parseProfileFilename(name); ok {
			t.Errorf("parseProfileFilename(%q) should be rejected", name)
		}
	}
}

func TestCIDRRoundtrip(t *testing.T) {
	addr, mask, ok := cidrToAddrNetmask("192.168.1.10/24")
	if !ok || addr != "192.168.1.10" || mask != "255.255.255.0" {
		t.Fatalf("cidrToAddrNetmask = (%q,%q,%v)", addr, mask, ok)
	}
	if got := joinAddrNetmask(addr, mask); got != "192.168.1.10/24" {
		t.Errorf("joinAddrNetmask = %q, want 192.168.1.10/24", got)
	}
}

func TestRenderParseProfile(t *testing.T) {
	spec := wnet.ProfileSpec{
		SSID:        "HomeNet",
		Autoconnect: true,
		Security:    wnet.Security{Kind: wnet.SecPSK, Passphrase: "correcthorse"},
		IPv4: &wnet.IPConfig{
			Method:    wnet.IPManual,
			Addresses: []string{"192.168.1.50/24"},
			Gateway:   "192.168.1.1",
			DNS:       []string{"1.1.1.1"},
		},
	}
	content := renderProfile(spec.Security.Kind, spec.Security.Passphrase, spec.Autoconnect, spec.Hidden, spec.IPv4, spec.IPv6)
	p := parseProfile("HomeNet", "psk", content)
	if p.SSID != "HomeNet" || p.Security.Kind != wnet.SecPSK || !p.Autoconnect {
		t.Errorf("parsed profile basics wrong: %+v", p)
	}
	if p.IPv4.Method != wnet.IPManual {
		t.Errorf("IPv4.Method = %v, want manual", p.IPv4.Method)
	}
	if len(p.IPv4.Addresses) != 1 || p.IPv4.Addresses[0] != "192.168.1.50/24" {
		t.Errorf("IPv4.Addresses = %v", p.IPv4.Addresses)
	}
	if p.IPv4.Gateway != "192.168.1.1" {
		t.Errorf("IPv4.Gateway = %q", p.IPv4.Gateway)
	}
	if len(p.IPv4.DNS) != 1 || p.IPv4.DNS[0] != "1.1.1.1" {
		t.Errorf("IPv4.DNS = %v", p.IPv4.DNS)
	}
}

func TestRenderProfileDHCP(t *testing.T) {
	content := renderProfile(wnet.SecPSK, "pw", true, false, nil, nil)
	p := parseProfile("Net", "psk", content)
	if p.IPv4.Method != wnet.IPAuto {
		t.Errorf("expected auto/DHCP, got %v", p.IPv4.Method)
	}
}
