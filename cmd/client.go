package cmd

import (
	"github.com/google/uuid"
	"github.com/lesomnus/xli"
	"github.com/lesomnus/xli/flg"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/lesomnus/wfm/internal/wifi"
)

const defaultServer = "127.0.0.1:50051"

func ptr[T any](v T) *T { return &v }

func serverAddr(cmd *xli.Command) string {
	if v, ok := flg.Find[string, *xli.Command](cmd, "server"); ok && v != "" {
		return v
	}
	return defaultServer
}

func dial(cmd *xli.Command) (*grpc.ClientConn, error) {
	return grpc.NewClient(serverAddr(cmd), grpc.WithTransportCredentials(insecure.NewCredentials()))
}

func uuidStr(b []byte) string {
	u, err := uuid.FromBytes(b)
	if err != nil {
		return ""
	}
	return u.String()
}

func uuidBytes(s string) ([]byte, error) {
	u, err := uuid.Parse(s)
	if err != nil {
		return nil, err
	}
	b, _ := u.MarshalBinary()
	return b, nil
}

var keyMgmtShort = map[wifi.KeyManagement]string{
	wifi.KeyManagement_KEY_MANAGEMENT_NONE:    "open",
	wifi.KeyManagement_KEY_MANAGEMENT_WPA_PSK: "wpa-psk",
	wifi.KeyManagement_KEY_MANAGEMENT_SAE:     "sae",
	wifi.KeyManagement_KEY_MANAGEMENT_WPA_EAP: "eap",
	wifi.KeyManagement_KEY_MANAGEMENT_OWE:     "owe",
}

func kmStr(ks []wifi.KeyManagement) string {
	if len(ks) == 0 {
		return "-"
	}
	out := ""
	for i, k := range ks {
		if i > 0 {
			out += ","
		}
		if s, ok := keyMgmtShort[k]; ok {
			out += s
		} else {
			out += k.String()
		}
	}
	return out
}

var stateShort = map[wifi.ConnectionState]string{
	wifi.ConnectionState_CONNECTION_STATE_UNSPECIFIED:    "unspecified",
	wifi.ConnectionState_CONNECTION_STATE_IDLE:           "idle",
	wifi.ConnectionState_CONNECTION_STATE_ASSOCIATING:    "associating",
	wifi.ConnectionState_CONNECTION_STATE_AUTHENTICATING: "authenticating",
	wifi.ConnectionState_CONNECTION_STATE_CONFIGURING:    "configuring",
	wifi.ConnectionState_CONNECTION_STATE_CONNECTED:      "connected",
	wifi.ConnectionState_CONNECTION_STATE_FAILED:         "failed",
	wifi.ConnectionState_CONNECTION_STATE_DISCONNECTING:  "disconnecting",
}

func stateStr(s wifi.ConnectionState) string {
	if v, ok := stateShort[s]; ok {
		return v
	}
	return s.String()
}
