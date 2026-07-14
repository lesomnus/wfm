package nmdbus

import (
	"reflect"
	"testing"

	"github.com/lesomnus/wfm/internal/wnet"
)

func TestKeyMgmtFromFlags(t *testing.T) {
	cases := []struct {
		name            string
		flags, wpa, rsn uint32
		want            []wnet.KeyMgmt
	}{
		{"open", 0, 0, 0, []wnet.KeyMgmt{wnet.KeyNone}},
		{"wpa2-psk", apFlagPrivacy, 0, secPSK, []wnet.KeyMgmt{wnet.KeyWPAPSK}},
		{"wpa-psk-legacy", apFlagPrivacy, secPSK, 0, []wnet.KeyMgmt{wnet.KeyWPAPSK}},
		{"wpa3-sae", apFlagPrivacy, 0, secSAE, []wnet.KeyMgmt{wnet.KeySAE}},
		{"wpa2/3-mixed", apFlagPrivacy, 0, secPSK | secSAE, []wnet.KeyMgmt{wnet.KeySAE, wnet.KeyWPAPSK}},
		{"eap", apFlagPrivacy, 0, sec8021X, []wnet.KeyMgmt{wnet.KeyWPAEAP}},
		{"owe", apFlagPrivacy, 0, secOWE, []wnet.KeyMgmt{wnet.KeyOWE}},
		{"wep", apFlagPrivacy, 0, 0, []wnet.KeyMgmt{wnet.KeyWPAPSK}},
	}
	for _, c := range cases {
		got := keyMgmtFromFlags(c.flags, c.wpa, c.rsn)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: keyMgmtFromFlags = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestMapDeviceState(t *testing.T) {
	cases := map[int]wnet.ConnState{
		nmStatePrepare:      wnet.StateAssociating,
		nmStateConfig:       wnet.StateAssociating,
		nmStateNeedAuth:     wnet.StateAuthenticating,
		nmStateIPConfig:     wnet.StateConfiguring,
		nmStateActivated:    wnet.StateConnected,
		nmStateDeactivating: wnet.StateDisconnecting,
		nmStateFailed:       wnet.StateFailed,
		nmStateDisconnected: wnet.StateIdle,
		nmStateUnmanaged:    wnet.StateUnspecified,
	}
	for code, want := range cases {
		if got := mapDeviceState(code); got != want {
			t.Errorf("mapDeviceState(%d) = %v, want %v", code, got, want)
		}
	}
}

func TestIP4U32Roundtrip(t *testing.T) {
	for _, ip := range []string{"192.168.1.1", "8.8.8.8", "10.0.0.254"} {
		u, ok := ip4ToU32(ip)
		if !ok {
			t.Fatalf("ip4ToU32(%q) failed", ip)
		}
		if got := u32ToIP4(u); got != ip {
			t.Errorf("roundtrip %q -> %d -> %q", ip, u, got)
		}
	}
	if _, ok := ip4ToU32("not-an-ip"); ok {
		t.Errorf("ip4ToU32 should reject non-ip")
	}
}

func TestSplitCIDR(t *testing.T) {
	ip, prefix, ok := splitCIDR("192.168.1.10/24")
	if !ok || ip != "192.168.1.10" || prefix != 24 {
		t.Errorf("splitCIDR = (%q,%d,%v)", ip, prefix, ok)
	}
	if _, _, ok := splitCIDR("192.168.1.10"); ok {
		t.Errorf("splitCIDR should reject bare ip")
	}
}
