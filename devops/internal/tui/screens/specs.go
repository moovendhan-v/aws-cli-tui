package screens

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"devops/internal/aws/auth"
	"devops/internal/aws/ecs"
	"devops/internal/aws/lambda"
	"devops/internal/aws/metrics"
	"devops/internal/aws/networking"
	"devops/internal/aws/rds"
	"devops/internal/tui/components"
	"devops/internal/tui/core"
	"devops/internal/tui/theme"
)

type col = components.Column

func lines(pairs ...[2]string) string {
	var out []string
	for _, p := range pairs {
		if p[1] == "" {
			continue
		}
		out = append(out, theme.Dim.Render(components.Pad(p[0], 14))+theme.Body.Render(p[1]))
	}
	return strings.Join(out, "\n")
}

// ───────────────────────────── ECS ─────────────────────────────

func ECSClusters() *Level {
	return &Level{
		Title: "ECS Clusters", Noun: "ECS clusters",
		Columns: []col{{Title: "Cluster", Width: 20, Flex: true}, {Title: "Status", Width: 9}, {Title: "Services", Width: 8},
			{Title: "Running", Width: 8}, {Title: "Pending", Width: 8}},
		Load: func(ctx context.Context, s *auth.Session) ([]components.Row, error) {
			list, err := ecs.ListClusters(ctx, s)
			rows := make([]components.Row, 0, len(list))
			for _, c := range list {
				tone := theme.StatusTone(c.Status)
				if c.Pending > 0 {
					tone = theme.ToneWarn
				}
				rows = append(rows, components.Row{Key: c.ARN, Data: c, Tone: tone,
					Cells: []string{c.Name, c.Status, fmt.Sprint(c.Services), fmt.Sprint(c.Running), fmt.Sprint(c.Pending)}})
			}
			return rows, err
		},
		Child: func(r components.Row) *Level { return ecsServices(r.Data.(ecs.Cluster).Name) },
		Actions: []Action{
			{Key: "d", Label: "Running vs desired", Run: func(e *core.Env, r components.Row) tea.Cmd {
				return e.RunCommandID("ecs.services.list", map[string]string{"cluster": r.Data.(ecs.Cluster).Name})
			}},
		},
		Empty: "No ECS clusters in this region.",
	}
}

func ecsServices(cluster string) *Level {
	return &Level{
		Title: cluster, Noun: "ECS services",
		Columns: []col{{Title: "Service", Width: 22, Flex: true}, {Title: "Status", Width: 8}, {Title: "Desired", Width: 7},
			{Title: "Running", Width: 7}, {Title: "Pending", Width: 7}, {Title: "Rollout", Width: 12}, {Title: "Launch", Width: 8}},
		Load: func(ctx context.Context, s *auth.Session) ([]components.Row, error) {
			list, err := ecs.ListServices(ctx, s, cluster)
			rows := make([]components.Row, 0, len(list))
			for _, sv := range list {
				tone := theme.ToneOK
				switch {
				case sv.Running < sv.Desired || sv.Rollout == "FAILED":
					tone = theme.ToneBad
				case sv.Pending > 0 || sv.Rollout == "IN_PROGRESS":
					tone = theme.ToneWarn
				}
				rows = append(rows, components.Row{Key: sv.ARN, Data: sv, Tone: tone,
					Cells: []string{sv.Name, sv.Status, fmt.Sprint(sv.Desired), fmt.Sprint(sv.Running), fmt.Sprint(sv.Pending), dash(sv.Rollout), dash(sv.LaunchType)}})
			}
			return rows, err
		},
		Child: func(r components.Row) *Level { return ecsTasks(cluster, r.Data.(ecs.Service).Name) },
		Detail: func(r components.Row) string {
			sv := r.Data.(ecs.Service)
			return lines([2]string{"Task def", sv.TaskDef}, [2]string{"ECS Exec", yesNo(sv.ExecEnabled)},
				[2]string{"Failed tasks", fmt.Sprint(sv.FailedTasks)}, [2]string{"Last event", sv.LastEvent})
		},
		Actions: []Action{
			{Key: "t", Label: "Triage", Run: func(e *core.Env, r components.Row) tea.Cmd {
				return e.RunCommandID("workflow.ecs.triage", map[string]string{"cluster": cluster, "service": r.Data.(ecs.Service).Name})
			}},
			{Key: "s", Label: "Stopped tasks", Run: func(e *core.Env, r components.Row) tea.Cmd {
				return e.RunCommandID("ecs.tasks.stopped", map[string]string{"cluster": cluster, "service": r.Data.(ecs.Service).Name})
			}},
			{Key: "D", Label: "Force deploy", Run: func(e *core.Env, r components.Row) tea.Cmd {
				return e.RunCommandID("ecs.service.force-deploy", map[string]string{"cluster": cluster, "service": r.Data.(ecs.Service).Name})
			}},
		},
		Empty: "No services in this cluster.",
	}
}

func ecsTasks(cluster, service string) *Level {
	return &Level{
		Title: service, Noun: "ECS tasks",
		Columns: []col{{Title: "Task", Width: 12}, {Title: "Status", Width: 12}, {Title: "Health", Width: 9},
			{Title: "Started", Width: 10}, {Title: "Stopped reason", Width: 20, Flex: true}},
		Load: func(ctx context.Context, s *auth.Session) ([]components.Row, error) {
			list, err := ecs.ListTasks(ctx, s, cluster, service)
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
				rows = append(rows, components.Row{Key: t.ARN, Data: t, Tone: tone,
					Cells: []string{shortID(t.ID), t.LastStatus, dash(t.Health), components.Ago(t.StartedAt), dash(reason)}})
			}
			return rows, err
		},
		Detail: func(r components.Row) string {
			t := r.Data.(ecs.Task)
			out := []string{lines([2]string{"Task", t.ID}, [2]string{"Task def", t.TaskDef}, [2]string{"Stopped", t.StoppedReason})}
			for _, c := range t.Containers {
				exit := "-"
				if c.ExitCode != nil {
					exit = fmt.Sprint(*c.ExitCode)
				}
				line := fmt.Sprintf("◆ %s  %s  health=%s  exit=%s", c.Name, c.Status, dash(c.Health), exit)
				if c.Reason != "" {
					line += "  " + c.Reason
				}
				tone := theme.StatusTone(c.Status)
				if c.ExitCode != nil && *c.ExitCode != 0 {
					tone = theme.ToneBad
				}
				out = append(out, tone.Style().Render(line))
			}
			return strings.Join(out, "\n")
		},
		Actions: []Action{
			{Key: "x", Label: "ECS Exec", Run: func(e *core.Env, r components.Row) tea.Cmd {
				t := r.Data.(ecs.Task)
				if t.LastStatus != "RUNNING" {
					return e.Toast("ECS Exec needs a RUNNING task", theme.ToneWarn)
				}
				if !t.ExecEnabled {
					return e.Toast("ECS Exec is not enabled on this task (enableExecuteCommand=false)", theme.ToneWarn)
				}
				args := map[string]string{"cluster": cluster, "task": t.ID}
				if len(t.Containers) > 0 {
					args["container"] = t.Containers[0].Name
				}
				return e.RunCommandID("ecs.exec.shell", args)
			}},
		},
		Empty: "No running or recently stopped tasks.",
	}
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// ───────────────────────────── RDS ─────────────────────────────

func RDSInstances() *Level {
	return &Level{
		Title: "Databases", Noun: "RDS instances",
		Columns: []col{{Title: "Identifier", Width: 20, Flex: true}, {Title: "Engine", Width: 16}, {Title: "Status", Width: 11},
			{Title: "Class", Width: 14}, {Title: "Multi-AZ", Width: 8}},
		Load: func(ctx context.Context, s *auth.Session) ([]components.Row, error) {
			list, err := rds.List(ctx, s)
			rows := make([]components.Row, 0, len(list))
			for _, d := range list {
				rows = append(rows, components.Row{Key: d.ID, Data: d, Tone: theme.StatusTone(d.Status), Search: d.Endpoint,
					Cells: []string{d.ID, d.Engine + " " + d.Version, d.Status, d.Class, yesNo(d.MultiAZ)}})
			}
			return rows, err
		},
		Detail: func(r components.Row) string {
			d := r.Data.(rds.Instance)
			public := yesNo(d.Public)
			if d.Public {
				public = theme.Warn.Render("yes — publicly accessible")
			}
			enc := yesNo(d.Encrypted)
			if !d.Encrypted {
				enc = theme.Warn.Render("no — storage not encrypted")
			}
			return lines([2]string{"Endpoint", d.EndpointLabel()}, [2]string{"Cluster", d.Cluster}, [2]string{"AZ", d.AZ},
				[2]string{"Public", public}, [2]string{"Encrypted", enc})
		},
		Actions: []Action{
			{Key: "k", Label: "Connectivity check", Run: func(e *core.Env, r components.Row) tea.Cmd {
				return e.RunCommandID("workflow.db.connectivity", map[string]string{"db_instance_id": r.Key})
			}},
			{Key: "c", Label: "Connections", Run: func(e *core.Env, r components.Row) tea.Cmd {
				return e.RunCommandID("rds.connections", map[string]string{"dimension_value": r.Key})
			}},
			{Key: "t", Label: "Tunnel", Run: func(e *core.Env, r components.Row) tea.Cmd {
				d := r.Data.(rds.Instance)
				return e.OpenCommand(e.Deps.Registry.Get("ssm.tunnel.database"),
					map[string]string{"db_host": d.Endpoint, "db_port": fmt.Sprint(d.Port)})
			}},
		},
		Empty: "No RDS instances in this region.",
	}
}

// ───────────────────────────── Lambda ─────────────────────────────

func LambdaFunctions() *Level {
	return &Level{
		Title: "Lambda", Noun: "Lambda functions",
		Columns: []col{{Title: "Function", Width: 24, Flex: true}, {Title: "Runtime", Width: 12}, {Title: "Memory", Width: 7},
			{Title: "Timeout", Width: 7}, {Title: "State", Width: 8}, {Title: "Modified", Width: 10}},
		Load: func(ctx context.Context, s *auth.Session) ([]components.Row, error) {
			list, err := lambda.List(ctx, s)
			rows := make([]components.Row, 0, len(list))
			for _, f := range list {
				mod := f.LastModified
				if len(mod) >= 10 {
					mod = mod[:10]
				}
				rows = append(rows, components.Row{Key: f.Name, Data: f, Tone: theme.StatusTone(f.State),
					Cells: []string{f.Name, dash(f.Runtime), fmt.Sprintf("%dMB", f.MemoryMB), fmt.Sprintf("%ds", f.TimeoutSec), dash(f.State), mod}})
			}
			return rows, err
		},
		Detail: func(r components.Row) string {
			f := r.Data.(lambda.Function)
			return lines([2]string{"Handler", f.Handler}, [2]string{"Code size", components.Bytes(f.CodeSize)})
		},
		Actions: []Action{
			{Key: "i", Label: "Configuration", Run: func(e *core.Env, r components.Row) tea.Cmd {
				return e.RunCommandID("lambda.function.config", map[string]string{"function_name": r.Key})
			}},
			{Key: "l", Label: "Logs", Run: func(e *core.Env, r components.Row) tea.Cmd {
				return e.Navigate("logs", "/aws/lambda/"+r.Key)
			}},
		},
		Empty: "No Lambda functions in this region.",
	}
}

// ───────────────────────────── Load balancers ─────────────────────────────

func LoadBalancers() *Level {
	return &Level{
		Title: "Load Balancers", Noun: "load balancers",
		Columns: []col{{Title: "Name", Width: 20, Flex: true}, {Title: "Type", Width: 11}, {Title: "Scheme", Width: 15},
			{Title: "State", Width: 10}, {Title: "DNS", Width: 20, Flex: true}},
		Load: func(ctx context.Context, s *auth.Session) ([]components.Row, error) {
			list, err := networking.LoadBalancers(ctx, s)
			rows := make([]components.Row, 0, len(list))
			for _, lb := range list {
				rows = append(rows, components.Row{Key: lb.ARN, Data: lb, Tone: theme.StatusTone(lb.State),
					Cells: []string{lb.Name, lb.Type, lb.Scheme, lb.State, lb.DNS}})
			}
			return rows, err
		},
		Child: func(r components.Row) *Level {
			lb := r.Data.(networking.LoadBalancer)
			return targetGroups(lb.Name, lb.ARN)
		},
		Empty: "No ALBs or NLBs in this region.",
	}
}

func targetGroups(lbName, lbARN string) *Level {
	return &Level{
		Title: lbName, Noun: "target groups",
		Columns: []col{{Title: "Target group", Width: 22, Flex: true}, {Title: "Protocol", Width: 8}, {Title: "Port", Width: 6},
			{Title: "Target type", Width: 11}, {Title: "Health check", Width: 16, Flex: true}},
		Load: func(ctx context.Context, s *auth.Session) ([]components.Row, error) {
			list, err := networking.TargetGroups(ctx, s, lbARN)
			rows := make([]components.Row, 0, len(list))
			for _, tg := range list {
				rows = append(rows, components.Row{Key: tg.ARN, Data: tg,
					Cells: []string{tg.Name, tg.Protocol, fmt.Sprint(tg.Port), tg.TargetType, dash(tg.HealthPath)}})
			}
			return rows, err
		},
		Child: func(r components.Row) *Level { tg := r.Data.(networking.TargetGroup); return targets(tg.Name, tg.ARN) },
		Empty: "No target groups attached.",
	}
}

func targets(tgName, tgARN string) *Level {
	return &Level{
		Title: tgName, Noun: "target health",
		Columns: []col{{Title: "Target", Width: 22}, {Title: "Port", Width: 6}, {Title: "State", Width: 12},
			{Title: "Reason", Width: 20}, {Title: "Description", Width: 20, Flex: true}},
		Load: func(ctx context.Context, s *auth.Session) ([]components.Row, error) {
			list, err := networking.TargetHealth(ctx, s, tgARN)
			rows := make([]components.Row, 0, len(list))
			for _, t := range list {
				rows = append(rows, components.Row{Key: fmt.Sprintf("%s:%d", t.ID, t.Port), Data: t, Tone: theme.StatusTone(t.State),
					Cells: []string{t.ID, fmt.Sprint(t.Port), t.State, dash(t.Reason), dash(t.Description)}})
			}
			return rows, err
		},
		Empty: "No registered targets.",
	}
}

// ───────────────────────────── Alarms ─────────────────────────────

func Alarms() *Level {
	return &Level{
		Title: "Metrics & Alarms", Noun: "CloudWatch alarms",
		Columns: []col{{Title: "Alarm", Width: 26, Flex: true}, {Title: "State", Width: 17}, {Title: "Metric", Width: 22},
			{Title: "Changed", Width: 10}},
		Load: func(ctx context.Context, s *auth.Session) ([]components.Row, error) {
			list, err := metrics.Alarms(ctx, s, "")
			rows := make([]components.Row, 0, len(list))
			for _, a := range list {
				metric := a.Metric
				if a.Namespace != "" && a.Metric != "" {
					metric = a.Namespace + "/" + a.Metric
				}
				rows = append(rows, components.Row{Key: a.Name, Data: a, Tone: theme.StatusTone(a.State), Search: a.Reason,
					Cells: []string{a.Name, a.State, dash(metric), components.Ago(a.Updated)}})
			}
			return rows, err
		},
		Detail: func(r components.Row) string {
			return lines([2]string{"Reason", r.Data.(metrics.Alarm).Reason})
		},
		Empty: "No CloudWatch alarms in this region.",
	}
}

// ───────────────────────────── Networking ─────────────────────────────

func VPCs() *Level {
	return &Level{
		Title: "Networking", Noun: "VPCs",
		Columns: []col{{Title: "VPC", Width: 22}, {Title: "Name", Width: 20, Flex: true}, {Title: "CIDR", Width: 18},
			{Title: "State", Width: 10}, {Title: "Default", Width: 7}},
		Load: func(ctx context.Context, s *auth.Session) ([]components.Row, error) {
			list, err := networking.VPCs(ctx, s)
			rows := make([]components.Row, 0, len(list))
			for _, v := range list {
				rows = append(rows, components.Row{Key: v.ID, Data: v, Tone: theme.StatusTone(v.State),
					Cells: []string{v.ID, dash(v.Name), v.CIDR, v.State, yesNo(v.Default)}})
			}
			return rows, err
		},
		Actions: []Action{
			{Key: "s", Label: "Subnets", Run: func(e *core.Env, r components.Row) tea.Cmd {
				return e.RunCommandID("vpc.subnets", map[string]string{"vpc_id": r.Key})
			}},
			{Key: "t", Label: "Route tables", Run: func(e *core.Env, r components.Row) tea.Cmd {
				return e.RunCommandID("vpc.route-tables", map[string]string{"vpc_id": r.Key})
			}},
		},
		Empty: "No VPCs in this region.",
	}
}
