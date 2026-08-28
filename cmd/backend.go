package cmd

import (
	"context"
	"fmt"

	"github.com/godbus/dbus/v5"

	"github.com/lesomnus/wfm/cmd/config"
	"github.com/lesomnus/wfm/internal/wnet"
	"github.com/lesomnus/wfm/internal/wnet/iwd"
	"github.com/lesomnus/wfm/internal/wnet/nmcli"
	"github.com/lesomnus/wfm/internal/wnet/nmdbus"
	"github.com/lesomnus/wfm/internal/wnet/ubus"
)

// newBackend constructs the named wifi backend and applies the interface
// exclusions from the active config (when one is in ctx), so every path that
// serves the backend — the standalone server and the in-process one a bare CLI
// invocation spins up — hides excluded interfaces the same way.
func newBackend(ctx context.Context, name string) (wnet.Backend, error) {
	c, _ := use_config.From(ctx)
	b, err := newRawBackend(name, c)
	if err != nil {
		return nil, err
	}
	if c != nil {
		b = wnet.WithExcluded(b, c.Interface.Exclude)
	}
	return b, nil
}

// newRawBackend constructs the named wifi backend without any wrapping. The
// config is used only by backends that need it (ubus); a nil config is fine for
// the local backends.
func newRawBackend(name string, c *config.Config) (wnet.Backend, error) {
	switch name {
	case "nmcli":
		return nmcli.New(), nil
	case "nmdbus":
		return nmdbus.New()
	case "iwd":
		return iwd.New()
	case "ubus":
		return newUbusBackend(c)
	default:
		return nil, fmt.Errorf("unknown backend %q (want nmcli|nmdbus|iwd|ubus)", name)
	}
}

// newUbusBackend builds the OpenWrt ubus backend from the config's `ubus`
// section, resolving the password (from password_file when set).
func newUbusBackend(c *config.Config) (wnet.Backend, error) {
	if c == nil || c.Ubus.Endpoint == "" {
		return nil, fmt.Errorf("ubus backend requires `ubus.endpoint` in the config")
	}
	pass, err := c.Ubus.ResolvePassword()
	if err != nil {
		return nil, err
	}
	return ubus.New(ubus.Options{
		Endpoint: c.Ubus.Endpoint,
		Username: c.Ubus.Username,
		Password: pass,
		Insecure: c.Ubus.Insecure,
		Radio:    c.Ubus.Radio,
		Network:  c.Ubus.Network,
	})
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
