package ubus

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/lesomnus/wfm/internal/wnet"
)

// ---- iwinfo shapes ----------------------------------------------------------

// encryption is the iwinfo encryption block, shared by scan results and info.
type encryption struct {
	Enabled        bool     `json:"enabled"`
	Wpa            []int    `json:"wpa"`
	Authentication []string `json:"authentication"`
	Wep            bool     `json:"wep"`
}

// scanResult is one entry of iwinfo `scan`.
type scanResult struct {
	SSID       string     `json:"ssid"`
	BSSID      string     `json:"bssid"`
	Channel    int        `json:"channel"`
	Mhz        uint32     `json:"mhz"`
	Signal     int        `json:"signal"` // dBm
	Quality    int        `json:"quality"`
	QualityMax int        `json:"quality_max"`
	Encryption encryption `json:"encryption"`
}

// iwinfoInfo is the iwinfo `info` reply for one device. bssid is the associated
// AP (all-zero when unassociated); hwaddr is the device's own MAC.
type iwinfoInfo struct {
	SSID       string     `json:"ssid"`
	BSSID      string     `json:"bssid"`
	Mode       string     `json:"mode"`
	Channel    int        `json:"channel"`
	Frequency  uint32     `json:"frequency"`
	Signal     int        `json:"signal"`
	Quality    int        `json:"quality"`
	QualityMax int        `json:"quality_max"`
	HwAddr     string     `json:"hwaddr"`
	Encryption encryption `json:"encryption"`
}

// signalQuality maps iwinfo's quality/quality_max pair to the 0-100 scale used
// by the wnet domain. quality_max is 70 on most drivers.
func signalQuality(q, qmax int) int {
	if qmax <= 0 {
		return 0
	}
	v := q * 100 / qmax
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

// encToKeyMgmt maps an iwinfo encryption block to wnet key-management methods.
func encToKeyMgmt(e encryption) []wnet.KeyMgmt {
	if !e.Enabled {
		return []wnet.KeyMgmt{wnet.KeyNone}
	}
	auth := map[string]bool{}
	for _, a := range e.Authentication {
		auth[strings.ToLower(a)] = true
	}
	var out []wnet.KeyMgmt
	seen := map[wnet.KeyMgmt]bool{}
	add := func(k wnet.KeyMgmt) {
		if !seen[k] {
			seen[k] = true
			out = append(out, k)
		}
	}
	if auth["sae"] {
		add(wnet.KeySAE)
	}
	if auth["owe"] {
		add(wnet.KeyOWE)
	}
	if auth["8021x"] || auth["eap"] {
		add(wnet.KeyWPAEAP)
	}
	if auth["psk"] {
		add(wnet.KeyWPAPSK)
	}
	if len(out) == 0 {
		// Secured but the auth list was empty/unrecognized (e.g. bare WEP);
		// report it as a pre-shared-key network rather than open.
		add(wnet.KeyWPAPSK)
	}
	return out
}

// apFromScan projects one iwinfo scan result onto the neutral AP model.
func apFromScan(r scanResult) wnet.AP {
	return wnet.AP{
		SSID:    r.SSID,
		BSSID:   strings.ToLower(r.BSSID),
		Signal:  signalQuality(r.Quality, r.QualityMax),
		FreqMHz: r.Mhz,
		KeyMgmt: encToKeyMgmt(r.Encryption),
	}
}

// isAssociated reports whether an iwinfo bssid denotes a real association
// rather than the all-zero placeholder returned when a station is idle.
func isAssociated(bssid string) bool {
	b := strings.TrimSpace(bssid)
	return b != "" && !strings.HasPrefix(b, "00:00:00:00:00:00")
}

// ---- uci wireless shapes ----------------------------------------------------

// wifiIface is a parsed `wifi-iface` section of /etc/config/wireless. Only the
// fields wfm maps to a profile are kept.
type wifiIface struct {
	Section    string
	Device     string
	Mode       string
	SSID       string
	Encryption string
	Key        string
	Network    string
	BSSID      string
	Disabled   bool
}

// parseWifiIfaces extracts the wifi-iface sections from a `uci get` values map
// (section-name -> raw section). Non-iface sections (wifi-device) are skipped.
func parseWifiIfaces(values map[string]json.RawMessage) []wifiIface {
	out := make([]wifiIface, 0, len(values))
	for name, raw := range values {
		var s struct {
			Type       string `json:".type"`
			Device     string `json:"device"`
			Mode       string `json:"mode"`
			SSID       string `json:"ssid"`
			Encryption string `json:"encryption"`
			Key        string `json:"key"`
			Network    any    `json:"network"`
			BSSID      string `json:"bssid"`
			Disabled   any    `json:"disabled"`
		}
		if err := json.Unmarshal(raw, &s); err != nil {
			continue
		}
		if s.Type != "wifi-iface" {
			continue
		}
		out = append(out, wifiIface{
			Section:    name,
			Device:     s.Device,
			Mode:       s.Mode,
			SSID:       s.SSID,
			Encryption: s.Encryption,
			Key:        s.Key,
			Network:    firstString(s.Network),
			BSSID:      s.BSSID,
			Disabled:   uciBool(s.Disabled),
		})
	}
	return out
}

// uciBool interprets a uci boolean, which arrives as the string "1"/"0" (or
// "true"/"on"), tolerating an actual JSON bool as well.
func uciBool(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "1", "true", "yes", "on":
			return true
		}
	}
	return false
}

// firstString normalizes a uci option that may be a single string or a list
// (uci returns space-joined options either way) into one string.
func firstString(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case []any:
		if len(t) > 0 {
			if s, ok := t[0].(string); ok {
				return s
			}
		}
	}
	return ""
}

func boolToUCI(v bool) string {
	if v {
		return "1"
	}
	return "0"
}

// ---- security mapping -------------------------------------------------------

// secType is the coarse security tag used to derive a stable profile id from an
// (ssid, security) pair, mirroring the iwd backend's scheme.
func secType(k wnet.SecurityKind) string {
	switch k {
	case wnet.SecPSK:
		return "psk"
	case wnet.SecEnterprise:
		return "8021x"
	default:
		return "open"
	}
}

// encStrToSecKind maps a uci `encryption` value (e.g. "psk2", "sae",
// "psk2+ccmp", "none") to a wnet security kind.
func encStrToSecKind(enc string) wnet.SecurityKind {
	e := strings.ToLower(strings.TrimSpace(enc))
	switch {
	case e == "" || e == "none":
		return wnet.SecOpen
	case strings.Contains(e, "8021x"), strings.Contains(e, "wpa-eap"),
		strings.Contains(e, "wpa2-eap"), strings.Contains(e, "wpa3-eap"):
		return wnet.SecEnterprise
	default: // psk, psk2, sae, psk-mixed, owe...
		return wnet.SecPSK
	}
}

// secKindToEnc maps a wnet security kind to the uci `encryption` value written
// for a new/updated station profile.
func secKindToEnc(k wnet.SecurityKind) string {
	switch k {
	case wnet.SecPSK:
		return "psk2"
	case wnet.SecEnterprise:
		return "wpa2" // enterprise is rejected before write; kept for completeness
	default:
		return "none"
	}
}

// profileID derives the stable id of a station profile from its ssid and
// security kind, so the same network always maps to the same id (as in iwd).
func profileID(ssid string, k wnet.SecurityKind) string {
	return wnet.DeriveID(ssid, secType(k))
}

// toProfile projects a station wifi-iface onto the neutral profile model. The
// passphrase is never read back.
func toProfile(w wifiIface) wnet.Profile {
	kind := encStrToSecKind(w.Encryption)
	return wnet.Profile{
		ID:          profileID(w.SSID, kind),
		Name:        w.SSID,
		SSID:        w.SSID,
		Autoconnect: !w.Disabled,
		Security:    wnet.Security{Kind: kind},
	}
}

// ---- runtime status shapes --------------------------------------------------

// parseWirelessStatus maps each wifi-iface section to the runtime device name
// it was brought up as, from a `network.wireless status` payload
// (radio -> {interfaces: [{section, ifname}]}). A section that is not up has an
// empty ifname.
func parseWirelessStatus(raw json.RawMessage) map[string]string {
	var m map[string]struct {
		Interfaces []struct {
			Section string `json:"section"`
			Ifname  string `json:"ifname"`
		} `json:"interfaces"`
	}
	_ = json.Unmarshal(raw, &m)
	out := map[string]string{}
	for _, radio := range m {
		for _, it := range radio.Interfaces {
			if it.Section != "" {
				out[it.Section] = it.Ifname
			}
		}
	}
	return out
}

// parseIfStatus extracts the assigned IPv4 configuration (addresses in CIDR
// form, default gateway, resolvers) from a `network.interface status` payload.
func parseIfStatus(raw json.RawMessage) (addrs []string, gateway string, dns []string) {
	var s struct {
		IPv4Address []struct {
			Address string `json:"address"`
			Mask    int    `json:"mask"`
		} `json:"ipv4-address"`
		Route []struct {
			Target  string `json:"target"`
			Mask    int    `json:"mask"`
			Nexthop string `json:"nexthop"`
		} `json:"route"`
		DNSServer []string `json:"dns-server"`
	}
	if json.Unmarshal(raw, &s) != nil {
		return nil, "", nil
	}
	for _, a := range s.IPv4Address {
		addrs = append(addrs, fmt.Sprintf("%s/%d", a.Address, a.Mask))
	}
	for _, r := range s.Route {
		if r.Target == "0.0.0.0" && r.Mask == 0 && r.Nexthop != "" {
			gateway = r.Nexthop
			break
		}
	}
	return addrs, gateway, s.DNSServer
}
