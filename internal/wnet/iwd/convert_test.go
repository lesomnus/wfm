package iwd

import (
	"testing"

	"github.com/lesomnus/wfm/internal/wnet"
)

func TestSignalToQuality(t *testing.T) {
	cases := []struct {
		iwdSignal int16
		want      int
	}{
		{-5400, 92}, // -54 dBm
		{-10000, 0}, // -100 dBm
		{-3000, 100},
		{-7500, 50}, // -75 dBm
	}
	for _, c := range cases {
		if got := signalToQuality(c.iwdSignal); got != c.want {
			t.Errorf("signalToQuality(%d) = %d, want %d", c.iwdSignal, got, c.want)
		}
	}
}

func TestIwdTypeToKeyMgmt(t *testing.T) {
	cases := map[string]wnet.KeyMgmt{
		"psk":   wnet.KeyWPAPSK,
		"open":  wnet.KeyNone,
		"8021x": wnet.KeyWPAEAP,
		"weird": wnet.KeyNone,
	}
	for typ, want := range cases {
		got := iwdTypeToKeyMgmt(typ)
		if len(got) != 1 || got[0] != want {
			t.Errorf("iwdTypeToKeyMgmt(%q) = %v, want [%v]", typ, got, want)
		}
	}
}

func TestMapState(t *testing.T) {
	cases := map[string]wnet.ConnState{
		"connected":     wnet.StateConnected,
		"roaming":       wnet.StateConnected,
		"connecting":    wnet.StateAssociating,
		"disconnecting": wnet.StateDisconnecting,
		"disconnected":  wnet.StateIdle,
		"":              wnet.StateUnspecified,
	}
	for s, want := range cases {
		if got := mapState(s); got != want {
			t.Errorf("mapState(%q) = %v, want %v", s, got, want)
		}
	}
}

func TestSecKindTypeRoundtrip(t *testing.T) {
	for _, k := range []wnet.SecurityKind{wnet.SecOpen, wnet.SecPSK, wnet.SecEnterprise} {
		if got := typeToSecKind(secKindToType(k)); got != k {
			t.Errorf("roundtrip kind %v -> %q -> %v", k, secKindToType(k), got)
		}
	}
}
