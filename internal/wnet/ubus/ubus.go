package ubus

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lesomnus/wfm/internal/wnet"
)

// defaultNetwork is the /etc/config/network interface a station is bound to for
// DHCP when the config does not name one; "wwan" is the OpenWrt convention.
const defaultNetwork = "wwan"

// Backend controls wifi on a remote OpenWrt node over ubus JSON-RPC.
type Backend struct {
	c       *Client
	radio   string // wifi-device for new station profiles ("" = first found)
	network string // network interface a station binds to for DHCP
}

var _ wnet.Backend = (*Backend)(nil)

// New builds an OpenWrt ubus backend from options.
func New(o Options) (*Backend, error) {
	c, err := NewClient(o)
	if err != nil {
		return nil, err
	}
	network := o.Network
	if network == "" {
		network = defaultNetwork
	}
	return &Backend{c: c, radio: o.Radio, network: network}, nil
}

func (b *Backend) Close() error { return nil }

// ---- interfaces -------------------------------------------------------------

// info fetches iwinfo info for one device.
func (b *Backend) info(ctx context.Context, device string) (iwinfoInfo, error) {
	var out iwinfoInfo
	err := b.c.Call(ctx, "iwinfo", "info", map[string]any{"device": device}, &out)
	return out, err
}

func (b *Backend) toIface(device string, info iwinfoInfo) wnet.Interface {
	return wnet.Interface{
		Name: device,
		Mac:  strings.ToLower(info.HwAddr),
		// Pci is not resolvable over ubus without extra device-tree lookups; it
		// is left empty, so exclusion by pci is unavailable for this backend
		// (name and mac work). Powered is true because the device is present in
		// iwinfo's device list (radio available).
		Powered: true,
		Up:      isAssociated(info.BSSID) || info.SSID != "",
		Desc:    info.Mode,
	}
}

func (b *Backend) Interfaces(ctx context.Context) ([]wnet.Interface, error) {
	var devs struct {
		Devices []string `json:"devices"`
	}
	if err := b.c.Call(ctx, "iwinfo", "devices", nil, &devs); err != nil {
		return nil, err
	}
	out := make([]wnet.Interface, 0, len(devs.Devices))
	for _, d := range devs.Devices {
		info, err := b.info(ctx, d)
		if err != nil {
			continue
		}
		out = append(out, b.toIface(d, info))
	}
	return out, nil
}

func (b *Backend) Interface(ctx context.Context, name string) (wnet.Interface, error) {
	info, err := b.info(ctx, name)
	if err != nil {
		return wnet.Interface{}, fmt.Errorf("%w: interface %s", wnet.ErrNotFound, name)
	}
	return b.toIface(name, info), nil
}

// SetPower has no per-interface equivalent on OpenWrt (radio power is a
// wifi-device property shared by every interface on that radio), so toggling it
// per wifi-iface would be misleading. It is reported as unsupported.
func (b *Backend) SetPower(ctx context.Context, name string, on bool) (wnet.Interface, error) {
	return wnet.Interface{}, fmt.Errorf("%w: per-interface power on OpenWrt", wnet.ErrUnsupported)
}

// ---- scan -------------------------------------------------------------------

func (b *Backend) Scan(ctx context.Context, iface string) ([]wnet.AP, error) {
	var res struct {
		Results []scanResult `json:"results"`
	}
	if err := b.c.Call(ctx, "iwinfo", "scan", map[string]any{"device": iface}, &res); err != nil {
		return nil, err
	}
	out := make([]wnet.AP, 0, len(res.Results))
	for _, r := range res.Results {
		out = append(out, apFromScan(r))
	}
	return out, nil
}

// ---- uci helpers ------------------------------------------------------------

// wifiIfaces reads and parses all wifi-iface sections from the wireless config.
func (b *Backend) wifiIfaces(ctx context.Context) ([]wifiIface, error) {
	var r struct {
		Values map[string]json.RawMessage `json:"values"`
	}
	if err := b.c.Call(ctx, "uci", "get", map[string]any{"config": "wireless"}, &r); err != nil {
		return nil, err
	}
	return parseWifiIfaces(r.Values), nil
}

// staIfaces returns only the station-mode wifi-iface sections (the ones wfm
// treats as connection profiles).
func (b *Backend) staIfaces(ctx context.Context) ([]wifiIface, error) {
	all, err := b.wifiIfaces(ctx)
	if err != nil {
		return nil, err
	}
	out := all[:0:0]
	for _, w := range all {
		if strings.EqualFold(w.Mode, "sta") {
			out = append(out, w)
		}
	}
	return out, nil
}

// commit persists staged wireless changes.
func (b *Backend) commit(ctx context.Context) error {
	return b.c.Call(ctx, "uci", "commit", map[string]any{"config": "wireless"}, nil)
}

// reloadWifi reapplies the wireless config so committed changes take effect.
func (b *Backend) reloadWifi(ctx context.Context) error {
	return b.c.Call(ctx, "network.wireless", "up", nil, nil)
}

// pickRadio returns the wifi-device a new station profile should attach to: the
// configured radio, or the first wifi-device in the wireless config.
func (b *Backend) pickRadio(ctx context.Context) (string, error) {
	if b.radio != "" {
		return b.radio, nil
	}
	var r struct {
		Values map[string]json.RawMessage `json:"values"`
	}
	if err := b.c.Call(ctx, "uci", "get", map[string]any{"config": "wireless"}, &r); err != nil {
		return "", err
	}
	for name, raw := range r.Values {
		var s struct {
			Type string `json:".type"`
		}
		if json.Unmarshal(raw, &s) == nil && s.Type == "wifi-device" {
			return name, nil
		}
	}
	return "", fmt.Errorf("no wifi-device found; set ubus.radio")
}
