package screens

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"devops/internal/aws/auth"
	"devops/internal/executor"
	"devops/internal/tui/components"
	"devops/internal/tui/core"
	"devops/internal/tui/theme"
)

// Level describes one table in a drill-down hierarchy
// (e.g. clusters → services → tasks).
type Level struct {
	Title   string
	Noun    string // for "Load <noun>" activity names
	Columns []components.Column
	Load    func(ctx context.Context, s *auth.Session) ([]components.Row, error)
	Child   func(r components.Row) *Level
	Detail  func(r components.Row) string
	Actions []Action
	Empty   string
}

type Action struct {
	Key, Label string
	Run        func(e *core.Env, r components.Row) tea.Cmd
}

type frame struct {
	level    *Level
	table    *components.Table
	loading  bool
	err      error
	seq      int
	loadedAt time.Time
}

// Resource is a generic, drill-down resource browser.
type Resource struct {
	id     string
	root   func() *Level
	stack  []*frame
	filter filterBar
	seq    int
}

func NewResource(id string, root func() *Level) *Resource {
	return &Resource{id: id, root: root, filter: newFilter()}
}

func (r *Resource) ID() string      { return r.id }
func (r *Resource) Capturing() bool { return r.filter.active }

func (r *Resource) top() *frame {
	if len(r.stack) == 0 {
		return nil
	}
	return r.stack[len(r.stack)-1]
}

func (r *Resource) Hints() []core.Hint {
	hints := []core.Hint{{Key: "f", Label: "Filter"}}
	if f := r.top(); f != nil {
		if f.level.Child != nil {
			hints = append(hints, core.Hint{Key: "enter", Label: "Open"})
		}
		for _, a := range f.level.Actions {
			hints = append(hints, core.Hint{Key: a.Key, Label: a.Label})
		}
	}
	hints = append(hints, core.Hint{Key: "r", Label: "Refresh"})
	if len(r.stack) > 1 {
		hints = append(hints, core.Hint{Key: "esc", Label: "Up"})
	}
	return hints
}

func (r *Resource) Init(e *core.Env) tea.Cmd {
	if e.Session.Ready() != nil {
		return nil
	}
	r.stack = nil
	r.filter.set("")
	return r.push(e, r.root())
}

func (r *Resource) push(e *core.Env, l *Level) tea.Cmd {
	r.seq++
	f := &frame{level: l, table: components.NewTable(l.Columns...), seq: r.seq}
	r.stack = append(r.stack, f)
	return r.load(e, f)
}

func (r *Resource) load(e *core.Env, f *frame) tea.Cmd {
	f.loading, f.err = true, nil
	sess := e.Session
	return e.Run(r.id, strconv.Itoa(f.seq), executor.Spec{Name: "Load " + f.level.Noun, Quiet: true},
		func(ctx context.Context, _ *executor.Job) (any, error) { return f.level.Load(ctx, sess) })
}

func (r *Resource) Focus(e *core.Env, q string) tea.Cmd {
	if len(r.stack) > 1 {
		r.stack = r.stack[:1]
	}
	r.filter.set(q)
	if f := r.top(); f != nil {
		f.table.SetFilter(q)
	}
	return nil
}

func (r *Resource) Back(e *core.Env) (bool, tea.Cmd) {
	if r.filter.value() != "" {
		r.filter.set("")
		if f := r.top(); f != nil {
			f.table.SetFilter("")
		}
		return true, nil
	}
	if len(r.stack) > 1 {
		r.stack = r.stack[:len(r.stack)-1]
		return true, nil
	}
	return false, nil
}

func (r *Resource) Update(e *core.Env, msg tea.Msg) tea.Cmd {
	f := r.top()
	switch m := msg.(type) {
	case core.ResultMsg:
		for _, fr := range r.stack {
			if strconv.Itoa(fr.seq) != m.Key {
				continue
			}
			fr.loading, fr.err, fr.loadedAt = false, m.Err, time.Now()
			rows, _ := m.Value.([]components.Row)
			fr.table.SetRows(rows)
			if fr == r.stack[0] {
				r.index(e, fr, rows)
			}
		}
	case tea.KeyMsg:
		if f == nil {
			return nil
		}
		if r.filter.active {
			changed, cmd := r.filter.update(m)
			if changed {
				f.table.SetFilter(r.filter.value())
			}
			return cmd
		}
		row, ok := f.table.Selected()
		switch m.String() {
		case "f":
			return r.filter.start()
		case "r":
			return r.load(e, f)
		case "enter":
			if ok && f.level.Child != nil {
				r.filter.set("")
				return r.push(e, f.level.Child(row))
			}
			return nil
		}
		for _, a := range f.level.Actions {
			if a.Key == m.String() && ok {
				return a.Run(e, row)
			}
		}
		f.table.Update(m)
	}
	return nil
}

func (r *Resource) index(e *core.Env, f *frame, rows []components.Row) {
	items := make([]core.IndexItem, 0, len(rows))
	for _, row := range rows {
		if len(row.Cells) == 0 {
			continue
		}
		items = append(items, core.IndexItem{Screen: r.id, Label: row.Cells[0], Detail: f.level.Title})
	}
	e.SetIndex(r.id, items)
}

func (r *Resource) View(e *core.Env, w, h int) string {
	f := r.top()
	if err := e.Session.Ready(); err != nil || f == nil {
		return errorView(fmt.Errorf("no AWS session: %v", err), w)
	}
	var crumbs []string
	for _, fr := range r.stack {
		crumbs = append(crumbs, fr.level.Title)
	}
	right := fmt.Sprintf("%s of %d", f.table.Position(), f.table.Total())
	if f.loading {
		right = e.Spinner() + " loading"
	}
	out := heading(strings.Join(crumbs, " › "), right, w) + "\n"
	if fv := r.filter.view(w); fv != "" {
		out += fv + "\n"
	}
	switch {
	case f.err != nil:
		return out + "\n" + errorView(f.err, w)
	case f.loading && f.table.Total() == 0:
		return out + "\n" + loadingView(e, f.level.Noun)
	case f.table.Total() == 0:
		empty := f.level.Empty
		if empty == "" {
			empty = "Nothing found in " + e.Ctx().Region + "."
		}
		return out + "\n" + theme.Dim.Render(empty)
	}
	used := strings.Count(out, "\n")
	avail := h - used
	detail := ""
	if f.level.Detail != nil {
		if row, ok := f.table.Selected(); ok {
			detail = f.level.Detail(row)
		}
	}
	if detail == "" {
		return out + f.table.View(w, avail)
	}
	dh := min(strings.Count(detail, "\n")+2, avail/2)
	sep := theme.Dim.Render(strings.Repeat("─", w))
	return out + f.table.View(w, avail-dh) + "\n" + sep + "\n" + components.Fit(detail, w, dh-1)
}
