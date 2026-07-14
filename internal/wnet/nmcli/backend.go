package nmcli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/lesomnus/wfm/internal/wnet"
)

// Backend adapts the nmcli wrappers in nm.go to wnet.Backend.
//
// Note: a few interface methods (Scan, SetPower) share a name with a
// package-level helper in nm.go. A method declaration does not shadow a
// package-level identifier, so an unqualified Scan(...) / SetPower(...) inside a
// method body calls the nm.go helper, not the method.
type Backend struct{}

// New returns an nmcli-backed wnet.Backend.
func New() *Backend { return &Backend{} }

var _ wnet.Backend = (*Backend)(nil)

func (b *Backend) Close() error { return nil }

// ---- interfaces -------------------------------------------------------------

func toIface(d Device) wnet.Interface {
	return wnet.Interface{
		Name:    d.Name,
		Mac:     strings.ToLower(d.Mac),
		Powered: d.StateCode >= StateDisconnected,
		Up:      d.Up,
		Desc:    d.StateText,
	}
}

func (b *Backend) Interfaces(ctx context.Context) ([]wnet.Interface, error) {
	names, err := WifiDeviceNames(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]wnet.Interface, 0, len(names))
	for _, n := range names {
		d, err := DeviceInfo(ctx, n)
		if err != nil {
			continue
		}
		out = append(out, toIface(d))
	}
	return out, nil
}

func (b *Backend) Interface(ctx context.Context, name string) (wnet.Interface, error) {
	d, err := DeviceInfo(ctx, name)
	if err != nil {
		return wnet.Interface{}, fmt.Errorf("%w: %v", wnet.ErrNotFound, err)
	}
	return toIface(d), nil
}

func (b *Backend) SetPower(ctx context.Context, name string, on bool) (wnet.Interface, error) {
	if err := SetPower(ctx, name, on); err != nil {
		return wnet.Interface{}, err
	}
	d, err := DeviceInfo(ctx, name)
	if err != nil {
		return wnet.Interface{}, err
	}
	return toIface(d), nil
}

// ---- scan -------------------------------------------------------------------

func parseKeyMgmt(sec string) []wnet.KeyMgmt {
	sec = strings.ToUpper(strings.TrimSpace(sec))
	if sec == "" {
		return []wnet.KeyMgmt{wnet.KeyNone}
	}
	seen := map[wnet.KeyMgmt]bool{}
	out := []wnet.KeyMgmt{}
	add := func(k wnet.KeyMgmt) {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	if strings.Contains(sec, "WPA3") {
		add(wnet.KeySAE)
	}
	if strings.Contains(sec, "802.1X") {
		add(wnet.KeyWPAEAP)
	}
	if strings.Contains(sec, "OWE") {
		add(wnet.KeyOWE)
	}
	if strings.Contains(sec, "WPA2") || strings.Contains(sec, "WPA1") || strings.Contains(sec, "WPA-") || sec == "WPA" {
		add(wnet.KeyWPAPSK)
	}
	if len(out) == 0 {
		add(wnet.KeyWPAPSK) // secured, but unrecognized token
	}
	return out
}

func toAP(ap AP) wnet.AP {
	return wnet.AP{
		SSID:    ap.SSID,
		BSSID:   ap.BSSID,
		Signal:  ap.Signal,
		FreqMHz: ap.FreqMHz,
		KeyMgmt: parseKeyMgmt(ap.Security),
	}
}

func (b *Backend) Scan(ctx context.Context, iface string) ([]wnet.AP, error) {
	aps, err := Scan(ctx, iface) // nm.go helper
	if err != nil {
		return nil, err
	}
	out := make([]wnet.AP, 0, len(aps))
	for _, ap := range aps {
		out = append(out, toAP(ap))
	}
	return out, nil
}

// ---- profiles ---------------------------------------------------------------

var profileFields = []string{
	"connection.id", "connection.autoconnect",
	"802-11-wireless.ssid", "802-11-wireless.hidden",
	"802-11-wireless-security.key-mgmt",
	"ipv4.method", "ipv4.addresses", "ipv4.gateway", "ipv4.dns", "ipv4.dns-search",
	"ipv6.method", "ipv6.addresses", "ipv6.gateway", "ipv6.dns", "ipv6.dns-search",
}

// splitMulti normalizes an nmcli list value into a slice. nmcli may return the
// values either comma-joined on one line or as separate field[n] entries.
func splitMulti(vs []string) []string {
	out := []string{}
	for _, v := range vs {
		for _, part := range strings.Split(v, ",") {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

func ipConfigFromDetail(pairs [][2]string, prefix string) wnet.IPConfig {
	cfg := wnet.IPConfig{}
	switch Get(pairs, prefix+".method") {
	case "auto":
		cfg.Method = wnet.IPAuto
	case "manual":
		cfg.Method = wnet.IPManual
	case "disabled":
		cfg.Method = wnet.IPDisabled
	default:
		cfg.Method = wnet.IPUnspecified
	}
	cfg.Addresses = splitMulti(GetList(pairs, prefix+".addresses"))
	cfg.Gateway = Get(pairs, prefix+".gateway")
	cfg.DNS = splitMulti(GetList(pairs, prefix+".dns"))
	cfg.DNSSearch = splitMulti(GetList(pairs, prefix+".dns-search"))
	return cfg
}

func securityFromKeyMgmt(km string) wnet.Security {
	switch km {
	case "", "none":
		return wnet.Security{Kind: wnet.SecOpen}
	case "wpa-eap":
		return wnet.Security{Kind: wnet.SecEnterprise}
	default: // wpa-psk, sae; passphrase intentionally not read back (secret)
		return wnet.Security{Kind: wnet.SecPSK}
	}
}

func (b *Backend) profileByUUID(ctx context.Context, id string) (wnet.Profile, error) {
	pairs, err := ConnectionDetail(ctx, id, profileFields...)
	if err != nil {
		return wnet.Profile{}, fmt.Errorf("%w: %v", wnet.ErrNotFound, err)
	}
	return wnet.Profile{
		ID:          id,
		Name:        Get(pairs, "connection.id"),
		SSID:        Get(pairs, "802-11-wireless.ssid"),
		Hidden:      Get(pairs, "802-11-wireless.hidden") == "yes",
		Autoconnect: Get(pairs, "connection.autoconnect") == "yes",
		Security:    securityFromKeyMgmt(Get(pairs, "802-11-wireless-security.key-mgmt")),
		IPv4:        ipConfigFromDetail(pairs, "ipv4"),
		IPv6:        ipConfigFromDetail(pairs, "ipv6"),
	}, nil
}

func (b *Backend) Profiles(ctx context.Context) ([]wnet.Profile, error) {
	profs, err := WifiProfiles(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]wnet.Profile, 0, len(profs))
	for _, pr := range profs {
		p, err := b.profileByUUID(ctx, pr.UUID)
		if err != nil {
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

func (b *Backend) Profile(ctx context.Context, id string) (wnet.Profile, error) {
	return b.profileByUUID(ctx, id)
}

func yesno(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

func ipArgs(prefix string, cfg *wnet.IPConfig) []string {
	if cfg == nil {
		return nil
	}
	method := ""
	switch cfg.Method {
	case wnet.IPAuto:
		method = "auto"
	case wnet.IPManual:
		method = "manual"
	case wnet.IPDisabled:
		method = "disabled"
	default:
		return nil // leave NM default
	}
	out := []string{prefix + ".method", method}
	if method == "manual" {
		if len(cfg.Addresses) > 0 {
			out = append(out, prefix+".addresses", strings.Join(cfg.Addresses, ","))
		}
		if cfg.Gateway != "" {
			out = append(out, prefix+".gateway", cfg.Gateway)
		}
		if len(cfg.DNS) > 0 {
			out = append(out, prefix+".dns", strings.Join(cfg.DNS, ","))
		}
		if len(cfg.DNSSearch) > 0 {
			out = append(out, prefix+".dns-search", strings.Join(cfg.DNSSearch, ","))
		}
	}
	return out
}

func (b *Backend) AddProfile(ctx context.Context, spec wnet.ProfileSpec) (wnet.Profile, error) {
	if spec.SSID == "" {
		return wnet.Profile{}, fmt.Errorf("ssid is required")
	}
	conName := spec.Name
	if conName == "" {
		conName = spec.SSID
	}
	args := []string{"type", "wifi", "con-name", conName, "ssid", spec.SSID}
	args = append(args, "802-11-wireless.hidden", yesno(spec.Hidden))
	args = append(args, "connection.autoconnect", yesno(spec.Autoconnect))

	switch spec.Security.Kind {
	case wnet.SecPSK:
		args = append(args, "wifi-sec.key-mgmt", "wpa-psk", "wifi-sec.psk", spec.Security.Passphrase)
	case wnet.SecEnterprise:
		return wnet.Profile{}, fmt.Errorf("%w: enterprise security", wnet.ErrUnsupported)
	}
	args = append(args, ipArgs("ipv4", spec.IPv4)...)
	args = append(args, ipArgs("ipv6", spec.IPv6)...)

	id, err := AddConnection(ctx, args...)
	if err != nil {
		return wnet.Profile{}, err
	}
	return b.profileByUUID(ctx, id)
}

func (b *Backend) PatchProfile(ctx context.Context, id string, patch wnet.ProfilePatch) (wnet.Profile, error) {
	kv := []string{}
	if patch.Name != nil {
		kv = append(kv, "connection.id", *patch.Name)
	}
	if patch.SSID != nil {
		kv = append(kv, "802-11-wireless.ssid", *patch.SSID)
	}
	if patch.Hidden != nil {
		kv = append(kv, "802-11-wireless.hidden", yesno(*patch.Hidden))
	}
	if patch.Autoconnect != nil {
		kv = append(kv, "connection.autoconnect", yesno(*patch.Autoconnect))
	}
	if patch.Security != nil {
		switch patch.Security.Kind {
		case wnet.SecPSK:
			kv = append(kv, "wifi-sec.key-mgmt", "wpa-psk", "wifi-sec.psk", patch.Security.Passphrase)
		case wnet.SecOpen:
			// clear key-mgmt and the stored passphrase
			kv = append(kv, "wifi-sec.key-mgmt", "none", "wifi-sec.psk", "")
		case wnet.SecEnterprise:
			return wnet.Profile{}, fmt.Errorf("%w: enterprise security", wnet.ErrUnsupported)
		}
	}
	if patch.IPv4 != nil {
		kv = append(kv, ipArgs("ipv4", patch.IPv4)...)
	}
	if patch.IPv6 != nil {
		kv = append(kv, ipArgs("ipv6", patch.IPv6)...)
	}
	if len(kv) > 0 {
		if err := ModifyConnection(ctx, id, kv...); err != nil {
			return wnet.Profile{}, err
		}
	}
	return b.profileByUUID(ctx, id)
}

func (b *Backend) DeleteProfile(ctx context.Context, id string) error {
	return DeleteConnection(ctx, id)
}

// ---- connections ------------------------------------------------------------

// In NetworkManager an active connection is keyed by the same UUID as the
// connection profile it activates, so a connection id == its profile id.
func (b *Backend) toActive(ctx context.Context, a Active) wnet.Active {
	out := wnet.Active{ID: a.UUID, Iface: a.Device, ProfileID: a.UUID}
	if ap, ok := CurrentAP(ctx, a.Device); ok {
		out.SSID = ap.SSID
		out.BSSID = ap.BSSID
	}
	return out
}

func (b *Backend) activeByUUID(ctx context.Context, id string) (wnet.Active, error) {
	actives, err := ActiveWifiConnections(ctx)
	if err != nil {
		return wnet.Active{}, err
	}
	for _, a := range actives {
		if a.UUID == id {
			return b.toActive(ctx, a), nil
		}
	}
	return wnet.Active{}, fmt.Errorf("%w: active connection %s", wnet.ErrNotFound, id)
}

func (b *Backend) Activate(ctx context.Context, iface, profileID, bssid string) (wnet.Active, error) {
	// Pin to the BSSID, or clear a stale pin when none is given — otherwise a
	// previous pin would lock later activations to a possibly-dead BSS and
	// defeat roaming. The pin lives on the saved profile, so it must be reset.
	if err := ModifyConnection(ctx, profileID, "802-11-wireless.bssid", bssid); err != nil {
		return wnet.Active{}, fmt.Errorf("set bssid: %w", err)
	}
	if err := ActivateConnection(ctx, profileID, iface, 60); err != nil {
		return wnet.Active{}, err
	}
	return b.activeByUUID(ctx, profileID)
}

func (b *Backend) Deactivate(ctx context.Context, connID string) error {
	return DeactivateConnection(ctx, connID)
}

func (b *Backend) Connections(ctx context.Context) ([]wnet.Active, error) {
	actives, err := ActiveWifiConnections(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]wnet.Active, 0, len(actives))
	for _, a := range actives {
		out = append(out, b.toActive(ctx, a))
	}
	return out, nil
}

func (b *Backend) Connection(ctx context.Context, connID string) (wnet.Active, error) {
	return b.activeByUUID(ctx, connID)
}

// ---- status / watch ---------------------------------------------------------

func mapState(code int) wnet.ConnState {
	switch code {
	case StatePrepare, StateConfig:
		return wnet.StateAssociating
	case StateNeedAuth:
		return wnet.StateAuthenticating
	case StateIPConfig, StateIPCheck, StateSecondaries:
		return wnet.StateConfiguring
	case StateActivated:
		return wnet.StateConnected
	case StateDeactivating:
		return wnet.StateDisconnecting
	case StateFailed:
		return wnet.StateFailed
	case StateDisconnected:
		return wnet.StateIdle
	default:
		return wnet.StateUnspecified
	}
}

func (b *Backend) statusForDevice(ctx context.Context, d Device, sawNeedAuth bool) wnet.Status {
	st := wnet.Status{State: mapState(d.StateCode)}
	if st.State == wnet.StateFailed {
		reason := strings.ToLower(d.Reason)
		if sawNeedAuth || strings.Contains(reason, "secret") || strings.Contains(reason, "auth") {
			st.Error = wnet.ErrCAuthFailed
		} else {
			st.Error = wnet.ErrCUnknown
		}
		st.Detail = d.Reason
	}
	if ap, ok := CurrentAP(ctx, d.Name); ok {
		st.BSSID = ap.BSSID
		st.Signal = ap.Signal
	}
	st.Addresses, st.Gateway, st.DNS = DeviceIP4(ctx, d.Name)
	return st
}

func (b *Backend) deviceForActive(ctx context.Context, id string) (string, error) {
	actives, err := ActiveWifiConnections(ctx)
	if err != nil {
		return "", err
	}
	for _, a := range actives {
		if a.UUID == id {
			return a.Device, nil
		}
	}
	return "", fmt.Errorf("%w: active connection %s", wnet.ErrNotFound, id)
}

func (b *Backend) Status(ctx context.Context, connID string) (wnet.Status, error) {
	dev, err := b.deviceForActive(ctx, connID)
	if err != nil {
		return wnet.Status{}, err
	}
	d, err := DeviceInfo(ctx, dev)
	if err != nil {
		return wnet.Status{}, err
	}
	return b.statusForDevice(ctx, d, d.StateCode == StateNeedAuth), nil
}

func (b *Backend) Watch(ctx context.Context, connID string) (<-chan wnet.Status, error) {
	dev, err := b.deviceForActive(ctx, connID)
	if err != nil {
		return nil, err
	}
	ch := make(chan wnet.Status, 1)
	go func() {
		defer close(ch)
		sawNeedAuth := false
		last := wnet.ConnState(-1)
		ticker := time.NewTicker(400 * time.Millisecond)
		defer ticker.Stop()
		for {
			d, err := DeviceInfo(ctx, dev)
			if err != nil {
				return
			}
			if d.StateCode == StateNeedAuth {
				sawNeedAuth = true
			}
			st := b.statusForDevice(ctx, d, sawNeedAuth)
			if st.State != last {
				select {
				case ch <- st:
				case <-ctx.Done():
					return
				}
				last = st.State
			}
			if st.State == wnet.StateConnected || st.State == wnet.StateFailed {
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return ch, nil
}
