package server

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/lesomnus/wfm/internal/wifi"
	"github.com/lesomnus/wfm/internal/wnet"
)

// fakeBackend implements wnet.Backend; only Scan is exercised. scanCalls counts
// how many times the radio was actually scanned so tests can assert cache hits.
type fakeBackend struct {
	wnet.Backend
	scanCalls int
	aps       []wnet.AP
}

func (b *fakeBackend) Scan(ctx context.Context, iface string) ([]wnet.AP, error) {
	b.scanCalls++
	return b.aps, nil
}

func scanReq(ifname string) *wifi.AccessPointScanRequest {
	req := &wifi.AccessPointScanRequest{}
	ref := &wifi.InterfaceRef{}
	ref.SetId(ifname)
	req.SetInterface(ref)
	return req
}

// TestScanServesFromCache checks the radio is scanned once and the second Scan
// within the TTL is served from the cache.
func TestScanServesFromCache(t *testing.T) {
	b := &fakeBackend{aps: []wnet.AP{{SSID: "home", BSSID: "aa:bb:cc:dd:ee:01", Signal: 70}}}
	c := NewScanCache(filepath.Join(t.TempDir(), ".wfm"), time.Hour)
	s := &AccessPointServer{b: b, cache: c}

	r1, err := s.Scan(context.Background(), scanReq("wlan0"))
	if err != nil {
		t.Fatalf("scan 1: %v", err)
	}
	r2, err := s.Scan(context.Background(), scanReq("wlan0"))
	if err != nil {
		t.Fatalf("scan 2: %v", err)
	}

	if b.scanCalls != 1 {
		t.Errorf("backend scanned %d times, want 1 (second served from cache)", b.scanCalls)
	}
	if len(r1.GetItems()) != 1 || len(r2.GetItems()) != 1 {
		t.Fatalf("items = %d/%d, want 1/1", len(r1.GetItems()), len(r2.GetItems()))
	}
	if r2.GetItems()[0].GetSsid() != "home" {
		t.Errorf("cached ssid = %q, want home", r2.GetItems()[0].GetSsid())
	}
}

// TestScanDifferentInterfaces checks the cache is keyed per interface: each
// interface triggers its own scan.
func TestScanDifferentInterfaces(t *testing.T) {
	b := &fakeBackend{aps: []wnet.AP{{SSID: "x"}}}
	c := NewScanCache(filepath.Join(t.TempDir(), ".wfm"), time.Hour)
	s := &AccessPointServer{b: b, cache: c}

	s.Scan(context.Background(), scanReq("wlan0"))
	s.Scan(context.Background(), scanReq("wlan1"))
	s.Scan(context.Background(), scanReq("wlan0")) // cached

	if b.scanCalls != 2 {
		t.Errorf("backend scanned %d times, want 2 (one per interface)", b.scanCalls)
	}
}

// TestScanNoCache checks that without a cache every request hits the radio.
func TestScanNoCache(t *testing.T) {
	b := &fakeBackend{}
	s := &AccessPointServer{b: b}

	s.Scan(context.Background(), scanReq("wlan0"))
	s.Scan(context.Background(), scanReq("wlan0"))

	if b.scanCalls != 2 {
		t.Errorf("backend scanned %d times, want 2 (no cache)", b.scanCalls)
	}
}

// TestScanCacheFiveSecondWindow locks the intended behavior: a scan is served
// from the cache for 5s after it ran, and a request later than that rescans.
func TestScanCacheFiveSecondWindow(t *testing.T) {
	now := time.Now()
	c := NewScanCache(filepath.Join(t.TempDir(), ".wfm"), 5*time.Second)
	if err := c.Put("wlan0", now, []*wifi.AccessPoint{ap("home", "aa:bb:cc:dd:ee:01", 70)}); err != nil {
		t.Fatalf("put: %v", err)
	}

	if _, ok := c.Get("wlan0", now.Add(5*time.Second)); !ok {
		t.Error("scan within the 5s window should be served from cache")
	}
	if _, ok := c.Get("wlan0", now.Add(5*time.Second+time.Millisecond)); ok {
		t.Error("scan older than 5s should trigger a rescan, not serve cache")
	}
}

// TestScanCacheExpiry checks a cached entry is not served once it is older than
// the TTL, forcing a fresh scan.
func TestScanCacheExpiry(t *testing.T) {
	now := time.Now()
	c := NewScanCache(filepath.Join(t.TempDir(), ".wfm"), time.Minute)
	aps := []*wifi.AccessPoint{ap("home", "aa:bb:cc:dd:ee:01", 70)}
	if err := c.Put("wlan0", now, aps); err != nil {
		t.Fatalf("put: %v", err)
	}

	if _, ok := c.Get("wlan0", now.Add(30*time.Second)); !ok {
		t.Error("entry within TTL should be served")
	}
	if _, ok := c.Get("wlan0", now.Add(2*time.Minute)); ok {
		t.Error("entry past TTL should not be served")
	}
}

// TestScanCachePersists checks a cache written by one instance is loaded by a
// second, with stale entries dropped on load.
func TestScanCachePersists(t *testing.T) {
	now := time.Now()
	path := filepath.Join(t.TempDir(), ".wfm")

	c1 := NewScanCache(path, time.Hour)
	if err := c1.Put("fresh", now, []*wifi.AccessPoint{ap("a", "00:00:00:00:00:01", 50)}); err != nil {
		t.Fatalf("put fresh: %v", err)
	}
	if err := c1.Put("stale", now.Add(-2*time.Hour), []*wifi.AccessPoint{ap("b", "00:00:00:00:00:02", 50)}); err != nil {
		t.Fatalf("put stale: %v", err)
	}

	c2 := NewScanCache(path, time.Hour)
	if err := c2.Load(now); err != nil {
		t.Fatalf("load: %v", err)
	}
	if _, ok := c2.Get("fresh", now); !ok {
		t.Error("fresh entry did not survive reload")
	}
	if _, ok := c2.Get("stale", now); ok {
		t.Error("stale entry should be evicted on load")
	}
}

// TestScanCacheUpsert checks a second scan for an interface replaces the first.
func TestScanCacheUpsert(t *testing.T) {
	now := time.Now()
	c := NewScanCache(filepath.Join(t.TempDir(), ".wfm"), time.Hour)
	c.Put("wlan0", now, []*wifi.AccessPoint{ap("old", "00:00:00:00:00:01", 50)})
	c.Put("wlan0", now, []*wifi.AccessPoint{ap("new", "00:00:00:00:00:02", 60)})

	got, ok := c.Get("wlan0", now)
	if !ok {
		t.Fatal("get after upsert = not ok")
	}
	if len(got) != 1 || got[0].GetSsid() != "new" {
		t.Errorf("upsert did not replace: %d items", len(got))
	}
}

// ap builds an AccessPoint for cache tests.
func ap(ssid, bssid string, sig int32) *wifi.AccessPoint {
	a := &wifi.AccessPoint{}
	a.SetId(bssid)
	a.SetSsid(ssid)
	a.SetBssid(bssid)
	a.SetSignal(sig)
	return a
}
