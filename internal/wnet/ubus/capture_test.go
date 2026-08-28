package ubus

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// capturingTransport tees each ubus response's data payload to a JSON file named
// for the (object, method) it answered, so a live run against a real OpenWrt
// node regenerates the testdata fixtures with real JSON. It is wired into
// TestUbusLive when WFM_TEST_UBUS_CAPTURE names a directory; point that at
// internal/wnet/ubus/testdata to overwrite the fixtures in place.
type capturingTransport struct {
	base http.RoundTripper
	dir  string
}

func captureHTTPClient(dir string) *http.Client {
	base := http.DefaultTransport
	return &http.Client{Transport: &capturingTransport{base: base, dir: dir}}
}

func (c *capturingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var reqBody []byte
	if req.Body != nil {
		reqBody, _ = io.ReadAll(req.Body)
		req.Body = io.NopCloser(bytes.NewReader(reqBody))
	}
	resp, err := c.base.RoundTrip(req)
	if err != nil {
		return resp, err
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body = io.NopCloser(bytes.NewReader(body))

	if name := fixtureFor(reqBody); name != "" {
		if data := extractData(body); len(data) > 0 {
			_ = os.MkdirAll(c.dir, 0o755)
			pretty := new(bytes.Buffer)
			if json.Indent(pretty, data, "", "\t") == nil {
				pretty.WriteByte('\n')
				_ = os.WriteFile(filepath.Join(c.dir, name), pretty.Bytes(), 0o644)
			}
		}
	}
	return resp, nil
}

// fixtureFor maps a ubus request to the stable fixture filename the parser tests
// load, or "" for calls that are not fixtures (e.g. uci writes, session login).
func fixtureFor(reqBody []byte) string {
	var r struct {
		Params []json.RawMessage `json:"params"`
	}
	if json.Unmarshal(reqBody, &r) != nil || len(r.Params) < 3 {
		return ""
	}
	var object, method string
	_ = json.Unmarshal(r.Params[1], &object)
	_ = json.Unmarshal(r.Params[2], &method)

	switch object + "." + method {
	case "iwinfo.devices":
		return "iwinfo_devices.json"
	case "iwinfo.scan":
		return "iwinfo_scan.json"
	case "iwinfo.info":
		return "iwinfo_info.json"
	case "network.wireless.status":
		return "network_wireless_status.json"
	case "uci.get":
		var a struct {
			Config string `json:"config"`
		}
		if len(r.Params) > 3 {
			_ = json.Unmarshal(r.Params[3], &a)
		}
		if a.Config == "wireless" {
			return "uci_get_wireless.json"
		}
	}
	if strings.HasPrefix(object, "network.interface.") && method == "status" {
		return "network_interface_status.json"
	}
	return ""
}

// extractData returns the data payload (second element) of a ubus [code, data]
// result.
func extractData(body []byte) json.RawMessage {
	var rr struct {
		Result json.RawMessage `json:"result"`
	}
	if json.Unmarshal(body, &rr) != nil {
		return nil
	}
	var parts []json.RawMessage
	if json.Unmarshal(rr.Result, &parts) != nil || len(parts) < 2 {
		return nil
	}
	return parts[1]
}
