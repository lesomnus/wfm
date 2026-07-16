package nmcli

import (
	"context"
	"os"
	"testing"
	"time"
)

func TestParseAPList(t *testing.T) {
	// Terse `device wifi list` output: IN-USE:SSID:BSSID:CHAN:FREQ:SIGNAL:SECURITY,
	// with ':' inside the BSSID escaped as '\:'.
	out := "*:shhwang:B2\\:38\\:6C\\:45\\:D5\\:80:112:5560 MHz:95:WPA2\n" +
		":cafe:11\\:22\\:33\\:44\\:55\\:66:1:2412 MHz:47:\n"

	aps := parseAPList(out)
	if len(aps) != 2 {
		t.Fatalf("parsed %d APs, want 2", len(aps))
	}

	if !aps[0].InUse || aps[0].SSID != "shhwang" || aps[0].BSSID != "B2:38:6C:45:D5:80" {
		t.Errorf("ap0 = %+v", aps[0])
	}
	if aps[0].Chan != 112 || aps[0].FreqMHz != 5560 || aps[0].Signal != 95 || aps[0].Security != "WPA2" {
		t.Errorf("ap0 fields = %+v", aps[0])
	}
	if aps[1].InUse || aps[1].SSID != "cafe" || aps[1].Security != "" {
		t.Errorf("ap1 = %+v (open network, not in use)", aps[1])
	}
}

func TestCountScannable(t *testing.T) {
	aps := []AP{{InUse: true, SSID: "me"}, {SSID: "a"}, {SSID: "b"}}
	if got := countScannable(aps); got != 2 {
		t.Errorf("countScannable = %d, want 2 (excludes the in-use AP)", got)
	}
	if got := countScannable([]AP{{InUse: true}}); got != 0 {
		t.Errorf("countScannable(only-connected) = %d, want 0", got)
	}
}

// TestScanLive exercises Scan against a real interface. It is skipped unless
// WFM_TEST_IFACE names one; run it (with privileges) as:
//
//	sudo -E env "PATH=$PATH" WFM_TEST_IFACE=wlx90de8061d724 \
//	  go test -count=1 -run TestScanLive -v ./internal/wnet/nmcli/
func TestScanLive(t *testing.T) {
	ifname := os.Getenv("WFM_TEST_IFACE")
	if ifname == "" {
		t.Skip("set WFM_TEST_IFACE to run the live scan test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	aps, err := Scan(ctx, ifname)
	if err != nil {
		t.Fatalf("Scan(%s): %v", ifname, err)
	}
	t.Logf("Scan(%s) returned %d APs (%d scannable)", ifname, len(aps), countScannable(aps))
	for _, ap := range aps {
		t.Logf("  in_use=%v %-24q %s ch=%d %dMHz sig=%d %s", ap.InUse, ap.SSID, ap.BSSID, ap.Chan, ap.FreqMHz, ap.Signal, ap.Security)
	}
	if countScannable(aps) == 0 {
		t.Errorf("no neighbouring APs found on %s; scan did not populate", ifname)
	}
}
