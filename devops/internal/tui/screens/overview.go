package screens

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"devops/internal/aws/summary"
	"devops/internal/executor"
	"devops/internal/safety"
	"devops/internal/tui/components"
	"devops/internal/tui/core"
	"devops/internal/tui/theme"
)

type Overview struct {
	loading  bool
	sum      *summary.Summary
	loadedAt time.Time
}

func NewOverview() *Overview { return &Overview{} }

func (o *Overview) ID() string      { return "overview" }
func (o *Overview) Capturing() bool { return false }

func (o *Overview) Hints() []core.Hint {
	return []core.Hint{{Key: "r", Label: "Refresh"}, {Key: "L", Label: "SSO login"}}
}

func (o *Overview) Init(e *core.Env) tea.Cmd {
	if err := e.Session.Ready(); err != nil {
		return nil
	}
	o.loading, o.sum = true, nil
	s := e.Session
	return e.Run(o.ID(), "summary",
		executor.Spec{Name: "Load infrastructure summary", Quiet: true, Timeout: 90 * time.Second},
		func(ctx context.Context, _ *executor.Job) (any, error) {
			sum := summary.Load(ctx, s)
			return &sum, sum.AllFailed()
		})
}

func (o *Overview) Update(e *core.Env, msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case core.ResultMsg:
		o.loading = false
		if v, ok := m.Value.(*summary.Summary); ok {
			o.sum, o.loadedAt = v, time.Now()
		}
	case tea.KeyMsg:
		if m.String() == "r" && !o.loading {
			return o.Init(e)
		}
	}
	return nil
}

func (o *Overview) View(e *core.Env, w, h int) string {
	var b strings.Builder
	right := ""
	if o.loading {
		right = e.Spinner() + " loading"
	} else if !o.loadedAt.IsZero() {
		right = "updated " + components.Ago(o.loadedAt)
	}
	b.WriteString(heading("AWS Infrastructure Summary", right, w) + "\n\n")

	if err := e.Session.Ready(); err != nil {
		b.WriteString(errorView(err, w))
		return b.String()
	}
	if e.IdentityErr != nil {
		b.WriteString(theme.Box(theme.RedBorder).Width(w-2).Render(errorView(e.IdentityErr, w-4)) + "\n")
	}

	var c summary.Summary
	if o.sum != nil {
		c = *o.sum
	}
	cards := []struct {
		label string
		count summary.Count
	}{{"EC2 Instances", c.EC2}, {"ECS Clusters", c.ECS}, {"RDS Databases", c.RDS}, {"Lambda Functions", c.Lambda}}
	n := 4
	switch {
	case w < 60:
		n = 2
	case w < 84:
		n = 3
	}
	var row []string
	cw := (w - (n - 1)) / n
	for i, cd := range cards {
		if i > 0 && i%n == 0 {
			b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, row...) + "\n")
			row = nil
		}
		width := cw
		if i%n == n-1 {
			width = w - (n-1)*(cw+1) // last card absorbs rounding
		}
		if len(row) > 0 {
			row = append(row, " ")
		}
		row = append(row, o.card(e, cd.label, cd.count, width, theme.ToneNormal))
	}
	b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, row...) + "\n")

	hw := (w - 1) / 2
	ut, at := theme.ToneNormal, theme.ToneNormal
	if c.UnhealthyTargets.Value > 0 {
		ut = theme.ToneBad
	}
	if c.ActiveAlarms.Value > 0 {
		at = theme.ToneWarn
	}
	b.WriteString(lipgloss.JoinHorizontal(lipgloss.Top,
		o.alert(e, "Unhealthy Targets", c.UnhealthyTargets, hw, ut), " ",
		o.alert(e, "Active Alarms", c.ActiveAlarms, w-hw-1, at)) + "\n\n")

	b.WriteString(theme.Title.Render("RECENT ACTIVITY") + "\n")
	used := lipgloss.Height(b.String())
	b.WriteString(recent(e, w, max(1, h-used)))
	return b.String()
}

func (o *Overview) card(e *core.Env, label string, c summary.Count, w int, tone theme.Tone) string {
	value, detail := theme.Strong.Render(fmt.Sprint(c.Value)), theme.Dim.Render(c.Detail)
	switch {
	case o.loading && o.sum == nil:
		value, detail = theme.Dim.Render(e.Spinner()), ""
	case c.Err != nil:
		value, detail = theme.Dim.Render("—"), theme.Bad.Render(string(safety.Classify(c.Err)))
	}
	body := theme.Dim.Render(label) + "\n" + value + "\n" + detail
	return theme.Box(tone.Border()).Width(w - 2).Render(components.Fit(body, w-4, 3))
}

func (o *Overview) alert(e *core.Env, label string, c summary.Count, w int, tone theme.Tone) string {
	title := fmt.Sprintf("%s: %d", label, c.Value)
	style := theme.Body
	if tone != theme.ToneNormal {
		style = tone.Style()
		if tone == theme.ToneWarn && !e.Pulse() {
			style = theme.Dim
		}
	}
	detail := c.Detail
	switch {
	case o.loading && o.sum == nil:
		title, detail = label+": "+e.Spinner(), ""
	case c.Err != nil:
		title, detail = label+": —", string(safety.Classify(c.Err))+" error"
	case c.Value == 0:
		detail = "none"
	}
	body := style.Render(title) + "\n" + theme.Dim.Render(detail)
	return theme.Box(tone.Border()).Width(w - 2).Render(components.Fit(body, w-4, 2))
}

// recent renders the executor's latest operations, like "• Tail API logs — running".
func recent(e *core.Env, w, h int) string {
	views := e.Deps.Exec.Views()
	if len(views) == 0 {
		return theme.Dim.Render("No commands run yet. Press / to search for an action.")
	}
	var lines []string
	for _, v := range views {
		if len(lines) >= h {
			break
		}
		when := components.Ago(v.Ended)
		switch {
		case !v.Status.Done() && (v.Kind == executor.KindStream || v.Kind == executor.KindInteractive):
			when = "running in background"
		case !v.Status.Done():
			when = "running " + components.Dur(v.Elapsed())
		case v.Status != executor.Completed:
			when = string(v.Status) + " · " + components.Ago(v.Ended)
		}
		name := v.Name
		if len(v.Resources) > 0 {
			name += " (" + strings.Join(v.Resources, ", ") + ")"
		}
		style := theme.Body
		if v.Quiet {
			style = theme.Dim
		}
		lines = append(lines, components.Truncate(core.StatusIcon(e, v.Status)+" "+style.Render(name)+theme.Dim.Render(" — "+when), w))
	}
	return strings.Join(lines, "\n")
}
