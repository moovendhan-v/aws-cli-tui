package screens

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/aws/lambda"
	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/aws/trail"
	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/tui/theme"
)

func TestTraceChangesLambdaViews(t *testing.T) {
	e := testEnv(t)
	tv := &traceView{start: traceStart{Alarm: "web-5xx-high"}, hops: []traceHop{
		{Kind: "Alarm", Title: "web-5xx-high", Status: "ALARM", Tone: theme.ToneBad, Lines: []string{"AWS/ApplicationELB/HTTPCode_Target_5XX_Count Sum > 10"}, Screen: "alarms"},
		{Kind: "Target group", Title: "tg-web", Status: "1/3 healthy", Tone: theme.ToneBad, Lines: []string{"Target.ResponseCodeMismatch ×2"}, Screen: "elb"},
		{Kind: "ECS service", Title: "prod-core/web-api", Status: "4/6 running · rollout FAILED", Tone: theme.ToneBad, Screen: "ecs"},
		{Kind: "Logs", Title: "/ecs/web-api", Status: "37 error lines in 30 min", Tone: theme.ToneBad, Lines: []string{"10:02:11  ERROR payment provider timeout"}, Screen: "logs"},
	}}
	out := ansi.Strip(tv.View(e, 100, 24))
	t.Log("\n" + out)
	if !strings.Contains(out, "prod-core/web-api") || !strings.Contains(out, "▼") {
		t.Error("trace chain not rendered")
	}

	c := NewChanges()
	now := time.Now()
	c.start, c.end, c.rng = now.Add(-time.Hour), now, time.Hour
	c.all = []trail.Change{
		{ID: "1", Kind: "api", Time: now.Add(-50 * time.Minute), Who: "deploy-role/github-actions", Action: "UpdateService", Service: "ecs", Resources: "web-api"},
		{ID: "2", Kind: "api", Time: now.Add(-48 * time.Minute), Who: "alice", Action: "AuthorizeSecurityGroupIngress", Service: "ec2", Resources: "sg-0abc", Error: "AccessDenied", ErrorMessage: "not authorized"},
		{Kind: "alarm", Time: now.Add(-45 * time.Minute), Who: "CloudWatch", Action: "→ ALARM", AlarmState: "ALARM", Service: "alarm", Resources: "web-5xx-high"},
		{ID: "3", Kind: "api", Time: now.Add(-40 * time.Minute), Who: "ssm", Action: "UpdateInstanceInformation", Service: "ssm"},
	}
	c.rebuild()
	out = ansi.Strip(c.View(e, 110, 20))
	t.Log("\n" + out)
	if !strings.Contains(out, "UpdateService") || strings.Contains(out, "UpdateInstanceInformation") || !strings.Contains(out, "▲") {
		t.Error("changes view: expected UpdateService, alarm marker, and noise hidden")
	}

	lv := &lambdaView{name: "resize-images", tabHit: map[int][2]int{}, d: &lambda.Detail{
		Function: lambda.Function{Name: "resize-images", Runtime: "python3.12", State: "Active", MemoryMB: 512, TimeoutSec: 30, Handler: "app.handler"},
		Role:     "resize-role", LogGroup: "/aws/lambda/resize-images", EnvNames: []string{"BUCKET", "DB_PASSWORD"}}}
	out = ansi.Strip(lv.View(e, 100, 26))
	t.Log("\n" + out)
	if !strings.Contains(out, "BUCKET, DB_PASSWORD") || !strings.Contains(out, "Invoke") {
		t.Error("lambda overview incomplete")
	}
}
