// Package wnet defines the backend-neutral wifi domain model and the Backend
// interface that the gRPC server depends on. Concrete backends live in
// subpackages (nmcli, nmdbus, iwd) and translate between this domain and a
// particular system wifi stack.
//
// The seam is here, not at the gRPC service interface: proto<->domain mapping,
// request validation and status-code translation live once in internal/server,
// and a backend only has to supply data and actions in these neutral terms.
// Capabilities a backend cannot express (e.g. per-profile static IP on iwd, or
// BSSID-level scan results) are reported as zero values or ErrUnsupported and
// surfaced by the server as codes.Unimplemented.
package wnet

import (
	"context"
	"errors"
)

// ErrUnsupported is returned by a backend for an operation or field it cannot
// express. The server maps it to codes.Unimplemented.
var ErrUnsupported = errors.New("operation not supported by this backend")

// ErrNotFound is returned when a referenced interface, profile or connection
// does not exist. The server maps it to codes.NotFound.
var ErrNotFound = errors.New("not found")

// KeyMgmt is an authentication/key-management method advertised by an AP or
// required by a profile.
type KeyMgmt int

const (
	KeyNone   KeyMgmt = iota // open / no security
	KeyWPAPSK                // WPA/WPA2 personal (pre-shared key)
	KeySAE                   // WPA3 personal
	KeyWPAEAP                // 802.1X / enterprise
	KeyOWE                   // opportunistic wireless encryption (enhanced open)
)

// SecurityKind is the credential type stored in a profile.
type SecurityKind int

const (
	SecOpen       SecurityKind = iota // no credential
	SecPSK                            // pre-shared key (WPA-PSK / SAE)
	SecEnterprise                     // 802.1X / EAP (placeholder)
)

// Security is the credential of a profile. Passphrase is write-only: it is
// supplied on Add/Patch but never read back.
type Security struct {
	Kind       SecurityKind
	Passphrase string
}

// IPMethod selects how an address family is configured.
type IPMethod int

const (
	IPUnspecified IPMethod = iota // leave the backend default
	IPAuto                        // DHCP / SLAAC
	IPManual                      // static (use Addresses/Gateway/DNS)
	IPDisabled                    // family turned off
)

// IPConfig is the per-family IP configuration of a profile.
type IPConfig struct {
	Method    IPMethod
	Addresses []string // CIDR, e.g. "192.168.1.10/24"
	Gateway   string
	DNS       []string
	DNSSearch []string
}

// Interface is a wireless device as seen by the backend.
type Interface struct {
	Name    string // also the stable id
	Mac     string
	Powered bool // managed and at least available (radio on)
	Up      bool // link IFF_UP
	Desc    string
}

// AP is a scanned access point. BSSID may be empty on backends that abstract
// BSS-level details away (iwd).
type AP struct {
	SSID    string
	BSSID   string
	Signal  int // quality 0-100
	FreqMHz uint32
	KeyMgmt []KeyMgmt
}

// Profile is a saved connection profile (NM connection / iwd known network).
type Profile struct {
	ID          string // backend-defined, always a UUID string
	Name        string
	SSID        string
	Hidden      bool
	Autoconnect bool
	Security    Security
	IPv4        IPConfig
	IPv6        IPConfig
}

// ProfileSpec is the input to AddProfile. A nil IPv4/IPv6 leaves the backend
// default for that family.
type ProfileSpec struct {
	Name        string
	SSID        string
	Hidden      bool
	Autoconnect bool
	Security    Security
	IPv4        *IPConfig
	IPv6        *IPConfig
}

// ProfilePatch is the input to PatchProfile. Only non-nil fields are changed.
type ProfilePatch struct {
	Name        *string
	SSID        *string
	Hidden      *bool
	Autoconnect *bool
	Security    *Security
	IPv4        *IPConfig
	IPv6        *IPConfig
}

// Active is an active connection: a profile applied on an interface.
type Active struct {
	ID        string // backend-defined, always a UUID string
	Iface     string // interface name
	ProfileID string
	SSID      string
	BSSID     string
}

// ConnState is the lifecycle state of a connection attempt.
type ConnState int

const (
	StateUnspecified ConnState = iota
	StateIdle
	StateAssociating
	StateAuthenticating
	StateConfiguring
	StateConnected
	StateFailed
	StateDisconnecting
)

// ConnError categorizes a failed connection.
type ConnError int

const (
	ErrCNone ConnError = iota
	ErrCUnknown
	ErrCAuthFailed
)

// Status is a point-in-time snapshot of a connection.
type Status struct {
	State     ConnState
	Error     ConnError
	Detail    string
	BSSID     string
	Signal    int
	Addresses []string
	Gateway   string
	DNS       []string
}

// Backend is the contract every wifi stack adapter implements. Methods take a
// context and must honor its cancellation. ids for Profile and Active are
// opaque UUID strings minted by the backend.
type Backend interface {
	// Interfaces / devices.
	Interfaces(ctx context.Context) ([]Interface, error)
	Interface(ctx context.Context, name string) (Interface, error)
	SetPower(ctx context.Context, name string, on bool) (Interface, error)

	// Scan returns the access points visible to an interface (forces a rescan).
	Scan(ctx context.Context, iface string) ([]AP, error)

	// Profiles (saved settings).
	Profiles(ctx context.Context) ([]Profile, error)
	Profile(ctx context.Context, id string) (Profile, error)
	AddProfile(ctx context.Context, spec ProfileSpec) (Profile, error)
	PatchProfile(ctx context.Context, id string, patch ProfilePatch) (Profile, error)
	DeleteProfile(ctx context.Context, id string) error

	// Connections (active). Activate applies a profile on an interface,
	// optionally pinned to a BSSID, and blocks until the link settles or fails.
	Activate(ctx context.Context, iface, profileID, bssid string) (Active, error)
	Deactivate(ctx context.Context, connID string) error
	Connections(ctx context.Context) ([]Active, error)
	Connection(ctx context.Context, connID string) (Active, error)
	Status(ctx context.Context, connID string) (Status, error)

	// Watch streams status changes for a connection. The channel is closed when
	// the connection reaches a terminal state (connected/failed) or ctx is done.
	Watch(ctx context.Context, connID string) (<-chan Status, error)

	// Close releases backend resources (bus connections etc.).
	Close() error
}
