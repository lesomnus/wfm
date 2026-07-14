package cmd

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lesomnus/xli"
	"github.com/lesomnus/xli/flg"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"

	"github.com/lesomnus/wfm/internal/server"
	"github.com/lesomnus/wfm/internal/wifi"
)

// defaultSocketPath is the default unix-domain socket the server listens on and
// clients connect to. Overridable via WFM_SOCKET.
func defaultSocketPath() string {
	if v := os.Getenv("WFM_SOCKET"); v != "" {
		return v
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		home = os.TempDir()
	}
	return filepath.Join(home, ".wfm", "wfm.sock")
}

// serverAddr returns the explicitly requested server address, or "" when the
// user did not set --server (meaning: use the default socket, else in-process).
func serverAddr(cmd *xli.Command) string {
	if v, ok := flg.Find[string](cmd, "server"); ok {
		return v
	}
	return ""
}

// dialTarget turns a user-supplied address into a gRPC target, treating
// filesystem-looking values as unix sockets.
func dialTarget(addr string) string {
	switch {
	case strings.HasPrefix(addr, "unix:"):
		return addr
	case strings.HasPrefix(addr, "/"), strings.HasPrefix(addr, "./"), strings.HasPrefix(addr, "~"):
		return "unix://" + addr
	default:
		return addr
	}
}

// clientConn couples a gRPC client connection with an optional teardown for an
// in-process server started on its behalf. It satisfies grpc.ClientConnInterface
// through the embedded *grpc.ClientConn, so call sites use it like a normal conn.
type clientConn struct {
	*grpc.ClientConn
	stop func()
}

func (c *clientConn) Close() error {
	err := c.ClientConn.Close()
	if c.stop != nil {
		c.stop()
	}
	return err
}

// dial connects to a running server, preferring an explicit --server address,
// then the default unix socket if a server is listening there. If neither is
// available it spins up an in-process server over an in-memory bufconn so a
// single invocation can serve itself.
func dial(cmd *xli.Command) (*clientConn, error) {
	if addr := serverAddr(cmd); addr != "" {
		cc, err := grpc.NewClient(dialTarget(addr), grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return nil, err
		}
		return &clientConn{ClientConn: cc}, nil
	}

	sock := defaultSocketPath()
	if isListening(sock) {
		cc, err := grpc.NewClient("unix://"+sock, grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			return nil, err
		}
		return &clientConn{ClientConn: cc}, nil
	}

	return dialInProc(cmd)
}

// isListening reports whether a server currently accepts connections on the
// given unix socket, so a stale socket file does not fool the client.
func isListening(sock string) bool {
	c, err := net.DialTimeout("unix", sock, 200*time.Millisecond)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

// dialInProc starts a server backed by the local wifi backend and returns a
// client wired to it over a bufconn; closing the conn tears both down.
func dialInProc(cmd *xli.Command) (*clientConn, error) {
	name, _ := flg.Find[string](cmd, "backend")
	if name == "" {
		name = detectBackend()
	}

	b, err := newBackend(name)
	if err != nil {
		return nil, err
	}

	lis := bufconn.Listen(1 << 20)
	s := grpc.NewServer()
	server.Register(s, b)
	go s.Serve(lis)

	cc, err := grpc.NewClient("passthrough:///bufnet",
		grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) {
			return lis.DialContext(ctx)
		}),
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		s.Stop()
		b.Close()
		return nil, err
	}

	return &clientConn{ClientConn: cc, stop: func() {
		s.Stop()
		b.Close()
	}}, nil
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
