package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/lesomnus/wfm/internal/wifi"
)

// spinnerFrames animate the scanning indicator; idleBullet is shown when idle.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

const idleBullet = "●"

// Access-point table column widths (SSID takes the remaining space). The SIGNAL
// column is exactly wide enough for "NNN gg": a 3-digit strength, a space, and
// the 2-cell braille gauge.
const (
	colChan = 4
	colSig  = 6
	colSec  = 8
	// colFixed is every non-SSID column plus the single-space separators.
	colFixed = colChan + colSig + colSec + 3
)

var (
	rightPanelStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			Padding(0, 1)

	leftPanelStyle = lipgloss.NewStyle().PaddingRight(1)

	headerStyle  = lipgloss.NewStyle().Bold(true)
	dividerStyle = lipgloss.NewStyle().Faint(true)
	colHeadStyle = lipgloss.NewStyle().Faint(true)
	faintStyle   = lipgloss.NewStyle().Faint(true)
	labelStyle   = lipgloss.NewStyle().Faint(true)

	selectedStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("0")).
			Background(lipgloss.Color("6"))
)

func (m model) View() string {
	if m.width == 0 || m.height == 0 {
		return "loading…"
	}

	// Reserve the bottom line for the status/help bar.
	bodyH := m.height - 1
	if bodyH < 3 {
		bodyH = 3
	}

	// The right (detail) panel is kept compact but wide enough for a BSSID; the
	// left panel takes the rest.
	rightOuter := m.width / 3
	if rightOuter < 32 {
		rightOuter = 32
	}
	if rightOuter > 40 {
		rightOuter = 40
	}
	if rightOuter > m.width/2 {
		rightOuter = m.width / 2
	}
	leftOuter := m.width - rightOuter

	// Left panel: no border, one column of right padding as a gutter.
	textLeft := leftOuter - 1
	// Right panel: lipgloss Width/Height cover the padded box (not the border),
	// and our text is a further horizontal padding(2) narrower.
	rightBox := rightOuter - 2
	rightInnerH := bodyH - 2
	textRight := rightOuter - 4
	for _, v := range []*int{&textLeft, &rightBox, &rightInnerH, &textRight} {
		if *v < 1 {
			*v = 1
		}
	}

	left := leftPanelStyle.Width(leftOuter).Height(bodyH).Render(m.leftContent(textLeft, bodyH))
	right := rightPanelStyle.Width(rightBox).Height(rightInnerH).Render(m.rightContent(textRight))

	body := lipgloss.JoinHorizontal(lipgloss.Top, left, right)
	return body + "\n" + m.statusBar(m.width)
}

// leftContent renders the interface header, a divider, and the scanned
// access-point table for the selected interface.
func (m model) leftContent(w, h int) string {
	var b strings.Builder

	// Header: "<indicator> <interface>" on the left, "idx/total" right-aligned.
	indicator := idleBullet
	if m.scanning {
		indicator = spinnerFrames[m.spinner%len(spinnerFrames)]
	}
	name := "(no interface)"
	left := headerStyle.Render(indicator + " " + name)
	if it := m.selectedIface(); it != nil {
		left = headerStyle.Render(indicator + " " + it.GetId())
		// Address items (MAC, IP if the backend ever reports one) shown to the
		// right of the name, each separated by a bullet.
		if meta := ifaceMeta(it, m.ifAddr); meta != "" {
			left += faintStyle.Render("  " + meta)
		}
	}
	counter := ""
	if n := len(m.ifaces); n > 0 {
		counter = fmt.Sprintf("%d/%d", m.ifIndex+1, n)
	}
	b.WriteString(alignEnds(left, counter, w))
	b.WriteString("\n")
	b.WriteString(dividerStyle.Render(strings.Repeat("─", w)))
	b.WriteString("\n")

	// Rows available = height - header(1) - divider(1).
	rows := h - 2
	if rows < 1 {
		rows = 1
	}
	b.WriteString(m.apTable(w, rows))

	return b.String()
}

// apTable renders a column header plus the visible window of access points. The
// window starts at m.top; the cursor is guaranteed visible even if m.top is
// stale (e.g. right after a resize).
func (m model) apTable(w, rows int) string {
	if len(m.aps) == 0 {
		if m.scanning {
			return faintStyle.Render("scanning…")
		}
		return faintStyle.Render("no access points")
	}

	ssidW := w - colFixed
	if ssidW < 4 {
		ssidW = 4
	}

	head := colHeadStyle.Render(clip(apLine(ssidW, "SSID", "CHAN", "SIGNAL", "SECURITY"), w))
	lines := []string{head}

	// One line is used by the column header.
	listRows := rows - 1
	if listRows < 1 {
		listRows = 1
	}
	start := m.top
	if start > m.apIndex {
		start = m.apIndex
	}
	if m.apIndex >= start+listRows {
		start = m.apIndex - listRows + 1
	}
	if start < 0 {
		start = 0
	}
	end := start + listRows
	if end > len(m.aps) {
		end = len(m.aps)
	}

	for i := start; i < end; i++ {
		ap := m.aps[i]
		ssid := ap.GetSsid()
		if ssid == "" {
			ssid = "--"
		}
		selected := i == m.apIndex
		sig := fmt.Sprintf("%3d %s", ap.GetSignal(), signalMeter(ap.GetSignal()))
		// Color the strength + gauge by level. The selected row is left plain so
		// the selection background stays legible.
		if !selected {
			sig = lipgloss.NewStyle().Foreground(signalColor(ap.GetSignal())).Render(sig)
		}
		line := apLine(ssidW,
			ssid,
			channelStr(ap.GetFrequency()),
			sig,
			securityLabel(ap.GetKeyManagement()),
		)
		if selected {
			lines = append(lines, selectedStyle.Width(w).Render(clip(line, w)))
		} else {
			lines = append(lines, clip(line, w))
		}
	}
	return strings.Join(lines, "\n")
}

// apLine lays out one row: SSID (left), CHAN (right-aligned), SIGNAL (strength
// plus braille gauge, preformatted to colSig), and SECURITY. The same layout
// renders the column header.
func apLine(ssidW int, ssid, chn, sig, sec string) string {
	return padRight(ssid, ssidW) + " " +
		padLeft(chn, colChan) + " " +
		padRight(sig, colSig) + " " +
		padRight(sec, colSec)
}

// rightContent renders the details of the highlighted access point.
func (m model) rightContent(w int) string {
	ap := m.selectedAP()
	if ap == nil {
		return faintStyle.Render("no access point selected")
	}

	ssid := ap.GetSsid()
	if ssid == "" {
		ssid = "<hidden>"
	}

	var b strings.Builder
	b.WriteString(headerStyle.Render(truncate(ssid, w)))
	b.WriteString("\n\n")

	writeField(&b, "BSSID", ap.GetBssid())
	sig := fmt.Sprintf("%d  %s", ap.GetSignal(), signalMeter(ap.GetSignal()))
	writeField(&b, "Signal", lipgloss.NewStyle().Foreground(signalColor(ap.GetSignal())).Render(sig))
	writeField(&b, "Channel", channelStr(ap.GetFrequency()))
	writeField(&b, "Frequency", fmt.Sprintf("%d MHz", ap.GetFrequency()))
	writeField(&b, "Security", kmStr(ap.GetKeyManagement()))
	// The backend commonly uses the BSSID as the AP id; only show it when it
	// carries something extra.
	if id := ap.GetId(); id != "" && id != ap.GetBssid() {
		writeField(&b, "ID", id)
	}

	return b.String()
}

func writeField(b *strings.Builder, label, value string) {
	b.WriteString(labelStyle.Render(fmt.Sprintf("%-10s", label)))
	b.WriteString(value)
	b.WriteString("\n")
}

func (m model) statusBar(w int) string {
	if m.err != nil {
		return clip(faintStyle.Render("error: "+m.err.Error()), w)
	}
	help := "tab: next interface   ↑/↓: select AP   r: rescan   q: quit"
	return clip(faintStyle.Render(help), w)
}

// ifaceMeta joins an interface's address items — its MAC and, when connected,
// the assigned IP — with bullet separators, skipping any that are empty.
func ifaceMeta(it *wifi.Interface, ip string) string {
	var parts []string
	if mac := it.GetMac(); mac != "" {
		parts = append(parts, mac)
	}
	if ip != "" {
		parts = append(parts, ip)
	}
	return strings.Join(parts, " • ")
}

// channelStr converts a channel frequency in MHz to its channel number, or "--"
// when it falls outside the known 2.4/5/6 GHz plans.
func channelStr(freq uint32) string {
	var ch int
	switch {
	case freq == 2484:
		ch = 14
	case freq >= 2412 && freq <= 2472:
		ch = int((freq - 2407) / 5)
	case freq >= 5160 && freq <= 5885:
		ch = int((freq - 5000) / 5)
	case freq >= 5955 && freq <= 7115: // 6 GHz (Wi-Fi 6E)
		ch = int((freq - 5950) / 5)
	default:
		return "--"
	}
	return fmt.Sprintf("%d", ch)
}

// signalMeters renders signal strength as a rising staircase built from two
// braille cells (a 4-column, 4-row dot grid). Column j is filled to height j+1
// once the level reaches it, so the gauge grows both taller and wider with
// strength: ⠀⠀ ⡀⠀ ⣠⠀ ⣠⡆ ⣠⣾ for levels 0–4.
var signalMeters = [5]string{"⠀⠀", "⡀⠀", "⣠⠀", "⣠⡆", "⣠⣾"}

// signalPalette is a 10-step red→yellow→green gradient indexed by signal/10, so
// the color tracks strength far more finely than the 5-level braille gauge.
var signalPalette = [10]lipgloss.Color{
	"#FF0000", "#FF3800", "#FF7100", "#FFAA00", "#FFE200",
	"#E2FF00", "#A9FF00", "#71FF00", "#38FF00", "#00FF00",
}

// signalColor maps a 0–100 signal to its gradient color.
func signalColor(sig int32) lipgloss.Color {
	i := sig / 10
	if i < 0 {
		i = 0
	}
	if i > 9 {
		i = 9
	}
	return signalPalette[i]
}

// signalMeter maps a 0–100 signal to its braille strength gauge.
func signalMeter(sig int32) string {
	switch {
	case sig >= 80:
		return signalMeters[4]
	case sig >= 55:
		return signalMeters[3]
	case sig >= 30:
		return signalMeters[2]
	case sig >= 5:
		return signalMeters[1]
	default:
		return signalMeters[0]
	}
}

var keyMgmtShort = map[wifi.KeyManagement]string{
	wifi.KeyManagement_KEY_MANAGEMENT_NONE:    "open",
	wifi.KeyManagement_KEY_MANAGEMENT_WPA_PSK: "wpa-psk",
	wifi.KeyManagement_KEY_MANAGEMENT_SAE:     "sae",
	wifi.KeyManagement_KEY_MANAGEMENT_WPA_EAP: "eap",
	wifi.KeyManagement_KEY_MANAGEMENT_OWE:     "owe",
}

// kmStr is the verbose, protocol-accurate security string for the detail panel.
func kmStr(ks []wifi.KeyManagement) string {
	if len(ks) == 0 {
		return "-"
	}
	parts := make([]string, len(ks))
	for i, k := range ks {
		if s, ok := keyMgmtShort[k]; ok {
			parts[i] = s
		} else {
			parts[i] = k.String()
		}
	}
	return strings.Join(parts, ",")
}

var keyMgmtLabel = map[wifi.KeyManagement]string{
	wifi.KeyManagement_KEY_MANAGEMENT_NONE:    "open",
	wifi.KeyManagement_KEY_MANAGEMENT_WPA_PSK: "WPA2",
	wifi.KeyManagement_KEY_MANAGEMENT_SAE:     "WPA3",
	wifi.KeyManagement_KEY_MANAGEMENT_WPA_EAP: "802.1X",
	wifi.KeyManagement_KEY_MANAGEMENT_OWE:     "OWE",
}

// securityLabel is the short, nmcli-like security tag for the list column.
func securityLabel(ks []wifi.KeyManagement) string {
	if len(ks) == 0 {
		return "--"
	}
	var parts []string
	seen := map[string]bool{}
	for _, k := range ks {
		s, ok := keyMgmtLabel[k]
		if !ok {
			s = k.String()
		}
		if !seen[s] {
			seen[s] = true
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, "/")
}

// alignEnds places left at the start and right at the end of a field of width w.
func alignEnds(left, right string, w int) string {
	gap := w - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		left = truncate(left, w-lipgloss.Width(right)-1)
		gap = w - lipgloss.Width(left) - lipgloss.Width(right)
		if gap < 1 {
			gap = 1
		}
	}
	return left + strings.Repeat(" ", gap) + right
}

// padRight clips s to w cells then left-justifies it in a field of w cells.
func padRight(s string, w int) string {
	s = clip(s, w)
	if gap := w - lipgloss.Width(s); gap > 0 {
		s += strings.Repeat(" ", gap)
	}
	return s
}

// padLeft clips s to w cells then right-justifies it in a field of w cells.
func padLeft(s string, w int) string {
	s = clip(s, w)
	if gap := w - lipgloss.Width(s); gap > 0 {
		s = strings.Repeat(" ", gap) + s
	}
	return s
}

// truncate shortens s to at most w cells, adding an ellipsis when cut.
func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	if w == 1 {
		return "…"
	}
	r := []rune(s)
	for len(r) > 0 && lipgloss.Width(string(r))+1 > w {
		r = r[:len(r)-1]
	}
	return string(r) + "…"
}

// clip hard-limits s to w cells without adding an ellipsis.
func clip(s string, w int) string {
	if lipgloss.Width(s) <= w {
		return s
	}
	r := []rune(s)
	for len(r) > 0 && lipgloss.Width(string(r)) > w {
		r = r[:len(r)-1]
	}
	return string(r)
}
