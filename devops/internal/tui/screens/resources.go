package screens

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/moovendhan-v/aws-cli-tui/devops/internal/aws/auth"
	"github.com/moovendhan-v/aws-cli-tui/devops/internal/executor"
	"github.com/moovendhan-v/aws-cli-tui/devops/internal/tui/components"
	"github.com/moovendhan-v/aws-cli-tui/devops/internal/tui/core"
	"github.com/moovendhan-v/aws-cli-tui/devops/internal/tui/theme"
)

// PageFunc loads one page of rows; token "" is the first page and the
// returned next token "" means there are no more pages.
type PageFunc func(ctx context.Context, s *auth.Session, token string) ([]components.Row, string, error)

// Full adapts a loader that returns everything at once.
func Full(load func(ctx context.Context, s *auth.Session) ([]components.Row, error)) PageFunc {
	return func(ctx context.Context, s *auth.Session, _ string) ([]components.Row, string, error) {
		rows, err := load(ctx, s)
		return rows, "", err
	}
}

// Level describes one table in a drill-down hierarchy
// (e.g. clusters → services → tasks).
type Level struct {
	Title     string
	Noun      string // for "Load <noun>" activity names
	CacheKind string // cache TTL bucket (ec2, ecs, logs…); "" = default TTL
	Columns   []components.Column
	Page      PageFunc
	Child     func(r components.Row) *Level
	// Open replaces Child when Enter should open a custom view instead of a
	// table. A nil Viewer means the opener handled it itself.
	Open   func(e *core.Env, r components.Row) (Viewer, tea.Cmd)
	Detail func(r components.Row) string
	// Link returns the AWS console URL for a row (o opens, Y copies).
	Link    func(region string, r components.Row) string
	Actions []Action
	Empty   string
}

// Viewer is a custom full-workspace view opened from a resource row
// (for example the tabbed ECS service view). Results whose key starts with
// "view:" are routed to it.
type Viewer interface {
	Update(e *core.Env, msg tea.Msg) tea.Cmd
	View(e *core.Env, w, h int) string
	Hints() []core.Hint
	// Back handles Esc inside the view; false closes the view.
	Back(e *core.Env) bool
	Capturing() bool
}

type Action struct {
	Key, Label string
	Run        func(e *core.Env, r components.Row) tea.Cmd
	// View opens a custom view instead of running a command.
	View func(e *core.Env, r components.Row) (Viewer, tea.Cmd)
}

// pageState is what gets cached for a level: all rows loaded so far.
type pageState struct {
	Rows []components.Row
	Next string
}

type frame struct {
	watch       bool
	loadedAt    time.Time
	level       *Level
	table       *components.Table
	loading     bool
	loadingMore bool
	err         error
	seq         int
	next        string
	cachedAt    time.Time
	cacheKey    string
}

// Resource is a generic, drill-down resource browser with caching and
// incremental pagination.
type Resource struct {
	focus  func(e *core.Env, q string) (Viewer, tea.Cmd, bool)
	view   Viewer
	id     string
	root   func() *Level
	stack  []*frame
	filter filterBar
	seq    int
}

func NewResource(id string, root func() *Level) *Resource {
	return &Resource{id: id, root: root, filter: newFilter()}
}

// WithFocus lets searches/navigation open custom views ("service:…", "fn:…").
func (r *Resource) WithFocus(f func(e *core.Env, q string) (Viewer, tea.Cmd, bool)) *Resource {
	r.focus = f
	return r
}

func (r *Resource) ID() string { return r.id }

func (r *Resource) Capturing() bool {
	if r.view != nil {
		return r.view.Capturing()
	}
	return r.filter.active
}

func (r *Resource) top() *frame {
	if len(r.stack) == 0 {
		return nil
	}
	return r.stack[len(r.stack)-1]
}

func (r *Resource) Hints() []core.Hint {
	if r.view != nil {
		return r.view.Hints()
	}
	hints := []core.Hint{{Key: "f", Label: "Filter"}}
	if f := r.top(); f != nil {
		if f.level.Child != nil || f.level.Open != nil {
			hints = append(hints, core.Hint{Key: "enter", Label: "Open"})
		}
		for _, a := range f.level.Actions {
			hints = append(hints, core.Hint{Key: a.Key, Label: a.Label})
		}
	}
	if f := r.top(); f != nil && f.level.Link != nil {
		hints = append(hints, core.Hint{Key: "o", Label: "Console"})
	}
	hints = append(hints, core.Hint{Key: "y", Label: "Copy ID"}, core.Hint{Key: "*", Label: "Pin"},
		core.Hint{Key: "W", Label: "Watch"}, core.Hint{Key: "r", Label: "Refresh"})
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
	r.view = nil
	r.filter.set("")
	return r.push(e, r.root())
}

func (r *Resource) push(e *core.Env, l *Level) tea.Cmd {
	r.seq++
	var crumbs []string
	for _, fr := range r.stack {
		crumbs = append(crumbs, fr.level.Title)
	}
	crumbs = append(crumbs, l.Title)
	kind := l.CacheKind
	if kind == "" {
		kind = r.id
	}
	f := &frame{level: l, table: components.NewTable(l.Columns...), seq: r.seq,
		cacheKey: e.CacheKey(kind, r.id, strings.Join(crumbs, "/"))}
	r.stack = append(r.stack, f)
	return r.load(e, f, false)
}

func (r *Resource) key(f *frame, kind string) string { return strconv.Itoa(f.seq) + ":" + kind }

// load fetches the first page (from cache when fresh unless force).
func (r *Resource) load(e *core.Env, f *frame, force bool) tea.Cmd {
	f.loading, f.err = true, nil
	sess, page := e.Session, f.level.Page
	return e.Cached(r.id, r.key(f, "first"), f.cacheKey, force, executor.Spec{Name: "Load " + f.level.Noun, Quiet: true},
		func(ctx context.Context, _ *executor.Job) (any, error) {
			rows, next, err := page(ctx, sess, "")
			return pageState{Rows: rows, Next: next}, err
		})
}

func (r *Resource) loadMore(e *core.Env, f *frame) tea.Cmd {
	if f.next == "" || f.loadingMore {
		return nil
	}
	f.loadingMore = true
	sess, page, token := e.Session, f.level.Page, f.next
	return e.Run(r.id, r.key(f, "more"), executor.Spec{Name: "Load more " + f.level.Noun, Quiet: true},
		func(ctx context.Context, _ *executor.Job) (any, error) {
			rows, next, err := page(ctx, sess, token)
			return pageState{Rows: rows, Next: next}, err
		})
}

func (r *Resource) Focus(e *core.Env, q string) tea.Cmd {
	if r.focus != nil {
		if v, cmd, ok := r.focus(e, q); ok {
			if len(r.stack) == 0 {
				cmd = tea.Batch(r.Init(e), cmd)
			}
			r.view = v
			return cmd
		}
	}
	r.view = nil
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
	if r.view != nil {
		if !r.view.Back(e) {
			r.view = nil
		}
		return true, nil
	}
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
	if r.view != nil {
		if m, ok := msg.(core.ResultMsg); ok && !strings.HasPrefix(m.Key, "view:") {
			return r.result(e, m)
		}
		return r.view.Update(e, msg)
	}
	f := r.top()
	switch m := msg.(type) {
	case core.TickMsg:
		if f != nil && f.watch && !f.loading && time.Since(f.loadedAt) > 10*time.Second {
			return r.load(e, f, true)
		}
	case core.ResultMsg:
		return r.result(e, m)
	case core.MouseMsg:
		if f == nil {
			return nil
		}
		switch {
		case m.WheelUp:
			f.table.Scroll(-3)
		case m.WheelDown:
			f.table.Scroll(3)
		case m.Click:
			before := f.table.Cursor()
			if f.table.Click(m.Y-r.tableTop()) && m.Double && before == f.table.Cursor() {
				return r.open(e, f)
			}
		}
		if f.table.WantsMore() {
			return r.loadMore(e, f)
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
			return r.load(e, f, true)
		case "enter":
			return r.open(e, f)
		case "o", "Y":
			if ok && f.level.Link != nil {
				u := f.level.Link(e.Ctx().Region, row)
				if m.String() == "Y" {
					return e.Copy(u, "console link")
				}
				return e.OpenURL(u, false)
			}
		case "y":
			if ok {
				return e.Copy(row.Key, "ID")
			}
		case "*":
			if ok && len(r.stack) == 1 && len(row.Cells) > 0 {
				label := row.Cells[0]
				if strings.TrimSpace(components.Strip(label)) == "" && len(row.Cells) > 1 {
					label = row.Cells[1]
				}
				return e.TogglePin(core.Pin{Screen: r.id, Key: row.Key, Label: components.Strip(label)})
			}
		case "W":
			f.watch = !f.watch
			if f.watch {
				return e.Toast("Watching — refreshes every 10s while this list is on screen", theme.ToneNormal)
			}
		}
		for _, a := range f.level.Actions {
			if a.Key == m.String() && ok {
				if a.View != nil {
					v, cmd := a.View(e, row)
					r.view = v
					return cmd
				}
				return a.Run(e, row)
			}
		}
		f.table.Update(m)
		if f.table.WantsMore() {
			return r.loadMore(e, f)
		}
	}
	return nil
}

func (r *Resource) open(e *core.Env, f *frame) tea.Cmd {
	row, ok := f.table.Selected()
	if !ok {
		return nil
	}
	if f.level.Open != nil {
		v, cmd := f.level.Open(e, row)
		if v != nil {
			r.view = v
		}
		if v != nil || cmd != nil || f.level.Child == nil {
			return cmd
		}
	}
	if f.level.Child != nil {
		r.filter.set("")
		return r.push(e, f.level.Child(row))
	}
	return nil
}

func (r *Resource) result(e *core.Env, m core.ResultMsg) tea.Cmd {
	for _, fr := range r.stack {
		switch m.Key {
		case r.key(fr, "first"):
			fr.loading, fr.err = m.Refreshing, m.Err
			fr.cachedAt = m.CachedAt
			if !m.Refreshing {
				fr.loadedAt = time.Now()
			}
			if ps, ok := m.Value.(pageState); ok {
				fr.table.SetRows(ps.Rows)
				fr.next = ps.Next
			}
			if fr == r.stack[0] {
				r.index(e, fr)
			}
		case r.key(fr, "more"):
			fr.loadingMore = false
			if m.Err != nil {
				return e.Toast("Loading more failed: "+m.Err.Error(), theme.ToneBad)
			}
			if ps, ok := m.Value.(pageState); ok {
				fr.table.AppendRows(ps.Rows)
				fr.next = ps.Next
				e.Deps.Cache.Put(fr.cacheKey, pageState{Rows: fr.table.Rows(), Next: fr.next})
			}
			if fr == r.stack[0] {
				r.index(e, fr)
			}
		}
	}
	return nil
}

func (r *Resource) index(e *core.Env, f *frame) {
	rows := f.table.Rows()
	items := make([]core.IndexItem, 0, len(rows))
	for _, row := range rows {
		if len(row.Cells) == 0 {
			continue
		}
		items = append(items, core.IndexItem{Screen: r.id, Label: row.Cells[0], Detail: f.level.Title})
	}
	e.SetIndex(r.id, items)
}

// tableTop is the line where the table header sits inside the workspace.
func (r *Resource) tableTop() int {
	if r.filter.active || r.filter.value() != "" {
		return 2
	}
	return 1
}

func (r *Resource) View(e *core.Env, w, h int) string {
	if r.view != nil {
		return r.view.View(e, w, h)
	}
	f := r.top()
	if err := e.Session.Ready(); err != nil || f == nil {
		return errorView(fmt.Errorf("no AWS session: %v", err), w)
	}
	var crumbs []string
	for _, fr := range r.stack {
		crumbs = append(crumbs, fr.level.Title)
	}
	right := fmt.Sprintf("%s loaded", f.table.Position())
	switch {
	case f.loading && f.table.Total() > 0:
		right = e.Spinner() + " refreshing · " + right
	case f.loading:
		right = e.Spinner() + " loading"
	case !f.cachedAt.IsZero():
		right += " · cached " + components.Ago(f.cachedAt)
	}
	if f.watch {
		dot := "◉"
		if !e.Pulse() {
			dot = "○"
		}
		right = theme.OK.Render(dot+" watching") + theme.Dim.Render(" · ") + right
	}
	out := heading(strings.Join(crumbs, " › "), right, w) + "\n"
	if fv := r.filter.view(w); fv != "" {
		out += fv + "\n"
	}
	switch {
	case f.err != nil && f.table.Total() == 0:
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
	f.table.SetMore(f.next != "", f.loadingMore, e.Spinner())
	used := strings.Count(out, "\n")
	avail := h - used
	detail := ""
	if f.level.Detail != nil {
		if row, ok := f.table.Selected(); ok {
			detail = components.Wrap(f.level.Detail(row), w)
		}
	}
	if detail == "" {
		return out + f.table.View(w, avail)
	}
	dh := min(strings.Count(detail, "\n")+2, avail/2)
	sep := theme.Dim.Render(strings.Repeat("─", w))
	return out + f.table.View(w, avail-dh) + "\n" + sep + "\n" + components.Fit(detail, w, dh-1)
}
