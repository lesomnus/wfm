package nmdbus

import (
	"encoding/binary"
	"fmt"
	"net"

	"github.com/lesomnus/wfm/internal/wnet"
)

// NMDeviceType
const nmDeviceTypeWifi = 2

// NMDeviceState (subset)
const (
	nmStateUnmanaged    = 10
	nmStateUnavailable  = 20
	nmStateDisconnected = 30
	nmStatePrepare      = 40
	nmStateConfig       = 50
	nmStateNeedAuth     = 60
	nmStateIPConfig     = 70
	nmStateIPCheck      = 80
	nmStateSecondaries  = 90
	nmStateActivated    = 100
	nmStateDeactivating = 110
	nmStateFailed       = 120
)

// NMDeviceStateReason (subset)
const nmReasonNoSecrets = 7

// NM80211ApFlags / NM80211ApSecurityFlags bits
const (
	apFlagPrivacy = 0x1
	secPSK        = 0x100
	sec8021X      = 0x200
	secSAE        = 0x400
	secOWE        = 0x800
	secOWETM      = 0x1000
)

// keyMgmtFromFlags derives the advertised key-management methods of an AP from
// its Flags / WpaFlags / RsnFlags bitfields (the logic libnm/nmcli use).
func keyMgmtFromFlags(flags, wpa, rsn uint32) []wnet.KeyMgmt {
	out := []wnet.KeyMgmt{}
	add := func(k wnet.KeyMgmt) { out = append(out, k) }
	if rsn&secSAE != 0 {
		add(wnet.KeySAE)
	}
	if (rsn|wpa)&sec8021X != 0 {
		add(wnet.KeyWPAEAP)
	}
	if rsn&(secOWE|secOWETM) != 0 {
		add(wnet.KeyOWE)
	}
	if (rsn|wpa)&secPSK != 0 {
		add(wnet.KeyWPAPSK)
	}
	if len(out) == 0 {
		if flags&apFlagPrivacy != 0 {
			add(wnet.KeyWPAPSK) // privacy on but no WPA/RSN: WEP / unknown, treat as secured
		} else {
			add(wnet.KeyNone) // open
		}
	}
	return out
}

// mapDeviceState maps an NM device state code to a wnet connection state.
func mapDeviceState(code int) wnet.ConnState {
	switch code {
	case nmStatePrepare, nmStateConfig:
		return wnet.StateAssociating
	case nmStateNeedAuth:
		return wnet.StateAuthenticating
	case nmStateIPConfig, nmStateIPCheck, nmStateSecondaries:
		return wnet.StateConfiguring
	case nmStateActivated:
		return wnet.StateConnected
	case nmStateDeactivating:
		return wnet.StateDisconnecting
	case nmStateFailed:
		return wnet.StateFailed
	case nmStateDisconnected:
		return wnet.StateIdle
	default:
		return wnet.StateUnspecified
	}
}

func stateName(code uint32) string {
	switch int(code) {
	case nmStateUnmanaged:
		return "unmanaged"
	case nmStateUnavailable:
		return "unavailable"
	case nmStateDisconnected:
		return "disconnected"
	case nmStatePrepare:
		return "prepare"
	case nmStateConfig:
		return "config"
	case nmStateNeedAuth:
		return "need-auth"
	case nmStateIPConfig:
		return "ip-config"
	case nmStateIPCheck:
		return "ip-check"
	case nmStateSecondaries:
		return "secondaries"
	case nmStateActivated:
		return "connected"
	case nmStateDeactivating:
		return "deactivating"
	case nmStateFailed:
		return "failed"
	default:
		return "unknown"
	}
}

func stateText(code uint32) string {
	return fmt.Sprintf("%d (%s)", code, stateName(code))
}

func securityFromKeyMgmt(km string) wnet.Security {
	switch km {
	case "", "none":
		return wnet.Security{Kind: wnet.SecOpen}
	case "wpa-eap":
		return wnet.Security{Kind: wnet.SecEnterprise}
	default: // wpa-psk, sae; passphrase not read back
		return wnet.Security{Kind: wnet.SecPSK}
	}
}

func ifaceUp(name string) bool {
	ifi, err := net.InterfaceByName(name)
	if err != nil {
		return false
	}
	return ifi.Flags&net.FlagUp != 0
}

// splitCIDR splits "192.168.1.10/24" into address string and prefix.
func splitCIDR(s string) (string, uint32, bool) {
	ip, ipnet, err := net.ParseCIDR(s)
	if err != nil {
		return "", 0, false
	}
	ones, _ := ipnet.Mask.Size()
	return ip.String(), uint32(ones), true
}

// ip4ToU32 encodes an IPv4 string for NM's legacy 'au' dns field. NM treats the
// uint32 as an in_addr in host (native) byte order, so we read the
// network-order bytes with the host endianness (matches inet_aton + "=I").
func ip4ToU32(s string) (uint32, bool) {
	ip := net.ParseIP(s)
	if ip == nil || ip.To4() == nil {
		return 0, false
	}
	return binary.NativeEndian.Uint32(ip.To4()), true
}

// u32ToIP4 reverses ip4ToU32.
func u32ToIP4(u uint32) string {
	b := make([]byte, 4)
	binary.NativeEndian.PutUint32(b, u)
	return fmt.Sprintf("%d.%d.%d.%d", b[0], b[1], b[2], b[3])
}
