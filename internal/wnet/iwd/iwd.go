// Package iwd is a wnet.Backend for iwd (iNet Wireless Daemon) over its D-Bus
// API (net.connman.iwd), using godbus directly (no libnm, no fork).
//
// Profiles map to iwd "known network" provisioning files under /var/lib/iwd;
// AddProfile writes a file (iwd watches the directory and picks it up), so a
// connect needs no D-Bus agent for the common PSK case. iwd abstracts BSS-level
// details, so scanned APs carry no BSSID, and connection failures surface as
// the Network.Connect() reply error rather than a distinct state.
//
// iwd does not publish the assigned IP over D-Bus; status reads it from the
// kernel. For iwd to configure IP at all, /etc/iwd/main.conf must set
// [General] EnableNetworkConfiguration=true (see docs).
package iwd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/lesomnus/wfm/internal/wnet"
)

const (
	service = "net.connman.iwd"
	// iwd implements org.freedesktop.DBus.ObjectManager at the bus root "/",
	// not under /net/connman/iwd.
	omPath = dbus.ObjectPath("/")

	ifaceDevice  = "net.connman.iwd.Device"
	ifaceStation = "net.connman.iwd.Station"
	ifaceNetwork = "net.connman.iwd.Network"
	ifaceDiag    = "net.connman.iwd.StationDiagnostic"

	mObjMgrGet  = "org.freedesktop.DBus.ObjectManager.GetManagedObjects"
	mPropsSet   = "org.freedesktop.DBus.Properties.Set"
	mPropChange = "org.freedesktop.DBus.Properties.PropertiesChanged"

	defaultStore = "/var/lib/iwd"
)

// Backend talks to iwd over the system bus.
type Backend struct {
	conn  *dbus.Conn
	store string
}

var _ wnet.Backend = (*Backend)(nil)

// New connects to the system bus and returns an iwd-backed wnet.Backend.
func New() (*Backend, error) {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return nil, fmt.Errorf("connect system bus: %w", err)
	}
	return &Backend{conn: conn, store: defaultStore}, nil
}

func (b *Backend) Close() error {
	if b.conn != nil {
		return b.conn.Close()
	}
	return nil
}

// objects is the GetManagedObjects shape: path -> interface -> property -> value.
type objects = map[dbus.ObjectPath]map[string]map[string]dbus.Variant

type orderedNetwork struct {
	Path   dbus.ObjectPath
	Signal int16
}

func variantString(v dbus.Variant) string { s, _ := v.Value().(string); return s }
func variantBool(v dbus.Variant) bool     { b, _ := v.Value().(bool); return b }
func variantPath(v dbus.Variant) dbus.ObjectPath {
	p, _ := v.Value().(dbus.ObjectPath)
	return p
}

func mapConnectErr(err error) error {
	var de dbus.Error
	if errors.As(err, &de) {
		return fmt.Errorf("connect: %s", de.Name)
	}
	return fmt.Errorf("connect: %w", err)
}

func (b *Backend) managed(ctx context.Context) (objects, error) {
	var m objects
	err := b.conn.Object(service, omPath).CallWithContext(ctx, mObjMgrGet, 0).Store(&m)
	return m, err
}

// ---- interfaces -------------------------------------------------------------

func deviceToIface(dev map[string]dbus.Variant) wnet.Interface {
	powered := variantBool(dev["Powered"])
	name := variantString(dev["Name"])
	return wnet.Interface{
		Name:    name,
		Mac:     strings.ToLower(variantString(dev["Address"])),
		Pci:     wnet.LocalPCI(name),
		Powered: powered,
		Up:      powered, // iwd Device.Powered == interface UP
	}
}

func (b *Backend) Interfaces(ctx context.Context) ([]wnet.Interface, error) {
	m, err := b.managed(ctx)
	if err != nil {
		return nil, err
	}
	out := []wnet.Interface{}
	for _, ifaces := range m {
		dev, ok := ifaces[ifaceDevice]
		if !ok {
			continue
		}
		it := deviceToIface(dev)
		if st, ok := ifaces[ifaceStation]; ok {
			it.Desc = variantString(st["State"])
		} else {
			it.Desc = variantString(dev["Mode"])
		}
		out = append(out, it)
	}
	return out, nil
}

func (b *Backend) Interface(ctx context.Context, name string) (wnet.Interface, error) {
	m, err := b.managed(ctx)
	if err != nil {
		return wnet.Interface{}, err
	}
	for _, ifaces := range m {
		dev, ok := ifaces[ifaceDevice]
		if !ok || variantString(dev["Name"]) != name {
			continue
		}
		it := deviceToIface(dev)
		if st, ok := ifaces[ifaceStation]; ok {
			it.Desc = variantString(st["State"])
		} else {
			it.Desc = variantString(dev["Mode"])
		}
		return it, nil
	}
	return wnet.Interface{}, fmt.Errorf("%w: interface %s", wnet.ErrNotFound, name)
}

func (b *Backend) devicePath(ctx context.Context, name string) (dbus.ObjectPath, bool) {
	m, err := b.managed(ctx)
	if err != nil {
		return "", false
	}
	for path, ifaces := range m {
		if dev, ok := ifaces[ifaceDevice]; ok && variantString(dev["Name"]) == name {
			return path, true
		}
	}
	return "", false
}

func (b *Backend) SetPower(ctx context.Context, name string, on bool) (wnet.Interface, error) {
	path, ok := b.devicePath(ctx, name)
	if !ok {
		return wnet.Interface{}, fmt.Errorf("%w: interface %s", wnet.ErrNotFound, name)
	}
	err := b.conn.Object(service, path).CallWithContext(ctx, mPropsSet, 0,
		ifaceDevice, "Powered", dbus.MakeVariant(on)).Err
	if err != nil {
		return wnet.Interface{}, fmt.Errorf("set power: %w", err)
	}
	return b.Interface(ctx, name)
}

// ---- scan -------------------------------------------------------------------

func (b *Backend) waitScanDone(ctx context.Context, devPath dbus.ObjectPath) {
	deadline := time.Now().Add(8 * time.Second)
	obj := b.conn.Object(service, devPath)
	for time.Now().Before(deadline) {
		v, err := obj.GetProperty(ifaceStation + ".Scanning")
		if err != nil || !variantBool(v) {
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(300 * time.Millisecond):
		}
	}
}

func (b *Backend) triggerScan(ctx context.Context, devPath dbus.ObjectPath) {
	err := b.conn.Object(service, devPath).CallWithContext(ctx, ifaceStation+".Scan", 0).Err
	// A scan already in progress is fine: just wait for it.
	_ = err
	b.waitScanDone(ctx, devPath)
}

func (b *Backend) stationPath(ctx context.Context, iface string) (dbus.ObjectPath, bool) {
	return b.devicePath(ctx, iface)
}

func (b *Backend) Scan(ctx context.Context, iface string) ([]wnet.AP, error) {
	devPath, ok := b.stationPath(ctx, iface)
	if !ok {
		return nil, fmt.Errorf("%w: interface %s", wnet.ErrNotFound, iface)
	}
	b.triggerScan(ctx, devPath)

	var ordered []orderedNetwork
	if err := b.conn.Object(service, devPath).CallWithContext(ctx, ifaceStation+".GetOrderedNetworks", 0).Store(&ordered); err != nil {
		return nil, fmt.Errorf("ordered networks: %w", err)
	}
	m, err := b.managed(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]wnet.AP, 0, len(ordered))
	for _, n := range ordered {
		ifaces, ok := m[n.Path]
		if !ok {
			continue
		}
		np := ifaces[ifaceNetwork]
		out = append(out, wnet.AP{
			SSID:    variantString(np["Name"]),
			Signal:  signalToQuality(n.Signal),
			KeyMgmt: iwdTypeToKeyMgmt(variantString(np["Type"])),
			// BSSID and FreqMHz are not exposed by iwd at scan time.
		})
	}
	return out, nil
}

func (b *Backend) findNetwork(ctx context.Context, devPath dbus.ObjectPath, ssid, typ string) (dbus.ObjectPath, bool) {
	m, err := b.managed(ctx)
	if err != nil {
		return "", false
	}
	for path, ifaces := range m {
		np, ok := ifaces[ifaceNetwork]
		if !ok || variantPath(np["Device"]) != devPath {
			continue
		}
		if variantString(np["Name"]) != ssid {
			continue
		}
		if typ != "" && variantString(np["Type"]) != typ {
			continue
		}
		return path, true
	}
	return "", false
}

// ---- profiles (known-network files) ----------------------------------------

func (b *Backend) writeProfile(name, content string) error {
	if err := os.MkdirAll(b.store, 0o700); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(b.store, name), []byte(content), 0o600)
}

// findProfileFile locates the provisioning file whose derived id matches.
func (b *Backend) findProfileFile(id string) (name, ssid, typ string, ok bool) {
	entries, err := os.ReadDir(b.store)
	if err != nil {
		return "", "", "", false
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		s, t, valid := parseProfileFilename(e.Name())
		if !valid {
			continue
		}
		if wnet.DeriveID(s, t) == id {
			return e.Name(), s, t, true
		}
	}
	return "", "", "", false
}

func (b *Backend) Profiles(ctx context.Context) ([]wnet.Profile, error) {
	entries, err := os.ReadDir(b.store)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	out := []wnet.Profile{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ssid, typ, ok := parseProfileFilename(e.Name())
		if !ok {
			continue
		}
		data, err := os.ReadFile(filepath.Join(b.store, e.Name()))
		if err != nil {
			continue
		}
		out = append(out, parseProfile(ssid, typ, string(data)))
	}
	return out, nil
}

func (b *Backend) Profile(ctx context.Context, id string) (wnet.Profile, error) {
	name, ssid, typ, ok := b.findProfileFile(id)
	if !ok {
		return wnet.Profile{}, fmt.Errorf("%w: profile %s", wnet.ErrNotFound, id)
	}
	data, err := os.ReadFile(filepath.Join(b.store, name))
	if err != nil {
		return wnet.Profile{}, err
	}
	return parseProfile(ssid, typ, string(data)), nil
}

func (b *Backend) AddProfile(ctx context.Context, spec wnet.ProfileSpec) (wnet.Profile, error) {
	if spec.SSID == "" {
		return wnet.Profile{}, fmt.Errorf("ssid is required")
	}
	if spec.Security.Kind == wnet.SecEnterprise {
		return wnet.Profile{}, fmt.Errorf("%w: enterprise security", wnet.ErrUnsupported)
	}
	typ := secKindToType(spec.Security.Kind)
	content := renderProfile(spec.Security.Kind, spec.Security.Passphrase, spec.Autoconnect, spec.Hidden, spec.IPv4, spec.IPv6)
	if err := b.writeProfile(profileFilename(spec.SSID, typ), content); err != nil {
		return wnet.Profile{}, err
	}
	return parseProfile(spec.SSID, typ, content), nil
}

func (b *Backend) PatchProfile(ctx context.Context, id string, patch wnet.ProfilePatch) (wnet.Profile, error) {
	name, ssid, typ, ok := b.findProfileFile(id)
	if !ok {
		return wnet.Profile{}, fmt.Errorf("%w: profile %s", wnet.ErrNotFound, id)
	}
	data, err := os.ReadFile(filepath.Join(b.store, name))
	if err != nil {
		return wnet.Profile{}, err
	}
	cur := parseProfile(ssid, typ, string(data))
	passphrase := parseINI(string(data))["Security"]["Passphrase"] // preserve secret

	spec := wnet.ProfileSpec{
		SSID:        cur.SSID,
		Hidden:      cur.Hidden,
		Autoconnect: cur.Autoconnect,
		Security:    wnet.Security{Kind: cur.Security.Kind, Passphrase: passphrase},
		IPv4:        &cur.IPv4,
		IPv6:        &cur.IPv6,
	}
	if patch.SSID != nil {
		spec.SSID = *patch.SSID
	}
	if patch.Hidden != nil {
		spec.Hidden = *patch.Hidden
	}
	if patch.Autoconnect != nil {
		spec.Autoconnect = *patch.Autoconnect
	}
	if patch.Security != nil {
		spec.Security = *patch.Security
		// keep the stored passphrase if the patch left it blank for the same kind
		if spec.Security.Passphrase == "" && patch.Security.Kind == cur.Security.Kind {
			spec.Security.Passphrase = passphrase
		}
	}
	if patch.IPv4 != nil {
		spec.IPv4 = patch.IPv4
	}
	if patch.IPv6 != nil {
		spec.IPv6 = patch.IPv6
	}
	if spec.Security.Kind == wnet.SecEnterprise {
		return wnet.Profile{}, fmt.Errorf("%w: enterprise security", wnet.ErrUnsupported)
	}

	newType := secKindToType(spec.Security.Kind)
	newName := profileFilename(spec.SSID, newType)
	content := renderProfile(spec.Security.Kind, spec.Security.Passphrase, spec.Autoconnect, spec.Hidden, spec.IPv4, spec.IPv6)
	if newName != name {
		_ = os.Remove(filepath.Join(b.store, name))
	}
	if err := b.writeProfile(newName, content); err != nil {
		return wnet.Profile{}, err
	}
	return parseProfile(spec.SSID, newType, content), nil
}

func (b *Backend) DeleteProfile(ctx context.Context, id string) error {
	name, _, _, ok := b.findProfileFile(id)
	if !ok {
		return fmt.Errorf("%w: profile %s", wnet.ErrNotFound, id)
	}
	return os.Remove(filepath.Join(b.store, name))
}

// ---- connections ------------------------------------------------------------

// findConnByID locates the station currently connected to the network whose
// (ssid, type) derives to id.
func (b *Backend) findConnByID(m objects, id string) (devPath dbus.ObjectPath, dev, station map[string]dbus.Variant, netPath dbus.ObjectPath, ok bool) {
	for path, ifaces := range m {
		st, has := ifaces[ifaceStation]
		if !has {
			continue
		}
		np := variantPath(st["ConnectedNetwork"])
		if np == "" {
			continue
		}
		netIfaces, has := m[np]
		if !has {
			continue
		}
		net := netIfaces[ifaceNetwork]
		if wnet.DeriveID(variantString(net["Name"]), variantString(net["Type"])) == id {
			return path, ifaces[ifaceDevice], st, np, true
		}
	}
	return "", nil, nil, "", false
}

func (b *Backend) connectedBSS(ctx context.Context, devPath dbus.ObjectPath) string {
	d, err := b.diagnostics(ctx, devPath)
	if err != nil {
		return ""
	}
	return variantString(d["ConnectedBss"])
}

func (b *Backend) connectionByID(ctx context.Context, id string) (wnet.Active, error) {
	m, err := b.managed(ctx)
	if err != nil {
		return wnet.Active{}, err
	}
	devPath, dev, _, netPath, ok := b.findConnByID(m, id)
	if !ok {
		return wnet.Active{}, fmt.Errorf("%w: connection %s", wnet.ErrNotFound, id)
	}
	net := m[netPath][ifaceNetwork]
	return wnet.Active{
		ID:        id,
		Iface:     variantString(dev["Name"]),
		ProfileID: id,
		SSID:      variantString(net["Name"]),
		BSSID:     b.connectedBSS(ctx, devPath),
	}, nil
}

func (b *Backend) Activate(ctx context.Context, iface, profileID, bssid string) (wnet.Active, error) {
	prof, err := b.Profile(ctx, profileID)
	if err != nil {
		return wnet.Active{}, err
	}
	devPath, ok := b.stationPath(ctx, iface)
	if !ok {
		return wnet.Active{}, fmt.Errorf("%w: interface %s", wnet.ErrNotFound, iface)
	}
	// bssid pinning is not supported by iwd (it abstracts BSS/roaming); ignored.
	b.triggerScan(ctx, devPath)

	netPath, ok := b.findNetwork(ctx, devPath, prof.SSID, secKindToType(prof.Security.Kind))
	if !ok {
		if prof.Hidden {
			if err := b.conn.Object(service, devPath).CallWithContext(ctx, ifaceStation+".ConnectHiddenNetwork", 0, prof.SSID).Err; err != nil {
				return wnet.Active{}, mapConnectErr(err)
			}
			return b.connectionByID(ctx, profileID)
		}
		return wnet.Active{}, fmt.Errorf("%w: network %q not in range", wnet.ErrNotFound, prof.SSID)
	}
	if err := b.conn.Object(service, netPath).CallWithContext(ctx, ifaceNetwork+".Connect", 0).Err; err != nil {
		return wnet.Active{}, mapConnectErr(err)
	}
	return b.connectionByID(ctx, profileID)
}

func (b *Backend) Deactivate(ctx context.Context, connID string) error {
	m, err := b.managed(ctx)
	if err != nil {
		return err
	}
	devPath, _, _, _, ok := b.findConnByID(m, connID)
	if !ok {
		return fmt.Errorf("%w: connection %s", wnet.ErrNotFound, connID)
	}
	return b.conn.Object(service, devPath).CallWithContext(ctx, ifaceStation+".Disconnect", 0).Err
}

func (b *Backend) Connections(ctx context.Context) ([]wnet.Active, error) {
	m, err := b.managed(ctx)
	if err != nil {
		return nil, err
	}
	out := []wnet.Active{}
	for devPath, ifaces := range m {
		st, ok := ifaces[ifaceStation]
		if !ok {
			continue
		}
		netPath := variantPath(st["ConnectedNetwork"])
		if netPath == "" {
			continue
		}
		net := m[netPath][ifaceNetwork]
		ssid := variantString(net["Name"])
		id := wnet.DeriveID(ssid, variantString(net["Type"]))
		out = append(out, wnet.Active{
			ID:        id,
			Iface:     variantString(ifaces[ifaceDevice]["Name"]),
			ProfileID: id,
			SSID:      ssid,
			BSSID:     b.connectedBSS(ctx, devPath),
		})
	}
	return out, nil
}

func (b *Backend) Connection(ctx context.Context, connID string) (wnet.Active, error) {
	return b.connectionByID(ctx, connID)
}

// ---- status / watch ---------------------------------------------------------

func (b *Backend) diagnostics(ctx context.Context, devPath dbus.ObjectPath) (map[string]dbus.Variant, error) {
	var d map[string]dbus.Variant
	err := b.conn.Object(service, devPath).CallWithContext(ctx, ifaceDiag+".GetDiagnostics", 0).Store(&d)
	return d, err
}

func (b *Backend) buildStatus(ctx context.Context, devPath dbus.ObjectPath, iface, stateStr string) wnet.Status {
	st := wnet.Status{State: mapState(stateStr)}
	if d, err := b.diagnostics(ctx, devPath); err == nil {
		st.BSSID = variantString(d["ConnectedBss"])
		if rssi, ok := d["RSSI"].Value().(int16); ok {
			st.Signal = dbmToQuality(int(rssi))
		}
	}
	if st.State == wnet.StateConnected {
		st.Addresses = ifaceAddrs(iface)
		st.Gateway = defaultGateway(iface)
		st.DNS = dnsServers()
	}
	return st
}

func (b *Backend) Status(ctx context.Context, connID string) (wnet.Status, error) {
	m, err := b.managed(ctx)
	if err != nil {
		return wnet.Status{}, err
	}
	devPath, dev, station, _, ok := b.findConnByID(m, connID)
	if !ok {
		return wnet.Status{}, fmt.Errorf("%w: connection %s", wnet.ErrNotFound, connID)
	}
	return b.buildStatus(ctx, devPath, variantString(dev["Name"]), variantString(station["State"])), nil
}

func (b *Backend) Watch(ctx context.Context, connID string) (<-chan wnet.Status, error) {
	m, err := b.managed(ctx)
	if err != nil {
		return nil, err
	}
	devPath, dev, station, _, ok := b.findConnByID(m, connID)
	if !ok {
		return nil, fmt.Errorf("%w: connection %s", wnet.ErrNotFound, connID)
	}
	iface := variantString(dev["Name"])

	match := []dbus.MatchOption{
		dbus.WithMatchInterface("org.freedesktop.DBus.Properties"),
		dbus.WithMatchMember("PropertiesChanged"),
		dbus.WithMatchObjectPath(devPath),
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
		sawAssociating := false
		emit := func(stateStr string) bool {
			st := b.buildStatus(ctx, devPath, iface, stateStr)
			if st.State == wnet.StateAssociating {
				sawAssociating = true
			}
			// iwd has no distinct failed state: a connect that was associating
			// and then drops back to idle/disconnected is a failure. Surface it
			// as a terminal StateFailed so the channel closes per the contract.
			if st.State == wnet.StateIdle && sawAssociating {
				st.State = wnet.StateFailed
				st.Error = wnet.ErrCUnknown
			}
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

		if emit(variantString(station["State"])) {
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
				if sig.Path != devPath || sig.Name != mPropChange || len(sig.Body) < 2 {
					continue
				}
				if iname, _ := sig.Body[0].(string); iname != ifaceStation {
					continue
				}
				changed, _ := sig.Body[1].(map[string]dbus.Variant)
				if v, ok := changed["State"]; ok {
					if emit(variantString(v)) {
						return
					}
				}
			}
		}
	}()
	return out, nil
}
