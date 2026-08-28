package ubus

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/lesomnus/wfm/internal/wnet"
)

// fakeNode is an in-memory OpenWrt node exposing just enough of the ubus
// JSON-RPC surface to drive the backend end-to-end: session login, iwinfo
// devices/info/scan, and uci get/add/set/delete/commit over the wireless config.
type fakeNode struct {
	mu       sync.Mutex
	user     string
	pass     string
	session  string
	loginN   int                       // number of successful logins
	expireIn int                       // if >0, reject this many calls with code 6 first
	wireless map[string]map[string]any // section -> options (incl. .type/.name)
	infos    map[string]map[string]any // device -> iwinfo info
	scan     []map[string]any          // iwinfo scan results
}

func newFakeNode() *fakeNode {
	return &fakeNode{
		user:    "root",
		pass:    "secret",
		session: "S3S5I0N0000000000000000000000000",
		wireless: map[string]map[string]any{
			"radio0": {".type": "wifi-device", ".name": "radio0", "type": "mac80211", "channel": "36"},
		},
		infos: map[string]map[string]any{
			"wlan0": {"hwaddr": "AA:BB:CC:DD:EE:00", "mode": "Client", "ssid": "", "bssid": "00:00:00:00:00:00", "quality": 0, "quality_max": 70},
		},
		scan: []map[string]any{
			{"ssid": "HomeNet", "bssid": "11:22:33:44:55:66", "channel": 36, "mhz": 5180, "signal": -55, "quality": 45, "quality_max": 70,
				"encryption": map[string]any{"enabled": true, "wpa": []int{2}, "authentication": []string{"psk"}}},
			{"ssid": "Cafe", "bssid": "AA:BB:CC:00:11:22", "channel": 1, "mhz": 2412, "signal": -70, "quality": 25, "quality_max": 70,
				"encryption": map[string]any{"enabled": false}},
		},
	}
}

func (f *fakeNode) server() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(f.handle))
}

func (f *fakeNode) handle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID     int64             `json:"id"`
		Method string            `json:"method"`
		Params []json.RawMessage `json:"params"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	var session, object, method string
	json.Unmarshal(req.Params[0], &session)
	json.Unmarshal(req.Params[1], &object)
	json.Unmarshal(req.Params[2], &method)
	var args map[string]any
	if len(req.Params) > 3 {
		json.Unmarshal(req.Params[3], &args)
	}

	code, data := f.dispatch(session, object, method, args)

	result := []any{code}
	if data != nil {
		result = append(result, data)
	}
	resp, _ := json.Marshal(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": result})
	w.Header().Set("Content-Type", "application/json")
	w.Write(resp)
}

func (f *fakeNode) dispatch(session, object, method string, args map[string]any) (int, any) {
	f.mu.Lock()
	defer f.mu.Unlock()

	if object == "session" && method == "login" {
		if args["username"] != f.user || args["password"] != f.pass {
			return 6, nil
		}
		f.loginN++
		return 0, map[string]any{"ubus_rpc_session": f.session, "timeout": 300}
	}
	if session != f.session {
		return 6, nil // not authenticated
	}
	if f.expireIn > 0 {
		f.expireIn--
		return 6, nil // simulate an expired session; the client should re-login
	}

	switch object {
	case "iwinfo":
		return f.iwinfo(method, args)
	case "uci":
		return f.uci(method, args)
	case "network.wireless":
		if method == "status" || method == "up" {
			return 0, nil
		}
	}
	return 3, nil // method not found
}

func (f *fakeNode) iwinfo(method string, args map[string]any) (int, any) {
	switch method {
	case "devices":
		names := make([]string, 0, len(f.infos))
		for n := range f.infos {
			names = append(names, n)
		}
		return 0, map[string]any{"devices": names}
	case "info":
		dev, _ := args["device"].(string)
		if info, ok := f.infos[dev]; ok {
			return 0, info
		}
		return 4, nil
	case "scan":
		return 0, map[string]any{"results": f.scan}
	}
	return 3, nil
}

func (f *fakeNode) uci(method string, args map[string]any) (int, any) {
	config, _ := args["config"].(string)
	if config != "wireless" {
		return 4, nil
	}
	switch method {
	case "get":
		values := map[string]any{}
		for k, v := range f.wireless {
			values[k] = v
		}
		return 0, map[string]any{"values": values}
	case "add":
		name, _ := args["name"].(string)
		if name == "" {
			name = "cfg_generated"
		}
		sec := map[string]any{".type": args["type"], ".name": name}
		if vals, ok := args["values"].(map[string]any); ok {
			for k, v := range vals {
				sec[k] = v
			}
		}
		f.wireless[name] = sec
		return 0, map[string]any{"section": name}
	case "set":
		section, _ := args["section"].(string)
		sec, ok := f.wireless[section]
		if !ok {
			return 4, nil
		}
		if vals, ok := args["values"].(map[string]any); ok {
			for k, v := range vals {
				sec[k] = v
			}
		}
		return 0, nil
	case "delete":
		section, _ := args["section"].(string)
		delete(f.wireless, section)
		return 0, nil
	case "commit":
		return 0, nil
	}
	return 3, nil
}

func newTestBackend(t *testing.T, f *fakeNode) *Backend {
	t.Helper()
	srv := f.server()
	t.Cleanup(srv.Close)
	b, err := New(Options{Endpoint: srv.URL, Username: "root", Password: "secret", HTTP: srv.Client()})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestInterfaces(t *testing.T) {
	f := newFakeNode()
	b := newTestBackend(t, f)

	its, err := b.Interfaces(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(its) != 1 || its[0].Name != "wlan0" || its[0].Mac != "aa:bb:cc:dd:ee:00" {
		t.Fatalf("interfaces = %+v", its)
	}
	if f.loginN != 1 {
		t.Errorf("expected exactly one login, got %d", f.loginN)
	}
}

func TestScan(t *testing.T) {
	b := newTestBackend(t, newFakeNode())

	aps, err := b.Scan(context.Background(), "wlan0")
	if err != nil {
		t.Fatal(err)
	}
	if len(aps) != 2 {
		t.Fatalf("scan returned %d APs, want 2", len(aps))
	}
	if aps[0].SSID != "HomeNet" || aps[0].BSSID != "11:22:33:44:55:66" || aps[0].Signal != 64 {
		t.Errorf("ap0 = %+v (want signal 45/70=64)", aps[0])
	}
	if len(aps[0].KeyMgmt) != 1 || aps[0].KeyMgmt[0] != wnet.KeyWPAPSK {
		t.Errorf("ap0 keymgmt = %v, want [WPAPSK]", aps[0].KeyMgmt)
	}
	if len(aps[1].KeyMgmt) != 1 || aps[1].KeyMgmt[0] != wnet.KeyNone {
		t.Errorf("ap1 (open) keymgmt = %v, want [None]", aps[1].KeyMgmt)
	}
}

func TestAddAndListProfile(t *testing.T) {
	f := newFakeNode()
	b := newTestBackend(t, f)
	ctx := context.Background()

	prof, err := b.AddProfile(ctx, wnet.ProfileSpec{
		SSID:        "HomeNet",
		Autoconnect: true,
		Security:    wnet.Security{Kind: wnet.SecPSK, Passphrase: "hunter2"},
	})
	if err != nil {
		t.Fatalf("AddProfile: %v", err)
	}
	if prof.SSID != "HomeNet" || prof.Security.Kind != wnet.SecPSK || !prof.Autoconnect {
		t.Fatalf("added profile = %+v", prof)
	}

	// The station section was written with the secret and correct encryption.
	var stored map[string]any
	for _, sec := range f.wireless {
		if sec["mode"] == "sta" {
			stored = sec
		}
	}
	if stored == nil {
		t.Fatal("no station section written")
	}
	if stored["ssid"] != "HomeNet" || stored["key"] != "hunter2" || stored["encryption"] != "psk2" ||
		stored["device"] != "radio0" || stored["network"] != "wwan" {
		t.Errorf("stored station = %+v", stored)
	}

	// It comes back through the list keyed by a stable id.
	profs, err := b.Profiles(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(profs) != 1 || profs[0].ID != prof.ID {
		t.Fatalf("profiles = %+v", profs)
	}

	// Deleting it removes the section.
	if err := b.DeleteProfile(ctx, prof.ID); err != nil {
		t.Fatalf("DeleteProfile: %v", err)
	}
	if profs, _ := b.Profiles(ctx); len(profs) != 0 {
		t.Fatalf("profile survived delete: %+v", profs)
	}
}

func TestEnterpriseAndStaticIPUnsupported(t *testing.T) {
	b := newTestBackend(t, newFakeNode())
	ctx := context.Background()

	_, err := b.AddProfile(ctx, wnet.ProfileSpec{SSID: "Corp", Security: wnet.Security{Kind: wnet.SecEnterprise}})
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Errorf("enterprise: want unsupported error, got %v", err)
	}
	_, err = b.AddProfile(ctx, wnet.ProfileSpec{
		SSID:     "Home",
		Security: wnet.Security{Kind: wnet.SecPSK, Passphrase: "x"},
		IPv4:     &wnet.IPConfig{Method: wnet.IPManual, Addresses: []string{"192.168.1.5/24"}},
	})
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Errorf("static ip: want unsupported error, got %v", err)
	}
}

// TestSessionExpiryRetry checks the client re-authenticates once when a call is
// refused with permission-denied (an expired session) and then succeeds.
func TestSessionExpiryRetry(t *testing.T) {
	f := newFakeNode()
	b := newTestBackend(t, f)

	// First call logs in (loginN=1). Then force the next call to be rejected as
	// expired, which must trigger exactly one transparent re-login.
	if _, err := b.Interfaces(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	f.expireIn = 1
	f.mu.Unlock()

	if _, err := b.Scan(context.Background(), "wlan0"); err != nil {
		t.Fatalf("Scan after expiry: %v", err)
	}
	if f.loginN != 2 {
		t.Errorf("expected 2 logins (initial + one re-login), got %d", f.loginN)
	}
}
