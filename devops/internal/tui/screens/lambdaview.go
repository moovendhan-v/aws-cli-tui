package screens

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"devops/internal/aws/console"
	"devops/internal/aws/lambda"
	"devops/internal/aws/logs"
	"devops/internal/aws/meter"
	"devops/internal/aws/metrics"
	"devops/internal/executor"
	"devops/internal/safety"
	"devops/internal/tui/components"
	"devops/internal/tui/core"
	"devops/internal/tui/theme"
)

var lambdaTabs = []string{"Overview", "Metrics", "Recent errors"}

// lambdaView shows one function: configuration, invocation/error/duration
// charts and its most recent error log lines.
type lambdaView struct {
	name   string
	tab    int
	offset int
	tabHit map[int][2]int

	d       *lambda.Detail
	err     error
	loading bool

	series        map[string][]metrics.Point
	metricsErr    error
	metricsBusy   bool
	metricsLoaded bool

	errors     []logs.Event
	errorsErr  error
	errorsBusy bool
	errorsDone bool
}

func newLambdaView(e *core.Env, name string) (*lambdaView, tea.Cmd) {
	v := &lambdaView{name: name, tabHit: map[int][2]int{}}
	return v, v.load(e, false)
}

func (v *lambdaView) load(e *core.Env, force bool) tea.Cmd {
	v.loading = true
	s, name := e.Session, v.name
	return e.Cached("lambda", "view:fn", e.CacheKey("lambda", "fn", name), force,
		executor.Spec{Name: "Describe Lambda function", Quiet: true, Resources: []string{name}},
		func(ctx context.Context, _ *executor.Job) (any, error) { return lambda.Describe(ctx, s, name) })
}

func (v *lambdaView) loadMetrics(e *core.Env) tea.Cmd {
	v.metricsBusy, v.metricsErr = true, nil
	s, name := e.Session, v.name
	return e.Run("lambda", "view:metrics", executor.Spec{Name: "Lambda metrics", Quiet: true, Resources: []string{name}},
		func(ctx context.Context, _ *executor.Job) (any, error) {
			return metrics.Multi(ctx, s, "AWS/Lambda", map[string]string{"FunctionName": name}, []metrics.Spec{
				{Metric: "Invocations", Stat: "Sum"}, {Metric: "Errors", Stat: "Sum"}, {Metric: "Throttles", Stat: "Sum"},
				{Metric: "Duration", Stat: "Average"}, {Metric: "Duration", Stat: "Maximum"},
			}, 3*time.Hour, 5*time.Minute)
		})
}

func (v *lambdaView) loadErrors(e *core.Env) tea.Cmd {
	if v.d == nil {
		return nil
	}
	v.errorsBusy, v.errorsErr = true, nil
	s, group := e.Session, v.d.LogGroup
	return e.Run("lambda", "view:errors", executor.Spec{Name: "Lambda recent errors", Quiet: true, Resources: []string{group}},
		func(ctx context.Context, _ *executor.Job) (any, error) {
			h := logs.NewHistory(s, group, errorPattern, time.Hour, time.Time{})
			return h.Older(ctx, 50)
		})
}

func (v *lambdaView) ensure(e *core.Env) tea.Cmd {
	switch v.tab {
	case 1:
		if !v.metricsLoaded && !v.metricsBusy && !e.Deps.Config.Limits.ConfirmBilled {
			return v.loadMetrics(e)
		}
	case 2:
		if !v.errorsDone && !v.errorsBusy {
			return v.loadErrors(e)
		}
	}
	return nil
}

func (v *lambdaView) Capturing() bool       { return false }
func (v *lambdaView) Back(e *core.Env) bool { return false }

func (v *lambdaView) Hints() []core.Hint {
	h := []core.Hint{{Key: "←→", Label: "Tabs"}, {Key: "i", Label: "Invoke"}, {Key: "l", Label: "Logs"}}
	if v.tab == 1 {
		h = append(h, core.Hint{Key: "m", Label: "Load metrics $"})
	}
	return append(h, core.Hint{Key: "o", Label: "Console"}, core.Hint{Key: "r", Label: "Refresh"}, core.Hint{Key: "esc", Label: "Back"})
}

func (v *lambdaView) Update(e *core.Env, msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case core.ResultMsg:
		switch m.Key {
		case "view:fn":
			v.loading, v.err = m.Refreshing, m.Err
			if d, ok := m.Value.(lambda.Detail); ok {
				v.d = &d
			}
			return v.ensure(e)
		case "view:metrics":
			v.metricsBusy, v.metricsErr, v.metricsLoaded = false, m.Err, true
			v.series, _ = m.Value.(map[string][]metrics.Point)
		case "view:errors":
			v.errorsBusy, v.errorsErr, v.errorsDone = false, m.Err, true
			v.errors, _ = m.Value.([]logs.Event)
		}
	case core.MouseMsg:
		switch {
		case m.Click && m.Y == 1:
			for i, r := range v.tabHit {
				if m.X >= r[0] && m.X < r[1] {
					v.tab, v.offset = i, 0
					return v.ensure(e)
				}
			}
		case m.WheelUp:
			v.offset = max(0, v.offset-3)
		case m.WheelDown:
			v.offset += 3
		}
	case tea.KeyMsg:
		switch m.String() {
		case "left", "[":
			v.tab, v.offset = (v.tab+len(lambdaTabs)-1)%len(lambdaTabs), 0
			return v.ensure(e)
		case "right", "]":
			v.tab, v.offset = (v.tab+1)%len(lambdaTabs), 0
			return v.ensure(e)
		case "1", "2", "3":
			n, _ := strconv.Atoi(m.String())
			v.tab, v.offset = n-1, 0
			return v.ensure(e)
		case "e":
			v.tab, v.offset = 2, 0
			return v.ensure(e)
		case "i":
			return e.RunCommandID("lambda.invoke", map[string]string{"function_name": v.name, "#form": "1"})
		case "l":
			group := "/aws/lambda/" + v.name
			if v.d != nil {
				group = v.d.LogGroup
			}
			return e.Navigate("logs", "open:"+group)
		case "m":
			return tea.Batch(v.loadMetrics(e), e.Toast("$ "+meter.BilledNote("CloudWatch", "GetMetricData"), theme.ToneWarn))
		case "r":
			cmds := []tea.Cmd{v.load(e, true)}
			switch v.tab {
			case 1:
				if v.metricsLoaded {
					cmds = append(cmds, v.loadMetrics(e))
				}
			case 2:
				cmds = append(cmds, v.loadErrors(e))
			}
			return tea.Batch(cmds...)
		case "o":
			return e.OpenURL(console.Lambda(e.Ctx().Region, v.name), false)
		case "y":
			return e.Copy(v.name, "function name")
		case "up", "k":
			v.offset = max(0, v.offset-1)
		case "down", "j":
			v.offset++
		case "pgup":
			v.offset = max(0, v.offset-10)
		case "pgdown":
			v.offset += 10
		}
	}
	return nil
}

func (v *lambdaView) View(e *core.Env, w, h int) string {
	right := ""
	if v.loading {
		right = e.Spinner() + " refreshing"
	}
	head := headingRaw(theme.Title.Render("LAMBDA › ")+v.name, right, w) + "\n"
	v.tabHit = map[int][2]int{}
	var tabs []string
	x := 0
	for i, t := range lambdaTabs {
		label := fmt.Sprintf(" %d %s ", i+1, t)
		st := theme.InfoBadge
		if i == v.tab {
			st = theme.NavActive
		}
		tabs = append(tabs, st.Render(label))
		width := lipgloss.Width(tabs[len(tabs)-1])
		v.tabHit[i] = [2]int{x, x + width}
		x += width + 1
	}
	head += strings.Join(tabs, " ") + "\n\n"
	if v.err != nil && v.d == nil {
		return head + errorView(v.err, w)
	}
	if v.d == nil {
		return head + loadingView(e, "function")
	}
	var lines []string
	switch v.tab {
	case 0:
		lines = v.overview(w)
	case 1:
		lines = v.metricsView(e, w)
	case 2:
		lines = v.errorsView(e, w)
	}
	body := h - 3
	v.offset = max(0, min(v.offset, len(lines)-body))
	return head + components.Fit(strings.Join(lines[v.offset:], "\n"), w, body)
}

func (v *lambdaView) overview(w int) []string {
	d := v.d
	state := theme.StatusTone(d.State).Style().Render(dash(d.State))
	lines := []string{state + theme.Dim.Render("  "+dash(d.Runtime)+" · "+dash(d.Arch)+" · "+dash(d.Package))}
	if d.Description != "" {
		lines = append(lines, theme.Body.Render(d.Description))
	}
	lines = append(lines, "")
	kv := [][2]string{
		{"Memory", fmt.Sprintf("%d MB", d.MemoryMB)}, {"Timeout", fmt.Sprintf("%d s", d.TimeoutSec)},
		{"Handler", d.Handler}, {"Code size", components.Bytes(d.CodeSize)},
		{"Ephemeral", fmt.Sprintf("%d MB", d.EphemeralMB)}, {"Modified", d.LastModified},
		{"Role", d.Role}, {"Log group", d.LogGroup},
		{"Tracing", d.Tracing}, {"Dead letter", d.DLQ},
		{"VPC", d.VPC}, {"Subnets", strings.Join(d.Subnets, ", ")},
		{"Layers", strings.Join(d.Layers, ", ")},
		{"Env (names)", strings.Join(d.EnvNames, ", ")},
		{"State reason", d.StateReason},
	}
	for _, p := range kv {
		if p[1] == "" {
			continue
		}
		for i, l := range components.WrapLines(p[1], max(20, w-15)) {
			key := ""
			if i == 0 {
				key = p[0]
			}
			lines = append(lines, theme.Dim.Render(components.Pad(key, 15))+theme.Body.Render(l))
		}
	}
	lines = append(lines, "", theme.Title.Render("ACTIONS"), hintLines([]core.Hint{
		{Key: "i", Label: "Invoke with a test event"}, {Key: "l", Label: "Open logs"}, {Key: "e", Label: "Recent errors"},
		{Key: "2", Label: "Metrics"}, {Key: "o", Label: "Console"}, {Key: "y", Label: "Copy name"},
	}, w))
	return lines
}

func values(pts []metrics.Point) ([]float64, float64) {
	out := make([]float64, len(pts))
	sum := 0.0
	for i, p := range pts {
		out[i] = p.Average
		sum += p.Average
	}
	return out, sum
}

func (v *lambdaView) metricsView(e *core.Env, w int) []string {
	switch {
	case v.metricsBusy:
		return []string{loadingView(e, "metrics")}
	case v.metricsErr != nil:
		return strings.Split(errorView(v.metricsErr, w), "\n")
	case !v.metricsLoaded:
		return []string{theme.Dim.Render("Invocations, errors, throttles and duration for the last 3 hours."), "",
			theme.Warn.Render("$ ") + theme.Dim.Render(meter.BilledNote("CloudWatch", "GetMetricData")), "",
			theme.Key.Render("m") + theme.Dim.Render(" load metrics")}
	}
	inv, invSum := values(v.series["Invocations/Sum"])
	errs, errSum := values(v.series["Errors/Sum"])
	_, thrSum := values(v.series["Throttles/Sum"])
	avgDur, avgSum := values(v.series["Duration/Average"])
	maxDur, _ := values(v.series["Duration/Maximum"])
	if len(inv) == 0 {
		return []string{theme.Dim.Render("No invocations in the last 3 hours.")}
	}
	rate := 0.0
	if invSum > 0 {
		rate = errSum / invSum * 100
	}
	rateStyle := theme.OK
	switch {
	case rate >= 5:
		rateStyle = theme.Bad
	case rate > 0:
		rateStyle = theme.Warn
	}
	peak := 0.0
	for _, d := range maxDur {
		peak = max(peak, d)
	}
	avg := 0.0
	if len(avgDur) > 0 {
		avg = avgSum / float64(len(avgDur))
	}
	stat := func(label, value string, st lipgloss.Style) string {
		return theme.Dim.Render(label+" ") + st.Render(value) + "    "
	}
	lines := []string{
		stat("invocations", fmt.Sprintf("%.0f", invSum), theme.Strong) + stat("errors", fmt.Sprintf("%.0f", errSum), rateStyle) +
			stat("error rate", fmt.Sprintf("%.2f%%", rate), rateStyle) + stat("throttles", fmt.Sprintf("%.0f", thrSum), theme.Body) +
			stat("duration avg", fmt.Sprintf("%.0f ms", avg), theme.Body) + stat("max", fmt.Sprintf("%.0f ms", peak), theme.Body),
		"",
		theme.Title.Render("── invocations") + "   " + theme.Bad.Render("── errors") + theme.Dim.Render("   per 5 minutes"),
	}
	lines = append(lines, components.LineChart([]components.Series{{Values: inv, Style: theme.Title}, {Values: errs, Style: theme.Bad}}, w, 8, "", nil)...)
	lines = append(lines, "", theme.Title.Render("── avg duration")+"   "+theme.Warn.Render("── max duration")+theme.Dim.Render("   ms"))
	lines = append(lines, components.LineChart([]components.Series{{Values: avgDur, Style: theme.Title}, {Values: maxDur, Style: theme.Warn}}, w, 6, "", nil)...)
	if pts := v.series["Invocations/Sum"]; len(pts) > 0 {
		lines = append(lines, theme.Dim.Render(components.Pad(pts[0].Time.Local().Format("15:04"), w-5)+pts[len(pts)-1].Time.Local().Format("15:04")))
	}
	return lines
}

func (v *lambdaView) errorsView(e *core.Env, w int) []string {
	switch {
	case v.errorsBusy:
		return []string{loadingView(e, "recent errors")}
	case v.errorsErr != nil:
		return strings.Split(errorView(v.errorsErr, w), "\n")
	case len(v.errors) == 0:
		return []string{theme.OK.Render("✓ No ERROR / Exception / timeout lines in " + v.d.LogGroup + " during the last hour.")}
	}
	lines := []string{theme.Dim.Render(fmt.Sprintf("%d most recent error lines from %s (last hour) · l opens the full log", len(v.errors), v.d.LogGroup)), ""}
	for i := len(v.errors) - 1; i >= 0; i-- {
		ev := v.errors[i]
		prefix := theme.Dim.Render(ev.Time.Local().Format("01-02 15:04:05") + "  ")
		for j, l := range readable(safety.Redact(strings.TrimRight(ev.Message, "\n"))) {
			for k, part := range components.WrapLines(l, max(20, w-17)) {
				if j == 0 && k == 0 {
					lines = append(lines, prefix+part)
				} else {
					lines = append(lines, strings.Repeat(" ", 17)+part)
				}
			}
		}
	}
	return lines
}
