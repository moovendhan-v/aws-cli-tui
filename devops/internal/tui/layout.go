package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"devops/internal/app"
	"devops/internal/safety"
	"devops/internal/tui/components"
	"devops/internal/tui/core"
	"devops/internal/tui/screens"
	"devops/internal/tui/theme"
)

type rect struct{ x, y, w, h int }

func (r rect) contains(x, y int) bool { return x >= r.x && x < r.x+r.w && y >= r.y && y < r.y+r.h }

// geom records where things were drawn so mouse events can be routed.
type geom struct {
	header       [2][2]int // x ranges of the profile and region labels on row 0
	search       rect
	nav, navBody rect
	navLine      map[int]int // nav content line → navigation index
	dropLine     map[int]int // nav content line → dropdown view index
	profileLine  int
	regionLine   int
	ws, wsBody   rect
	modal        rect // modal content area (below its title)
	modalOn      bool
}

func (m *Model) navWidth() int { return m.env.Deps.Config.UI.NavWidth }

func (m *Model) pads() (top, side, bottom int) {
	ui := m.env.Deps.Config.UI
	t, b := ui.PanelVPad()
	return t, ui.PanelPadding(), b
}

func (m *Model) View() string {
	if m.width < 60 || m.height < 16 {
		return theme.Warn.Render(fmt.Sprintf("Terminal too small (%dx%d). Resize to at least 60x16.", m.width, m.height))
	}
	m.g = geom{navLine: map[int]int{}, dropLine: map[int]int{}, profileLine: -1, regionLine: -1}
	header := m.header()
	searchBox := m.searchBar()
	footer := m.footer()
	bodyY := lipgloss.Height(header) + lipgloss.Height(searchBox)
	bodyH := m.height - bodyY - 1

	navW := m.navWidth()
	nav := m.nav(0, bodyY, navW, bodyH)
	ws := m.workspace(navW+1, bodyY, m.width-navW-1, bodyH)
	body := lipgloss.JoinHorizontal(lipgloss.Top, nav, " ", ws)
	return lipgloss.JoinVertical(lipgloss.Left, header, searchBox, body, footer)
}

// header: "DEVOPS  Profile: x | Account: y | Region: z        API 12  ●"
func (m *Model) header() string {
	e := m.env
	ctx := e.Ctx()
	acct := ctx.AccountID
	switch {
	case e.Identifying:
		acct = e.Spinner() + " verifying"
	case e.IdentityErr != nil:
		acct = theme.Bad.Background(theme.Bar).Render("✗ " + string(safety.Classify(e.IdentityErr)))
	case acct == "":
		acct = "-"
	}
	bar := theme.HeaderBar
	sep := bar.Foreground(theme.Muted).Render(" | ")
	brand := bar.Render(" ") + theme.Brand.Background(theme.Bar).Render("DEVOPS") + bar.Render("   ")
	prof := bar.Render("Profile: " + ctx.ProfileLabel() + " ▾")
	left := brand + prof
	m.g.header[0] = [2]int{lipgloss.Width(brand), lipgloss.Width(left)}
	left += sep + bar.Render("Account: ") + bar.Render(acct) + sep
	reg := bar.Render("Region: " + ctx.Region + " ▾")
	m.g.header[1] = [2]int{lipgloss.Width(left), lipgloss.Width(left) + lipgloss.Width(reg)}
	left += reg
	if e.Prod() {
		left += bar.Render("  ") + theme.ProdBadge.Render("PROD")
	}

	dot := theme.OK.Background(theme.Bar)
	switch {
	case e.IdentityErr != nil:
		dot = theme.Bad.Background(theme.Bar)
	case e.Identifying:
		dot = theme.Warn.Background(theme.Bar)
	case !e.Pulse():
		dot = lipgloss.NewStyle().Foreground(lipgloss.Color("#2a6b37")).Background(theme.Bar)
	}
	total, billed := e.Deps.Meter.Totals()
	api := fmt.Sprintf("API %d", total)
	if billed > 0 {
		api += fmt.Sprintf(" · $%d", billed)
	}
	right := bar.Foreground(theme.Muted).Render(api + "  ")
	if n := e.Deps.Exec.RunningCount(); n > 0 {
		right += bar.Foreground(theme.Yellow).Render(fmt.Sprintf("%s %d running  ", e.Spinner(), n))
	}
	right += dot.Render("●") + bar.Render(" ")
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		return components.Truncate(left, m.width)
	}
	return left + bar.Render(strings.Repeat(" ", gap)) + right
}

func (m *Model) searchBar() string {
	border := theme.Border
	if m.focus == focusSearch {
		border = theme.Accent
	}
	_, side, _ := m.pads()
	content := theme.Prompt.Render("Search: > ") + m.search.View()
	if m.focus != focusSearch {
		content = theme.Prompt.Render("Search: > ") + theme.Dim.Render("click here, press / or Ctrl+K — actions, screens and resources")
	}
	m.g.search = rect{0, 1, m.width, 3}
	return theme.PanelBox(border, 0, side, 0).Width(m.width - 2).Render(components.Truncate(content, m.width-2-2*side))
}

const switcherLines = 6

func (m *Model) nav(x, y, w, h int) string {
	top, side, bottom := m.pads()
	side = min(side, 2)
	cw, ch := w-2-2*side, h-2-top-bottom
	m.g.nav = rect{x, y, w, h}
	m.g.navBody = rect{x + 1 + side, y + 1 + top, cw, ch}

	itemsH := max(3, ch-switcherLines)
	var lines []string
	if m.drop != nil {
		s, hit := m.drop.render(cw, itemsH)
		lines = strings.Split(s, "\n")
		m.g.dropLine = hit
	} else {
		lines = m.navItems(cw, itemsH)
	}
	for len(lines) < itemsH {
		lines = append(lines, "")
	}
	lines = lines[:itemsH]

	ctx := m.env.Ctx()
	lines = append(lines, theme.Dim.Render(strings.Repeat("─", cw)))
	lines = append(lines, theme.Dim.Render("PROFILE"))
	m.g.profileLine = len(lines)
	lines = append(lines, theme.Select(ctx.ProfileLabel(), cw, m.drop != nil && m.drop.kind == "profile"))
	lines = append(lines, theme.Dim.Render("REGION"))
	m.g.regionLine = len(lines)
	lines = append(lines, theme.Select(ctx.Region, cw, m.drop != nil && m.drop.kind == "region"))

	border := theme.Border
	if m.focus == focusNav || m.drop != nil {
		border = theme.Accent
	}
	return theme.PanelBox(border, top, side, bottom).Width(w - 2).Height(h - 2).
		Render(components.Fit(strings.Join(lines, "\n"), cw, ch))
}

// navItems renders the navigation list, scrolled to keep the selection visible.
func (m *Model) navItems(w, h int) []string {
	type entry struct {
		text string
		idx  int
	}
	running := m.env.Deps.Exec.RunningCount()
	var all []entry
	for i, n := range app.Navigation {
		label := n.Icon + "  " + n.Label
		if n.ID == "activity" && running > 0 {
			label = fmt.Sprintf("%s (%d)", label, running)
		}
		line := components.Pad(" "+label, w)
		switch {
		case i == m.navIdx && m.focus == focusNav:
			line = theme.NavActive.Render(line)
		case i == m.navIdx:
			line = lipgloss.NewStyle().Background(theme.Bar).Foreground(theme.Blue).Bold(true).Render(line)
		default:
			line = theme.NavItem.Render(line)
		}
		all = append(all, entry{line, i})
		if n.ID == "builds" || n.ID == "terminals" {
			all = append(all, entry{"", -1})
		}
	}
	start := 0
	for i, en := range all {
		if en.idx == m.navIdx && i >= h {
			start = i - h + 1
		}
	}
	var out []string
	for i := start; i < len(all) && len(out) < h; i++ {
		if all[i].idx >= 0 {
			m.g.navLine[len(out)] = all[i].idx
		}
		out = append(out, all[i].text)
	}
	return out
}

func (m *Model) workspace(x, y, w, h int) string {
	e := m.env
	top, side, bottom := m.pads()
	iw, ih := w-2-2*side, h-2-top-bottom
	m.g.ws = rect{x, y, w, h}
	m.g.wsBody = rect{x + 1 + side, y + 1 + top, iw, ih}
	m.g.modalOn = false

	var content string
	switch {
	case m.modal != nil:
		content = m.modalView(iw, ih)
	case m.focus == focusSearch:
		content = m.palette.view(e, m.search.Value(), iw, ih)
	default:
		content = m.current().View(e, iw, ih)
	}
	border := theme.Border
	if m.focus == focusWorkspace && m.modal == nil && m.drop == nil {
		border = theme.Accent
	}
	return theme.PanelBox(border, top, side, bottom).Width(w - 2).Height(h - 2).Render(components.Fit(content, iw, ih))
}

// modalView centres the modal box in iw×ih and records its content area.
func (m *Model) modalView(iw, ih int) string {
	e := m.env
	mw, mh := m.modal.Size(iw, ih)
	mw, mh = max(30, min(mw, iw)), max(6, min(mh, ih))
	inner := m.modal.View(e, mw-4, mh-3)
	box := theme.Box(theme.Accent).Width(mw - 2).Render(
		theme.Title.Render(strings.ToUpper(m.modal.Title())) + "\n" + components.Fit(inner, mw-4, mh-3))
	bw, bh := lipgloss.Width(box), lipgloss.Height(box)
	ox, oy := max(0, (iw-bw)/2), max(0, (ih-bh)/2)
	m.g.modal = rect{m.g.wsBody.x + ox + 2, m.g.wsBody.y + oy + 2, mw - 4, mh - 3}
	m.g.modalOn = true
	pad := strings.Repeat(" ", ox)
	var b strings.Builder
	b.WriteString(strings.Repeat("\n", oy))
	for i, l := range strings.Split(box, "\n") {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(pad + l)
	}
	return b.String()
}

func (m *Model) footer() string {
	if m.toast != "" && time.Now().Before(m.toastUntil) {
		return components.Truncate(" "+m.toastTone.Style().Render(m.toast), m.width)
	}
	k := func(key, label string) string { return theme.Key.Render(key) + " " + theme.Dim.Render(label) }
	parts := []string{k("↑↓", "Navigate"), k("Enter", "Select"), k("/", "Search"), k("Ctrl+P", "Profile"), k("Ctrl+R", "Region"), k("?", "Help")}
	if m.focus == focusWorkspace && m.modal == nil {
		for _, h := range m.current().Hints() {
			parts = append(parts, k(h.Key, h.Label))
		}
	}
	return components.Truncate(" "+strings.Join(parts, "   "), m.width)
}

func helpText(s core.Screen) string {
	var b strings.Builder
	b.WriteString("GLOBAL\n")
	for _, k := range screens.Shortcuts {
		b.WriteString(fmt.Sprintf("  %-12s %s\n", k[0], k[1]))
	}
	if hints := s.Hints(); len(hints) > 0 {
		b.WriteString("\nTHIS SCREEN\n")
		for _, h := range hints {
			b.WriteString(fmt.Sprintf("  %-12s %s\n", h.Key, h.Label))
		}
	}
	b.WriteString("\nMouse: click the sidebar, rows and the profile/region selects; wheel scrolls; double-click opens.\n")
	b.WriteString("Resource-first: pick a screen, drill in with Enter, act with the listed keys.\n")
	b.WriteString("Problem-first:  press / and describe the problem (\"ecs errors\", \"connect database\").")
	return b.String()
}

// Run starts the TUI.
func Run(d *app.Deps) error {
	opts := []tea.ProgramOption{tea.WithAltScreen()}
	if d.Config.UI.Mouse {
		opts = append(opts, tea.WithMouseCellMotion())
	}
	p := tea.NewProgram(New(d), opts...)
	d.Meter.OnWarn(func(msg string) {
		p.Send(core.ToastMsg{Text: msg + " — see Settings › API usage", Tone: theme.ToneWarn})
	})
	_, err := p.Run()
	return err
}
