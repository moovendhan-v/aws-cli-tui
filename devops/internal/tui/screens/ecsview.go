package screens

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/moovendhan-v/aws-cli-tui/devops/internal/aws/console"
	"github.com/moovendhan-v/aws-cli-tui/devops/internal/aws/ecs"
	"github.com/moovendhan-v/aws-cli-tui/devops/internal/aws/meter"
	"github.com/moovendhan-v/aws-cli-tui/devops/internal/aws/metrics"
	"github.com/moovendhan-v/aws-cli-tui/devops/internal/executor"
	"github.com/moovendhan-v/aws-cli-tui/devops/internal/tui/components"
	"github.com/moovendhan-v/aws-cli-tui/devops/internal/tui/core"
	"github.com/moovendhan-v/aws-cli-tui/devops/internal/tui/theme"
)

var ecsTabs = []string{"Overview", "Deployments", "Tasks", "Events", "Task definition", "Metrics"}

const (
	tabOverview = iota
	tabDeployments
	tabTasks
	tabEvents
	tabTaskDef
	tabMetrics
)

// ecsServiceView is the tabbed view for one ECS service.
type ecsServiceView struct {
	screen, cluster, service string
	tab                      int
	offset                   int
	tabHit                   map[int][2]int

	sum        *ecs.ServiceDetail
	sumErr     error
	sumLoading bool
	sumAt      time.Time

	tasks        *components.Table
	tasksErr     error
	tasksLoading bool
	tasksLoaded  bool
	taskDetail   bool

	td        *ecs.TaskDef
	tdErr     error
	tdLoading bool
	tdRaw     bool
	revisions []string

	diffWith    string
	diff        []string
	diffLoading bool

	cpu, mem       []metrics.Point
	metricsErr     error
	metricsLoading bool
	metricsLoaded  bool
}

func newECSServiceView(e *core.Env, screen, cluster, service string) (*ecsServiceView, tea.Cmd) {
	v := &ecsServiceView{screen: screen, cluster: cluster, service: service, tabHit: map[int][2]int{},
		tasks: components.NewTable(
			components.Column{Title: "Task", Width: 12}, components.Column{Title: "Status", Width: 10},
			components.Column{Title: "Health", Width: 9}, components.Column{Title: "Rev", Width: 14},
			components.Column{Title: "Started", Width: 10}, components.Column{Title: "Stopped reason", Width: 20, Flex: true})}
	return v, v.loadSummary(e, false)
}

func (v *ecsServiceView) loadSummary(e *core.Env, force bool) tea.Cmd {
	v.sumLoading = true
	s, c, sv := e.Session, v.cluster, v.service
	return e.Cached(v.screen, "view:sum", e.CacheKey("ecs", "service", c, sv), force,
		executor.Spec{Name: "Describe ECS service", Quiet: true, Resources: []string{sv}},
		func(ctx context.Context, _ *executor.Job) (any, error) { return ecs.DescribeService(ctx, s, c, sv) })
}

func (v *ecsServiceView) loadTasks(e *core.Env, force bool) tea.Cmd {
	v.tasksLoading = true
	s, c, sv := e.Session, v.cluster, v.service
	return e.Cached(v.screen, "view:tasks", e.CacheKey("ecs", "tasks", c, sv), force,
		executor.Spec{Name: "List ECS tasks", Quiet: true, Resources: []string{sv}},
		func(ctx context.Context, _ *executor.Job) (any, error) { return ecs.ListTasks(ctx, s, c, sv) })
}

func (v *ecsServiceView) loadTaskDef(e *core.Env, force bool) tea.Cmd {
	if v.sum == nil {
		return nil
	}
	v.tdLoading = true
	s, arn := e.Session, v.sum.TaskDefARN
	fam := shortTD(arn)
	fam = fam[:max(0, strings.LastIndex(fam, ":"))]
	return e.Cached(v.screen, "view:td", e.CacheKey("taskdef", arn), force,
		executor.Spec{Name: "Describe task definition", Quiet: true, Resources: []string{shortTD(arn)}},
		func(ctx context.Context, _ *executor.Job) (any, error) {
			td, err := ecs.DescribeTaskDef(ctx, s, arn)
			if err != nil {
				return nil, err
			}
			revs, _ := ecs.Revisions(ctx, s, fam)
			return tdBundle{TD: td, Revisions: revs}, nil
		})
}

type tdBundle struct {
	TD        ecs.TaskDef
	Revisions []string
}

func (v *ecsServiceView) loadMetrics(e *core.Env) tea.Cmd {
	v.metricsLoading, v.metricsErr = true, nil
	s, c, sv := e.Session, v.cluster, v.service
	return e.Run(v.screen, "view:metrics", executor.Spec{Name: "ECS service CPU/memory", Quiet: true, Resources: []string{sv}},
		func(ctx context.Context, _ *executor.Job) (any, error) {
			dims := map[string]string{"ClusterName": c, "ServiceName": sv}
			cpu, err := metrics.Statistics(ctx, s, metrics.StatQuery{Namespace: "AWS/ECS", Metric: "CPUUtilization", Dimensions: dims, Since: 3 * time.Hour, Period: 5 * time.Minute})
			if err != nil {
				return nil, err
			}
			mem, err := metrics.Statistics(ctx, s, metrics.StatQuery{Namespace: "AWS/ECS", Metric: "MemoryUtilization", Dimensions: dims, Since: 3 * time.Hour, Period: 5 * time.Minute})
			return [2][]metrics.Point{cpu, mem}, err
		})
}

func shortTD(arn string) string { return arn[strings.LastIndex(arn, "/")+1:] }

// ensure loads data the current tab needs.
func (v *ecsServiceView) ensure(e *core.Env) tea.Cmd {
	switch v.tab {
	case tabTasks:
		if !v.tasksLoaded && !v.tasksLoading {
			return v.loadTasks(e, false)
		}
	case tabTaskDef:
		if v.td == nil && !v.tdLoading {
			return v.loadTaskDef(e, false)
		}
	case tabMetrics:
		if !v.metricsLoaded && !v.metricsLoading && !e.Deps.Config.Limits.ConfirmBilled {
			return v.loadMetrics(e)
		}
	}
	return nil
}

func (v *ecsServiceView) Capturing() bool { return false }

func (v *ecsServiceView) Back(e *core.Env) bool {
	if v.diff != nil {
		v.diff, v.diffWith = nil, ""
		return true
	}
	if v.taskDetail {
		v.taskDetail = false
		return true
	}
	return false
}

func (v *ecsServiceView) Hints() []core.Hint {
	h := []core.Hint{{Key: "←→", Label: "Tabs"}}
	switch v.tab {
	case tabTasks:
		h = append(h, core.Hint{Key: "enter", Label: "Containers"}, core.Hint{Key: "x", Label: "Exec"},
			core.Hint{Key: "p", Label: "Port-forward"}, core.Hint{Key: "l", Label: "Logs"}, core.Hint{Key: "k", Label: "Stop task"})
	case tabTaskDef:
		h = append(h, core.Hint{Key: "c", Label: "Compare revision"}, core.Hint{Key: "v", Label: "Raw YAML"}, core.Hint{Key: "l", Label: "Logs"})
	case tabMetrics:
		h = append(h, core.Hint{Key: "m", Label: "Load metrics $"})
	}
	return append(h, core.Hint{Key: "s", Label: "Scale"}, core.Hint{Key: "D", Label: "Redeploy"}, core.Hint{Key: "R", Label: "Rollback"},
		core.Hint{Key: "o", Label: "Console"}, core.Hint{Key: "r", Label: "Refresh"}, core.Hint{Key: "esc", Label: "Back"})
}

func (v *ecsServiceView) Update(e *core.Env, msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case core.ResultMsg:
		return v.result(e, m)
	case core.MouseMsg:
		switch {
		case m.Click && m.Y == 1:
			for i, r := range v.tabHit {
				if m.X >= r[0] && m.X < r[1] {
					return v.setTab(e, i)
				}
			}
		case v.tab == tabTasks && m.Y >= 3:
			if tableMouse(v.tasks, core.MouseMsg{X: m.X, Y: m.Y - 3, Click: m.Click, Double: m.Double, WheelUp: m.WheelUp, WheelDown: m.WheelDown}, 0) {
				v.taskDetail = true
			}
		case m.WheelUp:
			v.offset = max(0, v.offset-3)
		case m.WheelDown:
			v.offset += 3
		}
	case tea.KeyMsg:
		return v.key(e, m)
	}
	return nil
}

func (v *ecsServiceView) setTab(e *core.Env, i int) tea.Cmd {
	v.tab = (i + len(ecsTabs)) % len(ecsTabs)
	v.offset = 0
	return v.ensure(e)
}

func (v *ecsServiceView) result(e *core.Env, m core.ResultMsg) tea.Cmd {
	switch m.Key {
	case "view:sum":
		v.sumLoading, v.sumErr = m.Refreshing, m.Err
		if d, ok := m.Value.(ecs.ServiceDetail); ok {
			v.sum, v.sumAt = &d, time.Now()
			if !m.CachedAt.IsZero() {
				v.sumAt = m.CachedAt
			}
		}
		return v.ensure(e)
	case "view:tasks":
		v.tasksLoading, v.tasksErr, v.tasksLoaded = m.Refreshing, m.Err, true
		if list, ok := m.Value.([]ecs.Task); ok {
			rows := make([]components.Row, 0, len(list))
			for _, t := range list {
				tone := theme.StatusTone(t.LastStatus)
				if t.Health == "UNHEALTHY" {
					tone = theme.ToneBad
				}
				reason := t.StoppedReason
				if t.StopCode != "" && reason != "" {
					reason = t.StopCode + ": " + reason
				}
				rows = append(rows, components.Row{Key: t.ID, Data: t, Tone: tone,
					Cells: []string{shortID(t.ID), t.LastStatus, dash(t.Health), dash(t.TaskDef), components.Ago(t.StartedAt), dash(reason)}})
			}
			v.tasks.SetRows(rows)
		}
	case "view:td":
		v.tdLoading, v.tdErr = m.Refreshing, m.Err
		if b, ok := m.Value.(tdBundle); ok {
			v.td, v.revisions = &b.TD, b.Revisions
		}
	case "view:diff":
		v.diffLoading = false
		if m.Err != nil {
			return e.Toast("Compare failed: "+m.Err.Error(), theme.ToneBad)
		}
		v.diff, _ = m.Value.([]string)
		v.offset = 0
	case "view:metrics":
		v.metricsLoading, v.metricsErr, v.metricsLoaded = false, m.Err, true
		if p, ok := m.Value.([2][]metrics.Point); ok {
			v.cpu, v.mem = p[0], p[1]
		}
	}
	return nil
}

func (v *ecsServiceView) selectedTask() (ecs.Task, bool) {
	if r, ok := v.tasks.Selected(); ok {
		return r.Data.(ecs.Task), true
	}
	return ecs.Task{}, false
}

func (v *ecsServiceView) args(extra ...string) map[string]string {
	a := map[string]string{"cluster": v.cluster, "service": v.service}
	for i := 0; i+1 < len(extra); i += 2 {
		a[extra[i]] = extra[i+1]
	}
	return a
}

func (v *ecsServiceView) key(e *core.Env, k tea.KeyMsg) tea.Cmd {
	switch k.String() {
	case "left", "h", "[":
		return v.setTab(e, v.tab-1)
	case "right", "]":
		return v.setTab(e, v.tab+1)
	case "1", "2", "3", "4", "5", "6":
		n, _ := strconv.Atoi(k.String())
		return v.setTab(e, n-1)
	case "r":
		cmds := []tea.Cmd{v.loadSummary(e, true)}
		switch v.tab {
		case tabTasks:
			cmds = append(cmds, v.loadTasks(e, true))
		case tabTaskDef:
			cmds = append(cmds, v.loadTaskDef(e, true))
		case tabMetrics:
			cmds = append(cmds, v.loadMetrics(e))
		}
		return tea.Batch(cmds...)
	case "o":
		region := e.Ctx().Region
		if t, ok := v.selectedTask(); ok && v.tab == tabTasks {
			return e.OpenURL(console.ECSTask(region, v.cluster, t.ID), false)
		}
		if v.td != nil && v.tab == tabTaskDef {
			return e.OpenURL(console.ECSTaskDef(region, v.td.FamilyRev()), false)
		}
		return e.OpenURL(console.ECSService(region, v.cluster, v.service), false)
	case "y":
		if t, ok := v.selectedTask(); ok && v.tab == tabTasks {
			return e.Copy(t.ID, "task ID")
		}
		return e.Copy(v.service, "service name")
	case "s":
		desired := ""
		if v.sum != nil {
			desired = strconv.Itoa(int(v.sum.Desired))
		}
		return e.RunCommandID("ecs.service.scale", v.args("desired_count", desired, "#form", "1"))
	case "D":
		return e.RunCommandID("ecs.service.force-deploy", v.args())
	case "R":
		prev := ""
		if v.td != nil {
			for _, r := range v.revisions {
				if r != v.td.FamilyRev() {
					prev = r
					break
				}
			}
		}
		return e.RunCommandID("ecs.service.rollback", v.args("task_definition", prev, "#form", "1"))
	case "m":
		if v.tab == tabMetrics {
			return tea.Batch(v.loadMetrics(e), e.Toast("$ "+meter.BilledNote("CloudWatch", "GetMetricStatistics"), theme.ToneWarn))
		}
	case "v":
		v.tdRaw = !v.tdRaw
		v.offset = 0
	case "c":
		if v.tab == tabTaskDef && v.td != nil {
			return v.compare(e)
		}
	case "up":
		if v.tab == tabTasks && !v.taskDetail {
			v.tasks.Update(k)
		} else {
			v.offset = max(0, v.offset-1)
		}
	case "down":
		if v.tab == tabTasks && !v.taskDetail {
			v.tasks.Update(k)
		} else {
			v.offset++
		}
	case "pgup":
		v.offset = max(0, v.offset-10)
	case "pgdown":
		v.offset += 10
	case "enter":
		if v.tab == tabTasks {
			v.taskDetail = !v.taskDetail
		}
	case "l":
		return v.openLogs(e)
	}
	if v.tab == tabTasks {
		return v.taskKey(e, k)
	}
	return nil
}

func (v *ecsServiceView) taskKey(e *core.Env, k tea.KeyMsg) tea.Cmd {
	t, ok := v.selectedTask()
	if !ok {
		return nil
	}
	switch k.String() {
	case "x":
		if t.LastStatus != "RUNNING" {
			return e.Toast("ECS Exec needs a RUNNING task", theme.ToneWarn)
		}
		if !t.ExecEnabled {
			return e.Toast("ECS Exec is not enabled for this task (enableExecuteCommand=false)", theme.ToneWarn)
		}
		a := map[string]string{"cluster": v.cluster, "task": t.ID}
		if len(t.Containers) > 0 {
			a["container"] = t.Containers[0].Name
		}
		return e.RunCommandID("ecs.exec.shell", a)
	case "p":
		if len(t.Containers) == 0 || t.Containers[0].RuntimeID == "" {
			return e.Toast("No container runtime ID yet — the task must be RUNNING", theme.ToneWarn)
		}
		port := ""
		if v.td != nil && len(v.td.Containers) > 0 && len(v.td.Containers[0].Ports) > 0 {
			port, _, _ = strings.Cut(v.td.Containers[0].Ports[0], "/")
		}
		return e.RunCommandID("ssm.tunnel.ecs", map[string]string{"cluster": v.cluster, "task": t.ID,
			"runtime_id": t.Containers[0].RuntimeID, "port": port, "#form": "1"})
	case "k":
		return e.RunCommandID("ecs.task.stop", map[string]string{"cluster": v.cluster, "task": t.ID})
	}
	return nil
}

func (v *ecsServiceView) openLogs(e *core.Env) tea.Cmd {
	if v.td == nil {
		return e.Toast("Open the Task definition tab first (5) to find the log group", theme.ToneNormal)
	}
	for _, c := range v.td.Containers {
		if c.LogGroup != "" {
			return e.Navigate("logs", "open:"+c.LogGroup)
		}
	}
	return e.Toast("No awslogs log group in this task definition", theme.ToneWarn)
}

// compare picks another revision and diffs the two definitions.
func (v *ecsServiceView) compare(e *core.Env) tea.Cmd {
	var items []core.PickItem
	for _, r := range v.revisions {
		if r != v.td.FamilyRev() {
			items = append(items, core.PickItem{Label: r, Value: r})
		}
	}
	if len(items) == 0 {
		return e.Toast("No other revisions to compare", theme.ToneDim)
	}
	cur := *v.td
	return e.Open(core.NewPicker("Compare "+cur.FamilyRev()+" with", items, "", func(it core.PickItem) tea.Cmd {
		v.diffLoading, v.diffWith = true, it.Value
		s := e.Session
		return e.Run(v.screen, "view:diff", executor.Spec{Name: "Compare task definitions", Quiet: true},
			func(ctx context.Context, _ *executor.Job) (any, error) {
				other, err := ecs.DescribeTaskDef(ctx, s, it.Value)
				if err != nil {
					return nil, err
				}
				a := components.RenderResult(other.Raw, "yaml", 200)
				b := components.RenderResult(cur.Raw, "yaml", 200)
				return components.Diff(a, b), nil
			})
	}))
}

func (v *ecsServiceView) View(e *core.Env, w, h int) string {
	right := ""
	switch {
	case v.sumLoading:
		right = e.Spinner() + " refreshing"
	case !v.sumAt.IsZero():
		right = "updated " + components.Ago(v.sumAt)
	}
	head := headingRaw(theme.Title.Render("ECS › ")+v.cluster+" › "+v.service, right, w) + "\n"
	// Tab bar.
	v.tabHit = map[int][2]int{}
	var tabs []string
	x := 0
	for i, t := range ecsTabs {
		label := fmt.Sprintf(" %d %s ", i+1, t)
		if i == v.tab {
			tabs = append(tabs, theme.NavActive.Render(label))
		} else {
			tabs = append(tabs, theme.InfoBadge.Render(label))
		}
		width := lipgloss.Width(tabs[len(tabs)-1])
		v.tabHit[i] = [2]int{x, x + width}
		x += width + 1
	}
	head += components.Truncate(strings.Join(tabs, " "), w) + "\n\n"
	bodyH := h - 3
	if v.sumErr != nil && v.sum == nil {
		return head + errorView(v.sumErr, w)
	}
	if v.sum == nil {
		return head + loadingView(e, "service")
	}
	var lines []string
	switch v.tab {
	case tabOverview:
		lines = v.overview(e, w)
	case tabDeployments:
		lines = v.deployments(e, w)
	case tabTasks:
		return head + v.tasksView(e, w, bodyH)
	case tabEvents:
		lines = v.events(w)
	case tabTaskDef:
		lines = v.taskDefView(e, w)
	case tabMetrics:
		lines = v.metricsView(e, w)
	}
	v.offset = max(0, min(v.offset, len(lines)-bodyH))
	return head + components.Fit(strings.Join(lines[v.offset:], "\n"), w, bodyH)
}

func (v *ecsServiceView) overview(e *core.Env, w int) []string {
	s := v.sum
	card := func(label string, n int32, tone theme.Tone) string {
		st := theme.Strong
		if tone != theme.ToneNormal {
			st = tone.Style().Bold(true)
		}
		return theme.Box(tone.Border()).Width(14).Render(theme.Dim.Render(label) + "\n" + st.Render(strconv.Itoa(int(n))))
	}
	runTone := theme.ToneOK
	if s.Running < s.Desired {
		runTone = theme.ToneBad
	}
	pendTone := theme.ToneNormal
	if s.Pending > 0 {
		pendTone = theme.ToneWarn
	}
	failTone := theme.ToneNormal
	if s.FailedTasks > 0 {
		failTone = theme.ToneBad
	}
	cards := lipgloss.JoinHorizontal(lipgloss.Top, card("Desired", s.Desired, theme.ToneNormal), " ",
		card("Running", s.Running, runTone), " ", card("Pending", s.Pending, pendTone), " ", card("Failed", s.FailedTasks, failTone))
	lines := strings.Split(cards, "\n")
	tick := -1
	if s.Rollout == "IN_PROGRESS" || s.Pending > 0 {
		tick = e.Tick
	}
	bw := min(40, w-20)
	lines = append(lines, "",
		theme.Dim.Render(components.Pad("Capacity", 14))+components.Progress(s.Running, s.Pending, s.Desired, bw, tick)+
			theme.Dim.Render(fmt.Sprintf("  %d/%d running", s.Running, s.Desired)))
	rollout := s.Rollout
	if rollout == "" {
		rollout = "-"
	}
	lines = append(lines, theme.Dim.Render(components.Pad("Rollout", 14))+theme.StatusTone(rollout).Style().Render(rollout))
	cb := "off"
	if s.CircuitBreaker {
		cb = "on"
		if s.AutoRollback {
			cb += " (auto rollback)"
		}
	}
	kvs := [][2]string{
		{"Status", s.Status + " · " + dash(s.LaunchType)},
		{"Task def", shortTD(s.TaskDefARN)},
		{"ECS Exec", yesNo(s.ExecEnabled)},
		{"Circuit breaker", cb},
		{"Deploy limits", fmt.Sprintf("min %d%% · max %d%% healthy", s.MinHealthy, s.MaxPercent)},
		{"Network", fmt.Sprintf("%d subnets · %d security groups · public IP %s", len(s.Subnets), len(s.SecurityGroups), yesNo(s.PublicIP))},
		{"Load balancing", fmt.Sprintf("%d target groups", len(s.TargetGroups))},
		{"Capacity provider", dash(strings.Join(s.CapacityProviders, ", "))},
		{"Created", components.Ago(s.Created)},
	}
	for _, p := range kvs {
		lines = append(lines, theme.Dim.Render(components.Pad(p[0], 18))+theme.Body.Render(p[1]))
	}
	lines = append(lines, "", theme.Title.Render("RECENT EVENTS"))
	for i, ev := range s.Events {
		if i >= 5 {
			break
		}
		lines = append(lines, eventLine(ev, w)...)
	}
	return lines
}

func (v *ecsServiceView) deployments(e *core.Env, w int) []string {
	var lines []string
	for i, d := range v.sum.Deployments {
		if i > 0 {
			lines = append(lines, "")
		}
		dot := theme.Dim.Render("○")
		if d.Status == "PRIMARY" {
			dot = theme.Title.Render("●")
		}
		head := dot + " " + theme.Strong.Render(d.Status) + "  " + theme.StatusTone(d.Rollout).Style().Render(dash(d.Rollout)) +
			theme.Dim.Render("  "+d.TaskDef+"  ·  started "+components.Ago(d.Created)+"  ·  updated "+components.Ago(d.Updated))
		lines = append(lines, components.Truncate(head, w))
		tick := -1
		if d.Rollout == "IN_PROGRESS" {
			tick = e.Tick
		}
		lines = append(lines, "  "+components.Progress(d.Running, d.Pending, d.Desired, min(50, w-30), tick)+
			theme.Dim.Render(fmt.Sprintf("  %d/%d running · %d pending · %d failed", d.Running, d.Desired, d.Pending, d.Failed)))
		if d.Reason != "" {
			for _, l := range components.WrapLines(d.Reason, w-4) {
				lines = append(lines, "  "+theme.Dim.Render("↳ "+l))
			}
		}
	}
	if len(lines) == 0 {
		lines = []string{theme.Dim.Render("No deployments.")}
	}
	return lines
}

func eventLine(ev ecs.Event, w int) []string {
	msg := ev.Message
	low := strings.ToLower(msg)
	st := theme.Body
	switch {
	case strings.Contains(low, "unhealthy") || strings.Contains(low, "failed") || strings.Contains(low, "unable") || strings.Contains(low, "error"):
		st = theme.Bad
	case strings.Contains(low, "steady state"):
		st = theme.OK
	case strings.Contains(low, "started") || strings.Contains(low, "registered") || strings.Contains(low, "deployment"):
		st = theme.Title
	}
	prefix := theme.Dim.Render(components.Pad(ev.At.Local().Format("01-02 15:04"), 12))
	var out []string
	for i, l := range components.WrapLines(msg, max(20, w-13)) {
		if i == 0 {
			out = append(out, prefix+" "+st.Render(l))
		} else {
			out = append(out, strings.Repeat(" ", 13)+st.Render(l))
		}
	}
	return out
}

func (v *ecsServiceView) events(w int) []string {
	var lines []string
	for _, ev := range v.sum.Events {
		lines = append(lines, eventLine(ev, w)...)
	}
	if len(lines) == 0 {
		return []string{theme.Dim.Render("No events.")}
	}
	return lines
}

func (v *ecsServiceView) tasksView(e *core.Env, w, h int) string {
	switch {
	case v.tasksErr != nil && v.tasks.Total() == 0:
		return errorView(v.tasksErr, w)
	case v.tasksLoading && v.tasks.Total() == 0:
		return loadingView(e, "tasks")
	case v.tasks.Total() == 0:
		return theme.Dim.Render("No running or recently stopped tasks.")
	}
	if !v.taskDetail {
		return v.tasks.View(w, h)
	}
	th := max(4, h/2)
	t, _ := v.selectedTask()
	var lines []string
	lines = append(lines, theme.Title.Render("TASK "+t.ID)+theme.Dim.Render("  "+t.TaskDef+"  · started "+components.Ago(t.StartedAt)))
	if t.StoppedReason != "" {
		lines = append(lines, theme.Bad.Render(components.Truncate("stopped: "+t.StopCode+" "+t.StoppedReason, w)))
	}
	for _, c := range t.Containers {
		exit := "-"
		if c.ExitCode != nil {
			exit = strconv.Itoa(int(*c.ExitCode))
		}
		tone := theme.StatusTone(c.Status)
		if c.ExitCode != nil && *c.ExitCode != 0 {
			tone = theme.ToneBad
		}
		lines = append(lines, tone.Style().Render(fmt.Sprintf("◆ %s  %s  health=%s  exit=%s", c.Name, c.Status, dash(c.Health), exit)))
		lines = append(lines, theme.Dim.Render(components.Truncate("   image "+c.Image+"   runtime "+dash(c.RuntimeID), w)))
		if c.Reason != "" {
			lines = append(lines, theme.Warn.Render(components.Truncate("   "+c.Reason, w)))
		}
	}
	sep := theme.Dim.Render(strings.Repeat("─", w))
	return v.tasks.View(w, th) + "\n" + sep + "\n" + components.Fit(strings.Join(lines, "\n"), w, h-th-1)
}

func (v *ecsServiceView) taskDefView(e *core.Env, w int) []string {
	switch {
	case v.tdErr != nil && v.td == nil:
		return strings.Split(errorView(v.tdErr, w), "\n")
	case v.td == nil:
		return []string{loadingView(e, "task definition")}
	}
	if v.diffLoading {
		return []string{theme.Dim.Render(e.Spinner() + " comparing with " + v.diffWith + "…")}
	}
	if v.diff != nil {
		head := []string{theme.Title.Render("DIFF  "+v.diffWith+"  →  "+v.td.FamilyRev()) + theme.Dim.Render("   esc to close"), ""}
		return append(head, v.diff...)
	}
	td := v.td
	if v.tdRaw {
		return components.RenderResult(td.Raw, "yaml", w)
	}
	lines := []string{
		theme.Strong.Render(td.FamilyRev()) + theme.Dim.Render("  registered "+components.Ago(td.Registered)),
		theme.Dim.Render(fmt.Sprintf("cpu %s · memory %s · %s · %s", dash(td.CPU), dash(td.Memory), dash(td.NetworkMode), strings.Join(td.Compat, ","))),
		theme.Dim.Render("task role ") + theme.Body.Render(dash(td.TaskRole)) + theme.Dim.Render("   execution role ") + theme.Body.Render(dash(td.ExecRole)),
	}
	var revs []string
	for i, r := range v.revisions {
		if i >= 6 {
			revs = append(revs, "…")
			break
		}
		if r == td.FamilyRev() {
			r = theme.OK.Render(r + " (current)")
		}
		revs = append(revs, r)
	}
	lines = append(lines, theme.Dim.Render("revisions ")+strings.Join(revs, theme.Dim.Render(" · ")), "")
	for _, c := range td.Containers {
		mem := fmt.Sprintf("%d MiB", c.Memory)
		if c.Memory == 0 {
			mem = fmt.Sprintf("%d MiB reserved", c.MemoryReserved)
		}
		ess := ""
		if c.Essential {
			ess = theme.Dim.Render(" · essential")
		}
		lines = append(lines, theme.Title.Render("◆ "+c.Name)+ess)
		kv := [][2]string{
			{"image", c.Image}, {"cpu / memory", fmt.Sprintf("%d units · %s", c.CPU, mem)},
			{"ports", strings.Join(c.Ports, ", ")}, {"command", c.Command}, {"health check", c.HealthCheck},
			{"logs", strings.TrimSpace(c.LogGroup + " " + c.LogPrefix)},
			{"env (names)", strings.Join(c.EnvNames, ", ")}, {"secrets", strings.Join(c.Secrets, ", ")},
		}
		for _, p := range kv {
			if p[1] == "" {
				continue
			}
			for i, l := range components.WrapLines(p[1], max(20, w-18)) {
				key := ""
				if i == 0 {
					key = p[0]
				}
				lines = append(lines, "  "+theme.Dim.Render(components.Pad(key, 15))+" "+theme.Body.Render(l))
			}
		}
		lines = append(lines, "")
	}
	return lines
}

func (v *ecsServiceView) metricsView(e *core.Env, w int) []string {
	switch {
	case v.metricsLoading:
		return []string{loadingView(e, "metrics")}
	case v.metricsErr != nil:
		return strings.Split(errorView(v.metricsErr, w), "\n")
	case !v.metricsLoaded:
		return []string{theme.Dim.Render("CPU and memory for the last 3 hours (5-minute averages)."), "",
			theme.Warn.Render("$ " + meter.BilledNote("CloudWatch", "GetMetricStatistics")), "",
			theme.Key.Render("m") + theme.Dim.Render(" load metrics")}
	}
	var lines []string
	if len(v.cpu) == 0 && len(v.mem) == 0 {
		return []string{theme.Dim.Render("No datapoints (the service may be new, or metrics are disabled).")}
	}
	vals := func(pts []metrics.Point) (out []float64, last, avg, peak float64) {
		for _, p := range pts {
			out = append(out, p.Average)
			avg += p.Average
			peak = max(peak, p.Maximum)
		}
		if len(out) > 0 {
			last, avg = out[len(out)-1], avg/float64(len(out))
		}
		return
	}
	cpu, cl, ca, cp := vals(v.cpu)
	mem, ml, ma, mp := vals(v.mem)
	purple := lipgloss.NewStyle().Foreground(lipgloss.Color("#bc8cff"))
	lines = append(lines, theme.Title.Render("CPU & MEMORY · last 3h"),
		theme.Title.Render("── CPU")+theme.Dim.Render(fmt.Sprintf(" now %.1f%% avg %.1f%% peak %.1f%%   ", cl, ca, cp))+
			purple.Render("── Memory")+theme.Dim.Render(fmt.Sprintf(" now %.1f%% avg %.1f%% peak %.1f%%", ml, ma, mp)), "")
	lines = append(lines, components.LineChart([]components.Series{{Values: cpu, Style: theme.Title}, {Values: mem, Style: purple}}, w, 10, "%", nil)...)
	first := v.cpu
	if len(first) == 0 {
		first = v.mem
	}
	lines = append(lines, theme.Dim.Render(components.Pad(first[0].Time.Local().Format("15:04"), w-5)+first[len(first)-1].Time.Local().Format("15:04")), "",
		theme.Dim.Render("CPU  ")+components.Gauge(cl, 30)+theme.Dim.Render("    Memory  ")+components.Gauge(ml, 30))
	return lines
}
