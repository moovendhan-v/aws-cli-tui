package screens

import (
	"github.com/moovendhan-v/aws-cli-tui/devops/internal/aws/console"
	"github.com/moovendhan-v/aws-cli-tui/devops/internal/aws/trail"
	"github.com/moovendhan-v/aws-cli-tui/devops/internal/tui/core"
)

// ConsoleLink implementations: the AWS console page for whatever is selected,
// shown as a clickable link in the footer and opened by o / Ctrl+O.

func (r *Resource) ConsoleLink(e *core.Env) string {
	if r.view != nil {
		if l, ok := r.view.(core.Linker); ok {
			return l.ConsoleLink(e)
		}
		return ""
	}
	f := r.top()
	if f == nil || f.level.Link == nil {
		return ""
	}
	row, ok := f.table.Selected()
	if !ok {
		return ""
	}
	return f.level.Link(e.Ctx().Region, row)
}

func (s *EC2) ConsoleLink(e *core.Env) string {
	if in := s.selected(); in != nil {
		return console.EC2Instance(e.Ctx().Region, in.ID)
	}
	return ""
}

func (l *Logs) ConsoleLink(e *core.Env) string {
	if l.viewer != nil {
		return console.LogGroup(e.Ctx().Region, l.viewer.group.Name)
	}
	return l.list.ConsoleLink(e)
}

func (c *Changes) ConsoleLink(e *core.Env) string {
	row, ok := c.table.Selected()
	if !ok {
		return ""
	}
	ch := row.Data.(trail.Change)
	if ch.Kind == "alarm" {
		return console.Alarm(e.Ctx().Region, ch.ResourceName)
	}
	return console.CloudTrailEvent(e.Ctx().Region, ch.ID)
}

func (v *ecsServiceView) ConsoleLink(e *core.Env) string {
	region := e.Ctx().Region
	if t, ok := v.selectedTask(); ok && v.tab == tabTasks {
		return console.ECSTask(region, v.cluster, t.ID)
	}
	if v.td != nil && v.tab == tabTaskDef {
		return console.ECSTaskDef(region, v.td.FamilyRev())
	}
	return console.ECSService(region, v.cluster, v.service)
}

func (v *lambdaView) ConsoleLink(e *core.Env) string { return console.Lambda(e.Ctx().Region, v.name) }
func (v *pipelineView) ConsoleLink(e *core.Env) string {
	return console.Pipeline(e.Ctx().Region, v.name)
}
func (v *alarmView) ConsoleLink(e *core.Env) string {
	return console.Alarm(e.Ctx().Region, v.alarm.Name)
}
func (v *topologyView) ConsoleLink(e *core.Env) string {
	return console.LoadBalancer(e.Ctx().Region, v.lb.ARN)
}
func (p *s3Preview) ConsoleLink(e *core.Env) string {
	return console.S3Object(e.Ctx().Region, p.bucket, p.entry.Key)
}
func (v *buildView) ConsoleLink(e *core.Env) string {
	return console.Build(e.Ctx().Region, e.Ctx().AccountID, v.id)
}
