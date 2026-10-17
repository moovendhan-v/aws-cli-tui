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

const navWidth = 26

func (m *Model) View() string {
	if m.width < 60 || m.height < 16 {
		return theme.Warn.Render(fmt.Sprintf("Terminal too small (%dx%d). Resize to at least 60x16.", m.width, m.height))
	}
	header := m.header()
	searchBox := m.searchBar()
	footer := m.footer()
	bodyH := m.height - lipgloss.Height(header) - lipgloss.Height(searchBox) - 1

	nav := m.nav(bodyH)
	ws := m.workspace(m.width-navWidth-1, bodyH)
	body := lipgloss.JoinHorizontal(lipgloss.Top, nav, " ", ws)
	return lipgloss.JoinVertical(lipgloss.Left, header, searchBox, body, footer)
}

// header: "DEVOPS  Profile: x | Account: y | Region: z        ⟳ 2  ●"
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
	left := bar.Render(" ") + theme.Brand.Background(theme.Bar).Render("DEVOPS") + bar.Render("   ") +
		bar.Render("Profile: "+ctx.ProfileLabel()) + sep + bar.Render("Account: ") + bar.Render(acct) + sep + bar.Render("Region: "+ctx.Region)
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
	right := ""
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
	content := theme.Prompt.Render("Search: > ") + m.search.View()
	if m.focus != focusSearch {
		content = theme.Prompt.Render("Search: > ") + theme.Dim.Render("press / or Ctrl+K to search actions, screens and resources")
	}
	return theme.Box(border).Width(m.width - 2).Render(components.Truncate(content, m.width-4))
}

func (m *Model) nav(h int) string {
	var b strings.Builder
	running := m.env.Deps.Exec.RunningCount()
	for i, n := range app.Navigation {
		label := n.Label
		if n.ID == "activity" && running > 0 {
			label = fmt.Sprintf("%s (%d)", label, running)
		}
		line := components.Pad(" "+label, navWidth-4)
		switch {
		case i == m.navIdx && m.focus == focusNav:
			b.WriteString(theme.NavActive.Render(line))
		case i == m.navIdx:
			b.WriteString(lipgloss.NewStyle().Background(theme.Bar).Foreground(theme.Blue).Bold(true).Render(line))
		default:
			b.WriteString(theme.NavItem.Render(line))
		}
		b.WriteString("\n")
		if n.ID == "network" || n.ID == "activity" {
			b.WriteString("\n") // group separators
		}
	}
	border := theme.Border
	if m.focus == focusNav {
		border = theme.Accent
	}
	return theme.Box(border).Width(navWidth - 2).Height(h - 2).Render(components.Fit(strings.TrimRight(b.String(), "\n"), navWidth-4, h-2))
}

func (m *Model) workspace(w, h int) string {
	e := m.env
	iw, ih := w-4, h-2
	var content string
	switch {
	case m.modal != nil:
		mw, mh := m.modal.Size(iw, ih)
		mw, mh = max(30, min(mw, iw)), max(6, min(mh, ih))
		inner := m.modal.View(e, mw-4, mh-3)
		box := theme.Box(theme.Accent).Width(mw - 2).Render(
			theme.Title.Render(strings.ToUpper(m.modal.Title())) + "\n" + components.Fit(inner, mw-4, mh-3))
		content = lipgloss.Place(iw, ih, lipgloss.Center, lipgloss.Center, box)
	case m.focus == focusSearch:
		content = m.palette.view(e, m.search.Value(), iw, ih)
	default:
		content = m.current().View(e, iw, ih)
	}
	border := theme.Border
	if m.focus == focusWorkspace && m.modal == nil {
		border = theme.Accent
	}
	return theme.Box(border).Width(w - 2).Height(h - 2).Render(components.Fit(content, iw, ih))
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
	b.WriteString("\nResource-first: pick a screen, drill in with Enter, act with the listed keys.\n")
	b.WriteString("Problem-first:  press / and describe the problem (\"ecs errors\", \"connect database\").")
	return b.String()
}

// Run starts the TUI.
func Run(d *app.Deps) error {
	p := tea.NewProgram(New(d), tea.WithAltScreen())
	_, err := p.Run()
	return err
}
