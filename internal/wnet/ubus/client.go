// Package ubus is a wnet.Backend that controls wifi on a remote OpenWrt node
// over ubus exposed as JSON-RPC by rpcd + uhttpd (the uhttpd-mod-ubus module),
// reached at an HTTP(S) endpoint such as http://<node>/ubus.
//
// Unlike the local backends (nmcli, nmdbus, iwd) this one talks to a different
// host, so it fills the neutral domain fields (Interface.Mac, AP.KeyMgmt, ...)
// from the node's own ubus objects — iwinfo for radios/scan/association and uci
// for saved wifi-iface profiles — never from the machine wfm runs on. It is
// therefore never autodetected; select it explicitly with --backend ubus and
// configure the endpoint/credentials in the `ubus` config section.
//
// OpenWrt is AP-centric; wfm's station model applies to a wifi-iface in `sta`
// mode. Capabilities OpenWrt cannot express in these neutral terms (per-profile
// static IP, enterprise security, a per-interface radio toggle) are reported as
// wnet.ErrUnsupported and surfaced by the server as codes.Unimplemented.
package ubus

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/lesomnus/wfm/internal/wnet"
)

// anonSession is the all-zero ubus session id used before authentication; only
// `session login` is permitted under it.
const anonSession = "00000000000000000000000000000000"

// ubus status codes (subset) returned as the first element of a call result.
const (
	ubusOK              = 0
	ubusInvalidCommand  = 1
	ubusInvalidArgument = 2
	ubusMethodNotFound  = 3
	ubusNotFound        = 4
	ubusNoData          = 5
	ubusPermissionDenie = 6
	ubusTimeout         = 7
	ubusNotSupported    = 8
)

// Client is a minimal ubus-over-HTTP JSON-RPC client. It logs in lazily and
// re-logs in once when a call is refused because the session expired. It is safe
// for concurrent use.
type Client struct {
	endpoint string
	user     string
	pass     string
	http     *http.Client

	id atomic.Int64

	mu      sync.Mutex // guards session
	session string
}

// Options configures a Client / Backend. Endpoint is required. Password is the
// resolved secret (the caller reads any password file); HTTP overrides the HTTP
// client (tests inject an httptest transport).
type Options struct {
	Endpoint string
	Username string
	Password string
	Insecure bool // skip TLS verification (self-signed uhttpd certs)
	HTTP     *http.Client

	// Radio is the wifi-device a new station profile is attached to; empty
	// means the first wifi-device found. Network is the /etc/config/network
	// interface a station is bound to for DHCP (default "wwan").
	Radio   string
	Network string
}

// NewClient builds a Client from options.
func NewClient(o Options) (*Client, error) {
	if o.Endpoint == "" {
		return nil, fmt.Errorf("ubus endpoint is required")
	}
	hc := o.HTTP
	if hc == nil {
		tr := &http.Transport{}
		if o.Insecure {
			tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
		}
		hc = &http.Client{Transport: tr, Timeout: 30 * time.Second}
	}
	return &Client{
		endpoint: o.Endpoint,
		user:     o.Username,
		pass:     o.Password,
		http:     hc,
	}, nil
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
}

type rpcResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// rawCall performs one JSON-RPC `call` and returns the ubus status code and the
// data payload (the second element of the [code, data] result), if any.
func (c *Client) rawCall(ctx context.Context, session, object, method string, args any) (int, json.RawMessage, error) {
	if args == nil {
		args = struct{}{} // ubus expects a table, never null
	}
	body, err := json.Marshal(rpcRequest{
		JSONRPC: "2.0",
		ID:      c.id.Add(1),
		Method:  "call",
		Params:  []any{session, object, method, args},
	})
	if err != nil {
		return 0, nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("ubus request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, nil, fmt.Errorf("ubus http %d", resp.StatusCode)
	}

	var rr rpcResponse
	if err := json.NewDecoder(resp.Body).Decode(&rr); err != nil {
		return 0, nil, fmt.Errorf("decode ubus response: %w", err)
	}
	if rr.Error != nil {
		return 0, nil, fmt.Errorf("ubus jsonrpc error %d: %s", rr.Error.Code, rr.Error.Message)
	}

	// result is [status] or [status, data].
	var parts []json.RawMessage
	if err := json.Unmarshal(rr.Result, &parts); err != nil {
		return 0, nil, fmt.Errorf("decode ubus result: %w", err)
	}
	if len(parts) == 0 {
		return 0, nil, fmt.Errorf("empty ubus result")
	}
	var code int
	if err := json.Unmarshal(parts[0], &code); err != nil {
		return 0, nil, fmt.Errorf("decode ubus status: %w", err)
	}
	var data json.RawMessage
	if len(parts) > 1 {
		data = parts[1]
	}
	return code, data, nil
}

// session returns a valid session id, logging in on first use.
func (c *Client) sessionID(ctx context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.session == "" {
		if err := c.login(ctx); err != nil {
			return "", err
		}
	}
	return c.session, nil
}

// relogin re-authenticates when the caller's session was rejected, unless
// another goroutine already refreshed it.
func (c *Client) relogin(ctx context.Context, stale string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.session != stale {
		return c.session, nil
	}
	c.session = ""
	if err := c.login(ctx); err != nil {
		return "", err
	}
	return c.session, nil
}

// login authenticates and stores the session. The caller must hold c.mu.
func (c *Client) login(ctx context.Context) error {
	code, data, err := c.rawCall(ctx, anonSession, "session", "login", map[string]any{
		"username": c.user,
		"password": c.pass,
	})
	if err != nil {
		return fmt.Errorf("ubus login: %w", err)
	}
	if code != ubusOK {
		return fmt.Errorf("ubus login refused (status %d): check credentials and rpcd ACL", code)
	}
	var out struct {
		Session string `json:"ubus_rpc_session"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return fmt.Errorf("ubus login: decode session: %w", err)
	}
	if out.Session == "" {
		return fmt.Errorf("ubus login: no session returned")
	}
	c.session = out.Session
	return nil
}

// Call invokes object.method with args and decodes the data payload into out
// (which may be nil). Session expiry is retried once transparently.
func (c *Client) Call(ctx context.Context, object, method string, args, out any) error {
	s, err := c.sessionID(ctx)
	if err != nil {
		return err
	}
	code, data, err := c.rawCall(ctx, s, object, method, args)
	if err != nil {
		return err
	}
	if code == ubusPermissionDenie {
		// The session likely expired; re-authenticate once and retry.
		if s, err = c.relogin(ctx, s); err != nil {
			return err
		}
		code, data, err = c.rawCall(ctx, s, object, method, args)
		if err != nil {
			return err
		}
	}
	if err := statusErr(object+"."+method, code); err != nil {
		return err
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

// statusErr maps a non-zero ubus status to a wnet error where it has a neutral
// meaning, so the server can translate it (NotFound / Unimplemented).
func statusErr(action string, code int) error {
	switch code {
	case ubusOK, ubusNoData:
		return nil
	case ubusNotFound, ubusMethodNotFound:
		return fmt.Errorf("%w: %s", wnet.ErrNotFound, action)
	case ubusNotSupported:
		return fmt.Errorf("%w: %s", wnet.ErrUnsupported, action)
	case ubusPermissionDenie:
		return fmt.Errorf("%s: permission denied (rpcd ACL)", action)
	case ubusInvalidArgument, ubusInvalidCommand:
		return fmt.Errorf("%s: invalid ubus request (status %d)", action, code)
	case ubusTimeout:
		return fmt.Errorf("%s: ubus timeout", action)
	default:
		return fmt.Errorf("%s: ubus status %d", action, code)
	}
}
