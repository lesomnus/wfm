package ubus

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/lesomnus/wfm/internal/wnet"
)

// These tests decode the testdata/*.json fixtures with the exact structs the
// backend uses and assert the parsers extract the right *domain* values. The
// assertions are semantic (an AP named "HomeNet" at ~64% quality on 5180 MHz,
// a PSK station profile, a DHCP address), so if a fixture is replaced with real
// OpenWrt JSON whose field names differ from what the code expects, the decode
// yields zero values and the assertion fails — surfacing the drift. See
// testdata/README.md for how the fixtures are (re)generated from a live node.

func readFixture(t *testing.T, name string) json.RawMessage {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

func TestFixtureScan(t *testing.T) {
	var res struct {
		Results []scanResult `json:"results"`
	}
	if err := json.Unmarshal(readFixture(t, "iwinfo_scan.json"), &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Results) < 2 {
		t.Fatalf("expected >=2 scan results, got %d", len(res.Results))
	}
	ap0 := apFromScan(res.Results[0])
	// Field-name guards: an empty ssid / zero signal / zero freq means the JSON
	// shape drifted from what scanResult expects.
	if ap0.SSID == "" || ap0.Signal <= 0 || ap0.FreqMHz == 0 {
		t.Fatalf("scan shape drift: ssid=%q signal=%d freq=%d (check iwinfo scan field names)", ap0.SSID, ap0.Signal, ap0.FreqMHz)
	}
	if ap0.SSID != "HomeNet" || ap0.Signal != 64 || ap0.FreqMHz != 5180 {
		t.Errorf("ap0 = %+v (want HomeNet, 64, 5180)", ap0)
	}
	if len(ap0.KeyMgmt) != 1 || ap0.KeyMgmt[0] != wnet.KeyWPAPSK {
		t.Errorf("ap0 keymgmt = %v, want [WPAPSK] (check encryption block)", ap0.KeyMgmt)
	}
	ap1 := apFromScan(res.Results[1])
	if len(ap1.KeyMgmt) != 1 || ap1.KeyMgmt[0] != wnet.KeyNone {
		t.Errorf("ap1 (open) keymgmt = %v, want [None]", ap1.KeyMgmt)
	}
}

func TestFixtureInfo(t *testing.T) {
	var info iwinfoInfo
	if err := json.Unmarshal(readFixture(t, "iwinfo_info.json"), &info); err != nil {
		t.Fatal(err)
	}
	it := (&Backend{}).toIface("phy1-sta0", info)
	if it.Mac == "" {
		t.Fatalf("info shape drift: no mac parsed (check iwinfo info `hwaddr`)")
	}
	if it.Mac != "aa:bb:cc:dd:ee:11" {
		t.Errorf("mac = %q, want aa:bb:cc:dd:ee:11", it.Mac)
	}
	if !it.Up {
		t.Errorf("associated station should be Up (bssid=%q)", info.BSSID)
	}
}

func TestFixtureDevices(t *testing.T) {
	var d struct {
		Devices []string `json:"devices"`
	}
	if err := json.Unmarshal(readFixture(t, "iwinfo_devices.json"), &d); err != nil {
		t.Fatal(err)
	}
	if len(d.Devices) == 0 {
		t.Fatal("devices shape drift: empty (check iwinfo devices `devices`)")
	}
}

func TestFixtureUCIWireless(t *testing.T) {
	var r struct {
		Values map[string]json.RawMessage `json:"values"`
	}
	if err := json.Unmarshal(readFixture(t, "uci_get_wireless.json"), &r); err != nil {
		t.Fatal(err)
	}
	ifaces := parseWifiIfaces(r.Values)
	if len(ifaces) != 2 { // ap + sta; the wifi-device is not an iface
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
	if sta.SSID == "" {
		t.Fatalf("uci shape drift: station has no ssid")
	}
	prof := toProfile(*sta)
	if prof.SSID != "HomeNet" || prof.Security.Kind != wnet.SecPSK || !prof.Autoconnect {
		t.Errorf("station profile = %+v (want HomeNet/PSK/autoconnect)", prof)
	}
	if sta.Key == "" {
		t.Errorf("station key not parsed (check uci `key`)")
	}
}

func TestFixtureWirelessStatus(t *testing.T) {
	m := parseWirelessStatus(readFixture(t, "network_wireless_status.json"))
	if len(m) == 0 {
		t.Fatal("wireless status shape drift: no section->ifname mapping (check `interfaces[].section`/`ifname`)")
	}
	if m["wfm_ab12cd34ef56"] != "phy1-sta0" {
		t.Errorf("sta section mapped to %q, want phy1-sta0", m["wfm_ab12cd34ef56"])
	}
	if m["default_radio0"] != "phy0-ap0" {
		t.Errorf("ap section mapped to %q, want phy0-ap0", m["default_radio0"])
	}
}

func TestFixtureIfStatus(t *testing.T) {
	addrs, gw, dns := parseIfStatus(readFixture(t, "network_interface_status.json"))
	if len(addrs) == 0 || gw == "" {
		t.Fatalf("ifstatus shape drift: addrs=%v gw=%q (check `ipv4-address`/`route`)", addrs, gw)
	}
	if addrs[0] != "192.168.66.123/24" {
		t.Errorf("addr = %q, want 192.168.66.123/24", addrs[0])
	}
	if gw != "192.168.66.1" {
		t.Errorf("gateway = %q, want 192.168.66.1", gw)
	}
	if len(dns) == 0 || dns[0] != "192.168.66.1" {
		t.Errorf("dns = %v, want [192.168.66.1]", dns)
	}
}
