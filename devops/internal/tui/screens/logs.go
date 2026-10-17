package screens

import (
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"devops/internal/aws/logs"
	"devops/internal/executor"
	"devops/internal/safety"
	"devops/internal/tui/components"
	"devops/internal/tui/core"
	"devops/internal/tui/theme"
)

const errorPattern = "?ERROR ?Error ?error ?Exception ?exception ?timeout ?Timeout ?FATAL"

var ranges = []time.Duration{5 * time.Minute, 15 * time.Minute, time.Hour, 3 * time.Hour, 12 * time.Hour}

type tail struct {
	group     string
	job       *executor.Job
	paused    *atomic.Bool
	pattern   string
	rangeIdx  int
	base      int // lines before this index are hidden ("clear")
	offset    int
	follow    bool
	streams   bool
	editing   bool
	patternIn filterBar
}

type Logs struct {
	table   *components.Table
	filter  filterBar
	loading bool
	err     error
	tail    *tail
	height  int
}

func NewLogs() *Logs {
	return &Logs{
		table: components.NewTable(
			components.Column{Title: "Log group", Width: 30, Flex: true},
			components.Column{Title: "Stored", Width: 10},
			components.Column{Title: "Retention", Width: 9},
			components.Column{Title: "Created", Width: 10},
		),
		filter: newFilter(),
	}
}

func (l *Logs) ID() string { return "logs" }

func (l *Logs) Capturing() bool {
	return l.filter.active || (l.tail != nil && l.tail.editing)
}

func (l *Logs) Hints() []core.Hint {
	if l.tail != nil {
		pause := "Pause"
		if l.tail.paused.Load() {
			pause = "Resume"
		}
		return []core.Hint{{Key: "space", Label: pause}, {Key: "e", Label: "Errors only"}, {Key: "f", Label: "Filter pattern"}, {Key: "t", Label: "Range"},
			{Key: "s", Label: "Streams"}, {Key: "c", Label: "Clear"}, {Key: "b", Label: "Background"}, {Key: "esc", Label: "Stop"}}
	}
	return []core.Hint{{Key: "f", Label: "Filter"}, {Key: "enter", Label: "Tail"}, {Key: "e", Label: "Search errors"}, {Key: "r", Label: "Refresh"}}
}

func (l *Logs) Init(e *core.Env) tea.Cmd {
	if l.tail != nil {
		e.Deps.Exec.Cancel(l.tail.job.ID)
		l.tail = nil
	}
	if e.Session.Ready() != nil {
		return nil
	}
	l.loading, l.err = true, nil
	sess := e.Session
	return e.Run(l.ID(), "groups", executor.Spec{Name: "List log groups", Quiet: true},
		func(ctx context.Context, _ *executor.Job) (any, error) { return logs.ListGroups(ctx, sess, "", 2000) })
}

func (l *Logs) Focus(e *core.Env, q string) tea.Cmd {
	if l.tail == nil {
		l.filter.set(q)
		l.table.SetFilter(q)
	}
	return nil
}

func (l *Logs) Back(e *core.Env) (bool, tea.Cmd) {
	if l.tail != nil {
		e.Deps.Exec.Cancel(l.tail.job.ID)
		l.tail = nil
		return true, nil
	}
	if l.filter.value() != "" {
		l.filter.set("")
		l.table.SetFilter("")
		return true, nil
	}
	return false, nil
}

func (l *Logs) Update(e *core.Env, msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case core.ResultMsg:
		if m.Key == "groups" {
			l.loading, l.err = false, m.Err
			groups, _ := m.Value.([]logs.Group)
			rows := make([]components.Row, 0, len(groups))
			idx := make([]core.IndexItem, 0, len(groups))
			for _, g := range groups {
				ret := "never"
				if g.RetentionDays > 0 {
					ret = fmt.Sprintf("%dd", g.RetentionDays)
				}
				rows = append(rows, components.Row{Key: g.Name, Data: g,
					Cells: []string{g.Name, components.Bytes(g.StoredBytes), ret, g.Created.Format("2006-01-02")}})
				idx = append(idx, core.IndexItem{Screen: l.ID(), Label: g.Name, Detail: "log group"})
			}
			l.table.SetRows(rows)
			e.SetIndex(l.ID(), idx)
		}
	case tea.KeyMsg:
		if l.tail != nil {
			return l.tailKey(e, m)
		}
		if l.filter.active {
			changed, cmd := l.filter.update(m)
			if changed {
				l.table.SetFilter(l.filter.value())
			}
			return cmd
		}
		switch m.String() {
		case "f":
			return l.filter.start()
		case "r":
			return l.Init(e)
		case "enter":
			if r, ok := l.table.Selected(); ok {
				return l.startTail(e, r.Key, "", 0)
			}
		case "e":
			if r, ok := l.table.Selected(); ok {
				return e.RunCommandID("logs.errors.search", map[string]string{"log_group": r.Key})
			}
		default:
			l.table.Update(m)
		}
	}
	return nil
}

func (l *Logs) startTail(e *core.Env, group, pattern string, rangeIdx int) tea.Cmd {
	paused := &atomic.Bool{}
	since := ranges[rangeIdx]
	sess := e.Session
	label := "Tail " + group
	if pattern != "" {
		label += " (filtered)"
	}
	t := &tail{group: group, paused: paused, pattern: pattern, rangeIdx: rangeIdx, follow: true, patternIn: newFilter()}
	t.patternIn.input.Placeholder = "CloudWatch filter pattern, e.g. ERROR or { $.level = \"error\" }"
	t.patternIn.set(pattern)
	job, cmd := e.RunJob(l.ID(), "tail", executor.Spec{Name: label, Kind: executor.KindStream, Resources: []string{group}},
		func(ctx context.Context, j *executor.Job) (any, error) {
			j.Printf("$ tail %s  since=%s  pattern=%q", group, since, pattern)
			return nil, logs.Tail(ctx, sess, logs.TailOptions{Group: group, Pattern: pattern, Since: since, Paused: paused},
				func(ev logs.Event) {
					j.Write(ev.Time.Local().Format("15:04:05") + "\x1f" + ev.Stream + "\x1f" + strings.TrimRight(ev.Message, "\n"))
				})
		})
	t.job = job
	l.tail = t
	return cmd
}

func (l *Logs) tailKey(e *core.Env, m tea.KeyMsg) tea.Cmd {
	t := l.tail
	if t.editing {
		switch m.String() {
		case "enter":
			t.editing = false
			e.Deps.Exec.Cancel(t.job.ID)
			return l.startTail(e, t.group, strings.TrimSpace(t.patternIn.value()), t.rangeIdx)
		case "esc":
			t.editing = false
			t.patternIn.set(t.pattern)
			return nil
		}
		_, cmd := t.patternIn.update(m)
		t.patternIn.active = true
		return cmd
	}
	page := max(1, l.height-1)
	switch m.String() {
	case " ", "p":
		t.paused.Store(!t.paused.Load())
	case "e":
		e.Deps.Exec.Cancel(t.job.ID)
		p := errorPattern
		if t.pattern == errorPattern {
			p = ""
		}
		return l.startTail(e, t.group, p, t.rangeIdx)
	case "f":
		t.editing = true
		return t.patternIn.start()
	case "t":
		e.Deps.Exec.Cancel(t.job.ID)
		return l.startTail(e, t.group, t.pattern, (t.rangeIdx+1)%len(ranges))
	case "s":
		t.streams = !t.streams
	case "c":
		t.base = t.job.View().Lines
		t.follow = true
	case "b":
		l.tail = nil
		return e.Toast("Tail continues in the background — see Running Commands (Ctrl+J)", theme.ToneNormal)
	case "up", "k":
		t.follow, t.offset = false, max(0, t.offset-1)
	case "down", "j":
		t.offset++
	case "pgup", "ctrl+u":
		t.follow, t.offset = false, max(0, t.offset-page)
	case "pgdown", "ctrl+d":
		t.offset += page
	case "G", "end":
		t.follow = true
	case "g", "home":
		t.follow, t.offset = false, 0
	}
	return nil
}

func (l *Logs) View(e *core.Env, w, h int) string {
	if err := e.Session.Ready(); err != nil {
		return heading("CloudWatch Logs", "", w) + "\n\n" + errorView(err, w)
	}
	if l.tail != nil {
		return l.tailView(e, w, h)
	}
	right := fmt.Sprintf("%s of %d", l.table.Position(), l.table.Total())
	if l.loading {
		right = e.Spinner() + " loading"
	}
	out := heading("CloudWatch Logs", right, w) + "\n"
	if fv := l.filter.view(w); fv != "" {
		out += fv + "\n"
	}
	switch {
	case l.err != nil:
		return out + "\n" + errorView(l.err, w)
	case l.loading && l.table.Total() == 0:
		return out + "\n" + loadingView(e, "log groups")
	case l.table.Total() == 0:
		return out + "\n" + theme.Dim.Render("No log groups in "+e.Ctx().Region+".")
	}
	return out + l.table.View(w, h-strings.Count(out, "\n"))
}

func (l *Logs) tailView(e *core.Env, w, h int) string {
	t := l.tail
	v := t.job.View()
	state := theme.OK.Render("● streaming")
	switch {
	case v.Status.Done() && v.Err != nil:
		state = theme.Bad.Render("✗ " + string(v.ErrKind))
	case v.Status.Done():
		state = theme.Dim.Render("■ stopped")
	case t.paused.Load():
		state = theme.Warn.Render("❚❚ paused")
	case !e.Pulse():
		state = theme.Dim.Render("● streaming")
	}
	out := heading(t.group, "", w) + "\n"
	pat := t.pattern
	if pat == "" {
		pat = "all events"
	} else if pat == errorPattern {
		pat = "errors only"
	}
	meta := state + theme.Dim.Render(fmt.Sprintf("  ·  last %s  ·  %s  ·  %d lines", ranges[t.rangeIdx], pat, max(0, v.Lines-t.base)))
	if !t.follow {
		meta += theme.Warn.Render("  ·  scrolled (G to follow)")
	}
	out += meta + "\n"
	if t.editing {
		out += theme.Prompt.Render("Pattern: > ") + t.patternIn.input.View() + "\n"
	}
	if v.Err != nil {
		out += theme.Bad.Render(safety.Describe(v.Err)) + "\n"
		if hint := safety.Hint(v.ErrKind); hint != "" {
			out += theme.Warn.Render(hint) + "\n"
		}
	}
	body := h - strings.Count(out, "\n")
	l.height = body
	all := t.job.Lines(t.base)
	maxOff := max(0, len(all)-body)
	if t.follow || t.offset > maxOff {
		t.offset = maxOff
		t.follow = true
	}
	if t.offset < maxOff {
		t.follow = false
	}
	end := min(len(all), t.offset+body)
	var b strings.Builder
	for _, line := range all[t.offset:end] {
		b.WriteString(renderLogLine(line, t.streams) + "\n")
	}
	if len(all) == 0 {
		b.WriteString(theme.Dim.Render(e.Spinner() + " waiting for events…"))
	}
	return out + components.Fit(strings.TrimRight(b.String(), "\n"), w, body)
}

func renderLogLine(line string, streams bool) string {
	parts := strings.SplitN(line, "\x1f", 3)
	if len(parts) != 3 {
		return core.ColorLine(line)
	}
	prefix := theme.Dim.Render(parts[0] + " ")
	if streams {
		s := parts[1]
		if len(s) > 24 {
			s = "…" + s[len(s)-23:]
		}
		prefix += theme.Dim.Render(components.Pad(s, 24) + " ")
	}
	return prefix + core.ColorLine(parts[2])
}
