package core

import (
	"fmt"
	"sort"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/commands"
	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/executor"
	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/safety"
	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/tui/components"
	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/tui/theme"
)

func newInput(placeholder string) textinput.Model {
	ti := textinput.New()
	ti.Prompt = ""
	ti.Placeholder = placeholder
	ti.PlaceholderStyle = theme.Dim
	ti.TextStyle = theme.Body
	ti.Cursor.Style = theme.Prompt
	ti.CharLimit = 512
	return ti
}

// ───────────────────────────── Picker ─────────────────────────────

type PickItem struct {
	Label, Detail, Value string
	Tone                 theme.Tone
}

type Picker struct {
	off      int
	title    string
	items    []PickItem
	view     []int
	cursor   int
	input    textinput.Model
	onSelect func(PickItem) tea.Cmd
}

func NewPicker(title string, items []PickItem, current string, onSelect func(PickItem) tea.Cmd) *Picker {
	p := &Picker{title: title, items: items, input: newInput("type to filter…"), onSelect: onSelect}
	p.input.Focus()
	p.filter()
	for i, idx := range p.view {
		if items[idx].Value == current {
			p.cursor = i
		}
	}
	return p
}

func (p *Picker) Title() string { return p.title }

func (p *Picker) filter() {
	q := p.input.Value()
	p.view = p.view[:0]
	if q == "" {
		for i := range p.items {
			p.view = append(p.view, i)
		}
	} else {
		opts := make([]Option, len(p.items))
		for i, it := range p.items {
			opts[i] = Option{Value: it.Value, Label: it.Label, Detail: it.Detail}
		}
		p.view = append(p.view, RankOptions(opts, q)...)
	}
	p.cursor = 0
}

func (p *Picker) Update(e *Env, msg tea.Msg) (bool, tea.Cmd) {
	if mm, ok := msg.(MouseMsg); ok {
		switch {
		case mm.WheelUp:
			p.cursor = max(0, p.cursor-1)
		case mm.WheelDown:
			p.cursor = min(len(p.view)-1, p.cursor+1)
		case mm.Click:
			if i := p.off + mm.Y - 2; mm.Y >= 2 && i >= 0 && i < len(p.view) {
				return true, p.onSelect(p.items[p.view[i]])
			}
		}
		return false, nil
	}
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return false, nil
	}
	switch k.String() {
	case "esc":
		return true, nil
	case "up", "ctrl+k":
		p.cursor = max(0, p.cursor-1)
		return false, nil
	case "down", "ctrl+j":
		p.cursor = min(len(p.view)-1, p.cursor+1)
		return false, nil
	case "enter":
		if len(p.view) == 0 {
			return false, nil
		}
		return true, p.onSelect(p.items[p.view[p.cursor]])
	}
	var cmd tea.Cmd
	before := p.input.Value()
	p.input, cmd = p.input.Update(msg)
	if p.input.Value() != before {
		p.filter()
	}
	return false, cmd
}

func (p *Picker) Size(mw, mh int) (int, int) { return min(mw, 72), min(mh, len(p.items)+5) }

func (p *Picker) View(e *Env, w, h int) string {
	var b strings.Builder
	b.WriteString(theme.Prompt.Render("> ") + p.input.View() + "\n")
	rows := h - 2
	off := 0
	if p.cursor >= rows {
		off = p.cursor - rows + 1
	}
	p.off = off
	for i := off; i < len(p.view) && i < off+rows; i++ {
		it := p.items[p.view[i]]
		label := components.Pad(it.Label, w/2)
		line := label + " " + theme.Dim.Render(it.Detail)
		if i == p.cursor {
			line = theme.RowCursor.Render(components.Pad(components.Pad(it.Label, w/2)+" "+it.Detail, w))
		} else if it.Tone != theme.ToneNormal {
			line = it.Tone.Style().Render(label) + " " + theme.Dim.Render(it.Detail)
		}
		b.WriteString("\n" + line)
	}
	if len(p.view) == 0 {
		b.WriteString("\n" + theme.Dim.Render("no matches"))
	}
	return b.String()
}

// ───────────────────────────── Confirm ─────────────────────────────

type Confirm struct {
	billed     string
	fullScreen func() tea.Cmd // interactive only: run outside the layout
	p          *commands.Prepared
	prod       bool
	require    string
	input      textinput.Model
	ok         func() tea.Cmd
}

func NewConfirm(p *commands.Prepared, prod bool, require string, ok func() tea.Cmd) *Confirm {
	// No placeholder: showing the expected word would look pre-typed.
	c := &Confirm{p: p, prod: prod, require: require, ok: ok, input: newInput("")}
	if require != "" {
		c.input.Focus()
	}
	return c
}

func (c *Confirm) Title() string {
	if c.p.Cmd.Execution.Type == commands.TypeInteractive {
		return "Open interactive session?"
	}
	if c.p.Cmd.Risk == safety.ReadOnly && c.billed != "" {
		return "This action is billed"
	}
	return "Confirm " + c.p.Cmd.Risk.Label() + " action"
}

func (c *Confirm) Size(mw, mh int) (int, int) { return min(mw, 76), min(mh, 16+len(c.p.Values)) }

func (c *Confirm) Update(e *Env, msg tea.Msg) (bool, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return false, nil
	}
	switch k.String() {
	case "esc":
		return true, e.Toast("Cancelled — nothing was run", theme.ToneDim)
	case "enter":
		if c.require == "" || c.input.Value() == c.require {
			return true, c.ok()
		}
		return false, nil
	}
	if c.require == "" {
		switch k.String() {
		case "f", "F":
			if c.fullScreen != nil {
				return true, c.fullScreen()
			}
		case "y", "Y":
			return true, c.ok()
		case "n", "N", "q":
			return true, e.Toast("Cancelled — nothing was run", theme.ToneDim)
		}
		return false, nil
	}
	var cmd tea.Cmd
	c.input, cmd = c.input.Update(msg)
	return false, cmd
}

func (c *Confirm) View(e *Env, w, h int) string {
	ctx := c.p.Spec.Ctx
	kw := 10
	for _, t := range c.p.Target() {
		kw = max(kw, len(t[0])+2)
	}
	kv := func(k, v string) string { return theme.Dim.Render(components.Pad(k, kw)) + v }
	var lines []string
	lines = append(lines, theme.Strong.Render(c.p.Cmd.Name), theme.Dim.Render(c.p.Cmd.Description), "")
	acct := ctx.AccountID
	if acct == "" {
		acct = "(unverified)"
	}
	target := kv("Profile", ctx.ProfileLabel()) + "   " + kv("Region", ctx.Region)
	lines = append(lines, target, kv("Account", acct))
	if c.prod {
		lines = append(lines, kv("Env", theme.ProdBadge.Render("PRODUCTION")))
	}
	risk := theme.Warn.Render(c.p.Cmd.Risk.Label())
	if c.p.Cmd.Risk == safety.Destructive {
		risk = theme.Bad.Render(c.p.Cmd.Risk.Label())
	}
	lines = append(lines, kv("Risk", risk))
	if c.billed != "" {
		lines = append(lines, kv("Cost", theme.Warn.Render("$ "+c.billed)))
	}
	for _, t := range c.p.Target() {
		lines = append(lines, kv(t[0], theme.Body.Render(t[1])))
	}
	if len(c.p.Argv) > 0 {
		lines = append(lines, "", theme.Dim.Render("$ "+strings.Join(c.p.Argv, " ")))
	}
	if t := c.p.Then; t != nil {
		lines = append(lines, theme.Dim.Render("then, once ready: $ "+strings.Join(t.Argv, " ")))
	}
	lines = append(lines, "")
	if c.require != "" {
		lines = append(lines, theme.Warn.Render(fmt.Sprintf("Type %q to confirm:", c.require)),
			theme.Prompt.Render("> ")+c.input.View(), "", theme.Dim.Render("enter confirm · esc cancel"))
	} else {
		keys := theme.Key.Render("y") + theme.Dim.Render(" confirm   ")
		if c.fullScreen != nil {
			keys = theme.Key.Render("y") + theme.Dim.Render(" open in panel   ") + theme.Key.Render("f") + theme.Dim.Render(" full screen   ")
		}
		lines = append(lines, keys+theme.Key.Render("n/esc")+theme.Dim.Render(" cancel"))
	}
	return strings.Join(lines, "\n")
}

// ───────────────────────────── Output ─────────────────────────────

// Output shows a job's live output. Closing it leaves the job running.
type Output struct {
	job     *executor.Job
	offset  int
	follow  bool
	height  int
	wrap    bool
	wrapSet bool
	view    string // table, cards, yaml, json or raw
}

func NewOutput(j *executor.Job) *Output { return &Output{job: j, follow: true} }

func (o *Output) Title() string { return fmt.Sprintf("#%d %s", o.job.ID, o.job.Name) }

func (o *Output) Size(mw, mh int) (int, int) { return mw - 4, mh - 2 }

func (o *Output) Update(e *Env, msg tea.Msg) (bool, tea.Cmd) {
	if mm, ok := msg.(MouseMsg); ok {
		if mm.WheelUp {
			o.follow, o.offset = false, max(0, o.offset-3)
		}
		if mm.WheelDown {
			o.offset += 3
		}
		return false, nil
	}
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return false, nil
	}
	page := max(1, o.height-1)
	switch k.String() {
	case "esc", "q", "enter":
		if !o.job.View().Status.Done() {
			return true, e.Toast(fmt.Sprintf("#%d continues in the background (Ctrl+J)", o.job.ID), theme.ToneNormal)
		}
		return true, nil
	case "up", "k":
		o.follow, o.offset = false, max(0, o.offset-1)
	case "down", "j":
		o.offset++
	case "pgup", "ctrl+u":
		o.follow, o.offset = false, max(0, o.offset-page)
	case "pgdown", "ctrl+d", " ":
		o.offset += page
	case "g", "home":
		o.follow, o.offset = false, 0
	case "G", "end":
		o.follow = true
	case "w":
		o.wrap = !o.wrap
	case "v":
		views := append(append([]string{}, components.ResultViews...), "raw")
		for i, v := range views {
			if v == o.view {
				o.view = views[(i+1)%len(views)]
				break
			}
		}
		o.offset, o.follow = 0, false
	case "x":
		if e.Deps.Exec.Cancel(o.job.ID) {
			return false, e.Toast(fmt.Sprintf("Cancelling #%d…", o.job.ID), theme.ToneWarn)
		}
	}
	return false, nil
}

func (o *Output) View(e *Env, w, h int) string {
	v := o.job.View()
	head := JobHeader(e, v)
	body := h - 3
	o.height = body
	if !o.wrapSet {
		o.wrap, o.wrapSet = e.Deps.Config.UI.Wrap, true
		o.view = e.Deps.Config.UI.OutputFormat
	}
	raw := o.job.Lines(0)
	results := o.job.Results()
	var lines []string
	if o.view != "raw" && len(results) > 0 {
		for _, r := range results {
			if len(results) > 1 {
				lines = append(lines, theme.Title.Render("━━ "+r.Title))
			}
			if r.Err != nil {
				lines = append(lines, theme.Bad.Render("✗ "+safety.Describe(r.Err)))
			} else {
				lines = append(lines, components.RenderResult(r.Value, o.view, w)...)
			}
			if len(results) > 1 {
				lines = append(lines, "")
			}
		}
	} else {
		for _, l := range raw {
			lines = append(lines, colorLine(l))
		}
	}
	if o.wrap && o.view != "table" {
		wrapped := make([]string, 0, len(lines))
		for _, l := range lines {
			wrapped = append(wrapped, components.WrapLines(l, w)...)
		}
		lines = wrapped
	} else {
		for i, l := range lines {
			lines[i] = components.Truncate(l, w)
		}
	}
	maxOff := max(0, len(lines)-body)
	if o.follow || o.offset > maxOff {
		o.offset = maxOff
		if o.offset == maxOff {
			o.follow = true
		}
	}
	var b strings.Builder
	b.WriteString(head + "\n" + theme.Dim.Render(strings.Repeat("─", w)) + "\n")
	end := min(len(lines), o.offset+body)
	for i := o.offset; i < end; i++ {
		b.WriteString(lines[i] + "\n")
	}
	if len(lines) == 0 {
		if v.Status.Done() {
			b.WriteString(theme.Dim.Render("(no output)") + "\n")
		} else {
			b.WriteString(theme.Dim.Render(e.Spinner()+" waiting for output…") + "\n")
		}
	}
	if v.Err != nil {
		wrap := lipgloss.NewStyle().Width(w)
		b.WriteString(wrap.Inherit(theme.Bad).Render("✗ "+safety.Describe(v.Err)) + "\n")
		if hint := safety.Hint(v.ErrKind); hint != "" {
			b.WriteString(wrap.Inherit(theme.Warn).Render(hint) + "\n")
		}
	}
	viewName := "raw"
	if len(results) > 0 {
		viewName = o.view
	}
	foot := fmt.Sprintf("view: %s (v) · ↑↓ scroll · G follow · w wrap · x cancel · esc close   %d lines", viewName, len(lines))
	return components.Fit(b.String(), w, h-1) + "\n" + theme.Dim.Render(foot)
}

// JobHeader renders "✓ completed · 1.2s · profile/region".
func JobHeader(e *Env, v executor.View) string {
	parts := []string{StatusIcon(e, v.Status) + " " + string(v.Status), components.Dur(v.Elapsed()),
		v.Ctx.ProfileLabel() + " · " + v.Ctx.Region}
	if v.ExitCode != nil {
		parts = append(parts, fmt.Sprintf("exit %d", *v.ExitCode))
	}
	if len(v.Args) > 0 {
		keys := make([]string, 0, len(v.Args))
		for k := range v.Args {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var args []string
		for _, k := range keys {
			args = append(args, k+"="+v.Args[k])
		}
		parts = append(parts, strings.Join(args, " "))
	}
	return theme.Dim.Render(strings.Join(parts, "  ·  "))
}

// colorLine highlights severity keywords in output and logs.
func colorLine(l string) string {
	u := strings.ToUpper(l)
	switch {
	case strings.HasPrefix(l, "━━"):
		return theme.Title.Render(l)
	case strings.HasPrefix(l, "✓"):
		return theme.OK.Render(l)
	case strings.HasPrefix(l, "✗"), strings.Contains(u, "ERROR"), strings.Contains(u, "EXCEPTION"),
		strings.Contains(u, "FATAL"), strings.Contains(u, "PANIC"), strings.HasPrefix(l, "stderr:"):
		return theme.Bad.Render(l)
	case strings.Contains(u, "WARN"), strings.Contains(u, "TIMEOUT"):
		return theme.Warn.Render(l)
	case strings.HasPrefix(l, "$ "):
		return theme.Dim.Render(l)
	}
	return theme.Body.Render(l)
}

// ColorLine is exported for the log viewer.
func ColorLine(l string) string { return colorLine(l) }

// ───────────────────────────── Text ─────────────────────────────

// Text is a scrollable read-only modal (help, details, errors).
type Text struct {
	title  string
	lines  []string
	offset int
}

func NewText(title string, body string) *Text {
	return &Text{title: title, lines: strings.Split(body, "\n")}
}

func (t *Text) Title() string { return t.title }

func (t *Text) Size(mw, mh int) (int, int) {
	w := 0
	for _, l := range t.lines {
		w = max(w, len([]rune(l)))
	}
	return min(mw, max(w+4, 50)), min(mh, len(t.lines)+4)
}

func (t *Text) Update(e *Env, msg tea.Msg) (bool, tea.Cmd) {
	if mm, ok := msg.(MouseMsg); ok {
		if mm.WheelUp {
			t.offset = max(0, t.offset-3)
		}
		if mm.WheelDown {
			t.offset = min(max(0, len(t.lines)-1), t.offset+3)
		}
		return false, nil
	}
	if k, ok := msg.(tea.KeyMsg); ok {
		switch k.String() {
		case "esc", "q", "enter", "?":
			return true, nil
		case "up", "k":
			t.offset = max(0, t.offset-1)
		case "down", "j":
			t.offset = min(max(0, len(t.lines)-1), t.offset+1)
		}
	}
	return false, nil
}

func (t *Text) View(e *Env, w, h int) string {
	return components.Fit(strings.Join(t.lines[min(t.offset, len(t.lines)):], "\n"), w, h)
}
