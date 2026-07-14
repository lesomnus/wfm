package tui

import (
	"context"
	"sort"

	tea "github.com/charmbracelet/bubbletea"
	"google.golang.org/grpc"

	"github.com/lesomnus/wfm/internal/wifi"
)

// scrollOff keeps at least this many access-point rows visible ahead of the
// cursor in its direction of travel, so the next items are never hidden right at
// the viewport edge.
const scrollOff = 5

// model is the bubbletea state for the whole UI.
//
// The left panel shows the selected interface and the access points scanned on
// it (sorted by signal strength); the right panel shows the details of the
// highlighted access point. Tab cycles interfaces (re-scanning the newly
// selected one); up/down move the access-point selection; r forces a re-scan.
// The selected interface is also re-scanned periodically.
type model struct {
	ctx context.Context
	cc  grpc.ClientConnInterface

	width  int
	height int

	ifaces  []*wifi.Interface
	ifIndex int
	ifAddr  string            // IP assigned to the selected interface, if connected
	connAP  *wifi.AccessPoint // AP the selected interface is connected to, if any

	// profiles are the saved connection profiles, matched to access points by
	// SSID to flag which scanned networks are already known.
	profiles []*wifi.Profile

	// aps holds the scan result for the currently selected interface, sorted by
	// descending signal strength.
	aps     []*wifi.AccessPoint
	apIndex int
	top     int // index of the first access point rendered (scroll position)

	scanning bool  // a scan RPC is in flight for the current interface
	spinner  int   // scanning indicator frame
	err      error // last operation error, shown in the status line
}

func newModel(ctx context.Context, cc grpc.ClientConnInterface) model {
	return model{ctx: ctx, cc: cc}
}

// selectedIface returns the currently selected interface, or nil if none.
func (m model) selectedIface() *wifi.Interface {
	if m.ifIndex < 0 || m.ifIndex >= len(m.ifaces) {
		return nil
	}
	return m.ifaces[m.ifIndex]
}

// selectedAP returns the currently highlighted access point, or nil if none.
func (m model) selectedAP() *wifi.AccessPoint {
	if m.apIndex < 0 || m.apIndex >= len(m.aps) {
		return nil
	}
	return m.aps[m.apIndex]
}

// isConnected reports whether ap is the access point the selected interface is
// currently connected to, matching on id or BSSID.
func (m model) isConnected(ap *wifi.AccessPoint) bool {
	if m.connAP == nil || ap == nil {
		return false
	}
	if id := m.connAP.GetId(); id != "" && id == ap.GetId() {
		return true
	}
	if b := m.connAP.GetBssid(); b != "" && b == ap.GetBssid() {
		return true
	}
	return false
}

// profileFor returns the saved profile matching ap by SSID, or nil if none. APs
// with no SSID (hidden networks in a scan) never match.
func (m model) profileFor(ap *wifi.AccessPoint) *wifi.Profile {
	if ap == nil {
		return nil
	}
	ssid := ap.GetSsid()
	if ssid == "" {
		return nil
	}
	for _, p := range m.profiles {
		if p.GetSsid() == ssid {
			return p
		}
	}
	return nil
}

// triggerScan starts a scan of interface ifID. When clear is set the current
// access points are dropped (used when switching interface); otherwise they stay
// on screen while the refresh runs. It returns the scan RPC plus, unless the
// spinner is already animating, the spinner tick. A scan already in flight is
// left to finish (returns nil).
func (m *model) triggerScan(ifID string, clear bool) tea.Cmd {
	if m.scanning {
		return nil
	}
	m.scanning = true
	m.err = nil
	if clear {
		m.aps = nil
		m.apIndex = 0
		m.top = 0
		m.connAP = nil
	}
	return tea.Batch(m.scanCmd(ifID), spinnerTickCmd())
}

// visibleRows is the number of access-point rows the list can show, mirroring
// the layout math in View so scroll adjustments stay in sync with rendering.
func (m model) visibleRows() int {
	bodyH := m.height - 1 // status bar
	if bodyH < 3 {
		bodyH = 3
	}
	// minus interface header + divider + column header.
	rows := bodyH - 3
	if rows < 1 {
		rows = 1
	}
	return rows
}

// scroll adjusts the scroll position so the cursor stays visible and, when
// moving (dir is +1 down / -1 up), keeps scrollOff rows of context ahead of it
// in that direction. dir 0 (resize, new scan) only guarantees visibility. rows
// is the number of visible list rows.
func (m *model) scroll(rows, dir int) {
	n := len(m.aps)
	if n == 0 {
		m.top = 0
		return
	}
	if rows < 1 {
		rows = 1
	}
	if m.apIndex < 0 {
		m.apIndex = 0
	}
	if m.apIndex > n-1 {
		m.apIndex = n - 1
	}

	off := scrollOff
	if off > rows-1 { // a margin can never exceed the viewport
		off = rows - 1
	}

	switch {
	case dir > 0: // moving down: reveal up to off rows below the cursor
		want := m.apIndex + off
		if want > n-1 {
			want = n - 1
		}
		if m.top+rows-1 < want {
			m.top = want - rows + 1
		}
	case dir < 0: // moving up: reveal up to off rows above the cursor
		want := m.apIndex - off
		if want < 0 {
			want = 0
		}
		if m.top > want {
			m.top = want
		}
	}

	// Always keep the cursor within the viewport.
	if m.apIndex < m.top {
		m.top = m.apIndex
	}
	if m.apIndex > m.top+rows-1 {
		m.top = m.apIndex - rows + 1
	}
	// Clamp to the valid range.
	if maxTop := n - rows; m.top > maxTop {
		m.top = maxTop
	}
	if m.top < 0 {
		m.top = 0
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.listInterfacesCmd(), m.listProfilesCmd(), scanTickCmd())
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.scroll(m.visibleRows(), 0)
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case interfacesLoadedMsg:
		m.err = msg.err
		m.ifaces = msg.items
		if m.ifIndex >= len(m.ifaces) {
			m.ifIndex = 0
		}
		if it := m.selectedIface(); it != nil {
			m.ifAddr = ""
			return m, tea.Batch(m.triggerScan(it.GetId(), true), m.ifaceStatusCmd(it.GetId()))
		}
		return m, nil

	case ifaceStatusMsg:
		// Drop results for an interface that is no longer selected.
		if it := m.selectedIface(); it == nil || it.GetId() != msg.ifID {
			return m, nil
		}
		m.ifAddr = msg.addr
		m.connAP = msg.ap
		// While the scan is still loading, show the active connection right away
		// instead of an empty list.
		if msg.ap != nil && m.scanning && len(m.aps) == 0 {
			m.aps = []*wifi.AccessPoint{msg.ap}
			m.apIndex = 0
			m.top = 0
		}
		return m, nil

	case spinnerTickMsg:
		if !m.scanning {
			return m, nil
		}
		m.spinner++
		return m, spinnerTickCmd()

	case scanTickMsg:
		// Reschedule, refresh the profile list, and refresh the current interface
		// if idle.
		cmds := []tea.Cmd{scanTickCmd(), m.listProfilesCmd()}
		if it := m.selectedIface(); it != nil {
			if c := m.triggerScan(it.GetId(), false); c != nil {
				cmds = append(cmds, c)
			}
			cmds = append(cmds, m.ifaceStatusCmd(it.GetId()))
		}
		return m, tea.Batch(cmds...)

	case profilesLoadedMsg:
		// A profile-list failure is non-fatal: keep the previous list rather than
		// clobbering the status line used for scan/connection errors.
		if msg.err == nil {
			m.profiles = msg.items
		}
		return m, nil

	case scanResultMsg:
		// Ignore results for an interface the user already navigated away from.
		if it := m.selectedIface(); it == nil || it.GetId() != msg.ifID {
			return m, nil
		}
		m.scanning = false
		m.err = msg.err
		m.applyScan(msg.items)
		return m, nil
	}

	return m, nil
}

// applyScan installs a fresh scan result: sorted by descending signal, with the
// prior selection followed by BSSID so a periodic refresh does not move the
// cursor out from under the user.
func (m *model) applyScan(items []*wifi.AccessPoint) {
	selID := ""
	if ap := m.selectedAP(); ap != nil {
		selID = ap.GetId()
	}

	sort.SliceStable(items, func(i, j int) bool {
		return items[i].GetSignal() > items[j].GetSignal()
	})
	m.aps = items

	idx := m.apIndex
	if selID != "" {
		for i, ap := range items {
			if ap.GetId() == selID {
				idx = i
				break
			}
		}
	}
	if idx > len(items)-1 {
		idx = len(items) - 1
	}
	if idx < 0 {
		idx = 0
	}
	m.apIndex = idx
	m.scroll(m.visibleRows(), 0)
}

func (m model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "ctrl+c", "q":
		return m, tea.Quit

	case "tab":
		if len(m.ifaces) == 0 {
			return m, nil
		}
		m.ifIndex = (m.ifIndex + 1) % len(m.ifaces)
		m.ifAddr = ""
		id := m.selectedIface().GetId()
		return m, tea.Batch(m.triggerScan(id, true), m.ifaceStatusCmd(id))

	case "r":
		if it := m.selectedIface(); it != nil {
			return m, m.triggerScan(it.GetId(), false)
		}
		return m, nil

	case "up", "k":
		if m.apIndex > 0 {
			m.apIndex--
			m.scroll(m.visibleRows(), -1)
		}
		return m, nil

	case "down", "j":
		if m.apIndex < len(m.aps)-1 {
			m.apIndex++
			m.scroll(m.visibleRows(), +1)
		}
		return m, nil
	}

	return m, nil
}
