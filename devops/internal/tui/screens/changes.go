package screens

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/aws/console"
	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/aws/trail"
	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/executor"
	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/tui/components"
	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/tui/core"
	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/tui/theme"
)

var changeRanges = []time.Duration{time.Hour, 6 * time.Hour, 24 * time.Hour, 7 * 24 * time.Hour}

// Changes answers "what changed?": CloudTrail write events plus alarm state
// changes, newest first, with a timeline of change bursts and alarms.
type Changes struct {
	table      *components.Table
	filter     filterBar
	rng        time.Duration
	start, end time.Time
	all        []trail.Change
	next       string
	loading    bool
	more       bool
	err        error
	alarmErr   error
	showNoise  bool
	errorsOnly bool
	seq        int
}

func NewChanges() *Changes {
	return &Changes{rng: time.Hour, filter: newFilter(), table: components.NewTable(
		components.Column{Title: "When", Width: 9},
		components.Column{Title: "Who", Width: 22},
		components.Column{Title: "Action", Width: 26},
		components.Column{Title: "Service", Width: 14},
		components.Column{Title: "Resource", Width: 20, Flex: true},
		components.Column{Title: "Result", Width: 14},
	)}
}

func (c *Changes) ID() string      { return "changes" }
func (c *Changes) Capturing() bool { return c.filter.active }

func (c *Changes) Hints() []core.Hint {
	noise := "Show noise"
	if c.showNoise {
		noise = "Hide noise"
	}
	return []core.Hint{{Key: "t", Label: "Range " + components.Dur(c.rng)}, {Key: "e", Label: "Failed only"}, {Key: "n", Label: noise},
		{Key: "f", Label: "Filter"}, {Key: "enter", Label: "Event"}, {Key: "g", Label: "Go to resource"}, {Key: "o", Label: "Console"}, {Key: "r", Label: "Refresh"}}
}

func (c *Changes) Init(e *core.Env) tea.Cmd { return c.load(e) }

func (c *Changes) load(e *core.Env) tea.Cmd {
	if e.Session.Ready() != nil {
		return nil
	}
	c.seq++
	c.all, c.next, c.err, c.alarmErr = nil, "", nil, nil
	c.end = time.Now()
	c.start = c.end.Add(-c.rng)
	c.loading = true
	c.table.SetRows(nil)
	s, start, end, key := e.Session, c.start, c.end, fmt.Sprint(c.seq)
	return tea.Batch(
		e.Run(c.ID(), "page:"+key, executor.Spec{Name: "CloudTrail changes", Quiet: true},
			func(ctx context.Context, _ *executor.Job) (any, error) {
				cs, next, err := trail.Page(ctx, s, start, end, "")
				return changePage{cs, next}, err
			}),
		e.Run(c.ID(), "alarms:"+key, executor.Spec{Name: "Alarm state changes", Quiet: true},
			func(ctx context.Context, _ *executor.Job) (any, error) { return trail.AlarmChanges(ctx, s, start, end) }),
	)
}

type changePage struct {
	Changes []trail.Change
	Next    string
}

func (c *Changes) loadMore(e *core.Env) tea.Cmd {
	if c.next == "" || c.more {
		return nil
	}
	c.more = true
	s, start, end, token, key := e.Session, c.start, c.end, c.next, fmt.Sprint(c.seq)
	return e.Run(c.ID(), "page:"+key, executor.Spec{Name: "More CloudTrail changes", Quiet: true},
		func(ctx context.Context, _ *executor.Job) (any, error) {
			cs, next, err := trail.Page(ctx, s, start, end, token)
			return changePage{cs, next}, err
		})
}

func (c *Changes) Focus(e *core.Env, q string) tea.Cmd {
	c.filter.set(q)
	c.table.SetFilter(q)
	return nil
}

func (c *Changes) Back(e *core.Env) (bool, tea.Cmd) {
	if c.filter.value() != "" {
		c.filter.set("")
		c.table.SetFilter("")
		return true, nil
	}
	return false, nil
}

func (c *Changes) rebuild() {
	trail.SortNewest(c.all)
	rows := make([]components.Row, 0, len(c.all))
	for _, ch := range c.all {
		if !c.showNoise && ch.Noisy() {
			continue
		}
		if c.errorsOnly && ch.Error == "" && !(ch.Kind == "alarm" && ch.AlarmState == "ALARM") {
			continue
		}
		result := theme.OK.Render("ok")
		tone := theme.ToneNormal
		switch {
		case ch.Kind == "alarm":
			result = theme.StatusTone(ch.AlarmState).Style().Render(ch.AlarmState)
			tone = theme.StatusTone(ch.AlarmState)
		case ch.Error != "":
			result = theme.Bad.Render(ch.Error)
			tone = theme.ToneBad
		}
		action := ch.Action
		if ch.Kind == "alarm" {
			action = "alarm " + ch.Action
		}
		rows = append(rows, components.Row{Key: ch.ID + ch.Time.String() + ch.ResourceName, Data: ch, Tone: tone,
			Search: ch.SourceIP + " " + ch.ErrorMessage + " " + ch.ResourceType,
			Cells:  []string{ch.Time.Local().Format("15:04:05"), dash(ch.Who), action, ch.Service, dash(ch.Resources), result}})
	}
	c.table.SetRows(rows)
}

func (c *Changes) Update(e *core.Env, msg tea.Msg) tea.Cmd {
	key := fmt.Sprint(c.seq)
	switch m := msg.(type) {
	case core.ResultMsg:
		switch m.Key {
		case "page:" + key:
			c.loading, c.more = false, false
			if m.Err != nil {
				c.err = m.Err
				return nil
			}
			if p, ok := m.Value.(changePage); ok {
				c.all = append(c.all, p.Changes...)
				c.next = p.Next
			}
			c.rebuild()
		case "alarms:" + key:
			c.alarmErr = m.Err
			if cs, ok := m.Value.([]trail.Change); ok {
				c.all = append(c.all, cs...)
				c.rebuild()
			}
		}
		return nil
	case core.MouseMsg:
		top := 3 // heading, timeline, note
		if c.filter.active || c.filter.value() != "" {
			top++
		}
		if tableMouse(c.table, m, top) {
			return c.openEvent(e)
		}
		if c.table.WantsMore() {
			return c.loadMore(e)
		}
	case tea.KeyMsg:
		if c.filter.active {
			changed, cmd := c.filter.update(m)
			if changed {
				c.table.SetFilter(c.filter.value())
			}
			return cmd
		}
		row, ok := c.table.Selected()
		switch m.String() {
		case "f":
			return c.filter.start()
		case "r":
			return c.load(e)
		case "t":
			for i, r := range changeRanges {
				if r == c.rng {
					c.rng = changeRanges[(i+1)%len(changeRanges)]
					break
				}
			}
			return c.load(e)
		case "e":
			c.errorsOnly = !c.errorsOnly
			c.rebuild()
			return nil
		case "n":
			c.showNoise = !c.showNoise
			c.rebuild()
			return nil
		case "enter":
			return c.openEvent(e)
		case "o":
			if ok {
				if ch := row.Data.(trail.Change); ch.Kind == "api" {
					return e.OpenURL(console.CloudTrailEvent(e.Ctx().Region, ch.ID), false)
				}
				return e.OpenURL(console.Alarm(e.Ctx().Region, row.Data.(trail.Change).ResourceName), false)
			}
		case "g":
			if ok {
				return goToResource(e, row.Data.(trail.Change))
			}
		default:
			c.table.Update(m)
		}
		if c.table.WantsMore() {
			return c.loadMore(e)
		}
	}
	return nil
}

func (c *Changes) openEvent(e *core.Env) tea.Cmd {
	row, ok := c.table.Selected()
	if !ok {
		return nil
	}
	ch := row.Data.(trail.Change)
	body := ch.Raw
	var x any
	if json.Unmarshal([]byte(ch.Raw), &x) == nil {
		body = strings.Join(components.RenderResult(x, "yaml", 120), "\n")
	}
	return e.Open(core.NewText(ch.Action+" · "+ch.Time.Local().Format("2006-01-02 15:04:05"), components.Strip(body)))
}

// goToResource jumps to the screen that shows the changed resource.
func goToResource(e *core.Env, ch trail.Change) tea.Cmd {
	name := ch.ResourceName
	switch {
	case ch.Kind == "alarm":
		return e.Navigate("alarms", name)
	case ch.ResourceType == "AWS::EC2::Instance":
		return e.Navigate("ec2", name)
	case strings.HasPrefix(ch.ResourceType, "AWS::ECS::"), ch.Service == "ecs":
		return e.Navigate("ecs", name[strings.LastIndex(name, "/")+1:])
	case strings.HasPrefix(ch.ResourceType, "AWS::Lambda::"), strings.HasPrefix(ch.Service, "lambda"):
		return e.Navigate("lambda", name[strings.LastIndex(name, ":")+1:])
	case strings.HasPrefix(ch.ResourceType, "AWS::RDS::"):
		return e.Navigate("rds", name)
	case strings.HasPrefix(ch.ResourceType, "AWS::S3::"):
		return e.Navigate("s3", name)
	case strings.HasPrefix(ch.ResourceType, "AWS::CodePipeline::"):
		return e.Navigate("pipelines", name)
	case strings.HasPrefix(ch.ResourceType, "AWS::ElasticLoadBalancingV2::"):
		return e.Navigate("elb", name)
	}
	if name == "" {
		return e.Toast("This event has no resource to open", theme.ToneDim)
	}
	return e.Toast("No screen for "+dash(ch.ResourceType)+" yet — o opens the event in the console", theme.ToneDim)
}

func (c *Changes) View(e *core.Env, w, h int) string {
	if err := e.Session.Ready(); err != nil {
		return heading("What changed?", "", w) + "\n\n" + errorView(err, w)
	}
	shown := c.table.Total()
	right := fmt.Sprintf("last %s · %d changes", components.Dur(c.rng), shown)
	if c.loading {
		right = e.Spinner() + " loading · last " + components.Dur(c.rng)
	}
	if c.errorsOnly {
		right = theme.Bad.Render("failed only") + theme.Dim.Render(" · ") + right
	}
	out := heading("What changed?", right, w) + "\n"
	out += c.timeline(w) + "\n"
	note := theme.Dim.Render("CloudTrail write events (read-only calls excluded)")
	if !c.showNoise {
		note += theme.Dim.Render(" · routine agent calls hidden (n)")
	}
	if c.alarmErr != nil {
		note += theme.Warn.Render(" · alarm history unavailable")
	}
	out += components.Truncate(note, w) + "\n"
	if fv := c.filter.view(w); fv != "" {
		out += fv + "\n"
	}
	switch {
	case c.err != nil && len(c.all) == 0:
		return out + "\n" + errorView(c.err, w)
	case c.loading && len(c.all) == 0:
		return out + "\n" + loadingView(e, "changes")
	case shown == 0:
		return out + "\n" + theme.Dim.Render("No changes in this window. t widens the range, n shows routine agent calls.")
	}
	c.table.SetMore(c.next != "", c.more, e.Spinner())
	used := strings.Count(out, "\n")
	avail := h - used
	detail := ""
	if row, ok := c.table.Selected(); ok {
		detail = changeDetail(row.Data.(trail.Change), w)
	}
	dh := min(strings.Count(detail, "\n")+2, avail/3)
	sep := theme.Dim.Render(strings.Repeat("─", w))
	return out + c.table.View(w, avail-dh) + "\n" + sep + "\n" + components.Fit(detail, w, dh-1)
}

func changeDetail(ch trail.Change, w int) string {
	var lines []string
	add := func(k, v string, st func(...string) string) {
		if v != "" {
			lines = append(lines, theme.Dim.Render(components.Pad(k, 10))+st(components.Truncate(v, w-10)))
		}
	}
	add("who", ch.Who, theme.Body.Render)
	add("from", strings.TrimSpace(ch.SourceIP+" "+ch.Region), theme.Body.Render)
	add("resource", strings.TrimSpace(ch.ResourceType+" "+ch.Resources), theme.Body.Render)
	if ch.Error != "" {
		add("error", ch.Error+": "+ch.ErrorMessage, theme.Bad.Render)
	}
	if ch.Kind == "alarm" {
		add("summary", ch.Raw, theme.Body.Render)
	}
	return strings.Join(lines, "\n")
}

// timeline draws change counts per time bucket across the window, with a red
// ▲ where an alarm went into ALARM.
func (c *Changes) timeline(w int) string {
	if c.start.IsZero() {
		return ""
	}
	label := " " + c.start.Local().Format("15:04") + " "
	end := " now"
	n := w - len(label) - len(end)
	if n < 10 {
		return ""
	}
	counts := make([]float64, n)
	alarm := make([]bool, n)
	span := c.end.Sub(c.start)
	for _, ch := range c.all {
		i := int(float64(ch.Time.Sub(c.start)) / float64(span) * float64(n-1))
		if i < 0 || i >= n {
			continue
		}
		switch {
		case ch.Kind == "alarm" && ch.AlarmState == "ALARM":
			alarm[i] = true
		case ch.Kind == "api" && (c.showNoise || !ch.Noisy()):
			counts[i]++
		}
	}
	spark := []rune(components.Strip(components.Sparkline(counts, n, theme.Body)))
	var b strings.Builder
	for i, r := range spark {
		switch {
		case alarm[i]:
			b.WriteString(theme.Bad.Render("▲"))
		case counts[i] == 0:
			b.WriteString(theme.Dim.Render("·"))
		default:
			b.WriteString(theme.Title.Render(string(r)))
		}
	}
	return theme.Dim.Render(label) + b.String() + theme.Dim.Render(end)
}
