package ubus

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/lesomnus/wfm/internal/wnet"
)

// TestUbusLive exercises the backend against a real OpenWrt node reachable over
// ubus JSON-RPC. It is skipped unless WFM_TEST_UBUS names an endpoint, so the
// normal `go test ./...` stays hardware-free; the OpenWrt test harness under
// test/openwrt (and the ubus-live CI workflow) stand up a QEMU node and set the
// environment for it.
//
// Environment:
//
//	WFM_TEST_UBUS          ubus endpoint, e.g. http://127.0.0.1:8080/ubus (required)
//	WFM_TEST_UBUS_USER     login username (default "root")
//	WFM_TEST_UBUS_PASS     login password
//	WFM_TEST_UBUS_INSECURE "1" to skip TLS verification
//	WFM_TEST_UBUS_SSID     AP the harness broadcasts, expected in the scan (default "WFMTEST")
//	WFM_TEST_UBUS_PSK      that AP's passphrase (default "wfmtest123")
//	WFM_TEST_UBUS_RADIO    wifi-device new station profiles attach to, e.g. "radio1"
//	WFM_TEST_UBUS_STA      device to scan/connect from (default: first interface)
//	WFM_TEST_UBUS_CONNECT  "1" to also activate/status/watch (needs the AP up)
func TestUbusLive(t *testing.T) {
	endpoint := os.Getenv("WFM_TEST_UBUS")
	if endpoint == "" {
		t.Skip("set WFM_TEST_UBUS to run the live OpenWrt test")
	}
	ssid := envOr("WFM_TEST_UBUS_SSID", "WFMTEST")
	psk := envOr("WFM_TEST_UBUS_PSK", "wfmtest123")

	opts := Options{
		Endpoint: endpoint,
		Username: envOr("WFM_TEST_UBUS_USER", "root"),
		Password: os.Getenv("WFM_TEST_UBUS_PASS"),
		Insecure: os.Getenv("WFM_TEST_UBUS_INSECURE") == "1",
		Radio:    os.Getenv("WFM_TEST_UBUS_RADIO"),
	}
	// When capturing, tee real ubus responses into the fixtures so this run
	// regenerates testdata/*.json from the live node (see capture_test.go).
	if dir := os.Getenv("WFM_TEST_UBUS_CAPTURE"); dir != "" {
		opts.HTTP = captureHTTPClient(dir)
		t.Logf("capturing ubus fixtures into %s", dir)
	}
	b, err := New(opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// ---- interfaces ---------------------------------------------------------
	its, err := b.Interfaces(ctx)
	if err != nil {
		t.Fatalf("Interfaces: %v", err)
	}
	if len(its) == 0 {
		t.Fatal("no wireless interfaces reported")
	}
	for _, it := range its {
		t.Logf("iface %-8s mac=%s up=%v desc=%q", it.Name, it.Mac, it.Up, it.Desc)
	}
	dev := envOr("WFM_TEST_UBUS_STA", its[0].Name)

	// ---- scan ---------------------------------------------------------------
	aps, err := b.Scan(ctx, dev)
	if err != nil {
		t.Fatalf("Scan(%s): %v", dev, err)
	}
	sawSSID := false
	for _, ap := range aps {
		t.Logf("ap %-24q %s sig=%d %dMHz km=%v", ap.SSID, ap.BSSID, ap.Signal, ap.FreqMHz, ap.KeyMgmt)
		if ap.SSID == ssid {
			sawSSID = true
		}
	}
	if !sawSSID {
		// Not fatal: the AP may still be settling. Surface it loudly.
		t.Logf("WARNING: test AP %q not seen in scan of %s (%d APs)", ssid, dev, len(aps))
	}

	// ---- profile CRUD -------------------------------------------------------
	prof, err := b.AddProfile(ctx, wnet.ProfileSpec{
		SSID:        ssid,
		Autoconnect: false,
		Security:    wnet.Security{Kind: wnet.SecPSK, Passphrase: psk},
	})
	if err != nil {
		t.Fatalf("AddProfile: %v", err)
	}
	t.Logf("added profile id=%s ssid=%s", prof.ID, prof.SSID)
	t.Cleanup(func() {
		if err := b.DeleteProfile(context.Background(), prof.ID); err != nil {
			t.Logf("cleanup DeleteProfile: %v", err)
		}
	})

	got, err := b.Profile(ctx, prof.ID)
	if err != nil {
		t.Fatalf("Profile(%s): %v", prof.ID, err)
	}
	if got.SSID != ssid || got.Security.Kind != wnet.SecPSK {
		t.Errorf("round-tripped profile = %+v", got)
	}
	profs, err := b.Profiles(ctx)
	if err != nil {
		t.Fatalf("Profiles: %v", err)
	}
	if !containsProfile(profs, prof.ID) {
		t.Errorf("added profile %s not in list of %d", prof.ID, len(profs))
	}

	// ---- connect / status / watch (opt-in) ----------------------------------
	if os.Getenv("WFM_TEST_UBUS_CONNECT") != "1" {
		return
	}
	active, err := b.Activate(ctx, dev, prof.ID, "")
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	t.Logf("activated conn=%s iface=%s bssid=%s", active.ID, active.Iface, active.BSSID)
	t.Cleanup(func() {
		if err := b.Deactivate(context.Background(), active.ID); err != nil {
			t.Logf("cleanup Deactivate: %v", err)
		}
	})

	st, err := b.Status(ctx, active.ID)
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	t.Logf("status state=%v bssid=%s signal=%d addrs=%v gw=%s dns=%v",
		st.State, st.BSSID, st.Signal, st.Addresses, st.Gateway, st.DNS)
	if st.State != wnet.StateConnected {
		t.Errorf("expected connected, got state=%v (%s)", st.State, st.Detail)
	}
	if len(st.Addresses) == 0 {
		t.Logf("WARNING: connected but no IP assigned (check the AP's DHCP)")
	}

	// Watch should reach a terminal state.
	wctx, wcancel := context.WithTimeout(ctx, 35*time.Second)
	defer wcancel()
	ch, err := b.Watch(wctx, active.ID)
	if err != nil {
		t.Fatalf("Watch: %v", err)
	}
	var last wnet.ConnState = -1
	for s := range ch {
		t.Logf("watch state=%v", s.State)
		last = s.State
	}
	if last != wnet.StateConnected && last != wnet.StateFailed {
		t.Errorf("watch ended on non-terminal state %v", last)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func containsProfile(ps []wnet.Profile, id string) bool {
	for _, p := range ps {
		if p.ID == id {
			return true
		}
	}
	return false
}
