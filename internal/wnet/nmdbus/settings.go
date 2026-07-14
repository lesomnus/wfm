package nmdbus

import (
	"fmt"
	"net"

	"github.com/godbus/dbus/v5"

	"github.com/lesomnus/wfm/internal/wnet"
)

// nmSettings is NetworkManager's connection settings dict: a{sa{sv}} =
// group -> key -> value.
type nmSettings = map[string]map[string]dbus.Variant

func sGet(s nmSettings, group, key string) (dbus.Variant, bool) {
	g, ok := s[group]
	if !ok {
		return dbus.Variant{}, false
	}
	v, ok := g[key]
	return v, ok
}

func sString(s nmSettings, group, key string) string {
	if v, ok := sGet(s, group, key); ok {
		x, _ := v.Value().(string)
		return x
	}
	return ""
}

func sBool(s nmSettings, group, key string, def bool) bool {
	if v, ok := sGet(s, group, key); ok {
		if x, ok := v.Value().(bool); ok {
			return x
		}
	}
	return def
}

func sBytes(s nmSettings, group, key string) []byte {
	if v, ok := sGet(s, group, key); ok {
		x, _ := v.Value().([]byte)
		return x
	}
	return nil
}

// dnsVariant encodes DNS servers for the family: ipv4 uses the legacy 'au'
// (uint32 in_addr) field, ipv6 uses 'aay' (array of 16-byte addresses).
func dnsVariant(group string, dns []string) (dbus.Variant, bool) {
	if group == "ipv6" {
		arr := [][]byte{}
		for _, d := range dns {
			if ip := net.ParseIP(d); ip != nil && ip.To4() == nil && ip.To16() != nil {
				arr = append(arr, ip.To16())
			}
		}
		if len(arr) == 0 {
			return dbus.Variant{}, false
		}
		return dbus.MakeVariant(arr), true
	}
	arr := []uint32{}
	for _, d := range dns {
		if u, ok := ip4ToU32(d); ok {
			arr = append(arr, u)
		}
	}
	if len(arr) == 0 {
		return dbus.Variant{}, false
	}
	return dbus.MakeVariant(arr), true
}

// ipSection builds an ipv4/ipv6 settings group from a wnet.IPConfig. A nil cfg
// means DHCP (method=auto). group is "ipv4" or "ipv6".
func ipSection(group string, cfg *wnet.IPConfig) map[string]dbus.Variant {
	if cfg == nil {
		return map[string]dbus.Variant{"method": dbus.MakeVariant("auto")}
	}
	switch cfg.Method {
	case wnet.IPManual:
		out := map[string]dbus.Variant{"method": dbus.MakeVariant("manual")}
		ad := []map[string]dbus.Variant{}
		for _, c := range cfg.Addresses {
			ip, prefix, ok := splitCIDR(c)
			if !ok {
				continue
			}
			ad = append(ad, map[string]dbus.Variant{
				"address": dbus.MakeVariant(ip),
				"prefix":  dbus.MakeVariant(prefix),
			})
		}
		if len(ad) > 0 {
			out["address-data"] = dbus.MakeVariant(ad)
		}
		if cfg.Gateway != "" {
			out["gateway"] = dbus.MakeVariant(cfg.Gateway)
		}
		if v, ok := dnsVariant(group, cfg.DNS); ok {
			out["dns"] = v
		}
		if len(cfg.DNSSearch) > 0 {
			out["dns-search"] = dbus.MakeVariant(cfg.DNSSearch)
		}
		return out
	case wnet.IPDisabled:
		return map[string]dbus.Variant{"method": dbus.MakeVariant("disabled")}
	default: // IPAuto / IPUnspecified
		return map[string]dbus.Variant{"method": dbus.MakeVariant("auto")}
	}
}

// buildSettings constructs a full connection dict for a new wifi profile.
func buildSettings(spec wnet.ProfileSpec) nmSettings {
	id := spec.Name
	if id == "" {
		id = spec.SSID
	}
	wireless := map[string]dbus.Variant{
		"ssid": dbus.MakeVariant([]byte(spec.SSID)),
		"mode": dbus.MakeVariant("infrastructure"),
	}
	if spec.Hidden {
		wireless["hidden"] = dbus.MakeVariant(true)
	}
	s := nmSettings{
		"connection": {
			"id":          dbus.MakeVariant(id),
			"type":        dbus.MakeVariant("802-11-wireless"),
			"autoconnect": dbus.MakeVariant(spec.Autoconnect),
		},
		"802-11-wireless": wireless,
		"ipv4":            ipSection("ipv4", spec.IPv4),
		"ipv6":            ipSection("ipv6", spec.IPv6),
	}
	if spec.Security.Kind == wnet.SecPSK {
		s["802-11-wireless-security"] = map[string]dbus.Variant{
			"key-mgmt": dbus.MakeVariant("wpa-psk"),
			"psk":      dbus.MakeVariant(spec.Security.Passphrase),
		}
	}
	return s
}

func ipConfigFromSettings(s nmSettings, group string) wnet.IPConfig {
	cfg := wnet.IPConfig{}
	switch sString(s, group, "method") {
	case "auto":
		cfg.Method = wnet.IPAuto
	case "manual":
		cfg.Method = wnet.IPManual
	case "disabled":
		cfg.Method = wnet.IPDisabled
	default:
		cfg.Method = wnet.IPUnspecified
	}
	if v, ok := sGet(s, group, "address-data"); ok {
		if ad, ok := v.Value().([]map[string]dbus.Variant); ok {
			for _, e := range ad {
				addr, _ := e["address"].Value().(string)
				prefix, _ := e["prefix"].Value().(uint32)
				if addr != "" {
					cfg.Addresses = append(cfg.Addresses, fmt.Sprintf("%s/%d", addr, prefix))
				}
			}
		}
	}
	cfg.Gateway = sString(s, group, "gateway")
	if v, ok := sGet(s, group, "dns"); ok {
		if group == "ipv6" {
			if arr, ok := v.Value().([][]byte); ok {
				for _, b := range arr {
					if len(b) == 16 {
						cfg.DNS = append(cfg.DNS, net.IP(b).String())
					}
				}
			}
		} else {
			if arr, ok := v.Value().([]uint32); ok {
				for _, u := range arr {
					cfg.DNS = append(cfg.DNS, u32ToIP4(u))
				}
			}
		}
	}
	if v, ok := sGet(s, group, "dns-search"); ok {
		if arr, ok := v.Value().([]string); ok {
			cfg.DNSSearch = arr
		}
	}
	return cfg
}

func profileFromSettings(s nmSettings) wnet.Profile {
	return wnet.Profile{
		ID:          sString(s, "connection", "uuid"),
		Name:        sString(s, "connection", "id"),
		SSID:        string(sBytes(s, "802-11-wireless", "ssid")),
		Hidden:      sBool(s, "802-11-wireless", "hidden", false),
		Autoconnect: sBool(s, "connection", "autoconnect", true),
		Security:    securityFromKeyMgmt(sString(s, "802-11-wireless-security", "key-mgmt")),
		IPv4:        ipConfigFromSettings(s, "ipv4"),
		IPv6:        ipConfigFromSettings(s, "ipv6"),
	}
}
