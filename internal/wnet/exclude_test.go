package wnet

import (
	"context"
	"errors"
	"testing"
)

// fakeBackend implements the interface-scoped Backend methods over an in-memory
// map. Because exclusion resolves mac/pci by asking the backend, each Interface
// carries the neutral attributes a real backend would report.
type fakeBackend struct {
	Backend
	ifaces  map[string]Interface
	actives map[string]Active // connID -> active
}

func (f *fakeBackend) Interfaces(context.Context) ([]Interface, error) {
	out := make([]Interface, 0, len(f.ifaces))
	for _, it := range f.ifaces {
		out = append(out, it)
	}
	return out, nil
}

func (f *fakeBackend) Interface(_ context.Context, name string) (Interface, error) {
	if it, ok := f.ifaces[name]; ok {
		return it, nil
	}
	return Interface{}, ErrNotFound
}

func (f *fakeBackend) SetPower(_ context.Context, name string, _ bool) (Interface, error) {
	return f.Interface(context.Background(), name)
}

func (f *fakeBackend) Scan(_ context.Context, iface string) ([]AP, error) {
	if _, ok := f.ifaces[iface]; !ok {
		return nil, ErrNotFound
	}
	return nil, nil
}

func (f *fakeBackend) Connections(context.Context) ([]Active, error) {
	out := make([]Active, 0, len(f.actives))
	for _, a := range f.actives {
		out = append(out, a)
	}
	return out, nil
}

func (f *fakeBackend) Connection(_ context.Context, connID string) (Active, error) {
	if a, ok := f.actives[connID]; ok {
		return a, nil
	}
	return Active{}, ErrNotFound
}

func (f *fakeBackend) Deactivate(_ context.Context, connID string) error {
	if _, ok := f.actives[connID]; !ok {
		return ErrNotFound
	}
	return nil
}

func TestExcludeByName(t *testing.T) {
	fb := &fakeBackend{ifaces: map[string]Interface{
		"wlan0": {Name: "wlan0"},
		"wlan1": {Name: "wlan1"},
	}}
	b := WithExcluded(fb, []ExcludeRule{{Name: "wlan1"}})

	its, err := b.Interfaces(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(its) != 1 || its[0].Name != "wlan0" {
		t.Fatalf("expected only wlan0, got %+v", its)
	}

	if _, err := b.Interface(context.Background(), "wlan1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("excluded Interface: want ErrNotFound, got %v", err)
	}
	if _, err := b.Scan(context.Background(), "wlan1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("excluded Scan: want ErrNotFound, got %v", err)
	}
	if _, err := b.SetPower(context.Background(), "wlan1", true); !errors.Is(err, ErrNotFound) {
		t.Fatalf("excluded SetPower: want ErrNotFound, got %v", err)
	}
	if _, err := b.Interface(context.Background(), "wlan0"); err != nil {
		t.Fatalf("visible Interface: %v", err)
	}
}

// TestExcludeByMacAndPci checks that mac/pci rules match on the neutral
// attributes the backend reports — including on the name-only paths, which
// resolve the interface through the backend rather than reading the host.
func TestExcludeByMacAndPci(t *testing.T) {
	fb := &fakeBackend{ifaces: map[string]Interface{
		"wlan0": {Name: "wlan0", Mac: "AA:BB:CC:DD:EE:FF"},
		"wlan1": {Name: "wlan1", Mac: "11:22:33:44:55:66"},
		"wlan2": {Name: "wlan2", Pci: "0000:02:00.0"},
	}}
	b := WithExcluded(fb, []ExcludeRule{
		{Mac: "aabbccddeeff"}, // matches wlan0 (separator-insensitive)
		{Pci: "02:00.0"},      // matches wlan2 (domain-less)
	})

	its, err := b.Interfaces(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, it := range its {
		got[it.Name] = true
	}
	if !got["wlan1"] || got["wlan0"] || got["wlan2"] {
		t.Fatalf("expected only wlan1 visible, got %+v", got)
	}

	// Name-only paths must reach the same verdict by resolving through the
	// backend, not by reading local sysfs.
	if _, err := b.Scan(context.Background(), "wlan0"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("mac-excluded Scan: want ErrNotFound, got %v", err)
	}
	if _, err := b.Interface(context.Background(), "wlan2"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("pci-excluded Interface: want ErrNotFound, got %v", err)
	}
	if _, err := b.Scan(context.Background(), "wlan1"); err != nil {
		t.Fatalf("visible Scan: %v", err)
	}
}

func TestExcludeConnections(t *testing.T) {
	fb := &fakeBackend{
		ifaces: map[string]Interface{"wlan0": {Name: "wlan0"}, "wlan1": {Name: "wlan1"}},
		actives: map[string]Active{
			"c0": {ID: "c0", Iface: "wlan0"},
			"c1": {ID: "c1", Iface: "wlan1"}, // on excluded iface
		},
	}
	b := WithExcluded(fb, []ExcludeRule{{Name: "wlan1"}})

	cs, err := b.Connections(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 1 || cs[0].ID != "c0" {
		t.Fatalf("expected only c0, got %+v", cs)
	}

	if _, err := b.Connection(context.Background(), "c1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("excluded Connection: want ErrNotFound, got %v", err)
	}
	if err := b.Deactivate(context.Background(), "c1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("excluded Deactivate: want ErrNotFound, got %v", err)
	}
	if _, err := b.Connection(context.Background(), "c0"); err != nil {
		t.Fatalf("visible Connection: %v", err)
	}
}

func TestExcludeRuleAndSemantics(t *testing.T) {
	// A rule pinning both name and mac matches only when both agree.
	fb := &fakeBackend{ifaces: map[string]Interface{
		"wlan0": {Name: "wlan0", Mac: "aa:bb:cc:dd:ee:ff"},
	}}
	b := WithExcluded(fb, []ExcludeRule{{Name: "wlan0", Mac: "00:00:00:00:00:00"}})
	if _, err := b.Interface(context.Background(), "wlan0"); err != nil {
		t.Fatalf("name matches but mac does not: want visible, got %v", err)
	}
}

func TestExcludeNoRules(t *testing.T) {
	fb := &fakeBackend{ifaces: map[string]Interface{"wlan0": {Name: "wlan0"}}}
	// Empty and all-blank rules compile away; the backend is returned unwrapped.
	if b := WithExcluded(fb, []ExcludeRule{{}}); b != Backend(fb) {
		t.Fatalf("empty rule set should not wrap the backend")
	}
}

func TestExcludeRuleValidate(t *testing.T) {
	for _, tc := range []struct {
		rule ExcludeRule
		ok   bool
	}{
		{ExcludeRule{Name: "wlan0"}, true},
		{ExcludeRule{Mac: "aa:bb:cc:dd:ee:ff"}, true},
		{ExcludeRule{Pci: "0000:02:00.0"}, true},
		{ExcludeRule{}, false},
		{ExcludeRule{Mac: "not-a-mac"}, false},
		{ExcludeRule{Mac: "aabbccddee"}, false}, // too short
	} {
		err := tc.rule.Validate()
		if tc.ok && err != nil {
			t.Errorf("%+v: want ok, got %v", tc.rule, err)
		}
		if !tc.ok && err == nil {
			t.Errorf("%+v: want error", tc.rule)
		}
	}
}
