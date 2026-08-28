package wnet

import (
	"context"
	"fmt"
	"strings"
)

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
//
// Matching runs entirely on the neutral Interface a backend reports (Name, Mac,
// Pci), never on host state read directly, so exclusion behaves identically for
// a local backend (nmcli/nmdbus/iwd) and a remote one (e.g. ubus over HTTP).
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
// attributes. mac must already be normalized (see normalizeMac); an empty mac
// or pci simply fails a rule that pins that field.
func (r ExcludeRule) matches(name, mac, pci string) bool {
	matched := false
	if r.Name != "" {
		if r.Name != name {
			return false
		}
		matched = true
	}
	if r.Mac != "" {
		if normalizeMac(r.Mac) != mac {
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

// excludeSet is a compiled, ready-to-query form of a rule list. needMac/needPci
// record whether any rule pins that attribute, so a name-only rule set never
// forces an interface lookup.
type excludeSet struct {
	rules   []ExcludeRule
	needMac bool
	needPci bool
}

// newExcludeSet compiles rules, dropping empty ones.
func newExcludeSet(rules []ExcludeRule) *excludeSet {
	s := &excludeSet{}
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

// match reports whether any rule matches the given interface attributes.
func (s *excludeSet) match(name, mac, pci string) bool {
	macN := normalizeMac(mac)
	for _, r := range s.rules {
		if r.matches(name, macN, pci) {
			return true
		}
	}
	return false
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

// excludedIface reports whether a fully-resolved interface is excluded, matching
// on the neutral data the backend already supplied.
func (f *filteredBackend) excludedIface(it Interface) bool {
	return f.ex.match(it.Name, it.Mac, it.Pci)
}

// excludedName reports whether the named interface is excluded when only its
// name is known (scan/activate/connection targets). A name-only rule set decides
// without touching the backend; when a rule pins mac or pci, the backend is
// asked for the interface's neutral domain data and the match runs on that. If
// the interface cannot be resolved, only name rules apply — the underlying
// operation will report its absence.
func (f *filteredBackend) excludedName(ctx context.Context, name string) bool {
	if name == "" || len(f.ex.rules) == 0 {
		return false
	}
	if f.ex.match(name, "", "") {
		return true // a name-only rule matches without resolving mac/pci
	}
	if !f.ex.needMac && !f.ex.needPci {
		return false
	}
	it, err := f.Backend.Interface(ctx, name)
	if err != nil {
		return false
	}
	return f.excludedIface(it)
}

func (f *filteredBackend) Interfaces(ctx context.Context) ([]Interface, error) {
	its, err := f.Backend.Interfaces(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Interface, 0, len(its))
	for _, it := range its {
		if f.excludedIface(it) {
			continue
		}
		out = append(out, it)
	}
	return out, nil
}

func (f *filteredBackend) Interface(ctx context.Context, name string) (Interface, error) {
	// A name-only rule rejects without a lookup; otherwise resolve the interface
	// (which we must return anyway) and match on its neutral attributes so mac
	// and pci rules work regardless of backend.
	if f.ex.match(name, "", "") {
		return Interface{}, ErrNotFound
	}
	it, err := f.Backend.Interface(ctx, name)
	if err != nil {
		return Interface{}, err
	}
	if f.excludedIface(it) {
		return Interface{}, ErrNotFound
	}
	return it, nil
}

func (f *filteredBackend) SetPower(ctx context.Context, name string, on bool) (Interface, error) {
	if f.excludedName(ctx, name) {
		return Interface{}, ErrNotFound
	}
	return f.Backend.SetPower(ctx, name, on)
}

func (f *filteredBackend) Scan(ctx context.Context, iface string) ([]AP, error) {
	if f.excludedName(ctx, iface) {
		return nil, ErrNotFound
	}
	return f.Backend.Scan(ctx, iface)
}

func (f *filteredBackend) Activate(ctx context.Context, iface, profileID, bssid string) (Active, error) {
	if f.excludedName(ctx, iface) {
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
		if f.excludedName(ctx, a.Iface) {
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
	if f.excludedName(ctx, a.Iface) {
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
	if f.excludedName(ctx, a.Iface) {
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
