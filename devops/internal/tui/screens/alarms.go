package screens

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"devops/internal/aws/console"
	"devops/internal/aws/meter"
	"devops/internal/aws/metrics"
	"devops/internal/executor"
	"devops/internal/tui/components"
	"devops/internal/tui/core"
	"devops/internal/tui/theme"
)

// alarmView shows an alarm's state, reason, state-change timeline and its
// metric with the threshold drawn across the chart.
type alarmView struct {
	alarm      metrics.Alarm
	history    []metrics.StateChange
	histErr    error
	points     []metrics.Point
	chartErr   error
	charting   bool
	chartReady bool
	since      time.Duration
	offset     int
}

func newAlarmView(e *core.Env, a metrics.Alarm) (*alarmView, tea.Cmd) {
	v := &alarmView{alarm: a, since: 6 * time.Hour}
	s := e.Session
	cmds := []tea.Cmd{e.Run("alarms", "view:history", executor.Spec{Name: "Alarm history", Quiet: true, Resources: []string{a.Name}},
		func(ctx context.Context, _ *executor.Job) (any, error) { return metrics.History(ctx, s, a.Name) })}
	if !e.Deps.Config.Limits.ConfirmBilled {
		cmds = append(cmds, v.loadChart(e))
	}
	return v, tea.Batch(cmds...)
}

func (v *alarmView) loadChart(e *core.Env) tea.Cmd {
	a := v.alarm
	if a.Metric == "" {
		v.chartErr = fmt.Errorf("composite alarms have no single metric to chart")
		return nil
	}
	v.charting, v.chartErr = true, nil
	s, since := e.Session, v.since
	period := time.Duration(max(60, a.Period)) * time.Second
	// Keep the request under ~360 datapoints.
	for since/period > 360 {
		period *= 2
	}
	return e.Run("alarms", "view:chart", executor.Spec{Name: "Alarm metric", Quiet: true, Resources: []string{a.Name}},
		func(ctx context.Context, _ *executor.Job) (any, error) {
			return metrics.Statistics(ctx, s, metrics.StatQuery{Namespace: a.Namespace, Metric: a.Metric, Dimensions: a.Dimensions,
				Since: since, Period: period, Stat: a.Statistic})
		})
}

func (v *alarmView) Capturing() bool       { return false }
func (v *alarmView) Back(e *core.Env) bool { return false }

func (v *alarmView) Hints() []core.Hint {
	return []core.Hint{{Key: "m", Label: "Load chart $"}, {Key: "t", Label: "Time range"}, {Key: "o", Label: "Console"}, {Key: "esc", Label: "Back"}}
}

var alarmRanges = []time.Duration{time.Hour, 6 * time.Hour, 24 * time.Hour, 3 * 24 * time.Hour}

func (v *alarmView) Update(e *core.Env, msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case core.ResultMsg:
		switch m.Key {
		case "view:history":
			v.histErr = m.Err
			v.history, _ = m.Value.([]metrics.StateChange)
		case "view:chart":
			v.charting, v.chartErr, v.chartReady = false, m.Err, true
			v.points, _ = m.Value.([]metrics.Point)
		}
	case core.MouseMsg:
		if m.WheelUp {
			v.offset = max(0, v.offset-3)
		}
		if m.WheelDown {
			v.offset += 3
		}
	case tea.KeyMsg:
		switch m.String() {
		case "m":
			return tea.Batch(v.loadChart(e), e.Toast("$ "+meter.BilledNote("CloudWatch", "GetMetricStatistics"), theme.ToneWarn))
		case "t":
			for i, r := range alarmRanges {
				if r == v.since {
					v.since = alarmRanges[(i+1)%len(alarmRanges)]
					break
				}
			}
			if v.chartReady {
				return v.loadChart(e)
			}
		case "o":
			return e.OpenURL(console.Alarm(e.Ctx().Region, v.alarm.Name), false)
		case "y":
			return e.Copy(v.alarm.Name, "alarm name")
		case "up", "k":
			v.offset = max(0, v.offset-1)
		case "down", "j":
			v.offset++
		}
	}
	return nil
}

func (v *alarmView) View(e *core.Env, w, h int) string {
	a := v.alarm
	head := headingRaw(theme.Title.Render("ALARM › ")+a.Name, "changed "+components.Ago(a.Updated), w) + "\n\n"
	var lines []string
	state := theme.StatusTone(a.State).Style().Bold(true).Render(" " + a.State + " ")
	if a.State == "ALARM" && !e.Pulse() {
		state = theme.Dim.Render(" " + a.State + " ")
	}
	lines = append(lines, state+theme.Dim.Render("  "+a.Namespace+" / "+a.Metric))
	var dims []string
	for k, val := range a.Dimensions {
		dims = append(dims, k+"="+val)
	}
	sort.Strings(dims)
	cond := ""
	if a.Threshold != nil {
		cond = fmt.Sprintf("%s %s %g (period %ds)", dash(a.Statistic), comparison(a.Comparison), *a.Threshold, a.Period)
	}
	for _, p := range [][2]string{{"Condition", cond}, {"Dimensions", strings.Join(dims, "  ")}, {"Description", a.Description}} {
		if p[1] != "" {
			lines = append(lines, theme.Dim.Render(components.Pad(p[0], 13))+theme.Body.Render(p[1]))
		}
	}
	for i, l := range components.WrapLines(a.Reason, w-13) {
		key := ""
		if i == 0 {
			key = "Reason"
		}
		lines = append(lines, theme.Dim.Render(components.Pad(key, 13))+theme.Body.Render(l))
	}
	lines = append(lines, "", theme.Title.Render("METRIC · last "+components.Dur(v.since))+theme.Dim.Render("   t change range"))
	switch {
	case v.charting:
		lines = append(lines, loadingView(e, "metric"))
	case v.chartErr != nil:
		lines = append(lines, strings.Split(errorView(v.chartErr, w), "\n")...)
	case !v.chartReady:
		lines = append(lines, theme.Warn.Render("$ ")+theme.Dim.Render(meter.BilledNote("CloudWatch", "GetMetricStatistics")+" — press m to load the chart"))
	case len(v.points) == 0:
		lines = append(lines, theme.Dim.Render("No datapoints in this range."))
	default:
		vals := make([]float64, len(v.points))
		for i, p := range v.points {
			vals[i] = p.Average
		}
		st := theme.Title
		if a.State == "ALARM" {
			st = theme.Warn
		}
		lines = append(lines, components.LineChart([]components.Series{{Values: vals, Style: st}}, w, 8, "", a.Threshold)...)
		lines = append(lines, theme.Dim.Render(components.Pad(v.points[0].Time.Local().Format("01-02 15:04"), w-11)+v.points[len(v.points)-1].Time.Local().Format("01-02 15:04")))
		if a.Threshold != nil {
			lines = append(lines, theme.Bad.Render("┄┄ threshold ")+theme.Dim.Render(fmt.Sprint(*a.Threshold))+"   "+st.Render("── "+dash(a.Statistic)))
		}
	}
	lines = append(lines, "", theme.Title.Render("STATE CHANGES"))
	switch {
	case v.histErr != nil:
		lines = append(lines, theme.Bad.Render(v.histErr.Error()))
	case v.history == nil:
		lines = append(lines, loadingView(e, "history"))
	case len(v.history) == 0:
		lines = append(lines, theme.Dim.Render("No state changes recorded."))
	}
	for _, c := range v.history {
		tone := theme.ToneNormal
		switch {
		case strings.Contains(c.Summary, "to ALARM"):
			tone = theme.ToneBad
		case strings.Contains(c.Summary, "to OK"):
			tone = theme.ToneOK
		}
		lines = append(lines, theme.Dim.Render(c.At.Local().Format("01-02 15:04")+"  ")+tone.Style().Render(components.Truncate(c.Summary, w-13)))
	}
	body := h - 2
	v.offset = max(0, min(v.offset, len(lines)-body))
	return head + components.Fit(strings.Join(lines[v.offset:], "\n"), w, body)
}

func comparison(op string) string {
	switch op {
	case "GreaterThanThreshold":
		return ">"
	case "GreaterThanOrEqualToThreshold":
		return "≥"
	case "LessThanThreshold":
		return "<"
	case "LessThanOrEqualToThreshold":
		return "≤"
	}
	return op
}
