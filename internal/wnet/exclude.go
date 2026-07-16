package wnet

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// sysClassNet is the sysfs root where per-interface attributes (hardware
// address, backing device) are read from. It is a variable so tests can point
// resolution at a fixture tree.
var sysClassNet = "/sys/class/net"

// ExcludeRule matches a network interface that must be hidden from the service.
// A rule may pin any combination of Name, Mac and Pci; an interface is matched
// only when it satisfies every field that the rule sets (logical AND), and the
// empty rule (all fields blank) matches nothing. A slice of rules excludes an
// interface matched by any one of them (logical OR).
//
//   - Name is the exact interface name (e.g. "wlan0").
//   - Mac is the hardware address, compared case- and separator-insensitively,
//     so "AA:BB:CC:DD:EE:FF", "aa-bb-cc-dd-ee-ff" and "aabbccddeeff" are equal.
//   - Pci is the PCI address of the backing device (e.g. "0000:02:00.0"); a
//     value given without the domain prefix ("02:00.0") matches as well.
type ExcludeRule struct {
	Name string `yaml:"name,omitempty"`
	Mac  string `yaml:"mac,omitempty"`
	Pci  string `yaml:"pci,omitempty"`
}

// isEmpty reports whether the rule pins no attribute and therefore matches
// nothing.
func (r ExcludeRule) isEmpty() bool {
	return r.Name == "" && r.Mac == "" && r.Pci == ""
}

// Validate rejects a rule that pins nothing or carries a malformed MAC.
func (r ExcludeRule) Validate() error {
	if r.isEmpty() {
		return fmt.Errorf("rule must set at least one of name, mac or pci")
	}
	if r.Mac != "" && len(normalizeMac(r.Mac)) != 12 {
		return fmt.Errorf("invalid mac %q", r.Mac)
	}
	return nil
}

// matches reports whether the rule matches an interface with the given
// attributes. macs holds every known form of the interface's hardware address
// (normalized), since a single interface can present more than one (e.g. the
// backend-reported address and the current sysfs address).
func (r ExcludeRule) matches(name string, macs map[string]bool, pci string) bool {
	matched := false
	if r.Name != "" {
		if r.Name != name {
			return false
		}
		matched = true
	}
	if r.Mac != "" {
		if !macs[normalizeMac(r.Mac)] {
			return false
		}
		matched = true
	}
	if r.Pci != "" {
		if !pciMatch(r.Pci, pci) {
			return false
		}
		matched = true
	}
	return matched
}

// excludeSet is a compiled, ready-to-query form of a rule list. It records
// whether any rule needs the MAC or PCI attribute so name-only rule sets never
// touch sysfs.
type excludeSet struct {
	rules   []ExcludeRule
	needMac bool
	needPci bool
	// resolve returns the sysfs-derived MAC and PCI address of an interface. It
	// is a field so tests can inject a fake without a real sysfs tree.
	resolve func(name string) (mac, pci string)
}

// newExcludeSet compiles rules, dropping empty ones.
func newExcludeSet(rules []ExcludeRule) *excludeSet {
	s := &excludeSet{resolve: resolveSysfs}
	for _, r := range rules {
		if r.isEmpty() {
			continue
		}
		s.rules = append(s.rules, r)
		if r.Mac != "" {
			s.needMac = true
		}
		if r.Pci != "" {
			s.needPci = true
		}
	}
	return s
}

// excluded reports whether the named interface is excluded. knownMac, when
// non-empty, is a hardware address the caller already holds (e.g. from a
// backend listing); it is considered in addition to the sysfs address.
func (s *excludeSet) excluded(name, knownMac string) bool {
	if len(s.rules) == 0 || name == "" {
		return false
	}

	macs := map[string]bool{}
	if m := normalizeMac(knownMac); m != "" {
		macs[m] = true
	}
	var pci string
	if s.needMac || s.needPci {
		mac, p := s.resolve(name)
		if m := normalizeMac(mac); m != "" {
			macs[m] = true
		}
		pci = p
	}

	for _, r := range s.rules {
		if r.matches(name, macs, pci) {
			return true
		}
	}
	return false
}

// resolveSysfs reads an interface's hardware address and PCI address from
// sysfs. Missing files yield empty strings rather than an error: an interface
// without the queried attribute simply cannot match a rule that pins it.
func resolveSysfs(name string) (mac, pci string) {
	if b, err := os.ReadFile(filepath.Join(sysClassNet, name, "address")); err == nil {
		mac = strings.TrimSpace(string(b))
	}
	// /sys/class/net/<name>/device is a symlink into the device tree; its base
	// name is the bus address, which for PCI devices is the domain:bus:dev.func
	// form (e.g. "0000:02:00.0").
	if target, err := os.Readlink(filepath.Join(sysClassNet, name, "device")); err == nil {
		pci = strings.ToLower(filepath.Base(target))
	}
	return mac, pci
}

// normalizeMac reduces a MAC to its 12 lowercase hex digits, discarding any
// separators, so addresses written with colons, dashes or nothing compare
// equal.
func normalizeMac(s string) string {
	var b strings.Builder
	b.Grow(12)
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f':
			b.WriteRune(r)
		case r >= 'A' && r <= 'F':
			b.WriteRune(r + ('a' - 'A'))
		}
	}
	return b.String()
}

// pciMatch reports whether a configured PCI value identifies the interface's
// actual PCI address. A full address matches exactly; a domain-less value
// ("02:00.0") matches the tail of a full address ("0000:02:00.0").
func pciMatch(rule, actual string) bool {
	if actual == "" {
		return false
	}
	rule = strings.ToLower(strings.TrimSpace(rule))
	actual = strings.ToLower(actual)
	if rule == actual {
		return true
	}
	return strings.HasSuffix(actual, ":"+rule)
}

// filteredBackend wraps a Backend and makes excluded interfaces vanish: they
// are dropped from listings and every operation naming one (directly, or via a
// connection bound to one) fails with ErrNotFound, exactly as if the interface
// were absent from the system.
type filteredBackend struct {
	Backend
	ex *excludeSet
}

// WithExcluded returns a Backend that hides the interfaces matched by rules. If
// no rule is effective the original backend is returned unwrapped.
func WithExcluded(b Backend, rules []ExcludeRule) Backend {
	s := newExcludeSet(rules)
	if len(s.rules) == 0 {
		return b
	}
	return &filteredBackend{Backend: b, ex: s}
}

func (f *filteredBackend) Interfaces(ctx context.Context) ([]Interface, error) {
	its, err := f.Backend.Interfaces(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Interface, 0, len(its))
	for _, it := range its {
		if f.ex.excluded(it.Name, it.Mac) {
			continue
		}
		out = append(out, it)
	}
	return out, nil
}

func (f *filteredBackend) Interface(ctx context.Context, name string) (Interface, error) {
	if f.ex.excluded(name, "") {
		return Interface{}, ErrNotFound
	}
	return f.Backend.Interface(ctx, name)
}

func (f *filteredBackend) SetPower(ctx context.Context, name string, on bool) (Interface, error) {
	if f.ex.excluded(name, "") {
		return Interface{}, ErrNotFound
	}
	return f.Backend.SetPower(ctx, name, on)
}

func (f *filteredBackend) Scan(ctx context.Context, iface string) ([]AP, error) {
	if f.ex.excluded(iface, "") {
		return nil, ErrNotFound
	}
	return f.Backend.Scan(ctx, iface)
}

func (f *filteredBackend) Activate(ctx context.Context, iface, profileID, bssid string) (Active, error) {
	if f.ex.excluded(iface, "") {
		return Active{}, ErrNotFound
	}
	return f.Backend.Activate(ctx, iface, profileID, bssid)
}

func (f *filteredBackend) Connections(ctx context.Context) ([]Active, error) {
	as, err := f.Backend.Connections(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Active, 0, len(as))
	for _, a := range as {
		if f.ex.excluded(a.Iface, "") {
			continue
		}
		out = append(out, a)
	}
	return out, nil
}

func (f *filteredBackend) Connection(ctx context.Context, connID string) (Active, error) {
	a, err := f.Backend.Connection(ctx, connID)
	if err != nil {
		return Active{}, err
	}
	if f.ex.excluded(a.Iface, "") {
		return Active{}, ErrNotFound
	}
	return a, nil
}

// visibleConnection resolves a connection id and reports ErrNotFound when its
// interface is excluded, so status/watch/deactivate on a hidden interface's
// connection behave as if the connection did not exist.
func (f *filteredBackend) visibleConnection(ctx context.Context, connID string) error {
	a, err := f.Backend.Connection(ctx, connID)
	if err != nil {
		return err
	}
	if f.ex.excluded(a.Iface, "") {
		return ErrNotFound
	}
	return nil
}

func (f *filteredBackend) Deactivate(ctx context.Context, connID string) error {
	if err := f.visibleConnection(ctx, connID); err != nil {
		return err
	}
	return f.Backend.Deactivate(ctx, connID)
}

func (f *filteredBackend) Status(ctx context.Context, connID string) (Status, error) {
	if err := f.visibleConnection(ctx, connID); err != nil {
		return Status{}, err
	}
	return f.Backend.Status(ctx, connID)
}

func (f *filteredBackend) Watch(ctx context.Context, connID string) (<-chan Status, error) {
	if err := f.visibleConnection(ctx, connID); err != nil {
		return nil, err
	}
	return f.Backend.Watch(ctx, connID)
}
