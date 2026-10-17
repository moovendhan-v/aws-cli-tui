package screens

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"devops/internal/aws/auth"
	"devops/internal/aws/cicd"
	"devops/internal/aws/console"
	"devops/internal/aws/logs"
	"devops/internal/executor"
	"devops/internal/tui/components"
	"devops/internal/tui/core"
	"devops/internal/tui/theme"
)

// Builds lists CodeBuild projects → builds → a live build view. It also
// accepts "build:<id>" from the pipeline view to open a build directly.
type Builds struct{ *Resource }

func NewBuilds() *Builds { return &Builds{NewResource("builds", BuildProjects)} }

func (b *Builds) Focus(e *core.Env, q string) tea.Cmd {
	if id, ok := strings.CutPrefix(q, "build:"); ok {
		if len(b.stack) == 0 {
			b.Init(e)
		}
		v, cmd := newBuildView(e, "builds", id)
		b.view = v
		return cmd
	}
	return b.Resource.Focus(e, q)
}

func BuildProjects() *Level {
	return &Level{
		Title: "CodeBuild", Noun: "build projects", CacheKind: "pipelines",
		Columns: []col{{Title: "Project", Width: 30, Flex: true}},
		Page: func(ctx context.Context, s *auth.Session, token string) ([]components.Row, string, error) {
			names, next, err := cicd.ProjectsPage(ctx, s, token)
			rows := make([]components.Row, 0, len(names))
			for _, n := range names {
				rows = append(rows, components.Row{Key: n, Cells: []string{n}})
			}
			return rows, next, err
		},
		Child: func(r components.Row) *Level { return projectBuilds(r.Key) },
		Actions: []Action{
			{Key: "S", Label: "Start build", Run: func(e *core.Env, r components.Row) tea.Cmd {
				return e.RunCommandID("build.start", map[string]string{"project": r.Key})
			}},
		},
		Link:  func(region string, r components.Row) string { return console.BuildProject(region, r.Key) },
		Empty: "No CodeBuild projects in this region.",
	}
}

func projectBuilds(project string) *Level {
	return &Level{
		Title: project, Noun: "builds", CacheKind: "pipelines",
		Columns: []col{{Title: "#", Width: 6}, {Title: "Status", Width: 11}, {Title: "Phase", Width: 16}, {Title: "Source", Width: 12},
			{Title: "Started", Width: 10}, {Title: "Took", Width: 8}, {Title: "Initiator", Width: 16, Flex: true}},
		Page: func(ctx context.Context, s *auth.Session, token string) ([]components.Row, string, error) {
			bs, next, err := cicd.BuildsPage(ctx, s, project, token)
			rows := make([]components.Row, 0, len(bs))
			for _, b := range bs {
				rows = append(rows, components.Row{Key: b.ID, Data: b, Tone: theme.StatusTone(b.Status),
					Cells: []string{fmt.Sprint(b.Number), b.Status, dash(b.Phase), dash(b.SourceVersion), components.Ago(b.Start),
						components.Dur(b.Elapsed()), dash(b.Initiator)}})
			}
			return rows, next, err
		},
		Open: func(e *core.Env, r components.Row) (Viewer, tea.Cmd) { return newBuildView(e, "builds", r.Key) },
		Actions: []Action{
			{Key: "S", Label: "Start build", Run: func(e *core.Env, _ components.Row) tea.Cmd {
				return e.RunCommandID("build.start", map[string]string{"project": project})
			}},
		},
		Link: func(region string, r components.Row) string { return console.Build(region, "", r.Key) },
	}
}

type buildView struct {
	screen, id string
	build      *cicd.Build
	err        error
	loading    bool
	loadedAt   time.Time
	reader     *logs.StreamReader
	lines      []logs.Event
	logLoading bool
	logErr     error
	offset     int
	follow     bool
}

func newBuildView(e *core.Env, screen, id string) (*buildView, tea.Cmd) {
	v := &buildView{screen: screen, id: id, follow: true}
	return v, v.load(e)
}

func (v *buildView) load(e *core.Env) tea.Cmd {
	v.loading = true
	s, id := e.Session, v.id
	return e.Run(v.screen, "view:build", executor.Spec{Name: "Get build", Quiet: true, Resources: []string{id}},
		func(ctx context.Context, _ *executor.Job) (any, error) {
			bs, err := cicd.Builds(ctx, s, []string{id})
			if err != nil || len(bs) == 0 {
				if err == nil {
					err = fmt.Errorf("build %s not found", id)
				}
				return nil, err
			}
			return bs[0], nil
		})
}

func (v *buildView) loadLogs(e *core.Env) tea.Cmd {
	if v.build == nil || v.build.LogStream == "" || v.logLoading {
		return nil
	}
	if v.reader == nil {
		v.reader = logs.NewStreamReader(e.Session, v.build.LogGroup, v.build.LogStream)
	}
	v.logLoading = true
	r := v.reader
	return e.Run(v.screen, "view:buildlog", executor.Spec{Name: "Build log", Quiet: true, Resources: []string{v.id}},
		func(ctx context.Context, _ *executor.Job) (any, error) { return r.Next(ctx) })
}

func (v *buildView) Capturing() bool       { return false }
func (v *buildView) Back(e *core.Env) bool { return false }

func (v *buildView) Hints() []core.Hint {
	return []core.Hint{{Key: "↑↓", Label: "Scroll log"}, {Key: "G", Label: "Follow"}, {Key: "S", Label: "New build"},
		{Key: "X", Label: "Stop"}, {Key: "R", Label: "Retry"}, {Key: "o", Label: "Console"}, {Key: "esc", Label: "Back"}}
}

func (v *buildView) Update(e *core.Env, msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case core.TickMsg:
		running := v.build != nil && !v.build.Complete
		var cmds []tea.Cmd
		if running && !v.loading && time.Since(v.loadedAt) > 3*time.Second {
			cmds = append(cmds, v.load(e), v.loadLogs(e))
		}
		return tea.Batch(cmds...)
	case core.ResultMsg:
		switch m.Key {
		case "view:build":
			v.loading, v.err, v.loadedAt = false, m.Err, time.Now()
			if b, ok := m.Value.(cicd.Build); ok {
				first := v.build == nil
				v.build = &b
				if first {
					return v.loadLogs(e)
				}
			}
		case "view:buildlog":
			v.logLoading, v.logErr = false, m.Err
			if ev, ok := m.Value.([]logs.Event); ok {
				v.lines = append(v.lines, ev...)
			}
		}
	case core.MouseMsg:
		if m.WheelUp {
			v.follow, v.offset = false, max(0, v.offset-3)
		}
		if m.WheelDown {
			v.offset += 3
		}
	case tea.KeyMsg:
		switch m.String() {
		case "up", "k":
			v.follow, v.offset = false, max(0, v.offset-1)
		case "down", "j":
			v.offset++
		case "pgup":
			v.follow, v.offset = false, max(0, v.offset-10)
		case "pgdown":
			v.offset += 10
		case "G", "end":
			v.follow = true
		case "r":
			return tea.Batch(v.load(e), v.loadLogs(e))
		case "o":
			return e.OpenURL(console.Build(e.Ctx().Region, e.Ctx().AccountID, v.id), false)
		case "y":
			return e.Copy(v.id, "build ID")
		case "S":
			if v.build != nil {
				return e.RunCommandID("build.start", map[string]string{"project": v.build.Project})
			}
		case "X":
			return e.RunCommandID("build.stop", map[string]string{"build_id": v.id})
		case "R":
			return e.RunCommandID("build.retry", map[string]string{"build_id": v.id})
		}
	}
	return nil
}

func (v *buildView) View(e *core.Env, w, h int) string {
	if v.err != nil && v.build == nil {
		return headingRaw(v.id, "", w) + "\n\n" + errorView(v.err, w)
	}
	if v.build == nil {
		return headingRaw(v.id, "", w) + "\n\n" + loadingView(e, "build")
	}
	b := v.build
	right := "updated " + components.Ago(v.loadedAt)
	if !b.Complete {
		right = e.Spinner() + " live · " + right
	}
	head := headingRaw(theme.Title.Render("CODEBUILD › ")+b.Project+fmt.Sprintf(" #%d", b.Number), right, w) + "\n"
	head += statusIcon(e, b.Status) + " " + theme.StatusTone(b.Status).Style().Render(b.Status) +
		theme.Dim.Render(fmt.Sprintf("  ·  %s  ·  source %s  ·  by %s  ·  started %s", components.Dur(b.Elapsed()), dash(b.SourceVersion),
			dash(b.Initiator), components.Ago(b.Start))) + "\n\n"
	gantt := v.gantt(e, w)
	head += strings.Join(gantt, "\n") + "\n" + theme.Dim.Render(strings.Repeat("─", w)) + "\n"

	body := h - strings.Count(head, "\n")
	var logLines []string
	for _, ev := range v.lines {
		for _, l := range components.WrapLines(strings.TrimRight(ev.Message, "\n"), w) {
			logLines = append(logLines, core.ColorLine(l))
		}
	}
	switch {
	case b.LogStream == "":
		logLines = []string{theme.Dim.Render("No CloudWatch log stream for this build yet.")}
	case v.logErr != nil && len(v.lines) == 0:
		logLines = strings.Split(errorView(v.logErr, w), "\n")
	case len(v.lines) == 0:
		logLines = []string{theme.Dim.Render(e.Spinner() + " loading build log…")}
	}
	maxOff := max(0, len(logLines)-body)
	if v.follow || v.offset > maxOff {
		v.offset = maxOff
	}
	return head + components.Fit(strings.Join(logLines[v.offset:], "\n"), w, body)
}

// gantt draws each phase as a bar positioned on the build's timeline.
func (v *buildView) gantt(e *core.Env, w int) []string {
	b := v.build
	end := b.End
	if end.IsZero() {
		end = time.Now()
	}
	total := end.Sub(b.Start)
	if total <= 0 {
		total = time.Second
	}
	const labelW = 18
	barW := max(10, w-labelW-12)
	var out []string
	for _, p := range b.Phases {
		if p.Type == "COMPLETED" || p.Start.IsZero() {
			continue
		}
		pend := p.End
		if pend.IsZero() {
			pend = time.Now()
		}
		x0 := int(float64(p.Start.Sub(b.Start)) / float64(total) * float64(barW))
		n := max(1, int(float64(pend.Sub(p.Start))/float64(total)*float64(barW)))
		x0 = min(x0, barW-1)
		n = min(n, barW-x0)
		st := theme.StatusTone(p.Status).Style()
		fill := strings.Repeat("█", n)
		if p.Status == "" { // running
			st = theme.Warn
			frames := []string{"▓", "▒"}
			fill = strings.Repeat("█", max(0, n-1)) + frames[e.Tick%2]
		}
		dur := p.Duration
		if dur == 0 {
			dur = pend.Sub(p.Start)
		}
		row := theme.Dim.Render(components.Pad(strings.ToLower(strings.ReplaceAll(p.Type, "_", " ")), labelW)) +
			strings.Repeat(" ", x0) + st.Render(fill) + strings.Repeat(" ", barW-x0-n) + theme.Dim.Render(" "+components.Dur(dur))
		out = append(out, row)
		if p.Message != "" && p.Status != "SUCCEEDED" {
			out = append(out, theme.Bad.Render(components.Truncate(strings.Repeat(" ", labelW)+p.Message, w)))
		}
	}
	if len(out) == 0 {
		out = []string{theme.Dim.Render("No phases yet.")}
	}
	return out
}
