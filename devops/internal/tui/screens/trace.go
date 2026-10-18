package screens

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/aws/auth"
	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/aws/ec2"
	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/aws/ecs"
	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/aws/lambda"
	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/aws/logs"
	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/aws/metrics"
	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/aws/networking"
	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/aws/rds"
	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/executor"
	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/safety"
	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/tui/components"
	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/tui/core"
	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/tui/theme"
)

// traceHop is one step of a "follow the error" chain.
type traceHop struct {
	Kind, Title, Status string
	Lines               []string
	Tone                theme.Tone
	Screen, Query       string // where Enter jumps
}

type traceStart struct {
	Alarm      string
	TargetARN  string // target group ARN
	TargetName string
}

const (
	traceMaxClusters = 20
	traceErrorWindow = 30 * time.Minute
)

// runTrace walks alarm → target group → ECS service / EC2 → logs (or Lambda,
// RDS) and returns the hops it could resolve. Calls are bounded.
func runTrace(ctx context.Context, s *auth.Session, start traceStart) ([]traceHop, error) {
	var hops []traceHop
	var tgs []networking.TargetGroup
	if start.Alarm != "" {
		a, err := metrics.AlarmByName(ctx, s, start.Alarm)
		if err != nil {
			return nil, err
		}
		cond := a.Namespace + "/" + a.Metric
		if a.Threshold != nil {
			cond += fmt.Sprintf(" %s %s %g", dash(a.Statistic), comparison(a.Comparison), *a.Threshold)
		}
		hops = append(hops, traceHop{Kind: "Alarm", Title: a.Name, Status: a.State, Tone: theme.StatusTone(a.State),
			Lines: []string{cond, a.Reason}, Screen: "alarms", Query: a.Name})
		d := a.Dimensions
		switch {
		case d["TargetGroup"] != "":
			name := strings.Split(d["TargetGroup"], "/")[1]
			all, err := networking.TargetGroups(ctx, s, "")
			if err != nil {
				return hops, err
			}
			for _, tg := range all {
				if tg.Name == name {
					tgs = append(tgs, tg)
				}
			}
		case d["LoadBalancer"] != "":
			parts := strings.Split(d["LoadBalancer"], "/")
			lbs, err := networking.LoadBalancers(ctx, s)
			if err != nil {
				return hops, err
			}
			for _, lb := range lbs {
				if len(parts) > 1 && lb.Name == parts[1] {
					tgs, err = networking.TargetGroups(ctx, s, lb.ARN)
					if err != nil {
						return hops, err
					}
				}
			}
		case d["ClusterName"] != "" && d["ServiceName"] != "":
			return append(hops, ecsHops(ctx, s, d["ClusterName"], d["ServiceName"])...), nil
		case d["InstanceId"] != "":
			return append(hops, ec2Hop(ctx, s, []string{d["InstanceId"]})), nil
		case d["FunctionName"] != "":
			return append(hops, lambdaHops(ctx, s, d["FunctionName"])...), nil
		case d["DBInstanceIdentifier"] != "":
			return append(hops, rdsHop(ctx, s, d["DBInstanceIdentifier"])), nil
		default:
			return append(hops, traceHop{Kind: "Next step", Title: "No resource to follow", Tone: theme.ToneDim,
				Lines: []string{"This alarm's dimensions don't point at a load balancer, ECS service, instance, function or database."}}), nil
		}
	}
	if start.TargetARN != "" {
		tgs = append(tgs, networking.TargetGroup{ARN: start.TargetARN, Name: start.TargetName})
	}
	if len(tgs) > 3 {
		tgs = tgs[:3]
	}
	var clusters []ecs.Cluster
	for _, tg := range tgs {
		targets, err := networking.TargetHealth(ctx, s, tg.ARN)
		if err != nil {
			hops = append(hops, traceHop{Kind: "Target group", Title: tg.Name, Status: "error", Tone: theme.ToneWarn, Lines: []string{safety.Describe(err)}})
			continue
		}
		healthy, reasons, instances := 0, map[string]int{}, []string{}
		for _, t := range targets {
			if t.Healthy() {
				healthy++
			} else {
				reasons[dash(t.Reason)+" "+t.Description]++
			}
			if strings.HasPrefix(t.ID, "i-") {
				instances = append(instances, t.ID)
			}
		}
		tone := theme.ToneOK
		if healthy < len(targets) {
			tone = theme.ToneBad
		}
		hops = append(hops, traceHop{Kind: "Target group", Title: tg.Name, Status: fmt.Sprintf("%d/%d healthy", healthy, len(targets)),
			Tone: tone, Lines: topCounts(reasons, 3), Screen: "elb", Query: ""})
		if len(instances) > 0 {
			hops = append(hops, ec2Hop(ctx, s, instances))
			continue
		}
		// IP targets: find ECS services attached to this target group.
		if clusters == nil {
			clusters, _ = ecs.ListClusters(ctx, s)
			if len(clusters) > traceMaxClusters {
				clusters = clusters[:traceMaxClusters]
			}
		}
		found := 0
		for _, c := range clusters {
			svcs, err := ecs.ListServices(ctx, s, c.Name)
			if err != nil {
				continue
			}
			for _, sv := range svcs {
				for _, arn := range sv.TargetGroups {
					if arn == tg.ARN && found < 2 {
						found++
						hops = append(hops, ecsHops(ctx, s, c.Name, sv.Name)...)
					}
				}
			}
		}
		if found == 0 && len(targets) > 0 {
			hops = append(hops, traceHop{Kind: "Targets", Title: "IP targets", Tone: theme.ToneDim,
				Lines: []string{"No ECS service in the first " + fmt.Sprint(traceMaxClusters) + " clusters uses this target group."}})
		}
	}
	return hops, nil
}

func topCounts(m map[string]int, n int) []string {
	type kv struct {
		k string
		v int
	}
	var list []kv
	for k, v := range m {
		list = append(list, kv{strings.TrimSpace(k), v})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].v > list[j].v })
	var out []string
	for i, x := range list {
		if i >= n {
			break
		}
		out = append(out, fmt.Sprintf("%s ×%d", x.k, x.v))
	}
	return out
}

func ecsHops(ctx context.Context, s *auth.Session, cluster, service string) []traceHop {
	d, err := ecs.DescribeService(ctx, s, cluster, service)
	if err != nil {
		return []traceHop{{Kind: "ECS service", Title: cluster + "/" + service, Status: "error", Tone: theme.ToneWarn, Lines: []string{safety.Describe(err)}}}
	}
	tone := theme.ToneOK
	switch {
	case d.Running < d.Desired || d.Rollout == "FAILED":
		tone = theme.ToneBad
	case d.Rollout == "IN_PROGRESS":
		tone = theme.ToneWarn
	}
	var lines []string
	for _, ev := range d.Events {
		low := strings.ToLower(ev.Message)
		if strings.Contains(low, "unhealthy") || strings.Contains(low, "failed") || strings.Contains(low, "unable") {
			lines = append(lines, components.Ago(ev.At)+": "+ev.Message)
			if len(lines) == 2 {
				break
			}
		}
	}
	svc := traceHop{Kind: "ECS service", Title: cluster + "/" + service, Tone: tone,
		Status: fmt.Sprintf("%d/%d running · rollout %s", d.Running, d.Desired, dash(d.Rollout)),
		Lines:  lines, Screen: "ecs", Query: "service:" + cluster + "/" + service}
	hops := []traceHop{svc}

	if tasks, err := ecs.ListTasks(ctx, s, cluster, service); err == nil {
		reasons := map[string]int{}
		stopped := 0
		for _, t := range tasks {
			if t.LastStatus != "STOPPED" {
				continue
			}
			stopped++
			r := t.StoppedReason
			for _, c := range t.Containers {
				if c.Reason != "" {
					r = c.Reason
				} else if c.ExitCode != nil && *c.ExitCode != 0 {
					r = fmt.Sprintf("%s (exit %d)", r, *c.ExitCode)
				}
			}
			reasons[r]++
		}
		if stopped > 0 {
			hops = append(hops, traceHop{Kind: "Stopped tasks", Title: fmt.Sprintf("%d recently stopped", stopped), Tone: theme.ToneBad,
				Lines: topCounts(reasons, 3), Screen: "ecs", Query: "service:" + cluster + "/" + service})
		}
	}
	if td, err := ecs.DescribeTaskDef(ctx, s, d.TaskDefARN); err == nil {
		for _, c := range td.Containers {
			if c.LogGroup != "" {
				hops = append(hops, logsHop(ctx, s, c.LogGroup))
				break
			}
		}
	}
	return hops
}

func ec2Hop(ctx context.Context, s *auth.Session, ids []string) traceHop {
	if len(ids) > 10 {
		ids = ids[:10]
	}
	st, err := ec2.Health(ctx, s, ids)
	if err != nil {
		return traceHop{Kind: "EC2", Title: strings.Join(ids, ", "), Status: "error", Tone: theme.ToneWarn, Lines: []string{safety.Describe(err)}}
	}
	tone := theme.ToneOK
	var lines []string
	for _, id := range ids {
		v := st[id]
		if !strings.Contains(v, "checks ok") {
			tone = theme.ToneBad
		}
		lines = append(lines, id+"  "+dash(v))
	}
	return traceHop{Kind: "EC2 targets", Title: fmt.Sprintf("%d instances", len(ids)), Tone: tone, Lines: lines, Screen: "ec2", Query: ids[0]}
}

func lambdaHops(ctx context.Context, s *auth.Session, name string) []traceHop {
	d, err := lambda.Describe(ctx, s, name)
	if err != nil {
		return []traceHop{{Kind: "Lambda", Title: name, Status: "error", Tone: theme.ToneWarn, Lines: []string{safety.Describe(err)}}}
	}
	return []traceHop{
		{Kind: "Lambda", Title: name, Status: dash(d.State), Tone: theme.StatusTone(d.State),
			Lines:  []string{d.Runtime + " · " + fmt.Sprint(d.MemoryMB) + " MB · timeout " + fmt.Sprint(d.TimeoutSec) + "s", d.StateReason},
			Screen: "lambda", Query: "fn:" + name},
		logsHop(ctx, s, d.LogGroup),
	}
}

func rdsHop(ctx context.Context, s *auth.Session, id string) traceHop {
	list, err := rds.List(ctx, s)
	if err != nil {
		return traceHop{Kind: "Database", Title: id, Status: "error", Tone: theme.ToneWarn, Lines: []string{safety.Describe(err)}}
	}
	for _, d := range list {
		if d.ID == id {
			return traceHop{Kind: "Database", Title: id, Status: d.Status, Tone: theme.StatusTone(d.Status),
				Lines: []string{d.Engine + " " + d.Version + " · " + d.Class, d.EndpointLabel()}, Screen: "rds", Query: id}
		}
	}
	return traceHop{Kind: "Database", Title: id, Status: "not found", Tone: theme.ToneWarn}
}

func logsHop(ctx context.Context, s *auth.Session, group string) traceHop {
	h := logs.NewHistory(s, group, errorPattern, traceErrorWindow, time.Time{})
	ev, err := h.Older(ctx, 200)
	if err != nil {
		return traceHop{Kind: "Logs", Title: group, Status: "error", Tone: theme.ToneWarn, Lines: []string{safety.Describe(err)}}
	}
	if len(ev) == 0 {
		return traceHop{Kind: "Logs", Title: group, Status: "no errors in 30 min", Tone: theme.ToneOK, Screen: "logs", Query: "open:" + group}
	}
	count := fmt.Sprintf("%d error lines in 30 min", len(ev))
	if len(ev) >= 200 {
		count = "200+ error lines in 30 min"
	}
	var lines []string
	for i := len(ev) - 1; i >= 0 && len(lines) < 3; i-- {
		first := strings.SplitN(safety.Redact(ev[i].Message), "\n", 2)[0]
		lines = append(lines, ev[i].Time.Local().Format("15:04:05")+"  "+components.Strip(readable(first)[0]))
	}
	return traceHop{Kind: "Logs", Title: group, Status: count, Tone: theme.ToneBad, Lines: lines, Screen: "logs", Query: "errors:" + group}
}

// traceView renders the chain vertically; Enter jumps to the selected hop.
type traceView struct {
	screen  string
	start   traceStart
	hops    []traceHop
	err     error
	loading bool
	cursor  int
	offset  int
}

func newTraceView(e *core.Env, screen string, start traceStart) (*traceView, tea.Cmd) {
	v := &traceView{screen: screen, start: start}
	return v, v.run(e)
}

func (v *traceView) run(e *core.Env) tea.Cmd {
	v.loading, v.err = true, nil
	s, start := e.Session, v.start
	return e.Run(v.screen, "view:trace", executor.Spec{Name: "Follow the error", Quiet: true, Timeout: 90 * time.Second},
		func(ctx context.Context, _ *executor.Job) (any, error) { return runTrace(ctx, s, start) })
}

func (v *traceView) Capturing() bool       { return false }
func (v *traceView) Back(e *core.Env) bool { return false }
func (v *traceView) Hints() []core.Hint {
	return []core.Hint{{Key: "↑↓", Label: "Select hop"}, {Key: "enter", Label: "Open"}, {Key: "r", Label: "Re-run"}, {Key: "esc", Label: "Back"}}
}

func (v *traceView) Update(e *core.Env, msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case core.ResultMsg:
		if m.Key == "view:trace" {
			v.loading, v.err = false, m.Err
			v.hops, _ = m.Value.([]traceHop)
		}
	case core.MouseMsg:
		if m.WheelUp {
			v.cursor = max(0, v.cursor-1)
		}
		if m.WheelDown {
			v.cursor = min(len(v.hops)-1, v.cursor+1)
		}
	case tea.KeyMsg:
		switch m.String() {
		case "up", "k":
			v.cursor = max(0, v.cursor-1)
		case "down", "j":
			v.cursor = min(len(v.hops)-1, v.cursor+1)
		case "r":
			return v.run(e)
		case "enter":
			if v.cursor < len(v.hops) {
				h := v.hops[v.cursor]
				if h.Screen == "" {
					return e.Toast("Nothing to open for this step", theme.ToneDim)
				}
				return e.Navigate(h.Screen, h.Query)
			}
		}
	}
	return nil
}

func (v *traceView) View(e *core.Env, w, h int) string {
	origin := v.start.Alarm
	if origin == "" {
		origin = v.start.TargetName
	}
	right := ""
	if v.loading {
		right = e.Spinner() + " following"
	}
	head := headingRaw(theme.Title.Render("FOLLOW THE ERROR › ")+origin, right, w) + "\n\n"
	if v.err != nil && len(v.hops) == 0 {
		return head + errorView(v.err, w)
	}
	if v.loading && len(v.hops) == 0 {
		return head + loadingView(e, "the chain (alarm → targets → service → logs)")
	}
	var lines []string
	cursorLine := 0
	for i, hp := range v.hops {
		if i > 0 {
			lines = append(lines, theme.Dim.Render("  │"), theme.Dim.Render("  ▼"))
		}
		if i == v.cursor {
			cursorLine = len(lines)
		}
		dot := hp.Tone.Style().Render("●")
		title := theme.Dim.Render(components.Pad(hp.Kind, 14)) + theme.Strong.Render(hp.Title) + "  " + hp.Tone.Style().Render(hp.Status)
		row := "  " + dot + " " + title
		if i == v.cursor {
			row = theme.RowCursor.Render(components.Pad(" ▸"+components.Strip(row)[1:], w))
		}
		lines = append(lines, components.Truncate(row, w))
		for _, l := range hp.Lines {
			if strings.TrimSpace(l) == "" {
				continue
			}
			for _, wl := range components.WrapLines(l, w-20) {
				lines = append(lines, theme.Dim.Render("  │"+strings.Repeat(" ", 16))+theme.Body.Render(wl))
			}
		}
		if hp.Screen != "" && i == v.cursor {
			lines = append(lines, theme.Dim.Render("  │"+strings.Repeat(" ", 16))+theme.Key.Render("enter")+theme.Dim.Render(" open"))
		}
	}
	body := h - 2
	if cursorLine < v.offset {
		v.offset = cursorLine
	}
	if cursorLine >= v.offset+body-3 {
		v.offset = cursorLine - body + 4
	}
	v.offset = max(0, min(v.offset, len(lines)-body))
	return head + components.Fit(strings.Join(lines[v.offset:], "\n"), w, body)
}

// NewECS, NewLambda and NewAlarms add direct-open hooks used by traces,
// search and the Changes screen.
func NewECS() *Resource {
	return NewResource("ecs", ECSClusters).WithFocus(func(e *core.Env, q string) (Viewer, tea.Cmd, bool) {
		if ref, ok := strings.CutPrefix(q, "service:"); ok {
			if c, sv, ok := strings.Cut(ref, "/"); ok {
				v, cmd := newECSServiceView(e, "ecs", c, sv)
				return v, cmd, true
			}
		}
		return nil, nil, false
	})
}

func NewLambda() *Resource {
	return NewResource("lambda", LambdaFunctions).WithFocus(func(e *core.Env, q string) (Viewer, tea.Cmd, bool) {
		if name, ok := strings.CutPrefix(q, "fn:"); ok {
			v, cmd := newLambdaView(e, name)
			return v, cmd, true
		}
		return nil, nil, false
	})
}

func NewAlarms() *Resource {
	return NewResource("alarms", Alarms).WithFocus(func(e *core.Env, q string) (Viewer, tea.Cmd, bool) {
		if name, ok := strings.CutPrefix(q, "trace:"); ok {
			v, cmd := newTraceView(e, "alarms", traceStart{Alarm: name})
			return v, cmd, true
		}
		return nil, nil, false
	})
}
