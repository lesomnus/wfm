// Package nmdbus is a wnet.Backend for NetworkManager over its D-Bus API
// (org.freedesktop.NetworkManager), using godbus directly (no nmcli, no libnm,
// no fork). It is the low-overhead replacement for the nmcli backend: typed
// values instead of parsed terse output, and a signal-driven Watch instead of
// polling.
package nmdbus

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/lesomnus/wfm/internal/wnet"
)

const (
	nmService      = "org.freedesktop.NetworkManager"
	nmPath         = dbus.ObjectPath("/org/freedesktop/NetworkManager")
	nmSettingsPath = dbus.ObjectPath("/org/freedesktop/NetworkManager/Settings")

	iNM           = "org.freedesktop.NetworkManager"
	iDevice       = "org.freedesktop.NetworkManager.Device"
	iWireless     = "org.freedesktop.NetworkManager.Device.Wireless"
	iAP           = "org.freedesktop.NetworkManager.AccessPoint"
	iSettings     = "org.freedesktop.NetworkManager.Settings"
	iSettingsConn = "org.freedesktop.NetworkManager.Settings.Connection"
	iActive       = "org.freedesktop.NetworkManager.Connection.Active"
	iIP4          = "org.freedesktop.NetworkManager.IP4Config"

	mPropsSet   = "org.freedesktop.DBus.Properties.Set"
	mPropChange = "org.freedesktop.DBus.Properties.PropertiesChanged"
)

// Backend talks to NetworkManager over the system bus.
type Backend struct {
	conn *dbus.Conn
}

var _ wnet.Backend = (*Backend)(nil)

// New connects to the system bus and returns an NM-backed wnet.Backend.
func New() (*Backend, error) {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return nil, fmt.Errorf("connect system bus: %w", err)
	}
	return &Backend{conn: conn}, nil
}

func (b *Backend) Close() error {
	if b.conn != nil {
		return b.conn.Close()
	}
	return nil
}

// ---- property helpers -------------------------------------------------------

func (b *Backend) prop(path dbus.ObjectPath, iface, name string) (dbus.Variant, error) {
	return b.conn.Object(nmService, path).GetProperty(iface + "." + name)
}

func (b *Backend) propStr(path dbus.ObjectPath, iface, name string) string {
	v, err := b.prop(path, iface, name)
	if err != nil {
		return ""
	}
	s, _ := v.Value().(string)
	return s
}

func (b *Backend) propU32(path dbus.ObjectPath, iface, name string) uint32 {
	v, err := b.prop(path, iface, name)
	if err != nil {
		return 0
	}
	u, _ := v.Value().(uint32)
	return u
}

func (b *Backend) propU8(path dbus.ObjectPath, iface, name string) uint8 {
	v, err := b.prop(path, iface, name)
	if err != nil {
		return 0
	}
	u, _ := v.Value().(uint8)
	return u
}

func (b *Backend) propI64(path dbus.ObjectPath, iface, name string) int64 {
	v, err := b.prop(path, iface, name)
	if err != nil {
		return 0
	}
	x, _ := v.Value().(int64)
	return x
}

func (b *Backend) propBytes(path dbus.ObjectPath, iface, name string) []byte {
	v, err := b.prop(path, iface, name)
	if err != nil {
		return nil
	}
	x, _ := v.Value().([]byte)
	return x
}

func (b *Backend) propPath(path dbus.ObjectPath, iface, name string) dbus.ObjectPath {
	v, err := b.prop(path, iface, name)
	if err != nil {
		return ""
	}
	p, _ := v.Value().(dbus.ObjectPath)
	return p
}

func (b *Backend) propPaths(path dbus.ObjectPath, iface, name string) []dbus.ObjectPath {
	v, err := b.prop(path, iface, name)
	if err != nil {
		return nil
	}
	p, _ := v.Value().([]dbus.ObjectPath)
	return p
}

// ---- interfaces -------------------------------------------------------------

func (b *Backend) devicePaths(ctx context.Context) ([]dbus.ObjectPath, error) {
	var paths []dbus.ObjectPath
	err := b.conn.Object(nmService, nmPath).CallWithContext(ctx, iNM+".GetDevices", 0).Store(&paths)
	return paths, err
}

func (b *Backend) wifiDevicePaths(ctx context.Context) ([]dbus.ObjectPath, error) {
	paths, err := b.devicePaths(ctx)
	if err != nil {
		return nil, err
	}
	out := []dbus.ObjectPath{}
	for _, p := range paths {
		if b.propU32(p, iDevice, "DeviceType") == nmDeviceTypeWifi {
			out = append(out, p)
		}
	}
	return out, nil
}

func (b *Backend) deviceToIface(path dbus.ObjectPath) wnet.Interface {
	name := b.propStr(path, iDevice, "Interface")
	state := b.propU32(path, iDevice, "State")
	return wnet.Interface{
		Name:    name,
		Mac:     strings.ToLower(b.propStr(path, iDevice, "HwAddress")),
		Pci:     wnet.LocalPCI(name),
		Powered: int(state) >= nmStateDisconnected,
		Up:      ifaceUp(name),
		Desc:    stateText(state),
	}
}

func (b *Backend) Interfaces(ctx context.Context) ([]wnet.Interface, error) {
	paths, err := b.wifiDevicePaths(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]wnet.Interface, 0, len(paths))
	for _, p := range paths {
		out = append(out, b.deviceToIface(p))
	}
	return out, nil
}

func (b *Backend) deviceByName(ctx context.Context, name string) (dbus.ObjectPath, error) {
	var p dbus.ObjectPath
	err := b.conn.Object(nmService, nmPath).CallWithContext(ctx, iNM+".GetDeviceByIpIface", 0, name).Store(&p)
	if err != nil {
		return "", fmt.Errorf("%w: interface %s", wnet.ErrNotFound, name)
	}
	return p, nil
}

func (b *Backend) Interface(ctx context.Context, name string) (wnet.Interface, error) {
	p, err := b.deviceByName(ctx, name)
	if err != nil {
		return wnet.Interface{}, err
	}
	return b.deviceToIface(p), nil
}

func (b *Backend) SetPower(ctx context.Context, name string, on bool) (wnet.Interface, error) {
	p, err := b.deviceByName(ctx, name)
	if err != nil {
		return wnet.Interface{}, err
	}
	// NM has no per-device radio toggle; map power to the Managed flag.
	if err := b.conn.Object(nmService, p).CallWithContext(ctx, mPropsSet, 0,
		iDevice, "Managed", dbus.MakeVariant(on)).Err; err != nil {
		return wnet.Interface{}, fmt.Errorf("set power: %w", err)
	}
	// The managed change drives an async state transition; wait briefly so the
	// returned Interface reflects post-change state rather than the old one.
	b.waitPower(ctx, p, on)
	return b.deviceToIface(p), nil
}

// waitPower waits (bounded) for a device's state to cross the managed boundary
// after a SetPower toggle.
func (b *Backend) waitPower(ctx context.Context, dev dbus.ObjectPath, on bool) {
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		powered := int(b.propU32(dev, iDevice, "State")) >= nmStateDisconnected
		if powered == on {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(150 * time.Millisecond):
		}
	}
}

// ---- scan -------------------------------------------------------------------

// waitScan blocks until the device's LastScan advances past before (a fresh scan
// completed) and reports true, or returns false on timeout / cancellation (the
// scan did not run, e.g. throttled or not authorized).
func (b *Backend) waitScan(ctx context.Context, dev dbus.ObjectPath, before int64) bool {
	deadline := time.Now().Add(8 * time.Second)
	for time.Now().Before(deadline) {
		if b.propI64(dev, iWireless, "LastScan") > before {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(300 * time.Millisecond):
		}
	}
	return false
}

func (b *Backend) apToDomain(ap dbus.ObjectPath) wnet.AP {
	return wnet.AP{
		SSID:    string(b.propBytes(ap, iAP, "Ssid")),
		BSSID:   b.propStr(ap, iAP, "HwAddress"),
		Signal:  int(b.propU8(ap, iAP, "Strength")),
		FreqMHz: b.propU32(ap, iAP, "Frequency"),
		KeyMgmt: keyMgmtFromFlags(
			b.propU32(ap, iAP, "Flags"),
			b.propU32(ap, iAP, "WpaFlags"),
			b.propU32(ap, iAP, "RsnFlags"),
		),
	}
}

// scanAttempts bounds how many times Scan re-scans while it sees only the
// associated AP. Some drivers (notably Realtek rtw88 USB adapters) return just
// the connected BSS from a single scan while associated, and NetworkManager's
// cached list can collapse to that lone entry; recovering the neighbourhood
// takes a few scans, the same reason `nmcli device wifi rescan` has to be run by
// hand a couple of times.
const scanAttempts = 4

func (b *Backend) Scan(ctx context.Context, iface string) ([]wnet.AP, error) {
	dev, err := b.deviceByName(ctx, iface)
	if err != nil {
		return nil, err
	}

	var best []wnet.AP
	for attempt := 0; attempt < scanAttempts; attempt++ {
		before := b.propI64(dev, iWireless, "LastScan")
		// RequestScan may be throttled (NotAllowed) or need privileges; ignore the
		// error and read whatever the scan accumulates.
		_ = b.conn.Object(nmService, dev).CallWithContext(ctx, iWireless+".RequestScan", 0, map[string]dbus.Variant{}).Err
		advanced := b.waitScan(ctx, dev, before)

		var aps []dbus.ObjectPath
		if err := b.conn.Object(nmService, dev).CallWithContext(ctx, iWireless+".GetAllAccessPoints", 0).Store(&aps); err != nil {
			return nil, fmt.Errorf("get access points: %w", err)
		}
		out := make([]wnet.AP, 0, len(aps))
		for _, ap := range aps {
			out = append(out, b.apToDomain(ap))
		}
		if len(out) > len(best) {
			best = out
		}

		// Stop once a neighbour (an AP other than the associated one) appears.
		active := b.propPath(dev, iWireless, "ActiveAccessPoint")
		scannable := 0
		for _, ap := range aps {
			if ap != active {
				scannable++
			}
		}
		if scannable > 0 {
			break
		}
		// If the scan did not actually run (throttled / not authorized), retrying
		// will not help, so stop and return what we have.
		if !advanced {
			break
		}
	}
	return best, nil
}

// ---- profiles ---------------------------------------------------------------

func (b *Backend) listConnections(ctx context.Context) ([]dbus.ObjectPath, error) {
	var paths []dbus.ObjectPath
	err := b.conn.Object(nmService, nmSettingsPath).CallWithContext(ctx, iSettings+".ListConnections", 0).Store(&paths)
	return paths, err
}

func (b *Backend) getSettings(ctx context.Context, conn dbus.ObjectPath) (nmSettings, error) {
	var s nmSettings
	err := b.conn.Object(nmService, conn).CallWithContext(ctx, iSettingsConn+".GetSettings", 0).Store(&s)
	return s, err
}

func (b *Backend) getSecrets(ctx context.Context, conn dbus.ObjectPath, name string) (nmSettings, error) {
	var s nmSettings
	err := b.conn.Object(nmService, conn).CallWithContext(ctx, iSettingsConn+".GetSecrets", 0, name).Store(&s)
	return s, err
}

func (b *Backend) connByUUID(ctx context.Context, uuid string) (dbus.ObjectPath, error) {
	var p dbus.ObjectPath
	err := b.conn.Object(nmService, nmSettingsPath).CallWithContext(ctx, iSettings+".GetConnectionByUuid", 0, uuid).Store(&p)
	if err != nil {
		return "", fmt.Errorf("%w: profile %s", wnet.ErrNotFound, uuid)
	}
	return p, nil
}

func (b *Backend) Profiles(ctx context.Context) ([]wnet.Profile, error) {
	paths, err := b.listConnections(ctx)
	if err != nil {
		return nil, err
	}
	out := []wnet.Profile{}
	for _, p := range paths {
		s, err := b.getSettings(ctx, p)
		if err != nil {
			continue
		}
		if sString(s, "connection", "type") != "802-11-wireless" {
			continue
		}
		out = append(out, profileFromSettings(s))
	}
	return out, nil
}

func (b *Backend) Profile(ctx context.Context, id string) (wnet.Profile, error) {
	conn, err := b.connByUUID(ctx, id)
	if err != nil {
		return wnet.Profile{}, err
	}
	s, err := b.getSettings(ctx, conn)
	if err != nil {
		return wnet.Profile{}, err
	}
	return profileFromSettings(s), nil
}

func (b *Backend) AddProfile(ctx context.Context, spec wnet.ProfileSpec) (wnet.Profile, error) {
	if spec.SSID == "" {
		return wnet.Profile{}, fmt.Errorf("ssid is required")
	}
	if spec.Security.Kind == wnet.SecEnterprise {
		return wnet.Profile{}, fmt.Errorf("%w: enterprise security", wnet.ErrUnsupported)
	}
	var path dbus.ObjectPath
	if err := b.conn.Object(nmService, nmSettingsPath).CallWithContext(ctx, iSettings+".AddConnection", 0, buildSettings(spec)).Store(&path); err != nil {
		return wnet.Profile{}, err
	}
	s, err := b.getSettings(ctx, path)
	if err != nil {
		return wnet.Profile{}, err
	}
	return profileFromSettings(s), nil
}

func (b *Backend) PatchProfile(ctx context.Context, id string, patch wnet.ProfilePatch) (wnet.Profile, error) {
	conn, err := b.connByUUID(ctx, id)
	if err != nil {
		return wnet.Profile{}, err
	}
	s, err := b.getSettings(ctx, conn)
	if err != nil {
		return wnet.Profile{}, err
	}
	ensure := func(g string) map[string]dbus.Variant {
		if s[g] == nil {
			s[g] = map[string]dbus.Variant{}
		}
		return s[g]
	}
	if patch.Name != nil {
		ensure("connection")["id"] = dbus.MakeVariant(*patch.Name)
	}
	if patch.SSID != nil {
		ensure("802-11-wireless")["ssid"] = dbus.MakeVariant([]byte(*patch.SSID))
	}
	if patch.Hidden != nil {
		ensure("802-11-wireless")["hidden"] = dbus.MakeVariant(*patch.Hidden)
	}
	if patch.Autoconnect != nil {
		ensure("connection")["autoconnect"] = dbus.MakeVariant(*patch.Autoconnect)
	}
	if patch.Security != nil {
		switch patch.Security.Kind {
		case wnet.SecPSK:
			sec := ensure("802-11-wireless-security")
			sec["key-mgmt"] = dbus.MakeVariant("wpa-psk")
			sec["psk"] = dbus.MakeVariant(patch.Security.Passphrase)
		case wnet.SecOpen:
			delete(s, "802-11-wireless-security")
		case wnet.SecEnterprise:
			return wnet.Profile{}, fmt.Errorf("%w: enterprise security", wnet.ErrUnsupported)
		}
	} else if _, ok := s["802-11-wireless-security"]; ok {
		// Update replaces the whole connection; GetSettings omits secrets, so
		// re-inject the stored psk to avoid wiping it on an unrelated patch.
		if secrets, err := b.getSecrets(ctx, conn, "802-11-wireless-security"); err == nil {
			for k, v := range secrets["802-11-wireless-security"] {
				s["802-11-wireless-security"][k] = v
			}
		}
	}
	if patch.IPv4 != nil {
		s["ipv4"] = ipSection("ipv4", patch.IPv4)
	}
	if patch.IPv6 != nil {
		s["ipv6"] = ipSection("ipv6", patch.IPv6)
	}
	if c, ok := s["connection"]; ok {
		delete(c, "timestamp") // read-only
	}
	if err := b.conn.Object(nmService, conn).CallWithContext(ctx, iSettingsConn+".Update", 0, s).Err; err != nil {
		return wnet.Profile{}, err
	}
	ns, err := b.getSettings(ctx, conn)
	if err != nil {
		return wnet.Profile{}, err
	}
	return profileFromSettings(ns), nil
}

func (b *Backend) DeleteProfile(ctx context.Context, id string) error {
	conn, err := b.connByUUID(ctx, id)
	if err != nil {
		return err
	}
	return b.conn.Object(nmService, conn).CallWithContext(ctx, iSettingsConn+".Delete", 0).Err
}

// ---- connections ------------------------------------------------------------

func (b *Backend) activeConnPaths() []dbus.ObjectPath {
	return b.propPaths(nmPath, iNM, "ActiveConnections")
}

func (b *Backend) activeToDomain(ac dbus.ObjectPath) wnet.Active {
	uuid := b.propStr(ac, iActive, "Uuid")
	a := wnet.Active{ID: uuid, ProfileID: uuid}
	devs := b.propPaths(ac, iActive, "Devices")
	if len(devs) > 0 {
		a.Iface = b.propStr(devs[0], iDevice, "Interface")
		if ap := b.propPath(devs[0], iWireless, "ActiveAccessPoint"); ap != "" && ap != "/" {
			a.SSID = string(b.propBytes(ap, iAP, "Ssid"))
			a.BSSID = b.propStr(ap, iAP, "HwAddress")
		}
	}
	return a
}

func (b *Backend) findAP(ctx context.Context, dev dbus.ObjectPath, bssid string) (dbus.ObjectPath, bool) {
	var aps []dbus.ObjectPath
	if err := b.conn.Object(nmService, dev).CallWithContext(ctx, iWireless+".GetAllAccessPoints", 0).Store(&aps); err != nil {
		return "", false
	}
	for _, ap := range aps {
		if strings.EqualFold(b.propStr(ap, iAP, "HwAddress"), bssid) {
			return ap, true
		}
	}
	return "", false
}

func (b *Backend) waitActivated(ctx context.Context, dev dbus.ObjectPath) error {
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) {
		switch int(b.propU32(dev, iDevice, "State")) {
		case nmStateActivated:
			return nil
		case nmStateFailed:
			return fmt.Errorf("activation failed: %s", stateName(b.propU32(dev, iDevice, "State")))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
	return fmt.Errorf("activation timed out")
}

func (b *Backend) connectionByUUID(uuid string) (wnet.Active, error) {
	for _, ac := range b.activeConnPaths() {
		if b.propStr(ac, iActive, "Uuid") == uuid {
			return b.activeToDomain(ac), nil
		}
	}
	return wnet.Active{}, fmt.Errorf("%w: connection %s", wnet.ErrNotFound, uuid)
}

func (b *Backend) Activate(ctx context.Context, iface, profileID, bssid string) (wnet.Active, error) {
	conn, err := b.connByUUID(ctx, profileID)
	if err != nil {
		return wnet.Active{}, err
	}
	dev, err := b.deviceByName(ctx, iface)
	if err != nil {
		return wnet.Active{}, err
	}
	specific := dbus.ObjectPath("/")
	if bssid != "" {
		if ap, ok := b.findAP(ctx, dev, bssid); ok {
			specific = ap
		}
	}
	var active dbus.ObjectPath
	if err := b.conn.Object(nmService, nmPath).CallWithContext(ctx, iNM+".ActivateConnection", 0, conn, dev, specific).Store(&active); err != nil {
		return wnet.Active{}, fmt.Errorf("activate: %w", err)
	}
	if err := b.waitActivated(ctx, dev); err != nil {
		return wnet.Active{}, err
	}
	return b.connectionByUUID(profileID)
}

func (b *Backend) Deactivate(ctx context.Context, connID string) error {
	for _, ac := range b.activeConnPaths() {
		if b.propStr(ac, iActive, "Uuid") == connID {
			return b.conn.Object(nmService, nmPath).CallWithContext(ctx, iNM+".DeactivateConnection", 0, ac).Err
		}
	}
	return fmt.Errorf("%w: connection %s", wnet.ErrNotFound, connID)
}

func (b *Backend) Connections(ctx context.Context) ([]wnet.Active, error) {
	out := []wnet.Active{}
	for _, ac := range b.activeConnPaths() {
		if b.propStr(ac, iActive, "Type") != "802-11-wireless" {
			continue
		}
		out = append(out, b.activeToDomain(ac))
	}
	return out, nil
}

func (b *Backend) Connection(ctx context.Context, connID string) (wnet.Active, error) {
	return b.connectionByUUID(connID)
}

// ---- status / watch ---------------------------------------------------------

func (b *Backend) deviceForUUID(connID string) (dbus.ObjectPath, error) {
	for _, ac := range b.activeConnPaths() {
		if b.propStr(ac, iActive, "Uuid") == connID {
			if devs := b.propPaths(ac, iActive, "Devices"); len(devs) > 0 {
				return devs[0], nil
			}
		}
	}
	return "", fmt.Errorf("%w: connection %s", wnet.ErrNotFound, connID)
}

func (b *Backend) stateReason(dev dbus.ObjectPath) uint32 {
	v, err := b.prop(dev, iDevice, "StateReason")
	if err != nil {
		return 0
	}
	// StateReason is (uu) = (state, reason); decoded as []interface{}.
	if arr, ok := v.Value().([]interface{}); ok && len(arr) == 2 {
		if r, ok := arr[1].(uint32); ok {
			return r
		}
	}
	return 0
}

func (b *Backend) readIP4(ip4 dbus.ObjectPath) (addrs []string, gw string, dns []string) {
	if v, err := b.prop(ip4, iIP4, "AddressData"); err == nil {
		if ad, ok := v.Value().([]map[string]dbus.Variant); ok {
			for _, e := range ad {
				a, _ := e["address"].Value().(string)
				p, _ := e["prefix"].Value().(uint32)
				if a != "" {
					addrs = append(addrs, fmt.Sprintf("%s/%d", a, p))
				}
			}
		}
	}
	gw = b.propStr(ip4, iIP4, "Gateway")
	if v, err := b.prop(ip4, iIP4, "NameserverData"); err == nil {
		if nd, ok := v.Value().([]map[string]dbus.Variant); ok {
			for _, e := range nd {
				if a, _ := e["address"].Value().(string); a != "" {
					dns = append(dns, a)
				}
			}
		}
	}
	return
}

func (b *Backend) buildStatus(dev dbus.ObjectPath) wnet.Status {
	state := b.propU32(dev, iDevice, "State")
	st := wnet.Status{State: mapDeviceState(int(state))}
	if st.State == wnet.StateFailed {
		if b.stateReason(dev) == nmReasonNoSecrets {
			st.Error = wnet.ErrCAuthFailed
		} else {
			st.Error = wnet.ErrCUnknown
		}
		st.Detail = stateText(state)
	}
	if ap := b.propPath(dev, iWireless, "ActiveAccessPoint"); ap != "" && ap != "/" {
		st.BSSID = b.propStr(ap, iAP, "HwAddress")
		st.Signal = int(b.propU8(ap, iAP, "Strength"))
	}
	if ip4 := b.propPath(dev, iDevice, "Ip4Config"); ip4 != "" && ip4 != "/" {
		st.Addresses, st.Gateway, st.DNS = b.readIP4(ip4)
	}
	return st
}

func (b *Backend) Status(ctx context.Context, connID string) (wnet.Status, error) {
	dev, err := b.deviceForUUID(connID)
	if err != nil {
		return wnet.Status{}, err
	}
	return b.buildStatus(dev), nil
}

func (b *Backend) Watch(ctx context.Context, connID string) (<-chan wnet.Status, error) {
	dev, err := b.deviceForUUID(connID)
	if err != nil {
		return nil, err
	}
	match := []dbus.MatchOption{
		dbus.WithMatchInterface(iDevice),
		dbus.WithMatchMember("StateChanged"),
		dbus.WithMatchObjectPath(dev),
	}
	if err := b.conn.AddMatchSignal(match...); err != nil {
		return nil, err
	}
	sigCh := make(chan *dbus.Signal, 16)
	b.conn.Signal(sigCh)

	out := make(chan wnet.Status, 1)
	go func() {
		defer close(out)
		defer b.conn.RemoveSignal(sigCh)
		defer b.conn.RemoveMatchSignal(match...)

		last := wnet.ConnState(-1)
		emit := func() bool {
			st := b.buildStatus(dev)
			if st.State == last {
				return false
			}
			last = st.State
			select {
			case out <- st:
			case <-ctx.Done():
			}
			return st.State == wnet.StateConnected || st.State == wnet.StateFailed
		}
		if emit() {
			return
		}
		for {
			select {
			case <-ctx.Done():
				return
			case sig, ok := <-sigCh:
				if !ok {
					return
				}
				if sig.Path != dev || sig.Name != iDevice+".StateChanged" {
					continue
				}
				if emit() {
					return
				}
			}
		}
	}()
	return out, nil
}
