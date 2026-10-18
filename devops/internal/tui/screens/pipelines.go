package screens

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/aws/auth"
	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/aws/cicd"
	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/aws/console"
	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/executor"
	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/tui/components"
	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/tui/core"
	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/tui/theme"
)

// stageDot renders one stage as a coloured dot for the pipeline list.
func stageDot(status string) string {
	switch status {
	case "Succeeded":
		return theme.OK.Render("●")
	case "Failed", "Stopped", "Cancelled":
		return theme.Bad.Render("●")
	case "InProgress", "Stopping":
		return theme.Warn.Render("◐")
	}
	return theme.Dim.Render("○")
}

func statusIcon(e *core.Env, status string) string {
	switch status {
	case "Succeeded", "SUCCEEDED":
		return theme.OK.Render("✓")
	case "Failed", "FAILED", "FAULT", "TIMED_OUT", "Abandoned":
		return theme.Bad.Render("✗")
	case "Stopped", "STOPPED", "Cancelled":
		return theme.Dim.Render("■")
	case "InProgress", "IN_PROGRESS", "Stopping":
		return theme.Warn.Render(e.Spinner())
	}
	return theme.Dim.Render("○")
}

func Pipelines() *Level {
	return &Level{
		Title: "Pipelines", Noun: "pipelines", CacheKind: "pipelines",
		Columns: []col{{Title: "Pipeline", Width: 24, Flex: true}, {Title: "Status", Width: 11}, {Title: "Stages", Width: 16}, {Title: "Updated", Width: 10}},
		Page: func(ctx context.Context, s *auth.Session, token string) ([]components.Row, string, error) {
			ps, next, err := cicd.PipelinesPage(ctx, s, token)
			rows := make([]components.Row, 0, len(ps))
			for _, p := range ps {
				var dots []string
				for _, st := range p.Stages {
					dots = append(dots, stageDot(st.Status))
				}
				status := p.Overall()
				if p.Err != nil {
					status = "unknown"
				}
				rows = append(rows, components.Row{Key: p.Name, Data: p, Tone: theme.StatusTone(status),
					Cells: []string{p.Name, dash(status), strings.Join(dots, " "), components.Ago(p.Updated)}})
			}
			return rows, next, err
		},
		Open: func(e *core.Env, r components.Row) (Viewer, tea.Cmd) {
			return newPipelineView(e, "pipelines", r.Key)
		},
		Actions: []Action{
			{Key: "S", Label: "Start run", Run: func(e *core.Env, r components.Row) tea.Cmd {
				return e.RunCommandID("pipeline.start", map[string]string{"pipeline": r.Key})
			}},
		},
		Link:  func(region string, r components.Row) string { return console.Pipeline(region, r.Key) },
		Empty: "No pipelines in this region.",
	}
}

type pipelineView struct {
	screen, name string
	stages       []cicd.Stage
	execs        []cicd.Execution
	loading      bool
	err          error
	loadedAt     time.Time
	cursor       int
	offset       int
}

type pipelineData struct {
	Stages []cicd.Stage
	Execs  []cicd.Execution
}

func newPipelineView(e *core.Env, screen, name string) (*pipelineView, tea.Cmd) {
	v := &pipelineView{screen: screen, name: name}
	return v, v.load(e)
}

func (v *pipelineView) load(e *core.Env) tea.Cmd {
	v.loading = true
	s, name := e.Session, v.name
	return e.Run(v.screen, "view:pipeline", executor.Spec{Name: "Pipeline state", Quiet: true, Resources: []string{name}},
		func(ctx context.Context, _ *executor.Job) (any, error) {
			st, err := cicd.State(ctx, s, name)
			if err != nil {
				return nil, err
			}
			ex, err := cicd.Executions(ctx, s, name)
			return pipelineData{Stages: st, Execs: ex}, err
		})
}

func (v *pipelineView) running() bool {
	for _, s := range v.stages {
		if s.Status == "InProgress" {
			return true
		}
	}
	return false
}

func (v *pipelineView) Capturing() bool       { return false }
func (v *pipelineView) Back(e *core.Env) bool { return false }

func (v *pipelineView) Hints() []core.Hint {
	return []core.Hint{{Key: "↑↓", Label: "Actions"}, {Key: "enter", Label: "Open build"}, {Key: "S", Label: "Start"},
		{Key: "X", Label: "Stop"}, {Key: "T", Label: "Retry stage"}, {Key: "a/d", Label: "Approve/Reject"},
		{Key: "o", Label: "Console"}, {Key: "r", Label: "Refresh"}, {Key: "esc", Label: "Back"}}
}

type flatAction struct {
	stage  cicd.Stage
	action cicd.Action
}

func (v *pipelineView) actions() []flatAction {
	var out []flatAction
	for _, s := range v.stages {
		for _, a := range s.Actions {
			out = append(out, flatAction{s, a})
		}
	}
	return out
}

func (v *pipelineView) selected() (flatAction, bool) {
	acts := v.actions()
	if v.cursor < 0 || v.cursor >= len(acts) {
		return flatAction{}, false
	}
	return acts[v.cursor], true
}

func (v *pipelineView) Update(e *core.Env, msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case core.TickMsg:
		// Poll only while this view is on screen: fast during a run.
		every := 30 * time.Second
		if v.running() {
			every = 5 * time.Second
		}
		if !v.loading && time.Since(v.loadedAt) > every {
			return v.load(e)
		}
	case core.ResultMsg:
		if m.Key == "view:pipeline" {
			v.loading, v.err, v.loadedAt = false, m.Err, time.Now()
			if d, ok := m.Value.(pipelineData); ok {
				v.stages, v.execs = d.Stages, d.Execs
				v.cursor = min(v.cursor, max(0, len(v.actions())-1))
			}
		}
	case core.MouseMsg:
		switch {
		case m.WheelUp:
			v.cursor = max(0, v.cursor-1)
		case m.WheelDown:
			v.cursor = min(len(v.actions())-1, v.cursor+1)
		}
	case tea.KeyMsg:
		return v.key(e, m)
	}
	return nil
}

func (v *pipelineView) currentExec() string {
	for _, s := range v.stages {
		if s.Status == "InProgress" {
			return s.ExecutionID
		}
	}
	if len(v.execs) > 0 && v.execs[0].Status == "InProgress" {
		return v.execs[0].ID
	}
	return ""
}

func (v *pipelineView) key(e *core.Env, k tea.KeyMsg) tea.Cmd {
	fa, ok := v.selected()
	switch k.String() {
	case "up", "k":
		v.cursor = max(0, v.cursor-1)
	case "down", "j":
		v.cursor = min(len(v.actions())-1, v.cursor+1)
	case "pgup":
		v.offset = max(0, v.offset-5)
	case "pgdown":
		v.offset += 5
	case "r":
		return v.load(e)
	case "o":
		return e.OpenURL(console.Pipeline(e.Ctx().Region, v.name), false)
	case "y":
		if id := v.currentExec(); id != "" {
			return e.Copy(id, "execution ID")
		}
		return e.Copy(v.name, "pipeline name")
	case "S":
		return e.RunCommandID("pipeline.start", map[string]string{"pipeline": v.name})
	case "X":
		id := v.currentExec()
		if id == "" {
			return e.Toast("No run in progress", theme.ToneDim)
		}
		return e.RunCommandID("pipeline.stop", map[string]string{"pipeline": v.name, "execution_id": id})
	case "T":
		for _, s := range v.stages {
			if s.Status == "Failed" {
				return e.RunCommandID("pipeline.retry", map[string]string{"pipeline": v.name, "stage": s.Name, "execution_id": s.ExecutionID})
			}
		}
		return e.Toast("No failed stage to retry", theme.ToneDim)
	case "a", "d":
		if !ok || fa.action.Token == "" {
			return e.Toast("Select a pending manual approval action first", theme.ToneDim)
		}
		decision := "Approved"
		if k.String() == "d" {
			decision = "Rejected"
		}
		return e.RunCommandID("pipeline.approval", map[string]string{"pipeline": v.name, "stage": fa.stage.Name,
			"action": fa.action.Name, "token": fa.action.Token, "decision": decision, "#form": "1"})
	case "enter":
		if ok && fa.action.ExternalID != "" && strings.Contains(fa.action.ExternalURL, "codebuild") {
			return e.Navigate("builds", "build:"+fa.action.ExternalID)
		}
		return e.Toast("This action has no CodeBuild build to open", theme.ToneDim)
	}
	return nil
}

func (v *pipelineView) View(e *core.Env, w, h int) string {
	right := ""
	switch {
	case v.loading:
		right = e.Spinner() + " refreshing"
	case !v.loadedAt.IsZero():
		mode := "every 30s"
		if v.running() {
			mode = "every 5s"
		}
		right = "updated " + components.Ago(v.loadedAt) + " · auto " + mode
	}
	head := headingRaw(theme.Title.Render("PIPELINES › ")+v.name, right, w) + "\n\n"
	if v.err != nil && v.stages == nil {
		return head + errorView(v.err, w)
	}
	if v.stages == nil {
		return head + loadingView(e, "pipeline")
	}
	var lines []string
	lines = append(lines, strings.Split(v.diagram(e, w), "\n")...)
	lines = append(lines, "", theme.Title.Render("ACTIONS"))
	for i, fa := range v.actions() {
		a := fa.action
		since := ""
		if !a.Changed.IsZero() {
			since = components.Ago(a.Changed)
		}
		note := a.Summary
		if a.Error != "" {
			note = a.Error
		}
		if a.Token != "" && a.Status == "InProgress" {
			note = "waiting for approval — a approve · d reject"
		}
		row := fmt.Sprintf("%s  %s › %s", statusIcon(e, a.Status), components.Pad(fa.stage.Name, 12), components.Pad(a.Name, 20))
		row += theme.Dim.Render(" " + components.Pad(since, 10) + " " + strings.ReplaceAll(note, "\n", " "))
		if i == v.cursor {
			row = theme.RowCursor.Render(components.Pad(ansiStrip(row), w))
		}
		lines = append(lines, components.Truncate(row, w))
	}
	if fa, ok := v.selected(); ok && fa.action.Error != "" {
		lines = append(lines, "")
		for _, l := range components.WrapLines(fa.action.Error, w-2) {
			lines = append(lines, theme.Bad.Render("  "+l))
		}
	}
	lines = append(lines, "", theme.Title.Render("RECENT RUNS"))
	for _, x := range v.execs {
		dur := ""
		if !x.Started.IsZero() && !x.Updated.IsZero() {
			dur = components.Dur(x.Updated.Sub(x.Started))
		}
		row := fmt.Sprintf("%s  %-11s %s  %s  %s", statusIcon(e, x.Status), x.Status, theme.Dim.Render(components.Pad(components.Ago(x.Started), 10)),
			theme.Dim.Render(components.Pad(dur, 7)), theme.Body.Render(x.Revision))
		if x.Trigger != "" {
			row += theme.Dim.Render("  · " + x.Trigger)
		}
		lines = append(lines, components.Truncate(row, w))
	}
	body := h - 2
	v.offset = max(0, min(v.offset, len(lines)-body))
	return head + components.Fit(strings.Join(lines[v.offset:], "\n"), w, body)
}

// diagram draws stages as boxes joined by arrows; it falls back to a vertical
// list when the panel is too narrow.
func (v *pipelineView) diagram(e *core.Env, w int) string {
	n := len(v.stages)
	if n == 0 {
		return theme.Dim.Render("This pipeline has no stages.")
	}
	const arrowW = 5
	boxW := (w - (n-1)*arrowW) / n
	if boxW < 18 {
		return v.vertical(e, w)
	}
	boxW = min(boxW, 34)
	var parts []string
	for i, s := range v.stages {
		if i > 0 {
			parts = append(parts, v.arrow(e, v.stages[i-1], s))
		}
		parts = append(parts, v.box(e, s, boxW))
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, parts...)
}

func (v *pipelineView) box(e *core.Env, s cicd.Stage, w int) string {
	border := theme.Border
	switch s.Status {
	case "Succeeded":
		border = theme.Green
	case "Failed", "Stopped":
		border = theme.RedBorder
	case "InProgress":
		border = theme.YellowEdge
		if e.Pulse() {
			border = theme.Accent
		}
	}
	status := s.Status
	if status == "" {
		status = "not run"
	}
	lines := []string{theme.Strong.Render(components.Truncate(s.Name, w-4)), statusIcon(e, s.Status) + " " + theme.StatusTone(s.Status).Style().Render(status)}
	for _, a := range s.Actions {
		label := a.Name
		if a.Token != "" && a.Status == "InProgress" {
			label += " ⏸"
		}
		lines = append(lines, components.Truncate(statusIcon(e, a.Status)+" "+theme.Body.Render(label), w-4))
	}
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(border).Padding(0, 1).Width(w - 2).Render(strings.Join(lines, "\n"))
}

// arrow animates a dot travelling into a stage that is running.
func (v *pipelineView) arrow(e *core.Env, from, to cicd.Stage) string {
	a := "────▶"
	st := theme.Dim
	switch {
	case !to.TransitionEnabled:
		a, st = "──╳─▶", theme.Bad
	case to.Status == "InProgress":
		frames := []string{"•───▶", "─•──▶", "──•─▶", "───•▶"}
		a, st = frames[e.Tick%len(frames)], theme.Warn
	case from.Status == "Succeeded" && to.Status != "":
		st = theme.OK
	}
	return "\n\n" + st.Render(a)
}

func (v *pipelineView) vertical(e *core.Env, w int) string {
	var lines []string
	for i, s := range v.stages {
		if i > 0 {
			lines = append(lines, theme.Dim.Render("   │"))
		}
		lines = append(lines, statusIcon(e, s.Status)+" "+theme.Strong.Render(s.Name)+theme.Dim.Render("  "+dash(s.Status)))
		for _, a := range s.Actions {
			lines = append(lines, "   "+statusIcon(e, a.Status)+" "+components.Truncate(a.Name, w-6))
		}
	}
	return strings.Join(lines, "\n")
}

func ansiStrip(s string) string { return components.Strip(s) }
