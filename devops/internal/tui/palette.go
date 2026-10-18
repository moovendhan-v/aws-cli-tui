package tui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/sahilm/fuzzy"

	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/app"
	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/commands"
	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/tui/components"
	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/tui/core"
	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/tui/theme"
)

type paletteKind int

const (
	kindAction paletteKind = iota
	kindNav
	kindResource
)

type paletteItem struct {
	kind   paletteKind
	label  string
	detail string
	tone   theme.Tone
	cmd    *commands.Command
	screen string
	query  string
}

// palette is the unified search: problem-first (actions) and resource-first
// (screens and discovered resources) results for the same query.
type palette struct {
	items  []paletteItem
	cursor int
	hit    map[int]int // rendered line → item index (mouse)
}

func (p *palette) search(e *core.Env, q string) {
	p.items, p.cursor = nil, 0
	q = strings.TrimSpace(q)

	if q == "" {
		// Recently run and favorite commands, then screens.
		seen := map[string]bool{}
		for _, v := range e.Deps.Exec.Views() {
			if c := e.Deps.Registry.Get(v.CommandID); c != nil && !seen[c.ID] && len(seen) < 5 {
				seen[c.ID] = true
				p.items = append(p.items, actionItem(c, "recent", e.Deps.Registry.BilledNote(c) != ""))
			}
		}
		if e.Deps.Store != nil {
			for id := range e.Deps.Store.Favorites() {
				if c := e.Deps.Registry.Get(id); c != nil && !seen[id] {
					seen[id] = true
					p.items = append(p.items, actionItem(c, "★ favorite", e.Deps.Registry.BilledNote(c) != ""))
				}
			}
		}
		for _, n := range app.Navigation {
			p.items = append(p.items, paletteItem{kind: kindNav, label: n.Label, detail: "go to", screen: n.ID})
		}
		return
	}

	for _, m := range e.Deps.Registry.Search(q, 8) {
		p.items = append(p.items, actionItem(m.Command, m.Command.Category, e.Deps.Registry.BilledNote(m.Command) != ""))
	}

	lq := strings.ToLower(q)
	for _, n := range app.Navigation {
		hay := strings.ToLower(n.Label + " " + strings.Join(n.Keywords, " "))
		hit := false
		for _, t := range strings.Fields(lq) {
			if strings.Contains(hay, t) {
				hit = true
				break
			}
		}
		if hit {
			p.items = append(p.items, paletteItem{kind: kindNav, label: n.Label, detail: "go to", screen: n.ID})
		}
	}

	idx := e.Index()
	src := make([]string, len(idx))
	for i, it := range idx {
		src[i] = it.Label
	}
	n := 0
	for _, m := range fuzzy.Find(q, src) {
		if n >= 6 {
			break
		}
		it := idx[m.Index]
		p.items = append(p.items, paletteItem{kind: kindResource, label: it.Label, detail: it.Detail,
			screen: it.Screen, query: strings.Fields(it.Label)[0]})
		n++
	}
}

func actionItem(c *commands.Command, detail string, billed bool) paletteItem {
	tone := theme.ToneNormal
	if c.Risk.Level() > 0 {
		tone = theme.ToneWarn
	}
	risk := c.Risk.Label()
	if billed {
		risk += " · $ billed"
	}
	return paletteItem{kind: kindAction, label: c.Name, detail: detail + " · " + risk, cmd: c, tone: tone}
}

func (p *palette) move(d int) {
	if len(p.items) == 0 {
		return
	}
	p.cursor = (p.cursor + d + len(p.items)) % len(p.items)
}

// selected returns the command for the highlighted entry.
func (p *palette) selected(e *core.Env) tea.Cmd {
	if p.cursor >= len(p.items) {
		return nil
	}
	it := p.items[p.cursor]
	switch it.kind {
	case kindAction:
		return e.OpenCommand(it.cmd, nil)
	case kindNav:
		return e.Navigate(it.screen, "")
	default:
		return e.Navigate(it.screen, it.query)
	}
}

func (p *palette) view(e *core.Env, query string, w, h int) string {
	var b strings.Builder
	title := "SEARCH"
	if query == "" {
		title = "QUICK ACCESS"
	}
	b.WriteString(theme.Title.Render(title) + theme.Dim.Render("   ↑↓ select · enter open · esc close") + "\n")
	if len(p.items) == 0 {
		b.WriteString("\n" + theme.Dim.Render("No registered actions, screens or loaded resources match \""+query+"\"."))
		b.WriteString("\n" + theme.Dim.Render("Search only selects trusted, predefined commands — it never generates new ones."))
		return b.String()
	}
	p.hit = map[int]int{}
	line := 1 // the title line
	headers := map[paletteKind]string{kindAction: "ACTIONS", kindNav: "GO TO", kindResource: "RESOURCES"}
	last := paletteKind(-1)
	rows := 0
	for i, it := range p.items {
		if rows >= h-2 {
			break
		}
		if it.kind != last {
			b.WriteString("\n" + theme.Dim.Render(headers[it.kind]) + "\n")
			last = it.kind
			rows += 2
			line += 2
		}
		p.hit[line] = i
		line++
		icon := map[paletteKind]string{kindAction: "▸", kindNav: "→", kindResource: "◆"}[it.kind]
		labelW := max(20, w*55/100)
		if i == p.cursor {
			line := components.Pad(" "+icon+" "+it.label, labelW) + " " + it.detail
			b.WriteString(theme.NavActive.Render(components.Pad(line, w)) + "\n")
		} else {
			b.WriteString(" " + theme.Dim.Render(icon) + " " + it.tone.Style().Render(components.Pad(it.label, labelW-3)) + " " + theme.Dim.Render(it.detail) + "\n")
		}
		rows++
	}
	return b.String()
}
