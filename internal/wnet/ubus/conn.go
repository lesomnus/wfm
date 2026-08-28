package ubus

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lesomnus/wfm/internal/wnet"
)

// wirelessStatus maps each configured wifi-iface section to the runtime device
// name it was brought up as (empty until the section is up).
func (b *Backend) wirelessStatus(ctx context.Context) (map[string]string, error) {
	var raw json.RawMessage
	if err := b.c.Call(ctx, "network.wireless", "status", nil, &raw); err != nil {
		return nil, err
	}
	return parseWirelessStatus(raw), nil
}

// runtime couples a station profile with its live device name and bound network.
type runtime struct {
	iface   wifiIface
	ifname  string
	network string
}

func (b *Backend) runtimeByID(ctx context.Context, id string) (runtime, bool, error) {
	stas, err := b.staIfaces(ctx)
	if err != nil {
		return runtime{}, false, err
	}
	var w wifiIface
	found := false
	for _, s := range stas {
		if profileID(s.SSID, encStrToSecKind(s.Encryption)) == id {
			w, found = s, true
			break
		}
	}
	if !found {
		return runtime{}, false, nil
	}
	status, err := b.wirelessStatus(ctx)
	if err != nil {
		return runtime{}, false, err
	}
	network := w.Network
	if network == "" {
		network = b.network
	}
	return runtime{iface: w, ifname: status[w.Section], network: network}, true, nil
}

func (b *Backend) connectionByID(ctx context.Context, id string) (wnet.Active, error) {
	rt, ok, err := b.runtimeByID(ctx, id)
	if err != nil {
		return wnet.Active{}, err
	}
	if !ok || rt.ifname == "" {
		return wnet.Active{}, fmt.Errorf("%w: connection %s", wnet.ErrNotFound, id)
	}
	info, err := b.info(ctx, rt.ifname)
	if err != nil || !isAssociated(info.BSSID) {
		return wnet.Active{}, fmt.Errorf("%w: connection %s", wnet.ErrNotFound, id)
	}
	return wnet.Active{
		ID:        id,
		Iface:     rt.ifname,
		ProfileID: id,
		SSID:      rt.iface.SSID,
		BSSID:     strings.ToLower(info.BSSID),
	}, nil
}

func (b *Backend) Connections(ctx context.Context) ([]wnet.Active, error) {
	stas, err := b.staIfaces(ctx)
	if err != nil {
		return nil, err
	}
	status, err := b.wirelessStatus(ctx)
	if err != nil {
		return nil, err
	}
	out := []wnet.Active{}
	for _, w := range stas {
		ifname := status[w.Section]
		if ifname == "" {
			continue
		}
		info, err := b.info(ctx, ifname)
		if err != nil || !isAssociated(info.BSSID) {
			continue
		}
		id := profileID(w.SSID, encStrToSecKind(w.Encryption))
		out = append(out, wnet.Active{
			ID:        id,
			Iface:     ifname,
			ProfileID: id,
			SSID:      w.SSID,
			BSSID:     strings.ToLower(info.BSSID),
		})
	}
	return out, nil
}

func (b *Backend) Connection(ctx context.Context, connID string) (wnet.Active, error) {
	return b.connectionByID(ctx, connID)
}

// Activate enables the station profile (optionally pinning a BSSID), reapplies
// the wireless config and waits for the link to associate. The connection id is
// the profile id, as the profile and its active connection are one on OpenWrt.
func (b *Backend) Activate(ctx context.Context, iface, profileID, bssid string) (wnet.Active, error) {
	cur, ok, err := b.findByID(ctx, profileID)
	if err != nil {
		return wnet.Active{}, err
	}
	if !ok {
		return wnet.Active{}, fmt.Errorf("%w: profile %s", wnet.ErrNotFound, profileID)
	}

	// Enable the station and set (or clear) the BSSID pin. Clearing a stale pin
	// matters: a previous pin would otherwise lock this activation to a possibly
	// dead BSS and defeat roaming.
	values := map[string]any{"disabled": "0", "bssid": strings.ToLower(bssid)}
	if err := b.setAndCommit(ctx, cur.Section, values); err != nil {
		return wnet.Active{}, err
	}
	if err := b.reloadWifi(ctx); err != nil {
		return wnet.Active{}, err
	}
	if err := b.waitAssociated(ctx, profileID, 25*time.Second); err != nil {
		return wnet.Active{}, err
	}
	return b.connectionByID(ctx, profileID)
}

// waitAssociated polls until the profile's station is associated or the deadline
// passes.
func (b *Backend) waitAssociated(ctx context.Context, id string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		rt, ok, err := b.runtimeByID(ctx, id)
		if err == nil && ok && rt.ifname != "" {
			if info, err := b.info(ctx, rt.ifname); err == nil && isAssociated(info.BSSID) {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("association timed out for profile %s", id)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func (b *Backend) Deactivate(ctx context.Context, connID string) error {
	cur, ok, err := b.findByID(ctx, connID)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("%w: connection %s", wnet.ErrNotFound, connID)
	}
	if err := b.setAndCommit(ctx, cur.Section, map[string]any{"disabled": "1"}); err != nil {
		return err
	}
	return b.reloadWifi(ctx)
}

// ---- status / watch ---------------------------------------------------------

// ifStatus reads the assigned IPv4 configuration of a network interface.
func (b *Backend) ifStatus(ctx context.Context, network string) (addrs []string, gateway string, dns []string) {
	if network == "" {
		return nil, "", nil
	}
	var raw json.RawMessage
	if err := b.c.Call(ctx, "network.interface."+network, "status", nil, &raw); err != nil {
		return nil, "", nil
	}
	return parseIfStatus(raw)
}

func (b *Backend) Status(ctx context.Context, connID string) (wnet.Status, error) {
	rt, ok, err := b.runtimeByID(ctx, connID)
	if err != nil {
		return wnet.Status{}, err
	}
	if !ok {
		return wnet.Status{}, fmt.Errorf("%w: connection %s", wnet.ErrNotFound, connID)
	}
	return b.statusFor(ctx, rt), nil
}

// statusFor builds a status snapshot for a station from iwinfo (association,
// signal) and, once connected, the bound network's IP configuration.
func (b *Backend) statusFor(ctx context.Context, rt runtime) wnet.Status {
	st := wnet.Status{State: wnet.StateIdle}
	if rt.ifname == "" {
		// Disabled/never-brought-up: idle unless the config says enabled.
		if !rt.iface.Disabled {
			st.State = wnet.StateAssociating
		}
		return st
	}
	info, err := b.info(ctx, rt.ifname)
	if err != nil {
		return st
	}
	if isAssociated(info.BSSID) {
		st.State = wnet.StateConnected
		st.BSSID = strings.ToLower(info.BSSID)
		st.Signal = signalQuality(info.Quality, info.QualityMax)
		st.Addresses, st.Gateway, st.DNS = b.ifStatus(ctx, rt.network)
	} else if !rt.iface.Disabled {
		st.State = wnet.StateAssociating
	}
	return st
}

// Watch polls the connection and emits on each state change. OpenWrt/iwinfo does
// not expose fine-grained association failures, so a link that does not come up
// within a bounded window is reported as failed and the stream closes; a
// successful association closes the stream on the connected state.
func (b *Backend) Watch(ctx context.Context, connID string) (<-chan wnet.Status, error) {
	if _, ok, err := b.runtimeByID(ctx, connID); err != nil {
		return nil, err
	} else if !ok {
		return nil, fmt.Errorf("%w: connection %s", wnet.ErrNotFound, connID)
	}

	ch := make(chan wnet.Status, 1)
	go func() {
		defer close(ch)
		deadline := time.Now().Add(30 * time.Second)
		last := wnet.ConnState(-1)
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			rt, ok, err := b.runtimeByID(ctx, connID)
			if err != nil || !ok {
				return
			}
			st := b.statusFor(ctx, rt)
			if st.State != wnet.StateConnected && time.Now().After(deadline) {
				st = wnet.Status{State: wnet.StateFailed, Error: wnet.ErrCUnknown, Detail: "association timed out"}
			}
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
