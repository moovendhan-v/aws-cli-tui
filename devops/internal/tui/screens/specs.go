package screens

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"devops/internal/aws/auth"
	"devops/internal/aws/console"
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
		CacheKind: "ecs",
		Page: func(ctx context.Context, s *auth.Session, token string) ([]components.Row, string, error) {
			list, next, err := ecs.ListClustersPage(ctx, s, token)
			rows := make([]components.Row, 0, len(list))
			for _, c := range list {
				tone := theme.StatusTone(c.Status)
				if c.Pending > 0 {
					tone = theme.ToneWarn
				}
				rows = append(rows, components.Row{Key: c.ARN, Data: c, Tone: tone,
					Cells: []string{c.Name, c.Status, fmt.Sprint(c.Services), fmt.Sprint(c.Running), fmt.Sprint(c.Pending)}})
			}
			return rows, next, err
		},
		Child: func(r components.Row) *Level { return ecsServices(r.Data.(ecs.Cluster).Name) },
		Actions: []Action{
			{Key: "d", Label: "Running vs desired", Run: func(e *core.Env, r components.Row) tea.Cmd {
				return e.RunCommandID("ecs.services.list", map[string]string{"cluster": r.Data.(ecs.Cluster).Name})
			}},
		},
		Empty: "No ECS clusters in this region.",
		Link: func(region string, r components.Row) string {
			return console.ECSCluster(region, r.Data.(ecs.Cluster).Name)
		},
	}
}

func ecsServices(cluster string) *Level {
	return &Level{
		Title: cluster, Noun: "ECS services",
		Columns: []col{{Title: "Service", Width: 22, Flex: true}, {Title: "Status", Width: 8}, {Title: "Desired", Width: 7},
			{Title: "Running", Width: 7}, {Title: "Pending", Width: 7}, {Title: "Rollout", Width: 12}, {Title: "Launch", Width: 8}},
		CacheKind: "ecs",
		Page: func(ctx context.Context, s *auth.Session, token string) ([]components.Row, string, error) {
			list, next, err := ecs.ListServicesPage(ctx, s, cluster, token)
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
			return rows, next, err
		},
		Open: func(e *core.Env, r components.Row) (Viewer, tea.Cmd) {
			return newECSServiceView(e, "ecs", cluster, r.Data.(ecs.Service).Name)
		},
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
		Link: func(region string, r components.Row) string {
			return console.ECSService(region, cluster, r.Data.(ecs.Service).Name)
		},
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
		CacheKind: "rds",
		Page: func(ctx context.Context, s *auth.Session, token string) ([]components.Row, string, error) {
			list, next, err := rds.ListPage(ctx, s, token)
			rows := make([]components.Row, 0, len(list))
			for _, d := range list {
				rows = append(rows, components.Row{Key: d.ID, Data: d, Tone: theme.StatusTone(d.Status), Search: d.Endpoint,
					Cells: []string{d.ID, d.Engine + " " + d.Version, d.Status, d.Class, yesNo(d.MultiAZ)}})
			}
			return rows, next, err
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
			return lines([2]string{"Endpoint", d.EndpointLabel()}, [2]string{"Database", d.DBName}, [2]string{"Master user", d.MasterUser},
				[2]string{"VPC", d.VPC}, [2]string{"Cluster", d.Cluster}, [2]string{"AZ", d.AZ},
				[2]string{"Public", public}, [2]string{"Encrypted", enc})
		},
		Open: func(e *core.Env, r components.Row) (Viewer, tea.Cmd) { return nil, connectDB(e, r.Data.(rds.Instance)) },
		Actions: []Action{
			{Key: "x", Label: "SQL shell", Run: func(e *core.Env, r components.Row) tea.Cmd {
				return connectDB(e, r.Data.(rds.Instance))
			}},
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
		Link:  func(region string, r components.Row) string { return console.RDSInstance(region, r.Key) },
	}
}

// connectDB asks how to reach the database, then opens a SQL shell in the
// Terminals panel (through an SSM tunnel, or directly over VPN/public access).
func connectDB(e *core.Env, d rds.Instance) tea.Cmd {
	client := d.Client()
	if client == "" {
		return e.Toast("No SQL shell for engine "+d.Engine+" yet (PostgreSQL, MySQL, MariaDB and Aurora are supported)", theme.ToneWarn)
	}
	if d.Endpoint == "" {
		return e.Toast("This database has no endpoint yet (status "+d.Status+")", theme.ToneWarn)
	}
	dbname := d.DBName
	if dbname == "" {
		dbname = map[string]string{"postgres": "postgres", "mysql": "mysql"}[client]
	}
	items := []core.PickItem{
		{Label: "Through a bastion (SSM tunnel)", Detail: "private databases — pick an SSM-managed instance in the same VPC", Value: "tunnel"},
		{Label: "Directly", Detail: "you are on a VPN / the database is publicly reachable", Value: "direct"},
	}
	cur := "tunnel"
	if d.Public {
		cur = "direct"
	}
	return e.Open(core.NewPicker("Connect to "+d.ID+" ("+d.Engine+")", items, cur, func(it core.PickItem) tea.Cmd {
		if it.Value == "direct" {
			return e.RunCommandID("db.client."+client, map[string]string{"host": d.Endpoint, "port": fmt.Sprint(d.Port),
				"user": d.MasterUser, "dbname": dbname, "#form": "1"})
		}
		return e.RunCommandID("db.connect."+client, map[string]string{"db_host": d.Endpoint, "db_port": fmt.Sprint(d.Port),
			"db_user": d.MasterUser, "db_name": dbname, "#form": "1"})
	}))
}

// ───────────────────────────── Lambda ─────────────────────────────

func LambdaFunctions() *Level {
	return &Level{
		Title: "Lambda", Noun: "Lambda functions",
		Columns: []col{{Title: "Function", Width: 24, Flex: true}, {Title: "Runtime", Width: 12}, {Title: "Memory", Width: 7},
			{Title: "Timeout", Width: 7}, {Title: "State", Width: 8}, {Title: "Modified", Width: 10}},
		CacheKind: "lambda",
		Page: func(ctx context.Context, s *auth.Session, token string) ([]components.Row, string, error) {
			list, next, err := lambda.ListPage(ctx, s, token)
			rows := make([]components.Row, 0, len(list))
			for _, f := range list {
				mod := f.LastModified
				if len(mod) >= 10 {
					mod = mod[:10]
				}
				rows = append(rows, components.Row{Key: f.Name, Data: f, Tone: theme.StatusTone(f.State),
					Cells: []string{f.Name, dash(f.Runtime), fmt.Sprintf("%dMB", f.MemoryMB), fmt.Sprintf("%ds", f.TimeoutSec), dash(f.State), mod}})
			}
			return rows, next, err
		},
		Detail: func(r components.Row) string {
			f := r.Data.(lambda.Function)
			return lines([2]string{"Handler", f.Handler}, [2]string{"Code size", components.Bytes(f.CodeSize)})
		},
		Actions: []Action{
			{Key: "i", Label: "Invoke", Run: func(e *core.Env, r components.Row) tea.Cmd {
				return e.RunCommandID("lambda.invoke", map[string]string{"function_name": r.Key, "#form": "1"})
			}},
			{Key: "l", Label: "Logs", Run: func(e *core.Env, r components.Row) tea.Cmd {
				return e.Navigate("logs", "open:/aws/lambda/"+r.Key)
			}},
		},
		Open:  func(e *core.Env, r components.Row) (Viewer, tea.Cmd) { return newLambdaView(e, r.Key) },
		Empty: "No Lambda functions in this region.",
		Link:  func(region string, r components.Row) string { return console.Lambda(region, r.Key) },
	}
}

// ───────────────────────────── Load balancers ─────────────────────────────

func LoadBalancers() *Level {
	return &Level{
		Title: "Load Balancers", Noun: "load balancers",
		Columns: []col{{Title: "Name", Width: 20, Flex: true}, {Title: "Type", Width: 11}, {Title: "Scheme", Width: 15},
			{Title: "State", Width: 10}, {Title: "DNS", Width: 20, Flex: true}},
		CacheKind: "elb",
		Page: func(ctx context.Context, s *auth.Session, token string) ([]components.Row, string, error) {
			list, next, err := networking.LoadBalancersPage(ctx, s, token)
			rows := make([]components.Row, 0, len(list))
			for _, lb := range list {
				rows = append(rows, components.Row{Key: lb.ARN, Data: lb, Tone: theme.StatusTone(lb.State),
					Cells: []string{lb.Name, lb.Type, lb.Scheme, lb.State, lb.DNS}})
			}
			return rows, next, err
		},
		Child: func(r components.Row) *Level {
			lb := r.Data.(networking.LoadBalancer)
			return targetGroups(lb.Name, lb.ARN)
		},
		Actions: []Action{
			{Key: "m", Label: "Topology map", View: func(e *core.Env, r components.Row) (Viewer, tea.Cmd) {
				return newTopologyView(e, r.Data.(networking.LoadBalancer))
			}},
		},
		Empty: "No ALBs or NLBs in this region.",
		Link:  func(region string, r components.Row) string { return console.LoadBalancer(region, r.Key) },
	}
}

func targetGroups(lbName, lbARN string) *Level {
	return &Level{
		Title: lbName, Noun: "target groups",
		Columns: []col{{Title: "Target group", Width: 22, Flex: true}, {Title: "Protocol", Width: 8}, {Title: "Port", Width: 6},
			{Title: "Target type", Width: 11}, {Title: "Health check", Width: 16, Flex: true}},
		Page: Full(func(ctx context.Context, s *auth.Session) ([]components.Row, error) {
			list, err := networking.TargetGroups(ctx, s, lbARN)
			rows := make([]components.Row, 0, len(list))
			for _, tg := range list {
				rows = append(rows, components.Row{Key: tg.ARN, Data: tg,
					Cells: []string{tg.Name, tg.Protocol, fmt.Sprint(tg.Port), tg.TargetType, dash(tg.HealthPath)}})
			}
			return rows, err
		}),
		Child: func(r components.Row) *Level { tg := r.Data.(networking.TargetGroup); return targets(tg.Name, tg.ARN) },
		Actions: []Action{
			{Key: "F", Label: "Follow the error", View: func(e *core.Env, r components.Row) (Viewer, tea.Cmd) {
				tg := r.Data.(networking.TargetGroup)
				return newTraceView(e, "elb", traceStart{TargetARN: tg.ARN, TargetName: tg.Name})
			}},
		},
		Empty: "No target groups attached.",
		Link:  func(region string, r components.Row) string { return console.TargetGroup(region, r.Key) },
	}
}

func targets(tgName, tgARN string) *Level {
	return &Level{
		Title: tgName, Noun: "target health",
		Columns: []col{{Title: "Target", Width: 22}, {Title: "Port", Width: 6}, {Title: "State", Width: 12},
			{Title: "Reason", Width: 20}, {Title: "Description", Width: 20, Flex: true}},
		Page: Full(func(ctx context.Context, s *auth.Session) ([]components.Row, error) {
			list, err := networking.TargetHealth(ctx, s, tgARN)
			rows := make([]components.Row, 0, len(list))
			for _, t := range list {
				rows = append(rows, components.Row{Key: fmt.Sprintf("%s:%d", t.ID, t.Port), Data: t, Tone: theme.StatusTone(t.State),
					Cells: []string{t.ID, fmt.Sprint(t.Port), t.State, dash(t.Reason), dash(t.Description)}})
			}
			return rows, err
		}),
		Empty: "No registered targets.",
		Link:  func(region string, _ components.Row) string { return console.TargetGroup(region, tgARN) },
	}
}

// ───────────────────────────── Alarms ─────────────────────────────

func Alarms() *Level {
	return &Level{
		Title: "Metrics & Alarms", Noun: "CloudWatch alarms",
		Columns: []col{{Title: "Alarm", Width: 26, Flex: true}, {Title: "State", Width: 17}, {Title: "Metric", Width: 22},
			{Title: "Changed", Width: 10}},
		CacheKind: "alarms",
		Page: func(ctx context.Context, s *auth.Session, token string) ([]components.Row, string, error) {
			list, next, err := metrics.AlarmsPage(ctx, s, "", token)
			rows := make([]components.Row, 0, len(list))
			for _, a := range list {
				metric := a.Metric
				if a.Namespace != "" && a.Metric != "" {
					metric = a.Namespace + "/" + a.Metric
				}
				rows = append(rows, components.Row{Key: a.Name, Data: a, Tone: theme.StatusTone(a.State), Search: a.Reason,
					Cells: []string{a.Name, a.State, dash(metric), components.Ago(a.Updated)}})
			}
			return rows, next, err
		},
		Detail: func(r components.Row) string {
			return lines([2]string{"Reason", r.Data.(metrics.Alarm).Reason})
		},
		Open: func(e *core.Env, r components.Row) (Viewer, tea.Cmd) {
			return newAlarmView(e, r.Data.(metrics.Alarm))
		},
		Actions: []Action{
			{Key: "F", Label: "Follow the error", View: func(e *core.Env, r components.Row) (Viewer, tea.Cmd) {
				return newTraceView(e, "alarms", traceStart{Alarm: r.Key})
			}},
		},
		Empty: "No CloudWatch alarms in this region.",
		Link:  func(region string, r components.Row) string { return console.Alarm(region, r.Key) },
	}
}

// ───────────────────────────── Networking ─────────────────────────────

func VPCs() *Level {
	return &Level{
		Title: "Networking", Noun: "VPCs",
		Columns: []col{{Title: "VPC", Width: 22}, {Title: "Name", Width: 20, Flex: true}, {Title: "CIDR", Width: 18},
			{Title: "State", Width: 10}, {Title: "Default", Width: 7}},
		CacheKind: "network",
		Page: func(ctx context.Context, s *auth.Session, token string) ([]components.Row, string, error) {
			list, next, err := networking.VPCsPage(ctx, s, token)
			rows := make([]components.Row, 0, len(list))
			for _, v := range list {
				rows = append(rows, components.Row{Key: v.ID, Data: v, Tone: theme.StatusTone(v.State),
					Cells: []string{v.ID, dash(v.Name), v.CIDR, v.State, yesNo(v.Default)}})
			}
			return rows, next, err
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
		Link:  func(region string, r components.Row) string { return console.VPC(region, r.Key) },
	}
}
