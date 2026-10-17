package app

// NavItem is an entry in the left navigation. Keywords let the search bar
// route intent ("tail logs") to a screen as well as to commands.
type NavItem struct {
	ID       string
	Label    string
	Keywords []string
}

var Navigation = []NavItem{
	{ID: "overview", Label: "Overview", Keywords: []string{"home", "dashboard", "summary"}},
	{ID: "ec2", Label: "EC2 Instances", Keywords: []string{"instances", "servers", "vm", "ssm", "session", "ssh"}},
	{ID: "ecs", Label: "ECS Clusters", Keywords: []string{"containers", "services", "tasks", "fargate", "exec"}},
	{ID: "rds", Label: "Databases", Keywords: []string{"rds", "db", "postgres", "mysql", "aurora"}},
	{ID: "lambda", Label: "Lambda", Keywords: []string{"functions", "serverless"}},
	{ID: "elb", Label: "Load Balancers", Keywords: []string{"alb", "nlb", "targets", "target groups", "unhealthy"}},
	{ID: "logs", Label: "CloudWatch Logs", Keywords: []string{"tail", "logs", "log groups", "stream"}},
	{ID: "alarms", Label: "Metrics & Alarms", Keywords: []string{"alarms", "alerts", "metrics", "cloudwatch"}},
	{ID: "network", Label: "Networking", Keywords: []string{"vpc", "subnets", "routes", "network"}},
	{ID: "commands", Label: "Command Library", Keywords: []string{"commands", "actions", "workflows", "playbooks"}},
	{ID: "activity", Label: "Running Commands", Keywords: []string{"jobs", "activity", "history", "running"}},
	{ID: "settings", Label: "Settings", Keywords: []string{"config", "preferences", "profile", "help"}},
}
