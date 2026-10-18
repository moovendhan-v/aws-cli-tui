package screens

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"devops/internal/aws/auth"
	"devops/internal/aws/console"
	"devops/internal/aws/logs"
	"devops/internal/tui/components"
	"devops/internal/tui/core"
)

// Logs lists log groups (paged, cached) and opens a history + tail viewer.
type Logs struct {
	list   *Resource
	viewer *logViewer
}

func NewLogs() *Logs {
	l := &Logs{}
	l.list = NewResource("logs", func() *Level {
		return &Level{
			Title: "CloudWatch Logs", Noun: "log groups", CacheKind: "logs",
			Columns: []components.Column{{Title: "Log group", Width: 30, Flex: true}, {Title: "Stored", Width: 10},
				{Title: "Retention", Width: 9}, {Title: "Created", Width: 10}},
			Page: func(ctx context.Context, s *auth.Session, token string) ([]components.Row, string, error) {
				groups, next, err := logs.ListGroupsPage(ctx, s, "", token)
				rows := make([]components.Row, 0, len(groups))
				for _, g := range groups {
					ret := "never"
					if g.RetentionDays > 0 {
						ret = fmt.Sprintf("%dd", g.RetentionDays)
					}
					rows = append(rows, components.Row{Key: g.Name, Data: g,
						Cells: []string{g.Name, components.Bytes(g.StoredBytes), ret, g.Created.Format("2006-01-02")}})
				}
				return rows, next, err
			},
			Open: func(e *core.Env, r components.Row) (Viewer, tea.Cmd) {
				l.viewer = newLogViewer(r.Data.(logs.Group))
				return nil, l.viewer.start(e)
			},
			Actions: []Action{
				{Key: "e", Label: "Search errors", Run: func(e *core.Env, r components.Row) tea.Cmd {
					return e.RunCommandID("logs.errors.search", map[string]string{"log_group": r.Key})
				}},
			},
			Empty: "No log groups in this region.",
			Link:  func(region string, r components.Row) string { return console.LogGroup(region, r.Key) },
		}
	})
	return l
}

func (l *Logs) ID() string { return "logs" }

func (l *Logs) Capturing() bool {
	if l.viewer != nil {
		return l.viewer.editing
	}
	return l.list.Capturing()
}

func (l *Logs) Hints() []core.Hint {
	if l.viewer != nil {
		return l.viewer.hints()
	}
	return l.list.Hints()
}

func (l *Logs) Init(e *core.Env) tea.Cmd {
	if l.viewer != nil {
		l.viewer.stop(e)
		l.viewer = nil
	}
	return l.list.Init(e)
}

// Focus filters the list, or opens a group directly with "open:<name>".
func (l *Logs) Focus(e *core.Env, q string) tea.Cmd {
	if name, ok := strings.CutPrefix(q, "errors:"); ok {
		if l.viewer != nil {
			l.viewer.stop(e)
		}
		l.viewer = newLogViewer(logs.Group{Name: name})
		l.viewer.pattern = errorPattern
		return l.viewer.start(e)
	}
	if name, ok := strings.CutPrefix(q, "open:"); ok {
		if l.viewer != nil {
			l.viewer.stop(e)
		}
		l.viewer = newLogViewer(logs.Group{Name: name})
		return l.viewer.start(e)
	}
	if l.viewer != nil {
		l.viewer.stop(e)
		l.viewer = nil
	}
	return l.list.Focus(e, q)
}

func (l *Logs) Back(e *core.Env) (bool, tea.Cmd) {
	if l.viewer != nil {
		if l.viewer.back() {
			return true, nil
		}
		l.viewer.stop(e)
		l.viewer = nil
		return true, nil
	}
	return l.list.Back(e)
}

func (l *Logs) Update(e *core.Env, msg tea.Msg) tea.Cmd {
	if l.viewer != nil {
		if m, ok := msg.(tea.KeyMsg); ok && m.String() == "b" && !l.viewer.editing {
			cmd := l.viewer.detach(e)
			l.viewer = nil
			return cmd
		}
		if m, ok := msg.(core.ResultMsg); ok && !strings.HasPrefix(m.Key, "log:") {
			return l.list.Update(e, msg)
		}
		return l.viewer.update(e, msg)
	}
	return l.list.Update(e, msg)
}

func (l *Logs) View(e *core.Env, w, h int) string {
	if l.viewer != nil {
		return l.viewer.view(e, w, h)
	}
	return l.list.View(e, w, h)
}
