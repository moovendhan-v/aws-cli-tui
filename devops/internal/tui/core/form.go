package core

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/sahilm/fuzzy"

	"devops/internal/commands"
	"devops/internal/executor"
	"devops/internal/tui/components"
	"devops/internal/tui/theme"
)

// field is one parameter input. Fields backed by a Source show a searchable
// list under the input; Ctrl+T switches to plain typing.
type field struct {
	param   commands.Param
	input   textinput.Model
	src     *Source
	manual  bool
	options []Option
	loaded  string // dependency signature the options were loaded for
	loading bool
	err     error
	matches []int
	cursor  int
	label   string // display label of the chosen option
}

type Form struct {
	cmd    *commands.Command
	fields []*field
	focus  int
	err    string
	seq    int
	submit func(vals, display map[string]string) (tea.Cmd, error)
	lines  map[int]int // rendered line → field index (mouse)
	opts   map[int]int // rendered line → option index of the focused field
}

const listRows = 6

func NewForm(e *Env, c *commands.Command, prefill map[string]string, submit func(vals, display map[string]string) (tea.Cmd, error)) *Form {
	f := &Form{cmd: c, submit: submit}
	for _, p := range c.Parameters {
		fd := &field{param: p, input: newInput(placeholder(p))}
		if v := prefill[p.Name]; v != "" {
			fd.input.SetValue(v)
		}
		if src, ok := SourceFor(p); ok {
			fd.src = &src
		}
		f.fields = append(f.fields, fd)
	}
	for i, p := range c.Parameters {
		if p.Required && f.fields[i].input.Value() == "" {
			f.focus = i
			break
		}
	}
	f.fields[f.focus].input.Focus()
	return f
}

// setLabels shows known display names for prefilled values.
func (f *Form) setLabels(display map[string]string) {
	for _, fd := range f.fields {
		if l := display[fd.param.Name]; l != "" && fd.input.Value() != "" {
			fd.label = l
		}
	}
}

func placeholder(p commands.Param) string {
	switch {
	case p.Type == "resource":
		return "type to search " + strings.ReplaceAll(p.Resource, "_", " ") + "s…"
	case p.Default != "":
		return p.Default
	case len(p.Options) > 0:
		return strings.Join(p.Options, " | ")
	case p.Description != "":
		return p.Description
	}
	return p.Type
}

func (f *Form) Title() string { return f.cmd.Name }

func (f *Form) Size(mw, mh int) (int, int) {
	return min(mw, 96), min(mh, 12+len(f.fields)*2+listRows)
}

func (f *Form) values() map[string]string {
	v := map[string]string{}
	for _, fd := range f.fields {
		v[fd.param.Name] = strings.TrimSpace(fd.input.Value())
	}
	return v
}

func depSig(fd *field, vals map[string]string) string {
	var parts []string
	for _, n := range fd.src.Needs {
		parts = append(parts, vals[n])
	}
	return strings.Join(parts, "\x1f")
}

// ensure loads options for the focused field when needed (via cache).
func (f *Form) ensure(e *Env) tea.Cmd {
	fd := f.fields[f.focus]
	if fd.src == nil || fd.manual || fd.loading {
		return nil
	}
	vals := f.values()
	sig := depSig(fd, vals)
	if fd.options != nil && fd.loaded == sig {
		return nil
	}
	for _, n := range fd.src.Needs {
		if vals[n] == "" {
			fd.err = fmt.Errorf("choose %s first", n)
			return nil
		}
	}
	fd.loading, fd.err, fd.loaded = true, nil, sig
	f.seq++
	key := fmt.Sprintf("form:%d:%d", f.focus, f.seq)
	src, sess, deps := fd.src, e.Session, vals
	kind := src.Kind
	if kind == "" {
		return Msg(ResultMsg{Key: key, Gen: e.Gen, Value: mustStatic(src)})
	}
	return e.Cached("", key, e.CacheKey(kind, "picker", fd.param.Resource, sig), false,
		executor.Spec{Name: "List " + strings.ReplaceAll(fd.param.Resource, "_", " ") + "s", Quiet: true},
		func(ctx context.Context, _ *executor.Job) (any, error) { return src.Load(ctx, sess, deps) })
}

func mustStatic(s *Source) []Option {
	o, _ := s.Load(context.Background(), nil, nil)
	return o
}

// recentOptions merges recently used values (stored as value\x1flabel) on top.
func recentOptions(e *Env, name string, opts []Option) []Option {
	if e.Deps.Store == nil {
		return opts
	}
	var out []Option
	seen := map[string]bool{}
	for _, r := range e.Deps.Store.Recents("param:"+name, 5) {
		v, label, _ := strings.Cut(r, "\x1f")
		for _, o := range opts {
			if o.Value == v && !seen[v] {
				o.Recent = true
				out = append(out, o)
				seen[v] = true
			}
		}
		_ = label
	}
	for _, o := range opts {
		if !seen[o.Value] {
			out = append(out, o)
		}
	}
	return out
}

func (fd *field) filter() {
	fd.matches = fd.matches[:0]
	q := strings.TrimSpace(fd.input.Value())
	if q == "" || (fd.label != "" && q == fd.chosenValue()) {
		for i := range fd.options {
			fd.matches = append(fd.matches, i)
		}
		fd.cursor = 0
		return
	}
	fd.matches = RankOptions(fd.options, q)
	fd.cursor = 0
}

// RankOptions orders options for a query: every word found in the label
// beats matches in the ID, which beat matches in the details; fuzzy matching
// is only a fallback, so "web 02" picks web-02, never worker-batch.
func RankOptions(opts []Option, q string) []int {
	words := strings.Fields(strings.ToLower(q))
	type scored struct{ i, score int }
	var hits []scored
	for i, o := range opts {
		label, value, detail := strings.ToLower(o.Label), strings.ToLower(o.Value), strings.ToLower(o.Detail)
		score, all := 0, true
		for _, wd := range words {
			switch {
			case strings.Contains(label, wd):
				score += 100
				if strings.HasPrefix(label, wd) {
					score += 20
				}
			case strings.Contains(value, wd):
				score += 60
			case strings.Contains(detail, wd):
				score += 20
			default:
				all = false
			}
		}
		if all {
			if label == strings.ToLower(q) || value == strings.ToLower(q) {
				score += 1000
			}
			hits = append(hits, scored{i, score})
		}
	}
	sort.SliceStable(hits, func(a, b int) bool { return hits[a].score > hits[b].score })
	out := make([]int, 0, len(hits))
	seen := map[int]bool{}
	for _, h := range hits {
		out = append(out, h.i)
		seen[h.i] = true
	}
	if len(out) == 0 {
		src := make([]string, len(opts))
		for i, o := range opts {
			src[i] = o.Label + " " + o.Value
		}
		for _, m := range fuzzy.Find(q, src) {
			if !seen[m.Index] {
				out = append(out, m.Index)
			}
		}
	}
	return out
}

func (fd *field) chosenValue() string {
	for _, o := range fd.options {
		if o.Label == fd.label {
			return o.Value
		}
	}
	return ""
}

// listOpen reports whether the focused field shows its option list.
func (fd *field) listOpen() bool {
	return fd.src != nil && !fd.manual && len(fd.options) > 0 && fd.label == ""
}

func (f *Form) choose(e *Env, fd *field, i int) tea.Cmd {
	o := fd.options[fd.matches[i]]
	fd.input.SetValue(o.Value)
	fd.input.CursorEnd()
	fd.label = o.Label
	// Move on to the next empty field.
	for j := f.focus + 1; j < len(f.fields); j++ {
		if f.fields[j].input.Value() == "" {
			f.setFocus(j)
			return f.ensure(e)
		}
	}
	return nil
}

func (f *Form) setFocus(i int) {
	f.fields[f.focus].input.Blur()
	f.focus = (i + len(f.fields)) % len(f.fields)
	f.fields[f.focus].input.Focus()
}

func (f *Form) Update(e *Env, msg tea.Msg) (bool, tea.Cmd) {
	switch m := msg.(type) {
	case ResultMsg:
		for i, fd := range f.fields {
			if strings.HasPrefix(m.Key, fmt.Sprintf("form:%d:", i)) && m.Key == fmt.Sprintf("form:%d:%d", i, f.seq) {
				fd.loading = m.Refreshing
				fd.err = m.Err
				if o, ok := m.Value.([]Option); ok {
					fd.options = recentOptions(e, fd.param.Name, o)
					fd.filter()
				}
				if m.Err != nil && fd.options == nil {
					fd.manual = true
				}
			}
		}
		return false, nil
	case MouseMsg:
		fd := f.fields[f.focus]
		switch {
		case m.Click:
			if i, ok := f.opts[m.Y]; ok && fd.listOpen() {
				return false, f.choose(e, fd, i)
			}
			if i, ok := f.lines[m.Y]; ok {
				f.setFocus(i)
				return false, f.ensure(e)
			}
		case m.WheelUp && fd.listOpen():
			fd.cursor = max(0, fd.cursor-1)
		case m.WheelDown && fd.listOpen():
			fd.cursor = min(len(fd.matches)-1, fd.cursor+1)
		}
		return false, nil
	case tea.KeyMsg:
		return f.key(e, m)
	}
	return false, nil
}

func (f *Form) key(e *Env, k tea.KeyMsg) (bool, tea.Cmd) {
	fd := f.fields[f.focus]
	open := fd.listOpen() && len(fd.matches) > 0
	switch k.String() {
	case "esc":
		if fd.label == "" && fd.input.Value() != "" && open {
			fd.input.SetValue("")
			fd.filter()
			return false, nil
		}
		return true, nil
	case "tab":
		f.setFocus(f.focus + 1)
		return false, f.ensure(e)
	case "shift+tab":
		f.setFocus(f.focus - 1)
		return false, f.ensure(e)
	case "up":
		if open {
			fd.cursor = max(0, fd.cursor-1)
			return false, nil
		}
		f.setFocus(f.focus - 1)
		return false, f.ensure(e)
	case "down":
		if open {
			fd.cursor = min(len(fd.matches)-1, fd.cursor+1)
			return false, nil
		}
		f.setFocus(f.focus + 1)
		return false, f.ensure(e)
	case "ctrl+t":
		fd.manual = !fd.manual
		if !fd.manual {
			return false, f.ensure(e)
		}
		return false, nil
	case "enter":
		if open {
			return false, f.choose(e, fd, fd.cursor)
		}
		display := map[string]string{}
		for _, x := range f.fields {
			if x.label != "" {
				display[x.param.Name] = x.label
			}
		}
		cmd, err := f.submit(f.values(), display)
		if err != nil {
			f.err = err.Error()
			return false, nil
		}
		if e.Deps.Store != nil {
			for _, x := range f.fields {
				if v := strings.TrimSpace(x.input.Value()); v != "" && x.param.Type == "resource" {
					_ = e.Deps.Store.TouchRecent("param:"+x.param.Name, v+"\x1f"+x.label)
				}
			}
		}
		return true, cmd
	}
	var cmd tea.Cmd
	before := fd.input.Value()
	fd.input, cmd = fd.input.Update(k)
	if fd.input.Value() != before {
		fd.label = ""
		fd.filter()
	}
	f.err = ""
	return false, cmd
}

func (f *Form) View(e *Env, w, h int) string {
	var lines []string
	add := func(s string) { lines = append(lines, s) }
	add(theme.Dim.Render(components.Truncate(f.cmd.Description, w)))
	ctx := e.Ctx()
	add(theme.Dim.Render(fmt.Sprintf("runs in %s · %s · %s", ctx.ProfileLabel(), ctx.Region, f.cmd.Risk.Label())))
	add("")
	f.lines, f.opts = map[int]int{}, map[int]int{}
	labelW := 0
	for _, fd := range f.fields {
		labelW = max(labelW, len(fd.param.Name)+2)
	}
	for i, fd := range f.fields {
		name := fd.param.Name
		if fd.param.Required {
			name += "*"
		}
		style, marker := theme.Dim, "  "
		if i == f.focus {
			style, marker = theme.Title, theme.Prompt.Render("› ")
		}
		suffix := ""
		switch {
		case fd.label != "" && fd.label != fd.input.Value():
			suffix = theme.OK.Render("  ✓ " + fd.label)
		case fd.src != nil && fd.manual:
			suffix = theme.Dim.Render("  manual")
		case fd.src != nil && fd.param.Type == "resource":
			suffix = theme.Dim.Render("  ▾")
		}
		fd.input.Width = max(10, w-labelW-4-lipglossWidth(suffix))
		f.lines[len(lines)] = i
		add(marker + style.Render(components.Pad(name, labelW)) + fd.input.View() + suffix)
		if i != f.focus {
			continue
		}
		indent := strings.Repeat(" ", labelW+2)
		switch {
		case fd.loading && len(fd.options) == 0:
			add(indent + theme.Dim.Render(e.Spinner()+" loading choices…"))
		case fd.err != nil && len(fd.options) == 0:
			add(indent + theme.Warn.Render(fd.err.Error()+" — type a value (ctrl+t)"))
		case fd.listOpen():
			f.renderList(e, fd, indent, w, &lines)
		case fd.param.Description != "" && fd.param.Description != fd.input.Placeholder:
			add(indent + theme.Dim.Render(fd.param.Description))
		}
	}
	add("")
	if f.err != "" {
		add(theme.Bad.Render(components.Truncate(strings.ReplaceAll(f.err, "\n", "; "), w)))
	}
	add(theme.Dim.Render("type to search · ↑↓ choose · enter pick/run · tab next field · ctrl+t type manually · esc cancel"))
	return strings.Join(lines, "\n")
}

func (f *Form) renderList(e *Env, fd *field, indent string, w int, lines *[]string) {
	if len(fd.matches) == 0 {
		*lines = append(*lines, indent+theme.Dim.Render("no matches — enter uses what you typed"))
		return
	}
	start := max(0, min(fd.cursor-listRows/2, len(fd.matches)-listRows))
	lw := w - len(indent)
	for i := start; i < len(fd.matches) && i < start+listRows; i++ {
		o := fd.options[fd.matches[i]]
		mark := "  "
		if o.Recent {
			mark = "• "
		}
		label := components.Pad(mark+o.Label, min(32, lw/2))
		row := label + " " + o.Detail
		f.opts[len(*lines)] = i
		if i == fd.cursor {
			*lines = append(*lines, indent+theme.NavActive.Render(components.Pad(row, lw)))
		} else {
			*lines = append(*lines, indent+o.Tone.Style().Render(components.Truncate(label, lw))+theme.Dim.Render(components.Truncate(" "+o.Detail, max(0, lw-lipglossWidth(label)))))
		}
	}
	if n := len(fd.matches); n > listRows {
		*lines = append(*lines, indent+theme.Dim.Render(fmt.Sprintf("  %d of %d — keep typing to narrow", min(listRows, n), n)))
	}
}

func lipglossWidth(s string) int { return lipgloss.Width(s) }
