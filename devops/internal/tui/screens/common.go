// Package screens implements the workspace views selected from the navigation.
package screens

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/moovendhan-v/aws-cli-tui/devops/internal/safety"
	"github.com/moovendhan-v/aws-cli-tui/devops/internal/tui/components"
	"github.com/moovendhan-v/aws-cli-tui/devops/internal/tui/core"
	"github.com/moovendhan-v/aws-cli-tui/devops/internal/tui/theme"
)

// Backer screens consume Esc for their own sub-views (detail, tail, drill-down).
// Returning false lets the shell move focus back to the navigation.
type Backer interface {
	Back(e *core.Env) (bool, tea.Cmd)
}

// heading renders "TITLE › name › name" on the left (only the first,
// static segment is upper-cased; resource names keep their case) and dim
// text on the right.
func heading(title, right string, w int) string {
	first, rest, ok := strings.Cut(title, " › ")
	title = strings.ToUpper(first)
	if ok {
		title += " › " + rest
	}
	return headingRaw(title, right, w)
}

// headingRaw is heading without any case change.
func headingRaw(title, right string, w int) string {
	l := theme.Title.Render(title)
	r := theme.Dim.Render(right)
	gap := w - lipgloss.Width(l) - lipgloss.Width(r)
	if gap < 1 {
		return components.Truncate(l+" "+r, w)
	}
	return l + strings.Repeat(" ", gap) + r
}

// errorView explains a failure with its kind and a recovery hint.
func errorView(err error, w int) string {
	kind := safety.Classify(err)
	title := "✗ " + kindLabel(kind)
	lines := []string{theme.Bad.Render(title), theme.Body.Render(safety.Describe(err))}
	if h := safety.Hint(kind); h != "" {
		lines = append(lines, "", theme.Warn.Render(h))
	}
	return lipgloss.NewStyle().Width(w).Render(strings.Join(lines, "\n"))
}

// filterBar is an inline "Filter: >" input bound to a table.
type filterBar struct {
	input  textinput.Model
	active bool
}

func newFilter() filterBar {
	ti := textinput.New()
	ti.Prompt = ""
	ti.Placeholder = "name, id, ip, tag…"
	ti.PlaceholderStyle = theme.Dim
	ti.TextStyle = theme.Body
	return filterBar{input: ti}
}

func (f *filterBar) start() tea.Cmd {
	f.active = true
	return f.input.Focus()
}

// update edits the filter; it returns changed=true when the text changed.
func (f *filterBar) update(msg tea.KeyMsg) (changed bool, cmd tea.Cmd) {
	switch msg.String() {
	case "enter", "tab":
		f.active = false
		f.input.Blur()
		return false, nil
	case "esc":
		f.active = false
		f.input.Blur()
		f.input.SetValue("")
		return true, nil
	}
	before := f.input.Value()
	f.input, cmd = f.input.Update(msg)
	return f.input.Value() != before, cmd
}

func (f *filterBar) set(v string) {
	f.input.SetValue(v)
	f.input.CursorEnd()
}

func (f *filterBar) value() string { return f.input.Value() }

func (f *filterBar) view(w int) string {
	if !f.active && f.input.Value() == "" {
		return ""
	}
	label := theme.Dim.Render("Filter: ")
	if f.active {
		label = theme.Prompt.Render("Filter: > ")
	}
	return components.Truncate(label+f.input.View(), w)
}

func loadingView(e *core.Env, what string) string {
	return theme.Dim.Render(e.Spinner() + " Loading " + what + "…")
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func dash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// kv renders aligned key/value pairs in two columns when there is room.
func kv(w int, pairs ...[2]string) string {
	colW := w
	two := w >= 70
	if two {
		colW = w / 2
	}
	var lines []string
	var cur string
	for i, p := range pairs {
		cell := components.Pad(theme.Dim.Render(components.Pad(p[0], 13))+theme.Body.Render(dash(p[1])), colW)
		if !two {
			lines = append(lines, cell)
			continue
		}
		if i%2 == 0 {
			cur = cell
		} else {
			lines = append(lines, cur+cell)
			cur = ""
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return strings.Join(lines, "\n")
}

// hintLines lays hints out across as many lines as needed for width w.
func hintLines(hints []core.Hint, w int) string {
	var parts []string
	for _, h := range hints {
		parts = append(parts, theme.Key.Render(h.Key)+" "+theme.Dim.Render(h.Label))
	}
	return pack(parts, "   ", w)
}

// pack joins styled items with sep, wrapping instead of cutting items off.
func pack(items []string, sep string, w int) string {
	return strings.Join(components.Pack(items, sep, w), "\n")
}

// tableMouse applies wheel and click to a table whose header is on line top.
// It returns true when a row was double-clicked (open it).
func tableMouse(t *components.Table, m core.MouseMsg, top int) bool {
	switch {
	case m.WheelUp:
		t.Scroll(-3)
	case m.WheelDown:
		t.Scroll(3)
	case m.Click:
		before := t.Cursor()
		return t.Click(m.Y-top) && m.Double && before == t.Cursor()
	}
	return false
}

// kindLabel renders an error kind as "network error", or just "error".
func kindLabel(k safety.ErrorKind) string {
	if k == safety.ErrUnknown || k == "" {
		return "error"
	}
	return string(k) + " error"
}
