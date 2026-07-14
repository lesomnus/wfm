package tui

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/lesomnus/wfm/internal/wifi"
)

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
const scanInterval = 10 * time.Second

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
