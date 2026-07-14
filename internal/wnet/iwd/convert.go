package iwd

import "github.com/lesomnus/wfm/internal/wnet"

// dbmToQuality maps an RSSI in dBm to the 0-100 quality scale used by the wnet
// domain (matching NM's Strength). Clamped to the [-100, -50] dBm band.
func dbmToQuality(dbm int) int {
	switch {
	case dbm <= -100:
		return 0
	case dbm >= -50:
		return 100
	default:
		return 2 * (dbm + 100)
	}
}

// signalToQuality converts iwd's GetOrderedNetworks signal strength (int16,
// 100 * dBm) into 0-100 quality. (StationDiagnostic RSSI is plain dBm; use
// dbmToQuality for that.)
func signalToQuality(iwdSignal int16) int {
	return dbmToQuality(int(iwdSignal) / 100)
}

// iwdTypeToKeyMgmt maps an iwd network/known-network Type to wnet key
// management. iwd reports a single coarse type per network.
func iwdTypeToKeyMgmt(t string) []wnet.KeyMgmt {
	switch t {
	case "psk":
		return []wnet.KeyMgmt{wnet.KeyWPAPSK}
	case "8021x":
		return []wnet.KeyMgmt{wnet.KeyWPAEAP}
	case "open":
		return []wnet.KeyMgmt{wnet.KeyNone}
	default:
		return []wnet.KeyMgmt{wnet.KeyNone}
	}
}

// secKindToType maps a wnet security kind to an iwd network type / file suffix.
func secKindToType(k wnet.SecurityKind) string {
	switch k {
	case wnet.SecPSK:
		return "psk"
	case wnet.SecEnterprise:
		return "8021x"
	default:
		return "open"
	}
}

func typeToSecKind(t string) wnet.SecurityKind {
	switch t {
	case "psk":
		return wnet.SecPSK
	case "8021x":
		return wnet.SecEnterprise
	default:
		return wnet.SecOpen
	}
}

// mapState maps an iwd Station.State string to a wnet connection state. iwd
// lumps association/auth/dhcp into a single "connecting" state.
func mapState(s string) wnet.ConnState {
	switch s {
	case "connected", "roaming":
		return wnet.StateConnected
	case "connecting":
		return wnet.StateAssociating
	case "disconnecting":
		return wnet.StateDisconnecting
	case "disconnected":
		return wnet.StateIdle
	default:
		return wnet.StateUnspecified
	}
}
