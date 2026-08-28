package wnet

import (
	"os"
	"path/filepath"
	"strings"
)

// sysClassNet is the sysfs root a local backend reads device attributes from.
// It, and osReadlink, are variables so tests can stand in a fixture without a
// real sysfs tree.
var (
	sysClassNet = "/sys/class/net"
	osReadlink  = os.Readlink
)

// LocalPCI returns the PCI address of a local interface's backing device, or ""
// when the interface has none (e.g. a USB or virtual device) or does not exist.
// It is a convenience for backends that run on the same host as the interfaces
// (nmcli, nmdbus, iwd) to populate Interface.Pci; a remote backend fills that
// field from its own source instead. Filtering never calls this — it matches on
// the domain Interface a backend already returned — so the sysfs dependency
// stays inside the local backends that opt into it.
func LocalPCI(name string) string {
	if name == "" {
		return ""
	}
	// /sys/class/net/<name>/device is a symlink into the device tree; its base
	// name is the bus address, which for PCI devices is the domain:bus:dev.func
	// form (e.g. "0000:02:00.0"). A missing link (non-PCI or absent) yields "".
	target, err := osReadlink(filepath.Join(sysClassNet, name, "device"))
	if err != nil {
		return ""
	}
	return strings.ToLower(filepath.Base(target))
}
