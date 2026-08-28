package ubus

import (
	"encoding/json"
	"testing"

	"github.com/lesomnus/wfm/internal/wnet"
)

func TestSignalQuality(t *testing.T) {
	cases := []struct {
		q, qmax, want int
	}{
		{70, 70, 100},
		{35, 70, 50},
		{0, 70, 0},
		{100, 70, 100}, // clamp above max
		{-1, 70, 0},    // clamp below zero
		{50, 0, 0},     // guard divide-by-zero
	}
	for _, c := range cases {
		if got := signalQuality(c.q, c.qmax); got != c.want {
			t.Errorf("signalQuality(%d,%d) = %d, want %d", c.q, c.qmax, got, c.want)
		}
	}
}

func TestEncToKeyMgmt(t *testing.T) {
	cases := []struct {
		name string
		enc  encryption
		want []wnet.KeyMgmt
	}{
		{"open", encryption{Enabled: false}, []wnet.KeyMgmt{wnet.KeyNone}},
		{"psk2", encryption{Enabled: true, Wpa: []int{2}, Authentication: []string{"psk"}}, []wnet.KeyMgmt{wnet.KeyWPAPSK}},
		{"sae", encryption{Enabled: true, Wpa: []int{3}, Authentication: []string{"sae"}}, []wnet.KeyMgmt{wnet.KeySAE}},
		{"mixed", encryption{Enabled: true, Wpa: []int{2, 3}, Authentication: []string{"psk", "sae"}}, []wnet.KeyMgmt{wnet.KeySAE, wnet.KeyWPAPSK}},
		{"eap", encryption{Enabled: true, Authentication: []string{"8021x"}}, []wnet.KeyMgmt{wnet.KeyWPAEAP}},
		{"owe", encryption{Enabled: true, Authentication: []string{"owe"}}, []wnet.KeyMgmt{wnet.KeyOWE}},
		{"secured-unknown", encryption{Enabled: true, Wep: true}, []wnet.KeyMgmt{wnet.KeyWPAPSK}},
	}
	for _, c := range cases {
		got := encToKeyMgmt(c.enc)
		if len(got) != len(c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s: got %v, want %v", c.name, got, c.want)
				break
			}
		}
	}
}

func TestIsAssociated(t *testing.T) {
	cases := map[string]bool{
		"AA:BB:CC:DD:EE:FF": true,
		"00:00:00:00:00:00": false,
		"":                  false,
		"  ":                false,
	}
	for bssid, want := range cases {
		if got := isAssociated(bssid); got != want {
			t.Errorf("isAssociated(%q) = %v, want %v", bssid, got, want)
		}
	}
}

func TestSecurityMapping(t *testing.T) {
	cases := map[string]wnet.SecurityKind{
		"":          wnet.SecOpen,
		"none":      wnet.SecOpen,
		"psk":       wnet.SecPSK,
		"psk2":      wnet.SecPSK,
		"psk2+ccmp": wnet.SecPSK,
		"sae":       wnet.SecPSK,
		"wpa2-eap":  wnet.SecEnterprise,
		"8021x":     wnet.SecEnterprise,
	}
	for enc, want := range cases {
		if got := encStrToSecKind(enc); got != want {
			t.Errorf("encStrToSecKind(%q) = %v, want %v", enc, got, want)
		}
	}
	// profileID is stable and distinguishes security kinds.
	a := profileID("HomeNet", wnet.SecPSK)
	if a != profileID("HomeNet", wnet.SecPSK) {
		t.Error("profileID not deterministic")
	}
	if a == profileID("HomeNet", wnet.SecOpen) {
		t.Error("profileID collides across security kinds")
	}
}

func TestParseWifiIfaces(t *testing.T) {
	values := map[string]json.RawMessage{
		"radio0":  json.RawMessage(`{".type":"wifi-device",".name":"radio0","type":"mac80211","channel":"36"}`),
		"default": json.RawMessage(`{".type":"wifi-iface",".name":"default","device":"radio0","mode":"ap","ssid":"Router"}`),
		"wfm_ab":  json.RawMessage(`{".type":"wifi-iface",".name":"wfm_ab","device":"radio0","mode":"sta","ssid":"HomeNet","encryption":"psk2","key":"secret","network":"wwan","disabled":"0"}`),
	}
	ifaces := parseWifiIfaces(values)
	if len(ifaces) != 2 { // both wifi-iface sections, not the wifi-device
		t.Fatalf("parsed %d wifi-iface sections, want 2", len(ifaces))
	}
	var sta *wifiIface
	for i := range ifaces {
		if ifaces[i].Mode == "sta" {
			sta = &ifaces[i]
		}
	}
	if sta == nil {
		t.Fatal("no station iface parsed")
	}
	if sta.SSID != "HomeNet" || sta.Encryption != "psk2" || sta.Key != "secret" || sta.Network != "wwan" || sta.Disabled {
		t.Errorf("station parsed wrong: %+v", *sta)
	}
}

func TestUCIBool(t *testing.T) {
	for _, tc := range []struct {
		in   any
		want bool
	}{
		{"1", true}, {"0", false}, {"true", true}, {"on", true},
		{"", false}, {true, true}, {false, false}, {"no", false},
	} {
		if got := uciBool(tc.in); got != tc.want {
			t.Errorf("uciBool(%v) = %v, want %v", tc.in, got, tc.want)
		}
	}
}
