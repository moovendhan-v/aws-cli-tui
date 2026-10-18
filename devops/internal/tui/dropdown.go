package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/aws/profiles"
	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/tui/components"
	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/tui/core"
	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/tui/theme"
)

// dropdown is the profile/region chooser that opens inside the sidebar,
// above the selects at the bottom-left.
type dropdown struct {
	kind   string // "profile" or "region"
	items  []core.PickItem
	view   []int
	cursor int
	offset int
	input  textinput.Model
}

func newDropdown(kind string, items []core.PickItem, current string) *dropdown {
	in := textinput.New()
	in.Prompt = ""
	in.Placeholder = "type to filter"
	in.PlaceholderStyle = theme.Dim
	in.TextStyle = theme.Body
	in.Focus()
	d := &dropdown{kind: kind, items: items, input: in}
	d.filter()
	for i, idx := range d.view {
		if items[idx].Value == current {
			d.cursor = i
		}
	}
	return d
}

func (d *dropdown) filter() {
	d.view = d.view[:0]
	q := d.input.Value()
	if q == "" {
		for i := range d.items {
			d.view = append(d.view, i)
		}
	} else {
		opts := make([]core.Option, len(d.items))
		for i, it := range d.items {
			opts[i] = core.Option{Value: it.Value, Label: it.Label}
		}
		d.view = append(d.view, core.RankOptions(opts, q)...)
	}
	d.cursor, d.offset = 0, 0
}

func (d *dropdown) move(n int) {
	if len(d.view) == 0 {
		return
	}
	d.cursor = max(0, min(len(d.view)-1, d.cursor+n))
}

func (d *dropdown) selected() (core.PickItem, bool) {
	if d.cursor < 0 || d.cursor >= len(d.view) {
		return core.PickItem{}, false
	}
	return d.items[d.view[d.cursor]], true
}

// update handles a key; it returns the chosen item, or closed=true on Esc.
func (d *dropdown) update(k tea.KeyMsg) (chosen *core.PickItem, closed bool, cmd tea.Cmd) {
	switch k.String() {
	case "esc":
		return nil, true, nil
	case "up", "ctrl+k":
		d.move(-1)
		return nil, false, nil
	case "down", "ctrl+j", "tab":
		d.move(1)
		return nil, false, nil
	case "enter":
		if it, ok := d.selected(); ok {
			return &it, true, nil
		}
		return nil, false, nil
	}
	before := d.input.Value()
	d.input, cmd = d.input.Update(k)
	if d.input.Value() != before {
		d.filter()
	}
	return nil, false, cmd
}

// render draws the dropdown into w×h and returns content-line → view index.
func (d *dropdown) render(w, h int) (string, map[int]int) {
	title := "SELECT PROFILE"
	if d.kind == "region" {
		title = "SELECT REGION"
	}
	lines := []string{theme.Title.Render(title), theme.Prompt.Render("> ") + components.Truncate(d.input.View(), w-2)}
	hit := map[int]int{}
	rows := max(1, h-len(lines))
	if d.cursor < d.offset {
		d.offset = d.cursor
	}
	if d.cursor >= d.offset+rows {
		d.offset = d.cursor - rows + 1
	}
	for i := d.offset; i < len(d.view) && i < d.offset+rows; i++ {
		it := d.items[d.view[i]]
		label := it.Label
		if it.Detail != "" {
			label += " " + it.Detail
		}
		hit[len(lines)] = i
		switch {
		case i == d.cursor:
			lines = append(lines, theme.NavActive.Render(components.Pad(" "+label, w)))
		case it.Tone != theme.ToneNormal:
			lines = append(lines, it.Tone.Style().Render(components.Pad(" "+label, w)))
		default:
			lines = append(lines, theme.Body.Render(components.Pad(" "+label, w)))
		}
	}
	if len(d.view) == 0 {
		lines = append(lines, theme.Dim.Render(" no matches"))
	}
	return strings.Join(lines, "\n"), hit
}

// profileItems lists profiles with recently used ones first.
func (m *Model) profileItems() []core.PickItem {
	e := m.env
	var recent []string
	if e.Deps.Store != nil {
		recent = e.Deps.Store.Recents("profile", 5)
	}
	var out []core.PickItem
	seen := map[string]bool{}
	add := func(p profiles.Profile, isRecent bool) {
		if seen[p.Name] {
			return
		}
		seen[p.Name] = true
		detail := ""
		if isRecent {
			detail = "•"
		}
		tone := theme.ToneNormal
		if e.Deps.Production.Is(p.Name, "") {
			tone, detail = theme.ToneBad, strings.TrimSpace(detail+" prod")
		}
		out = append(out, core.PickItem{Label: p.Name, Detail: detail, Value: p.Name, Tone: tone})
	}
	for _, r := range recent {
		for _, p := range e.Deps.Profiles {
			if p.Name == r {
				add(p, true)
			}
		}
	}
	for _, p := range e.Deps.Profiles {
		add(p, false)
	}
	return out
}

func (m *Model) regionItems() []core.PickItem {
	var recent []string
	if m.env.Deps.Store != nil {
		recent = m.env.Deps.Store.Recents("region", 5)
	}
	var out []core.PickItem
	seen := map[string]bool{}
	for _, r := range recent {
		seen[r] = true
		out = append(out, core.PickItem{Label: r, Detail: "•", Value: r})
	}
	for _, r := range profiles.Regions {
		if !seen[r] {
			out = append(out, core.PickItem{Label: r, Value: r})
		}
	}
	return out
}

// openDropdown shows the sidebar chooser for "profile" or "region".
func (m *Model) openDropdown(kind string) tea.Cmd {
	e := m.env
	if kind == "profile" {
		items := m.profileItems()
		if len(items) == 0 {
			return e.Toast("No profiles found in "+profiles.ConfigPath(), theme.ToneWarn)
		}
		m.drop = newDropdown("profile", items, e.Ctx().Profile)
	} else {
		m.drop = newDropdown("region", m.regionItems(), e.Ctx().Region)
	}
	m.search.Blur()
	return textinput.Blink
}

func (m *Model) chooseDropdown(it core.PickItem) tea.Cmd {
	e := m.env
	kind := m.drop.kind
	m.drop = nil
	if e.Deps.Store != nil {
		_ = e.Deps.Store.TouchRecent(kind, it.Value)
	}
	if kind == "profile" {
		if it.Value == e.Ctx().Profile {
			return nil
		}
		return core.Msg(core.SwitchContextMsg{Profile: it.Value, Region: e.Deps.Config.UI.DefaultRegion})
	}
	if it.Value == e.Ctx().Region {
		return nil
	}
	return core.Msg(core.SwitchContextMsg{Profile: e.Ctx().Profile, Region: it.Value})
}
