// Package nmcli is a wnet.Backend built on top of the `nmcli` command line
// tool. nm.go holds the low-level nmcli wrappers (devices, scanning, connection
// profiles and active connections); backend.go adapts them to wnet types.
//
// The process is expected to run with privileges (e.g. launched via sudo) so
// that mutating operations such as `connection add` succeed.
package nmcli

import (
	"bufio"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// NetworkManager device state codes (subset) as reported by
// `nmcli -f GENERAL.STATE device show`.
const (
	StateUnmanaged    = 10
	StateUnavailable  = 20
	StateDisconnected = 30
	StatePrepare      = 40
	StateConfig       = 50
	StateNeedAuth     = 60
	StateIPConfig     = 70
	StateIPCheck      = 80
	StateSecondaries  = 90
	StateActivated    = 100
	StateDeactivating = 110
	StateFailed       = 120
)

// Run executes nmcli with the given args and returns its stdout.
func Run(ctx context.Context, args ...string) (string, error) {
	return run(ctx, "nmcli", args...)
}

func run(ctx context.Context, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	var out, errb strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(errb.String())
		if msg == "" {
			msg = strings.TrimSpace(out.String())
		}
		return "", fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, msg)
	}
	return out.String(), nil
}

// splitTerse splits one line of nmcli `-t` table output on unescaped ':' and
// unescapes the '\:' / '\\' sequences nmcli uses inside values (e.g. BSSID).
func splitTerse(line string) []string {
	fields := []string{}
	var b strings.Builder
	for i := 0; i < len(line); i++ {
		c := line[i]
		switch {
		case c == '\\' && i+1 < len(line):
			b.WriteByte(line[i+1])
			i++
		case c == ':':
			fields = append(fields, b.String())
			b.Reset()
		default:
			b.WriteByte(c)
		}
	}
	fields = append(fields, b.String())
	return fields
}

// parseDetail parses single-object nmcli output (`field:value`, one per line).
// It splits on the first ':' only, so values containing ':' (MAC, IPv6) are
// preserved. Keys keep any '[n]' multi-value suffix.
func parseDetail(out string) [][2]string {
	pairs := [][2]string{}
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if i := strings.IndexByte(line, ':'); i >= 0 {
			pairs = append(pairs, [2]string{line[:i], line[i+1:]})
		}
	}
	return pairs
}

func detailGet(pairs [][2]string, key string) string {
	for _, p := range pairs {
		if p[0] == key {
			return p[1]
		}
	}
	return ""
}

// detailPrefix collects values whose key equals base or starts with `base[`.
func detailPrefix(pairs [][2]string, base string) []string {
	vs := []string{}
	for _, p := range pairs {
		if p[0] == base || strings.HasPrefix(p[0], base+"[") {
			if p[1] != "" {
				vs = append(vs, p[1])
			}
		}
	}
	return vs
}

func stateCode(s string) int {
	f := strings.Fields(s) // e.g. "100 (connected)"
	if len(f) == 0 {
		return -1
	}
	n, err := strconv.Atoi(f[0])
	if err != nil {
		return -1
	}
	return n
}

func atoi(s string) int {
	n, _ := strconv.Atoi(strings.TrimSpace(s))
	return n
}

// Device is a network device as seen by NetworkManager.
type Device struct {
	Name      string
	Type      string
	StateCode int
	StateText string // raw, e.g. "100 (connected)"
	Reason    string // raw GENERAL.REASON
	Mac       string
	ConUUID   string // active connection UUID, if any
	Up        bool   // link IFF_UP
}

// WifiDeviceNames returns the names of station (non-p2p) wifi devices.
func WifiDeviceNames(ctx context.Context) ([]string, error) {
	out, err := Run(ctx, "-t", "-f", "DEVICE,TYPE", "device", "status")
	if err != nil {
		return nil, err
	}
	names := []string{}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		f := splitTerse(sc.Text())
		if len(f) >= 2 && f[1] == "wifi" {
			names = append(names, f[0])
		}
	}
	return names, nil
}

// DeviceInfo returns details for a single device.
func DeviceInfo(ctx context.Context, name string) (Device, error) {
	out, err := Run(ctx, "-t", "-f",
		"GENERAL.DEVICE,GENERAL.TYPE,GENERAL.HWADDR,GENERAL.STATE,GENERAL.REASON,GENERAL.CON-UUID",
		"device", "show", name)
	if err != nil {
		return Device{}, err
	}
	pairs := parseDetail(out)
	d := Device{
		Name:      detailGet(pairs, "GENERAL.DEVICE"),
		Type:      detailGet(pairs, "GENERAL.TYPE"),
		Mac:       detailGet(pairs, "GENERAL.HWADDR"),
		StateText: detailGet(pairs, "GENERAL.STATE"),
		Reason:    detailGet(pairs, "GENERAL.REASON"),
		ConUUID:   detailGet(pairs, "GENERAL.CON-UUID"),
	}
	d.StateCode = stateCode(d.StateText)
	d.Up = linkUp(ctx, name)
	return d, nil
}

// DeviceIP4 returns the active IPv4 addresses (CIDR), gateway and DNS servers.
func DeviceIP4(ctx context.Context, name string) (addresses []string, gateway string, dns []string) {
	out, err := Run(ctx, "-t", "-f", "IP4.ADDRESS,IP4.GATEWAY,IP4.DNS", "device", "show", name)
	if err != nil {
		return nil, "", nil
	}
	pairs := parseDetail(out)
	return detailPrefix(pairs, "IP4.ADDRESS"), detailGet(pairs, "IP4.GATEWAY"), detailPrefix(pairs, "IP4.DNS")
}

func linkUp(ctx context.Context, name string) bool {
	out, err := run(ctx, "ip", "-br", "link", "show", name)
	if err != nil {
		return false
	}
	f := strings.Fields(out)
	return len(f) >= 2 && f[1] == "UP"
}

// SetPower turns a wireless interface on or off.
//
// A plain `ip link` toggle is fought by NetworkManager (it re-manages and
// re-ups the device), so power is expressed by toggling NM management together
// with the link: off => unmanage + link down; on => link up + manage.
func SetPower(ctx context.Context, name string, on bool) error {
	if on {
		_, _ = run(ctx, "ip", "link", "set", "dev", name, "up") // best effort
		_, err := Run(ctx, "device", "set", name, "managed", "yes")
		return err
	}
	if _, err := Run(ctx, "device", "set", name, "managed", "no"); err != nil {
		return err
	}
	_, err := run(ctx, "ip", "link", "set", "dev", name, "down")
	return err
}

// AP is a scanned access point (BSS).
type AP struct {
	InUse    bool
	SSID     string
	BSSID    string
	Chan     int
	FreqMHz  uint32
	Signal   int    // nmcli signal quality, 0-100
	Security string // raw, e.g. "WPA2", "WPA1 WPA2", "" for open
}

// Scan lists access points visible to the given interface, forcing a rescan.
func Scan(ctx context.Context, ifname string) ([]AP, error) {
	out, err := Run(ctx, "-t", "-f", "IN-USE,SSID,BSSID,CHAN,FREQ,SIGNAL,SECURITY",
		"device", "wifi", "list", "ifname", ifname, "--rescan", "yes")
	if err != nil {
		return nil, err
	}
	aps := []AP{}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		f := splitTerse(sc.Text())
		if len(f) < 7 {
			continue
		}
		freq := uint32(atoi(strings.TrimSuffix(strings.TrimSpace(f[4]), " MHz")))
		aps = append(aps, AP{
			InUse:    f[0] == "*",
			SSID:     f[1],
			BSSID:    f[2],
			Chan:     atoi(f[3]),
			FreqMHz:  freq,
			Signal:   atoi(f[5]),
			Security: strings.TrimSpace(f[6]),
		})
	}
	return aps, nil
}

// CurrentAP returns the access point the interface is currently associated
// with, using cached scan results (no forced rescan). ok is false if none.
func CurrentAP(ctx context.Context, ifname string) (AP, bool) {
	out, err := Run(ctx, "-t", "-f", "IN-USE,SSID,BSSID,CHAN,FREQ,SIGNAL,SECURITY",
		"device", "wifi", "list", "ifname", ifname)
	if err != nil {
		return AP{}, false
	}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		f := splitTerse(sc.Text())
		if len(f) < 7 || f[0] != "*" {
			continue
		}
		return AP{
			InUse:    true,
			SSID:     f[1],
			BSSID:    f[2],
			Chan:     atoi(f[3]),
			FreqMHz:  uint32(atoi(strings.TrimSuffix(strings.TrimSpace(f[4]), " MHz"))),
			Signal:   atoi(f[5]),
			Security: strings.TrimSpace(f[6]),
		}, true
	}
	return AP{}, false
}

// ConnProfile is a saved NetworkManager connection profile.
type ConnProfile struct {
	UUID string
	Name string
	Type string
}

// WifiProfiles returns saved wifi connection profiles.
func WifiProfiles(ctx context.Context) ([]ConnProfile, error) {
	out, err := Run(ctx, "-t", "-f", "UUID,NAME,TYPE", "connection", "show")
	if err != nil {
		return nil, err
	}
	ps := []ConnProfile{}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		f := splitTerse(sc.Text())
		if len(f) >= 3 && f[2] == "802-11-wireless" {
			ps = append(ps, ConnProfile{UUID: f[0], Name: f[1], Type: f[2]})
		}
	}
	return ps, nil
}

// ConnectionDetail returns the requested fields of a connection profile.
func ConnectionDetail(ctx context.Context, uuid string, fields ...string) ([][2]string, error) {
	out, err := Run(ctx, "-t", "-f", strings.Join(fields, ","), "connection", "show", uuid)
	if err != nil {
		return nil, err
	}
	return parseDetail(out), nil
}

// Get is a small helper to read one field from a detail result.
func Get(pairs [][2]string, key string) string { return detailGet(pairs, key) }

// GetList reads a (possibly multi-valued) field from a detail result.
func GetList(pairs [][2]string, base string) []string { return detailPrefix(pairs, base) }

var uuidRe = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}`)

// AddConnection runs `nmcli connection add <args...>` and returns the new UUID.
func AddConnection(ctx context.Context, args ...string) (string, error) {
	out, err := Run(ctx, append([]string{"connection", "add"}, args...)...)
	if err != nil {
		return "", err
	}
	if u := uuidRe.FindString(out); u != "" {
		return u, nil
	}
	return "", fmt.Errorf("could not determine new connection uuid from: %q", strings.TrimSpace(out))
}

// ModifyConnection runs `nmcli connection modify <uuid> <kv...>`.
func ModifyConnection(ctx context.Context, uuid string, kv ...string) error {
	_, err := Run(ctx, append([]string{"connection", "modify", uuid}, kv...)...)
	return err
}

// DeleteConnection deletes a saved profile.
func DeleteConnection(ctx context.Context, uuid string) error {
	_, err := Run(ctx, "connection", "delete", uuid)
	return err
}

// ActivateConnection activates a profile, optionally pinned to ifname.
func ActivateConnection(ctx context.Context, uuid, ifname string, waitSec int) error {
	args := []string{"-w", strconv.Itoa(waitSec), "connection", "up", uuid}
	if ifname != "" {
		args = append(args, "ifname", ifname)
	}
	_, err := Run(ctx, args...)
	return err
}

// DeactivateConnection deactivates an active connection.
func DeactivateConnection(ctx context.Context, uuid string) error {
	_, err := Run(ctx, "connection", "down", uuid)
	return err
}

// Active is an active connection.
type Active struct {
	Name   string
	UUID   string
	Device string
	Type   string
	State  string
}

// ActiveWifiConnections returns active wifi connections.
func ActiveWifiConnections(ctx context.Context) ([]Active, error) {
	out, err := Run(ctx, "-t", "-f", "NAME,UUID,DEVICE,TYPE,STATE", "connection", "show", "--active")
	if err != nil {
		return nil, err
	}
	as := []Active{}
	sc := bufio.NewScanner(strings.NewReader(out))
	for sc.Scan() {
		f := splitTerse(sc.Text())
		if len(f) >= 5 && f[3] == "802-11-wireless" {
			as = append(as, Active{Name: f[0], UUID: f[1], Device: f[2], Type: f[3], State: f[4]})
		}
	}
	return as, nil
}
