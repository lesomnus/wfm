package nmdbus

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestScanLive exercises Scan against a real interface over the system bus. It
// is skipped unless WFM_TEST_IFACE names one; run it (with privileges) as:
//
//	sudo -E env "PATH=$PATH" WFM_TEST_IFACE=wlx90de8061d724 \
//	  go test -count=1 -run TestScanLive -v ./internal/wnet/nmdbus/
func TestScanLive(t *testing.T) {
	ifname := os.Getenv("WFM_TEST_IFACE")
	if ifname == "" {
		t.Skip("set WFM_TEST_IFACE to run the live scan test")
	}

	b, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer b.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	aps, err := b.Scan(ctx, ifname)
	if err != nil {
		t.Fatalf("Scan(%s): %v", ifname, err)
	}
	t.Logf("Scan(%s) returned %d APs", ifname, len(aps))
	for _, ap := range aps {
		t.Logf("  %-24q %s %dMHz sig=%d", ap.SSID, ap.BSSID, ap.FreqMHz, ap.Signal)
	}
	if len(aps) < 2 {
		t.Errorf("only %d AP(s) on %s; scan did not populate the neighbourhood", len(aps), ifname)
	}
}
