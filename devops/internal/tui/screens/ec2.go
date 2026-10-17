package screens

import (
	"context"
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"devops/internal/aws/ec2"
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
	table   *components.Table
	filter  filterBar
	loading bool
	err     error
	detail  *ec2.Instance
	ssm     map[string]*ssmState
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
	return []core.Hint{{Key: "f", Label: "Filter"}, {Key: "enter", Label: "Inspect"}, {Key: "s", Label: "SSM session"}, {Key: "r", Label: "Refresh"}}
}

func (s *EC2) Init(e *core.Env) tea.Cmd {
	if e.Session.Ready() != nil {
		return nil
	}
	s.loading, s.err, s.detail = true, nil, nil
	s.ssm = map[string]*ssmState{}
	sess := e.Session
	return e.Run(s.ID(), "list", executor.Spec{Name: "List EC2 instances", Quiet: true},
		func(ctx context.Context, _ *executor.Job) (any, error) { return ec2.List(ctx, sess) })
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
		if m.Key == "list" {
			s.loading, s.err = false, m.Err
			list, _ := m.Value.([]ec2.Instance)
			s.setRows(e, list)
		} else if id, ok := strings.CutPrefix(m.Key, "ssm:"); ok {
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
	case tea.KeyMsg:
		if s.filter.active {
			changed, cmd := s.filter.update(m)
			if changed {
				s.table.SetFilter(s.filter.value())
			}
			return cmd
		}
		return s.key(e, m)
	}
	return nil
}

func (s *EC2) key(e *core.Env, m tea.KeyMsg) tea.Cmd {
	in := s.selected()
	switch m.String() {
	case "f":
		if s.detail == nil {
			return s.filter.start()
		}
	case "r":
		return s.Init(e)
	case "enter":
		if s.detail == nil && in != nil {
			s.detail = in
			return s.loadSSM(e, in.ID)
		}
	}
	if in != nil {
		args := map[string]string{"instance_id": in.ID}
		switch m.String() {
		case "s":
			return e.RunCommandID("ssm.session.start", args)
		case "h":
			return e.RunCommandID("workflow.ec2.health", args)
		case "c":
			return e.RunCommandID("ec2.instance.cpu", map[string]string{"dimension_value": in.ID})
		case "g":
			return e.RunCommandID("ec2.instance.security-groups", args)
		case "b":
			return e.RunCommandID("ec2.instance.reboot", args)
		case "m":
			return s.loadSSM(e, in.ID)
		}
	}
	if s.detail == nil {
		s.table.Update(m)
	}
	return nil
}

func (s *EC2) loadSSM(e *core.Env, id string) tea.Cmd {
	s.ssm[id] = &ssmState{loading: true}
	sess := e.Session
	return e.Run(s.ID(), "ssm:"+id, executor.Spec{Name: "Check SSM status", Quiet: true, Resources: []string{id}},
		func(ctx context.Context, _ *executor.Job) (any, error) { return ssm.Status(ctx, sess, id) })
}

func (s *EC2) setRows(e *core.Env, list []ec2.Instance) {
	rows := make([]components.Row, 0, len(list))
	idx := make([]core.IndexItem, 0, len(list))
	for _, in := range list {
		var tags []string
		for k, v := range in.Tags {
			tags = append(tags, k+"="+v)
		}
		rows = append(rows, components.Row{
			Key:    in.ID,
			Cells:  []string{dash(in.Name), in.ID, in.State, in.Type, dash(in.PrivateIP), in.AZ},
			Tone:   theme.StatusTone(in.State),
			Search: strings.Join(tags, " ") + " " + in.PublicIP + " " + in.VPC,
			Data:   in,
		})
		idx = append(idx, core.IndexItem{Screen: s.ID(), Label: in.ID + " " + in.Name, Detail: "EC2 · " + in.State + " · " + in.PrivateIP})
	}
	s.table.SetRows(rows)
	e.SetIndex(s.ID(), idx)
}

func (s *EC2) View(e *core.Env, w, h int) string {
	if err := e.Session.Ready(); err != nil {
		return heading("EC2 Instances", "", w) + "\n\n" + errorView(err, w)
	}
	if s.detail != nil {
		return s.detailView(e, w, h)
	}
	right := fmt.Sprintf("%s of %d", s.table.Position(), s.table.Total())
	if s.loading {
		right = e.Spinner() + " loading"
	}
	out := heading("EC2 Instances", right, w) + "\n"
	if fv := s.filter.view(w); fv != "" {
		out += fv + "\n"
	}
	switch {
	case s.err != nil:
		return out + "\n" + errorView(s.err, w)
	case s.loading && s.table.Total() == 0:
		return out + "\n" + loadingView(e, "instances")
	case s.table.Total() == 0:
		return out + "\n" + theme.Dim.Render("No instances in "+e.Ctx().Region+". Press Ctrl+R to change region.")
	}
	used := strings.Count(out, "\n")
	return out + s.table.View(w, h-used)
}

func (s *EC2) detailView(e *core.Env, w, h int) string {
	in := s.detail
	var b strings.Builder
	b.WriteString(heading(dash(in.Name), "esc back", w) + "\n")
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
		{Key: "s", Label: "SSM session"}, {Key: "h", Label: "Health workflow"}, {Key: "c", Label: "CPU metrics"}, {Key: "g", Label: "Security groups"}, {Key: "m", Label: "Re-check SSM"}, {Key: "b", Label: "Reboot"},
	}))
	return components.Fit(b.String(), w, h)
}
