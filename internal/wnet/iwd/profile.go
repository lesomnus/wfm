package iwd

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"net"
	"strings"

	"github.com/lesomnus/wfm/internal/wnet"
)

// iwd stores each known network as a file /var/lib/iwd/<encoded-ssid>.<type>.
// type is one of open / psk / 8021x.

var profileTypes = map[string]bool{"open": true, "psk": true, "8021x": true}

// simpleSSID reports whether an SSID appears verbatim in the filename, i.e. it
// contains only alphanumerics, spaces, underscores or minus signs.
func simpleSSID(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		ok := c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' ||
			c == ' ' || c == '_' || c == '-'
		if !ok {
			return false
		}
	}
	return true
}

// encodeSSID renders an SSID for use in a provisioning filename.
func encodeSSID(ssid string) string {
	if simpleSSID(ssid) {
		return ssid
	}
	return "=" + hex.EncodeToString([]byte(ssid))
}

// decodeSSID reverses encodeSSID.
func decodeSSID(name string) (string, bool) {
	if strings.HasPrefix(name, "=") {
		b, err := hex.DecodeString(name[1:])
		if err != nil {
			return "", false
		}
		return string(b), true
	}
	return name, true
}

// profileFilename returns the on-disk name for an SSID + type.
func profileFilename(ssid, typ string) string {
	return encodeSSID(ssid) + "." + typ
}

// parseProfileFilename splits a provisioning filename into SSID and type. The
// type is the part after the last dot; a literal SSID never contains a dot and
// an encoded one is pure hex, so splitting on the last dot is unambiguous.
func parseProfileFilename(name string) (ssid, typ string, ok bool) {
	i := strings.LastIndexByte(name, '.')
	if i < 0 {
		return "", "", false
	}
	typ = name[i+1:]
	if !profileTypes[typ] {
		return "", "", false
	}
	ssid, ok = decodeSSID(name[:i])
	if !ok {
		return "", "", false
	}
	return ssid, typ, true
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func parseBool(s string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "yes", "1":
		return true
	case "false", "no", "0":
		return false
	default:
		return def
	}
}

// cidrToAddrNetmask splits a CIDR ("192.168.1.10/24") into iwd's separate
// Address and Netmask form ("192.168.1.10", "255.255.255.0"). A bare IP yields
// an empty netmask.
func cidrToAddrNetmask(cidr string) (addr, netmask string, ok bool) {
	if ip, ipnet, err := net.ParseCIDR(cidr); err == nil {
		m := ipnet.Mask
		if len(m) == 4 {
			netmask = fmt.Sprintf("%d.%d.%d.%d", m[0], m[1], m[2], m[3])
		}
		return ip.String(), netmask, true
	}
	if ip := net.ParseIP(cidr); ip != nil {
		return cidr, "", true
	}
	return "", "", false
}

// joinAddrNetmask reverses cidrToAddrNetmask back into CIDR form.
func joinAddrNetmask(addr, netmask string) string {
	if netmask == "" {
		return addr
	}
	ip := net.ParseIP(netmask)
	if ip == nil || ip.To4() == nil {
		return addr
	}
	ones, _ := net.IPMask(ip.To4()).Size()
	return fmt.Sprintf("%s/%d", addr, ones)
}

// renderProfile produces iwd provisioning-file content for the given settings.
// An absent/auto IPv4 section means DHCP (when EnableNetworkConfiguration=true).
func renderProfile(kind wnet.SecurityKind, passphrase string, autoconnect, hidden bool, ipv4, ipv6 *wnet.IPConfig) string {
	var b strings.Builder
	if kind == wnet.SecPSK && passphrase != "" {
		b.WriteString("[Security]\n")
		b.WriteString("Passphrase=" + passphrase + "\n\n")
	}
	b.WriteString("[Settings]\n")
	b.WriteString("AutoConnect=" + boolStr(autoconnect) + "\n")
	if hidden {
		b.WriteString("Hidden=true\n")
	}
	if ipv4 != nil && ipv4.Method == wnet.IPManual && len(ipv4.Addresses) > 0 {
		if addr, mask, ok := cidrToAddrNetmask(ipv4.Addresses[0]); ok {
			b.WriteString("\n[IPv4]\n")
			b.WriteString("Address=" + addr + "\n")
			if mask != "" {
				b.WriteString("Netmask=" + mask + "\n")
			}
			if ipv4.Gateway != "" {
				b.WriteString("Gateway=" + ipv4.Gateway + "\n")
			}
			if len(ipv4.DNS) > 0 {
				b.WriteString("DNS=" + strings.Join(ipv4.DNS, " ") + "\n")
			}
			if len(ipv4.DNSSearch) > 0 {
				b.WriteString("DomainName=" + ipv4.DNSSearch[0] + "\n")
			}
		}
	}
	if ipv6 != nil && ipv6.Method == wnet.IPManual && len(ipv6.Addresses) > 0 {
		b.WriteString("\n[IPv6]\n")
		b.WriteString("Enabled=true\n")
		b.WriteString("Address=" + ipv6.Addresses[0] + "\n") // iwd IPv6 uses CIDR
		if ipv6.Gateway != "" {
			b.WriteString("Gateway=" + ipv6.Gateway + "\n")
		}
		if len(ipv6.DNS) > 0 {
			b.WriteString("DNS=" + strings.Join(ipv6.DNS, " ") + "\n")
		}
	}
	return b.String()
}

// parseINI is a minimal INI reader for iwd provisioning files.
func parseINI(content string) map[string]map[string]string {
	out := map[string]map[string]string{}
	cur := ""
	sc := bufio.NewScanner(strings.NewReader(content))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			cur = line[1 : len(line)-1]
			if out[cur] == nil {
				out[cur] = map[string]string{}
			}
			continue
		}
		if i := strings.IndexByte(line, '='); i >= 0 && cur != "" {
			out[cur][strings.TrimSpace(line[:i])] = strings.TrimSpace(line[i+1:])
		}
	}
	return out
}

// parseProfile builds a wnet.Profile from a provisioning file's name parts and
// content. The passphrase is never read back.
func parseProfile(ssid, typ, content string) wnet.Profile {
	sec := parseINI(content)
	p := wnet.Profile{
		ID:          wnet.DeriveID(ssid, typ),
		Name:        ssid,
		SSID:        ssid,
		Autoconnect: true,
		Security:    wnet.Security{Kind: typeToSecKind(typ)},
		IPv4:        wnet.IPConfig{Method: wnet.IPAuto},
		IPv6:        wnet.IPConfig{Method: wnet.IPAuto},
	}
	if v, ok := sec["Settings"]["AutoConnect"]; ok {
		p.Autoconnect = parseBool(v, true)
	}
	if v, ok := sec["Settings"]["Hidden"]; ok {
		p.Hidden = parseBool(v, false)
	}
	if ip4, ok := sec["IPv4"]; ok {
		cfg := wnet.IPConfig{Method: wnet.IPManual}
		if addr := ip4["Address"]; addr != "" {
			cfg.Addresses = []string{joinAddrNetmask(addr, ip4["Netmask"])}
		}
		cfg.Gateway = ip4["Gateway"]
		if d := ip4["DNS"]; d != "" {
			cfg.DNS = strings.Fields(d)
		}
		if dn := ip4["DomainName"]; dn != "" {
			cfg.DNSSearch = []string{dn}
		}
		p.IPv4 = cfg
	}
	if ip6, ok := sec["IPv6"]; ok {
		cfg := wnet.IPConfig{Method: wnet.IPManual}
		if a := ip6["Address"]; a != "" {
			cfg.Addresses = []string{a}
		}
		cfg.Gateway = ip6["Gateway"]
		if d := ip6["DNS"]; d != "" {
			cfg.DNS = strings.Fields(d)
		}
		p.IPv6 = cfg
	}
	return p
}
