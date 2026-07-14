package tui

import (
	"context"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lesomnus/wfm/internal/wifi"
)

// statusTimeout bounds the connection-status lookup used to resolve an
// interface's assigned IP address.
const statusTimeout = 5 * time.Second

// ifaceStatusMsg carries the live status of interface ifID: the assigned IP
// address and the currently connected access point, if any.
type ifaceStatusMsg struct {
	ifID string
	addr string
	ap   *wifi.AccessPoint // the connected AP, enriched with live signal/bssid
	err  error
}

// ifaceStatusCmd resolves the IP address of the connection currently active on
// interface ifID by listing its connections and reading their live status. The
// address (CIDR mask stripped) of the first CONNECTED connection is returned;
// absence of a connection is not an error.
func (m model) ifaceStatusCmd(ifID string) tea.Cmd {
	parent, cc := m.ctx, m.cc
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, statusTimeout)
		defer cancel()

		cl := wifi.NewConnectionServiceClient(cc)
		req := &wifi.ConnectionListRequest{}
		ref := &wifi.InterfaceRef{}
		ref.SetId(ifID)
		req.SetInterface(ref)

		resp, err := cl.List(ctx, req)
		if err != nil {
			return ifaceStatusMsg{ifID: ifID, err: err}
		}
		for _, c := range resp.GetItems() {
			cref := &wifi.ConnectionRef{}
			cref.SetId(c.GetId())
			st, err := cl.GetStatus(ctx, cref)
			if err != nil {
				continue
			}
			if st.GetState() != wifi.ConnectionState_CONNECTION_STATE_CONNECTED {
				continue
			}
			return ifaceStatusMsg{
				ifID: ifID,
				addr: pickAddr(st.GetAddresses()),
				ap:   connectedAP(c, st),
			}
		}
		return ifaceStatusMsg{ifID: ifID}
	}
}

// connectedAP builds the access point to display for an active connection,
// preferring the connection's own AP and overlaying the live BSSID/signal from
// its status. Returns nil when there is nothing to show.
func connectedAP(c *wifi.Connection, st *wifi.ConnectionStatus) *wifi.AccessPoint {
	ap := c.GetAccessPoint()
	if ap == nil {
		if st.GetBssid() == "" && st.GetSignal() == 0 {
			return nil
		}
		ap = &wifi.AccessPoint{}
	}
	if b := st.GetBssid(); b != "" {
		ap.SetBssid(b)
	}
	if st.GetSignal() > 0 {
		ap.SetSignal(st.GetSignal())
	}
	// The AP id is what the scan result is matched against, so keep it aligned
	// with the BSSID the backend reports.
	if ap.GetId() == "" {
		ap.SetId(ap.GetBssid())
	}
	return ap
}

// pickAddr chooses an address to display, preferring IPv4, and strips the CIDR
// prefix length.
func pickAddr(addrs []string) string {
	if len(addrs) == 0 {
		return ""
	}
	pick := addrs[0]
	for _, a := range addrs {
		if strings.Contains(a, ".") { // IPv4
			pick = a
			break
		}
	}
	if i := strings.IndexByte(pick, '/'); i >= 0 {
		pick = pick[:i]
	}
	return pick
}

// spinnerTickMsg advances the scanning indicator animation.
type spinnerTickMsg struct{}

// spinnerInterval is the cadence of the scanning indicator.
const spinnerInterval = 100 * time.Millisecond

// spinnerTickCmd schedules the next spinner frame.
func spinnerTickCmd() tea.Cmd {
	return tea.Tick(spinnerInterval, func(time.Time) tea.Msg { return spinnerTickMsg{} })
}

// scanTickMsg fires the periodic re-scan of the selected interface.
type scanTickMsg struct{}

// scanInterval is how often the selected interface is re-scanned automatically.
const scanInterval = 60 * time.Second

// scanTickCmd schedules the next periodic scan.
func scanTickCmd() tea.Cmd {
	return tea.Tick(scanInterval, func(time.Time) tea.Msg { return scanTickMsg{} })
}

// interfacesLoadedMsg carries the result of listing wireless interfaces.
type interfacesLoadedMsg struct {
	items []*wifi.Interface
	err   error
}

// scanResultMsg carries the access points scanned on interface ifID. ifID lets
// Update discard results that arrive after the user moved to another interface.
type scanResultMsg struct {
	ifID  string
	items []*wifi.AccessPoint
	err   error
}

// listInterfacesCmd lists the wireless interfaces known to the server.
func (m model) listInterfacesCmd() tea.Cmd {
	ctx, cc := m.ctx, m.cc
	return func() tea.Msg {
		resp, err := wifi.NewInterfaceServiceClient(cc).List(ctx, &wifi.InterfaceListRequest{})
		if err != nil {
			return interfacesLoadedMsg{err: err}
		}
		return interfacesLoadedMsg{items: resp.GetItems()}
	}
}

// scanCmd scans the access points visible to interface ifID.
func (m model) scanCmd(ifID string) tea.Cmd {
	parent, cc := m.ctx, m.cc
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(parent, scanTimeout)
		defer cancel()

		req := &wifi.AccessPointScanRequest{}
		ref := &wifi.InterfaceRef{}
		ref.SetId(ifID)
		req.SetInterface(ref)

		resp, err := wifi.NewAccessPointServiceClient(cc).Scan(ctx, req)
		if err != nil {
			return scanResultMsg{ifID: ifID, err: err}
		}
		return scanResultMsg{ifID: ifID, items: resp.GetItems()}
	}
}
