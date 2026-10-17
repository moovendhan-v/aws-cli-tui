package screens

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"devops/internal/aws/console"
	"devops/internal/aws/ec2"
	"devops/internal/aws/meter"
	"devops/internal/aws/metrics"
	"devops/internal/aws/ssm"
	"devops/internal/executor"
	"devops/internal/tui/components"
	"devops/internal/tui/core"
	"devops/internal/tui/theme"
)

type ssmState struct {
	loading bool
	status  ssm.NodeStatus
	err     error
}

type EC2 struct {
	table       *components.Table
	filter      filterBar
	loading     bool
	loadingMore bool
	next        string
	cachedAt    time.Time
	err         error
	detail      *ec2.Instance
	ssm         map[string]*ssmState
	cpu         map[string][]float64
	cpuLoading  bool
}

// ec2Page is the cached form of the loaded instance list.
type ec2Page struct {
	Instances []ec2.Instance
	Next      string
}

func NewEC2() *EC2 {
	return &EC2{
		table: components.NewTable(
			components.Column{Title: "Name", Width: 18, Flex: true},
			components.Column{Title: "Instance ID", Width: 19},
			components.Column{Title: "State", Width: 10},
			components.Column{Title: "Type", Width: 11},
			components.Column{Title: "Private IP", Width: 15},
			components.Column{Title: "AZ", Width: 15},
			components.Column{Title: "CPU 3h", Width: 0},
		),
		filter: newFilter(),
		ssm:    map[string]*ssmState{},
	}
}

func (s *EC2) ID() string      { return "ec2" }
func (s *EC2) Capturing() bool { return s.filter.active }

func (s *EC2) Hints() []core.Hint {
	if s.detail != nil {
		return []core.Hint{{Key: "s", Label: "SSM session"}, {Key: "h", Label: "Health check"}, {Key: "c", Label: "CPU"}, {Key: "g", Label: "Sec groups"}, {Key: "esc", Label: "Back"}}
	}
	return []core.Hint{{Key: "f", Label: "Filter"}, {Key: "enter", Label: "Inspect"}, {Key: "s", Label: "SSM session"}, {Key: "C", Label: "CPU trends $"}, {Key: "o", Label: "Console"}, {Key: "y", Label: "Copy ID"}, {Key: "r", Label: "Refresh"}}
}

func (s *EC2) Init(e *core.Env) tea.Cmd {
	s.detail = nil
	s.ssm = map[string]*ssmState{}
	return s.load(e, false)
}

func (s *EC2) load(e *core.Env, force bool) tea.Cmd {
	if e.Session.Ready() != nil {
		return nil
	}
	s.loading, s.err = true, nil
	sess := e.Session
	return e.Cached(s.ID(), "list", e.CacheKey("ec2", "instances"), force, executor.Spec{Name: "List EC2 instances", Quiet: true},
		func(ctx context.Context, _ *executor.Job) (any, error) {
			list, next, err := ec2.ListPage(ctx, sess, "")
			return ec2Page{Instances: list, Next: next}, err
		})
}

func (s *EC2) loadMore(e *core.Env) tea.Cmd {
	if s.next == "" || s.loadingMore {
		return nil
	}
	s.loadingMore = true
	sess, token := e.Session, s.next
	return e.Run(s.ID(), "more", executor.Spec{Name: "List more EC2 instances", Quiet: true},
		func(ctx context.Context, _ *executor.Job) (any, error) {
			list, next, err := ec2.ListPage(ctx, sess, token)
			return ec2Page{Instances: list, Next: next}, err
		})
}

func (s *EC2) Focus(e *core.Env, q string) tea.Cmd {
	s.detail = nil
	s.filter.set(q)
	s.table.SetFilter(q)
	return nil
}

func (s *EC2) Back(e *core.Env) (bool, tea.Cmd) {
	if s.detail != nil {
		s.detail = nil
		return true, nil
	}
	if s.filter.value() != "" {
		s.filter.set("")
		s.table.SetFilter("")
		return true, nil
	}
	return false, nil
}

func (s *EC2) selected() *ec2.Instance {
	if s.detail != nil {
		return s.detail
	}
	if r, ok := s.table.Selected(); ok {
		in := r.Data.(ec2.Instance)
		return &in
	}
	return nil
}

func (s *EC2) Update(e *core.Env, msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case core.ResultMsg:
		switch m.Key {
		case "list":
			s.loading, s.err, s.cachedAt = m.Refreshing, m.Err, m.CachedAt
			if p, ok := m.Value.(ec2Page); ok {
				s.table.SetRows(nil)
				s.addRows(e, p.Instances)
				s.next = p.Next
			}
			return nil
		case "cpu":
			s.cpuLoading = false
			if m.Err != nil {
				return e.Toast("CPU load failed: "+m.Err.Error(), theme.ToneBad)
			}
			s.cpu, _ = m.Value.(map[string][]float64)
			s.refreshCells()
			return nil
		case "more":
			s.loadingMore = false
			if m.Err != nil {
				return e.Toast("Loading more instances failed: "+m.Err.Error(), theme.ToneBad)
			}
			if p, ok := m.Value.(ec2Page); ok {
				s.addRows(e, p.Instances)
				s.next = p.Next
				e.Deps.Cache.Put(e.CacheKey("ec2", "instances"), ec2Page{Instances: s.instances(), Next: s.next})
			}
			return nil
		}
		if id, ok := strings.CutPrefix(m.Key, "ssm:"); ok {
			st := s.ssm[id]
			if st == nil {
				st = &ssmState{}
				s.ssm[id] = st
			}
			st.loading, st.err = false, m.Err
			if v, ok := m.Value.(ssm.NodeStatus); ok {
				st.status = v
			}
		}
	case core.MouseMsg:
		if s.detail != nil {
			return nil
		}
		switch {
		case m.WheelUp:
			s.table.Scroll(-3)
		case m.WheelDown:
			s.table.Scroll(3)
		case m.Click:
			top := 1
			if s.filter.active || s.filter.value() != "" {
				top = 2
			}
			before := s.table.Cursor()
			if s.table.Click(m.Y-top) && m.Double && before == s.table.Cursor() {
				return s.key(e, tea.KeyMsg{Type: tea.KeyEnter})
			}
		}
		if s.table.WantsMore() {
			return s.loadMore(e)
		}
	case tea.KeyMsg:
		if s.filter.active {
			changed, cmd := s.filter.update(m)
			if changed {
				s.table.SetFilter(s.filter.value())
			}
			return cmd
		}
		cmd := s.key(e, m)
		if s.detail == nil && s.table.WantsMore() {
			return tea.Batch(cmd, s.loadMore(e))
		}
		return cmd
	}
	return nil
}

func (s *EC2) instances() []ec2.Instance {
	rows := s.table.Rows()
	out := make([]ec2.Instance, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Data.(ec2.Instance))
	}
	return out
}

func (s *EC2) key(e *core.Env, m tea.KeyMsg) tea.Cmd {
	in := s.selected()
	switch m.String() {
	case "f":
		if s.detail == nil {
			return s.filter.start()
		}
	case "C":
		if s.detail == nil {
			return s.loadCPU(e)
		}
	case "r":
		if s.detail != nil {
			return s.loadSSM(e, s.detail.ID)
		}
		return s.load(e, true)
	case "enter":
		if s.detail == nil && in != nil {
			s.detail = in
			return s.loadSSM(e, in.ID)
		}
	}
	if in != nil {
		args := map[string]string{"instance_id": in.ID, "instance_id#label": in.Name}
		switch m.String() {
		case "s":
			return e.RunCommandID("ssm.session.start", args)
		case "h":
			return e.RunCommandID("workflow.ec2.health", args)
		case "c":
			return e.RunCommandID("ec2.instance.cpu", map[string]string{"dimension_value": in.ID, "dimension_value#label": in.Name})
		case "g":
			return e.RunCommandID("ec2.instance.security-groups", args)
		case "b":
			return e.RunCommandID("ec2.instance.reboot", args)
		case "m":
			return s.loadSSM(e, in.ID)
		case "*":
			label := in.Name
			if label == "" {
				label = in.ID
			}
			return e.TogglePin(core.Pin{Screen: "ec2", Key: in.ID, Label: label})
		case "o":
			return e.OpenURL(console.EC2Instance(e.Ctx().Region, in.ID), false)
		case "y":
			return e.Copy(in.ID, "instance ID")
		}
	}
	if s.detail == nil {
		s.table.Update(m)
	}
	return nil
}

// loadCPU fetches 3h CPU for loaded instances in one batched, billed call.
func (s *EC2) loadCPU(e *core.Env) tea.Cmd {
	var ids []string
	for _, in := range s.instances() {
		if in.State == "running" {
			ids = append(ids, in.ID)
		}
	}
	if len(ids) == 0 {
		return e.Toast("No running instances loaded", theme.ToneDim)
	}
	s.cpuLoading = true
	s.table.Columns[6].Width = 18
	sess := e.Session
	return tea.Batch(e.Toast(fmt.Sprintf("$ CPU for %d instances — %s", len(ids), meter.BilledNote("CloudWatch", "GetMetricData")), theme.ToneWarn),
		e.Cached(s.ID(), "cpu", e.CacheKey("ec2", "cpu", strings.Join(ids, ",")), false,
			executor.Spec{Name: "EC2 CPU sparklines", Quiet: true},
			func(ctx context.Context, _ *executor.Job) (any, error) {
				return metrics.EC2CPU(ctx, sess, ids, 3*time.Hour)
			}))
}

func (s *EC2) cpuCell(id string) string {
	vals := s.cpu[id]
	if len(vals) == 0 {
		return ""
	}
	last := vals[len(vals)-1]
	st := theme.OK
	switch {
	case last >= 85:
		st = theme.Bad
	case last >= 65:
		st = theme.Warn
	}
	return components.Sparkline(vals, 12, st) + fmt.Sprintf(" %3.0f%%", last)
}

func (s *EC2) refreshCells() {
	rows := s.table.Rows()
	for i := range rows {
		rows[i].Cells[6] = s.cpuCell(rows[i].Key)
	}
	s.table.SetRows(rows)
}

func (s *EC2) loadSSM(e *core.Env, id string) tea.Cmd {
	s.ssm[id] = &ssmState{loading: true}
	sess := e.Session
	return e.Run(s.ID(), "ssm:"+id, executor.Spec{Name: "Check SSM status", Quiet: true, Resources: []string{id}},
		func(ctx context.Context, _ *executor.Job) (any, error) { return ssm.Status(ctx, sess, id) })
}

func (s *EC2) addRows(e *core.Env, list []ec2.Instance) {
	rows := make([]components.Row, 0, len(list))
	for _, in := range list {
		var tags []string
		for k, v := range in.Tags {
			tags = append(tags, k+"="+v)
		}
		rows = append(rows, components.Row{
			Key:    in.ID,
			Cells:  []string{dash(in.Name), in.ID, in.State, in.Type, dash(in.PrivateIP), in.AZ, s.cpuCell(in.ID)},
			Tone:   theme.StatusTone(in.State),
			Search: strings.Join(tags, " ") + " " + in.PublicIP + " " + in.VPC,
			Data:   in,
		})
	}
	s.table.AppendRows(rows)
	idx := make([]core.IndexItem, 0, s.table.Total())
	for _, in := range s.instances() {
		idx = append(idx, core.IndexItem{Screen: s.ID(), Label: in.ID + " " + in.Name, Detail: "EC2 · " + in.State + " · " + in.PrivateIP})
	}
	e.SetIndex(s.ID(), idx)
}

func (s *EC2) View(e *core.Env, w, h int) string {
	if err := e.Session.Ready(); err != nil {
		return heading("EC2 Instances", "", w) + "\n\n" + errorView(err, w)
	}
	if s.detail != nil {
		return s.detailView(e, w, h)
	}
	right := s.table.Position() + " loaded"
	switch {
	case s.loading && s.table.Total() > 0:
		right = e.Spinner() + " refreshing · " + right
	case s.loading:
		right = e.Spinner() + " loading"
	case !s.cachedAt.IsZero():
		right += " · cached " + components.Ago(s.cachedAt)
	}
	out := heading("EC2 Instances", right, w) + "\n"
	if fv := s.filter.view(w); fv != "" {
		out += fv + "\n"
	}
	switch {
	case s.err != nil && s.table.Total() == 0:
		return out + "\n" + errorView(s.err, w)
	case s.loading && s.table.Total() == 0:
		return out + "\n" + loadingView(e, "instances")
	case s.table.Total() == 0:
		return out + "\n" + theme.Dim.Render("No instances in "+e.Ctx().Region+". Press Ctrl+R to change region.")
	}
	s.table.SetMore(s.next != "", s.loadingMore, e.Spinner())
	used := strings.Count(out, "\n")
	return out + s.table.View(w, h-used)
}

func (s *EC2) detailView(e *core.Env, w, h int) string {
	in := s.detail
	var b strings.Builder
	b.WriteString(headingRaw(dash(in.Name), "esc back", w) + "\n")
	b.WriteString(theme.Strong.Render(in.ID) + "  " + theme.StatusTone(in.State).Style().Render("● "+in.State) + "\n\n")
	b.WriteString(kv(w,
		[2]string{"Type", in.Type}, [2]string{"AZ", in.AZ},
		[2]string{"Private IP", in.PrivateIP}, [2]string{"Public IP", in.PublicIP},
		[2]string{"VPC", in.VPC}, [2]string{"Subnet", in.Subnet},
		[2]string{"IAM profile", in.IAMProfile}, [2]string{"Platform", in.Platform},
		[2]string{"Image", in.ImageID}, [2]string{"Launched", components.Ago(in.LaunchTime)},
		[2]string{"Key pair", in.Key},
	) + "\n\n")
	b.WriteString(theme.Title.Render("SECURITY GROUPS") + "\n" + theme.Body.Render(dash(strings.Join(in.SecurityGroups, ", "))) + "\n\n")

	b.WriteString(theme.Title.Render("SYSTEMS MANAGER") + "\n")
	st := s.ssm[in.ID]
	switch {
	case st == nil || st.loading:
		b.WriteString(theme.Dim.Render(e.Spinner() + " checking managed-node status…"))
	case st.err != nil:
		b.WriteString(errorView(st.err, w))
	case !st.status.Managed:
		b.WriteString(theme.Bad.Render("✗ Not an SSM managed node") + "\n" +
			theme.Dim.Render("Check: SSM agent running, instance profile with AmazonSSMManagedInstanceCore, and a network path to the SSM endpoints."))
	case !st.status.Ready():
		b.WriteString(theme.Warn.Render("● "+st.status.PingStatus) + theme.Dim.Render(" · last ping "+components.Ago(st.status.LastPing)))
	default:
		b.WriteString(theme.OK.Render("● Online") + theme.Dim.Render(fmt.Sprintf(" · agent %s · %s · ready for session", st.status.AgentVersion, st.status.Platform)))
	}
	if !ssm.PluginInstalled() {
		b.WriteString("\n" + theme.Warn.Render("session-manager-plugin is not installed — sessions will fail."))
	}
	b.WriteString("\n\n")

	if len(in.Tags) > 0 {
		keys := make([]string, 0, len(in.Tags))
		for k := range in.Tags {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var tags []string
		for _, k := range keys {
			tags = append(tags, theme.Dim.Render(k+"=")+theme.Body.Render(in.Tags[k]))
		}
		b.WriteString(theme.Title.Render("TAGS") + "\n" + strings.Join(tags, "  ") + "\n\n")
	}
	b.WriteString(theme.Title.Render("ACTIONS") + "\n" + hintLine([]core.Hint{
		{Key: "s", Label: "SSM session"}, {Key: "h", Label: "Health workflow"}, {Key: "c", Label: "CPU metrics"}, {Key: "g", Label: "Security groups"}, {Key: "m", Label: "Re-check SSM"}, {Key: "o", Label: "Open in console"}, {Key: "y", Label: "Copy ID"}, {Key: "b", Label: "Reboot"},
	}))
	return components.Fit(b.String(), w, h)
}
