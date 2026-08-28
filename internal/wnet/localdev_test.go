package wnet

import (
	"os"
	"testing"
)

func TestLocalPCI(t *testing.T) {
	orig := osReadlink
	t.Cleanup(func() { osReadlink = orig })

	links := map[string]string{
		// A PCI device: the /device symlink resolves into the PCI bus tree.
		"/sys/class/net/wlan0/device": "../../../0000:02:00.0",
		// A USB device: base name is not a PCI address, but LocalPCI returns
		// whatever the bus address is; callers only compare it to a pci rule.
		"/sys/class/net/wlan1/device": "../../../1-1.4:1.0",
	}
	osReadlink = func(name string) (string, error) {
		if v, ok := links[name]; ok {
			return v, nil
		}
		return "", os.ErrNotExist
	}

	if got := LocalPCI("wlan0"); got != "0000:02:00.0" {
		t.Errorf("LocalPCI(wlan0) = %q, want 0000:02:00.0", got)
	}
	if got := LocalPCI("wlan1"); got != "1-1.4:1.0" {
		t.Errorf("LocalPCI(wlan1) = %q", got)
	}
	if got := LocalPCI("wlan9"); got != "" { // no device link
		t.Errorf("LocalPCI(missing) = %q, want empty", got)
	}
	if got := LocalPCI(""); got != "" {
		t.Errorf("LocalPCI(\"\") = %q, want empty", got)
	}
}
