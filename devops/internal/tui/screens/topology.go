package screens

import (
	"context"
	"fmt"
	"strings"
	"sync"

	tea "github.com/charmbracelet/bubbletea"

	"devops/internal/aws/console"
	"devops/internal/aws/networking"
	"devops/internal/executor"
	"devops/internal/tui/components"
	"devops/internal/tui/core"
	"devops/internal/tui/theme"
)

type tgHealth struct {
	TG      networking.TargetGroup
	Targets []networking.Target
	Err     error
}

type topology struct {
	Listeners []networking.Listener
	Groups    []tgHealth
}

// topologyView draws a load balancer as a tree: listeners → target groups →
// targets, coloured by health.
type topologyView struct {
	lb      networking.LoadBalancer
	data    *topology
	err     error
	loading bool
	offset  int
}

func newTopologyView(e *core.Env, lb networking.LoadBalancer) (*topologyView, tea.Cmd) {
	v := &topologyView{lb: lb}
	return v, v.load(e)
}

func (v *topologyView) load(e *core.Env) tea.Cmd {
	v.loading = true
	s, arn := e.Session, v.lb.ARN
	return e.Run("elb", "view:topology", executor.Spec{Name: "Map load balancer", Quiet: true, Resources: []string{v.lb.Name}},
		func(ctx context.Context, _ *executor.Job) (any, error) {
			ls, err := networking.Listeners(ctx, s, arn)
			if err != nil {
				return nil, err
			}
			tgs, err := networking.TargetGroups(ctx, s, arn)
			if err != nil {
				return nil, err
			}
			out := topology{Listeners: ls, Groups: make([]tgHealth, len(tgs))}
			var wg sync.WaitGroup
			sem := make(chan struct{}, 5)
			for i, tg := range tgs {
				wg.Add(1)
				go func() {
					defer wg.Done()
					sem <- struct{}{}
					defer func() { <-sem }()
					ts, err := networking.TargetHealth(ctx, s, tg.ARN)
					out.Groups[i] = tgHealth{TG: tg, Targets: ts, Err: err}
				}()
			}
			wg.Wait()
			return out, nil
		})
}

func (v *topologyView) Capturing() bool       { return false }
func (v *topologyView) Back(e *core.Env) bool { return false }
func (v *topologyView) Hints() []core.Hint {
	return []core.Hint{{Key: "r", Label: "Refresh"}, {Key: "o", Label: "Console"}, {Key: "esc", Label: "Back"}}
}

func (v *topologyView) Update(e *core.Env, msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case core.ResultMsg:
		if m.Key == "view:topology" {
			v.loading, v.err = false, m.Err
			if t, ok := m.Value.(topology); ok {
				v.data = &t
			}
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
		case "r":
			return v.load(e)
		case "o":
			return e.OpenURL(console.LoadBalancer(e.Ctx().Region, v.lb.ARN), false)
		case "up", "k":
			v.offset = max(0, v.offset-1)
		case "down", "j":
			v.offset++
		}
	}
	return nil
}

func healthDots(ts []networking.Target) (string, int, int) {
	var b strings.Builder
	healthy := 0
	for i, t := range ts {
		if i >= 24 {
			b.WriteString(theme.Dim.Render("…"))
			break
		}
		switch t.State {
		case "healthy":
			healthy++
			b.WriteString(theme.OK.Render("●"))
		case "unhealthy", "unavailable":
			b.WriteString(theme.Bad.Render("●"))
		case "initial", "draining":
			b.WriteString(theme.Warn.Render("●"))
		default:
			b.WriteString(theme.Dim.Render("○"))
		}
	}
	return b.String(), healthy, len(ts)
}

// instanceNames labels EC2 targets from the cached instance list (no API call).
func instanceNames(e *core.Env) map[string]string {
	names := map[string]string{}
	if entry, ok, _ := e.Deps.Cache.Get(e.CacheKey("ec2", "instances")); ok {
		if p, ok := entry.Value.(ec2Page); ok {
			for _, in := range p.Instances {
				names[in.ID] = in.Name
			}
		}
	}
	return names
}

func (v *topologyView) View(e *core.Env, w, h int) string {
	right := ""
	if v.loading {
		right = e.Spinner() + " mapping"
	}
	head := headingRaw(theme.Title.Render("TOPOLOGY › ")+v.lb.Name, right, w) + "\n\n"
	if v.err != nil {
		return head + errorView(v.err, w)
	}
	if v.data == nil {
		return head + loadingView(e, "topology")
	}
	names := instanceNames(e)
	byARN := map[string]tgHealth{}
	for _, g := range v.data.Groups {
		byARN[g.TG.ARN] = g
	}
	var lines []string
	lines = append(lines, theme.Strong.Render("⇆ "+v.lb.Name)+theme.Dim.Render(fmt.Sprintf("  %s · %s · ", v.lb.Type, v.lb.Scheme))+
		theme.StatusTone(v.lb.State).Style().Render(v.lb.State))
	lines = append(lines, theme.Dim.Render("  "+v.lb.DNS))
	seen := map[string]bool{}
	group := func(prefix string, g tgHealth, last bool) {
		branch, cont := "├─▶ ", "│   "
		if last {
			branch, cont = "└─▶ ", "    "
		}
		dots, ok, total := healthDots(g.Targets)
		tone := theme.ToneOK
		switch {
		case g.Err != nil:
			tone = theme.ToneWarn
		case total == 0:
			tone = theme.ToneDim
		case ok < total:
			tone = theme.ToneBad
		}
		lines = append(lines, prefix+theme.Dim.Render(branch)+tone.Style().Render(g.TG.Name)+
			theme.Dim.Render(fmt.Sprintf(" %s:%d ", g.TG.Protocol, g.TG.Port))+dots+theme.Dim.Render(fmt.Sprintf("  %d/%d healthy", ok, total)))
		for i, t := range g.Targets {
			tb := "├─ "
			if i == len(g.Targets)-1 {
				tb = "└─ "
			}
			label := t.ID
			if n := names[t.ID]; n != "" {
				label = n + " (" + t.ID + ")"
			}
			state := theme.StatusTone(t.State).Style().Render(t.State)
			extra := ""
			if t.Reason != "" {
				extra = theme.Dim.Render("  " + t.Reason)
			}
			lines = append(lines, components.Truncate(prefix+theme.Dim.Render(cont+tb)+theme.Body.Render(fmt.Sprintf("%s:%d", label, t.Port))+"  "+state+extra, w))
		}
	}
	for li, l := range v.data.Listeners {
		lb, lc := "├── ", "│   "
		if li == len(v.data.Listeners)-1 {
			lb, lc = "└── ", "    "
		}
		desc := theme.Title.Render(fmt.Sprintf("%s:%d", l.Protocol, l.Port)) + theme.Dim.Render(" → "+l.Action)
		lines = append(lines, theme.Dim.Render(lb)+desc)
		for gi, arn := range l.TargetGroups {
			seen[arn] = true
			if g, ok := byARN[arn]; ok {
				group(theme.Dim.Render(lc), g, gi == len(l.TargetGroups)-1)
			}
		}
	}
	rest := orphans(v.data, seen)
	if len(rest) > 0 {
		lines = append(lines, "", theme.Dim.Render("Target groups used by listener rules:"))
		for i, g := range rest {
			group("  ", g, i == len(rest)-1)
		}
	}
	body := h - 2
	v.offset = max(0, min(v.offset, len(lines)-body))
	return head + components.Fit(strings.Join(lines[v.offset:], "\n"), w, body)
}

// orphans returns target groups not reached through a default action.
func orphans(t *topology, seen map[string]bool) []tgHealth {
	var out []tgHealth
	for _, g := range t.Groups {
		if !seen[g.TG.ARN] {
			out = append(out, g)
		}
	}
	return out
}
