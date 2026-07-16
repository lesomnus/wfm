package cmd

import (
	"context"
	"fmt"

	"github.com/godbus/dbus/v5"

	"github.com/lesomnus/wfm/internal/wnet"
	"github.com/lesomnus/wfm/internal/wnet/iwd"
	"github.com/lesomnus/wfm/internal/wnet/nmcli"
	"github.com/lesomnus/wfm/internal/wnet/nmdbus"
)

// newBackend constructs the named wifi backend and applies the interface
// exclusions from the active config (when one is in ctx), so every path that
// serves the backend — the standalone server and the in-process one a bare CLI
// invocation spins up — hides excluded interfaces the same way.
func newBackend(ctx context.Context, name string) (wnet.Backend, error) {
	b, err := newRawBackend(name)
	if err != nil {
		return nil, err
	}
	if c, ok := use_config.From(ctx); ok {
		b = wnet.WithExcluded(b, c.Interface.Exclude)
	}
	return b, nil
}

// newRawBackend constructs the named wifi backend without any wrapping.
func newRawBackend(name string) (wnet.Backend, error) {
	switch name {
	case "nmcli":
		return nmcli.New(), nil
	case "nmdbus":
		return nmdbus.New()
	case "iwd":
		return iwd.New()
	default:
		return nil, fmt.Errorf("unknown backend %q (want nmcli|nmdbus|iwd)", name)
	}
}

// detectBackend picks a backend by which wifi daemon owns a name on the system
// bus: iwd first, then NetworkManager (via D-Bus), falling back to nmcli.
func detectBackend() string {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return "nmcli"
	}
	defer conn.Close()
	if nameHasOwner(conn, "net.connman.iwd") {
		return "iwd"
	}
	if nameHasOwner(conn, "org.freedesktop.NetworkManager") {
		return "nmdbus"
	}
	return "nmcli"
}

func nameHasOwner(conn *dbus.Conn, name string) bool {
	var has bool
	err := conn.BusObject().Call("org.freedesktop.DBus.NameHasOwner", 0, name).Store(&has)
	return err == nil && has
}
