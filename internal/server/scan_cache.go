package server

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"time"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/lesomnus/wfm/internal/wifi"
)

// ScanCache caches the most recent access-point scan per interface so repeated
// Scan RPCs within the TTL are served without re-scanning the radio, which is
// slow and can briefly disrupt connectivity. It is shared across every client
// of one server and persisted to a file, so a restarted server keeps warm
// results. It is safe for concurrent use.
type ScanCache struct {
	path string
	ttl  time.Duration

	mu   sync.Mutex
	data *wifi.ScanCache
}

// NewScanCache returns an empty cache backed by path, serving entries younger
// than ttl. Call Load to populate it from disk.
func NewScanCache(path string, ttl time.Duration) *ScanCache {
	return &ScanCache{path: path, ttl: ttl, data: &wifi.ScanCache{}}
}

// Load reads the cache file and drops entries older than the TTL relative to
// now. A missing file is not an error (the cache stays empty); a corrupt file
// is reported.
func (c *ScanCache) Load(now time.Time) error {
	blob, err := os.ReadFile(c.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}

	sc := &wifi.ScanCache{}
	if err := proto.Unmarshal(blob, sc); err != nil {
		return err
	}

	kept := sc.GetInterfaces()[:0]
	for _, e := range sc.GetInterfaces() {
		if c.fresh(e, now) {
			kept = append(kept, e)
		}
	}
	sc.SetInterfaces(kept)

	c.mu.Lock()
	c.data = sc
	c.mu.Unlock()
	return nil
}

// Get returns the cached access points for ifID when a fresh entry exists.
func (c *ScanCache) Get(ifID string, now time.Time) ([]*wifi.AccessPoint, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	e := findScan(c.data, ifID)
	if e == nil || !c.fresh(e, now) {
		return nil, false
	}
	return e.GetAccessPoints(), true
}

// Put records aps as the latest scan for ifID at time at, replacing any previous
// entry for that interface, and persists the whole cache. The in-memory cache is
// updated even if the disk write fails; the write error is returned so the
// caller can log it.
func (c *ScanCache) Put(ifID string, at time.Time, aps []*wifi.AccessPoint) error {
	c.mu.Lock()
	putScan(c.data, ifID, at, aps)
	blob, err := proto.Marshal(c.data)
	c.mu.Unlock()
	if err != nil {
		return err
	}
	return writeFileAtomic(c.path, blob)
}

// fresh reports whether e was scanned within the TTL of now. An entry with no
// timestamp is treated as stale.
func (c *ScanCache) fresh(e *wifi.InterfaceScan, now time.Time) bool {
	ts := e.GetScannedAt()
	return ts != nil && now.Sub(ts.AsTime()) <= c.ttl
}

// findScan returns the entry for ifID, or nil if the cache has none.
func findScan(sc *wifi.ScanCache, ifID string) *wifi.InterfaceScan {
	for _, e := range sc.GetInterfaces() {
		if e.GetInterfaceId() == ifID {
			return e
		}
	}
	return nil
}

// putScan upserts the scan for ifID: it replaces the existing entry or appends a
// new one, so there is at most one entry per interface (latest wins; access
// points that vanished simply drop out).
func putScan(sc *wifi.ScanCache, ifID string, at time.Time, aps []*wifi.AccessPoint) {
	e := &wifi.InterfaceScan{}
	e.SetInterfaceId(ifID)
	e.SetScannedAt(timestamppb.New(at))
	e.SetAccessPoints(aps)

	ifaces := sc.GetInterfaces()
	for i, x := range ifaces {
		if x.GetInterfaceId() == ifID {
			ifaces[i] = e
			sc.SetInterfaces(ifaces)
			return
		}
	}
	sc.SetInterfaces(append(ifaces, e))
}

// writeFileAtomic replaces path with data by writing a temp file in the same
// directory and renaming it, so a crash mid-write cannot corrupt the cache.
func writeFileAtomic(path string, data []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".wfm-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return err
	}
	return nil
}
