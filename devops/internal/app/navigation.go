package app

// NavItem is an entry in the left navigation. Keywords let the search bar
// route intent ("tail logs") to a screen as well as to commands.
type NavItem struct {
	ID       string
	Icon     string
	Label    string
	Keywords []string
}

var Navigation = []NavItem{
	{ID: "overview", Icon: "◎", Label: "Overview", Keywords: []string{"home", "dashboard", "summary"}},
	{ID: "ec2", Icon: "▣", Label: "EC2 Instances", Keywords: []string{"instances", "servers", "vm", "ssm", "session", "ssh"}},
	{ID: "ecs", Icon: "◈", Label: "ECS Clusters", Keywords: []string{"containers", "services", "tasks", "fargate", "exec"}},
	{ID: "rds", Icon: "◍", Label: "Databases", Keywords: []string{"rds", "db", "postgres", "mysql", "aurora"}},
	{ID: "lambda", Icon: "λ", Label: "Lambda", Keywords: []string{"functions", "serverless"}},
	{ID: "elb", Icon: "⇆", Label: "Load Balancers", Keywords: []string{"alb", "nlb", "targets", "target groups", "unhealthy"}},
	{ID: "logs", Icon: "≡", Label: "CloudWatch Logs", Keywords: []string{"tail", "logs", "log groups", "stream"}},
	{ID: "alarms", Icon: "∿", Label: "Metrics & Alarms", Keywords: []string{"alarms", "alerts", "metrics", "cloudwatch"}},
	{ID: "network", Icon: "⌗", Label: "Networking", Keywords: []string{"vpc", "subnets", "routes", "network"}},
	{ID: "s3", Icon: "◫", Label: "S3 Buckets", Keywords: []string{"s3", "bucket", "objects", "files", "images", "preview"}},
	{ID: "pipelines", Icon: "≫", Label: "Pipelines", Keywords: []string{"codepipeline", "deploy", "release", "stages", "approval"}},
	{ID: "builds", Icon: "◧", Label: "CodeBuild", Keywords: []string{"codebuild", "build", "ci", "phases"}},
	{ID: "commands", Icon: "⌘", Label: "Command Library", Keywords: []string{"commands", "actions", "workflows", "playbooks"}},
	{ID: "activity", Icon: "↻", Label: "Running Commands", Keywords: []string{"jobs", "activity", "history", "running"}},
	{ID: "terminals", Icon: "»", Label: "Terminals", Keywords: []string{"ssm", "shell", "session", "exec", "tunnel", "terminal"}},
	{ID: "settings", Icon: "✱", Label: "Settings", Keywords: []string{"config", "preferences", "profile", "help"}},
}
