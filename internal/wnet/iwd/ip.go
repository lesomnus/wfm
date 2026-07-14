package iwd

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"strings"
)

// iwd does not expose the assigned IP over D-Bus, so the connection status
// reads it straight from the kernel: addresses from the interface, the default
// gateway from /proc/net/route, and resolvers from /etc/resolv.conf.

// ifaceAddrs returns the global-unicast addresses on an interface in CIDR form.
func ifaceAddrs(name string) []string {
	ifi, err := net.InterfaceByName(name)
	if err != nil {
		return nil
	}
	addrs, err := ifi.Addrs()
	if err != nil {
		return nil
	}
	out := []string{}
	for _, a := range addrs {
		if ipn, ok := a.(*net.IPNet); ok && ipn.IP.IsGlobalUnicast() {
			out = append(out, ipn.String())
		}
	}
	return out
}

// defaultGateway returns the IPv4 default gateway for an interface, parsed from
// /proc/net/route (the Gateway column is a little-endian hex IPv4).
func defaultGateway(iface string) string {
	f, err := os.Open("/proc/net/route")
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Scan() // header
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 3 || fields[0] != iface || fields[1] != "00000000" {
			continue
		}
		if gw := parseHexIPv4LE(fields[2]); gw != "" {
			return gw
		}
	}
	return ""
}

func parseHexIPv4LE(h string) string {
	b, err := hex.DecodeString(h)
	if err != nil || len(b) != 4 {
		return ""
	}
	return fmt.Sprintf("%d.%d.%d.%d", b[3], b[2], b[1], b[0])
}

// dnsServers returns the nameservers from /etc/resolv.conf.
func dnsServers() []string {
	f, err := os.Open("/etc/resolv.conf")
	if err != nil {
		return nil
	}
	defer f.Close()
	out := []string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) >= 2 && fields[0] == "nameserver" {
			out = append(out, fields[1])
		}
	}
	return out
}
