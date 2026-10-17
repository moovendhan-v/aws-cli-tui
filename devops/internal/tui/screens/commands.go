package screens

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"devops/internal/commands"
	"devops/internal/safety"
	"devops/internal/tui/components"
	"devops/internal/tui/core"
	"devops/internal/tui/theme"
)

type Library struct {
	table  *components.Table
	filter filterBar
	favs   map[string]bool
}

func NewLibrary() *Library {
	return &Library{
		table: components.NewTable(
			components.Column{Title: "", Width: 1},
			components.Column{Title: "Command", Width: 26, Flex: true},
			components.Column{Title: "Category", Width: 11},
			components.Column{Title: "Type", Width: 11},
			components.Column{Title: "Risk", Width: 11},
		),
		filter: newFilter(),
		favs:   map[string]bool{},
	}
}

func (l *Library) ID() string      { return "commands" }
func (l *Library) Capturing() bool { return l.filter.active }

func (l *Library) Hints() []core.Hint {
	return []core.Hint{{Key: "enter", Label: "Run"}, {Key: "f", Label: "Filter"}, {Key: "s", Label: "Favorite"}, {Key: "!", Label: "Rejected defs"}}
}

func (l *Library) Init(e *core.Env) tea.Cmd {
	if e.Deps.Store != nil {
		l.favs = e.Deps.Store.Favorites()
	}
	l.rebuild(e)
	return nil
}

func (l *Library) rebuild(e *core.Env) {
	all := append([]*commands.Command(nil), e.Deps.Registry.All()...)
	sort.SliceStable(all, func(i, j int) bool { return l.favs[all[i].ID] && !l.favs[all[j].ID] })
	rows := make([]components.Row, 0, len(all))
	for _, c := range all {
		star := " "
		if l.favs[c.ID] {
			star = "★"
		}
		tone := theme.ToneNormal
		switch c.Risk {
		case safety.Mutating:
			tone = theme.ToneWarn
		case safety.Destructive:
			tone = theme.ToneBad
		}
		rows = append(rows, components.Row{Key: c.ID, Data: c, Tone: tone,
			Search: c.Description + " " + strings.Join(c.Tags, " "),
			Cells:  []string{star, c.Name, c.Category, c.Execution.Type, riskCell(e, c)}})
	}
	l.table.SetRows(rows)
}

func (l *Library) Focus(e *core.Env, q string) tea.Cmd {
	l.filter.set(q)
	l.table.SetFilter(q)
	return nil
}

func (l *Library) Back(e *core.Env) (bool, tea.Cmd) {
	if l.filter.value() != "" {
		l.filter.set("")
		l.table.SetFilter("")
		return true, nil
	}
	return false, nil
}

func (l *Library) Update(e *core.Env, msg tea.Msg) tea.Cmd {
	if mm, ok := msg.(core.MouseMsg); ok {
		top := 1
		if l.filter.active || l.filter.value() != "" {
			top = 2
		}
		if tableMouse(l.table, mm, top) {
			if row, ok := l.table.Selected(); ok {
				return e.OpenCommand(row.Data.(*commands.Command), nil)
			}
		}
		return nil
	}
	m, ok := msg.(tea.KeyMsg)
	if !ok {
		return nil
	}
	if l.filter.active {
		changed, cmd := l.filter.update(m)
		if changed {
			l.table.SetFilter(l.filter.value())
		}
		return cmd
	}
	row, has := l.table.Selected()
	switch m.String() {
	case "f":
		return l.filter.start()
	case "enter":
		if has {
			return e.OpenCommand(row.Data.(*commands.Command), nil)
		}
	case "s":
		if has && e.Deps.Store != nil {
			on, err := e.Deps.Store.ToggleFavorite(row.Key)
			if err != nil {
				return e.Toast(err.Error(), theme.ToneBad)
			}
			l.favs[row.Key] = on
			l.rebuild(e)
			l.table.Select(row.Key)
		}
	case "!":
		return e.Open(core.NewText("Rejected command definitions", rejected(e)))
	default:
		l.table.Update(m)
	}
	return nil
}

func rejected(e *core.Env) string {
	rej := e.Deps.Registry.Rejected
	if len(rej) == 0 {
		return "All command definitions passed validation."
	}
	var b strings.Builder
	for _, r := range rej {
		b.WriteString("✗ " + r.Error() + "\n")
	}
	return b.String()
}

func (l *Library) View(e *core.Env, w, h int) string {
	reg := e.Deps.Registry
	right := fmt.Sprintf("%s · %d commands", l.table.Position(), len(reg.All()))
	if n := len(reg.Rejected); n > 0 {
		right += theme.Bad.Render(fmt.Sprintf(" · %d rejected", n))
	}
	out := heading("Command Library", right, w) + "\n"
	if fv := l.filter.view(w); fv != "" {
		out += fv + "\n"
	}
	used := strings.Count(out, "\n")
	detail := ""
	if row, ok := l.table.Selected(); ok {
		detail = commandDetail(row.Data.(*commands.Command), w)
	}
	dh := min(strings.Count(detail, "\n")+2, (h-used)/2)
	sep := theme.Dim.Render(strings.Repeat("─", w))
	return out + l.table.View(w, h-used-dh) + "\n" + sep + "\n" + components.Fit(detail, w, dh-1)
}

func commandDetail(c *commands.Command, w int) string {
	var b strings.Builder
	b.WriteString(theme.Strong.Render(c.Name) + theme.Dim.Render("  "+c.ID) + "\n")
	b.WriteString(theme.Body.Render(c.Description) + "\n")
	exec := c.Execution.Type
	switch c.Execution.Type {
	case commands.TypeSDK:
		exec += " · " + c.Execution.Service + ":" + c.Execution.Operation
	case commands.TypeProcess, commands.TypeInteractive:
		exec += " · " + c.Execution.Program + " " + strings.Join(c.Execution.Args, " ")
	case commands.TypeWorkflow:
		var steps []string
		for _, s := range c.Execution.Steps {
			steps = append(steps, s.Command)
		}
		exec += " · " + strings.Join(steps, " → ")
	}
	b.WriteString(theme.Dim.Render(exec) + "\n")
	if len(c.Parameters) > 0 {
		var ps []string
		for _, p := range c.Parameters {
			s := p.Name
			if p.Required {
				s += "*"
			}
			if p.Default != "" {
				s += "=" + p.Default
			}
			ps = append(ps, s)
		}
		b.WriteString(theme.Dim.Render("params: ") + theme.Body.Render(strings.Join(ps, "  ")) + "\n")
	}
	b.WriteString(theme.Dim.Render("source: " + c.Source))
	return b.String()
}

// riskCell renders the risk with a $ badge for billed commands.
func riskCell(e *core.Env, c *commands.Command) string {
	if e.Deps.Registry.BilledNote(c) != "" {
		return c.Risk.Label() + " $"
	}
	return c.Risk.Label()
}
