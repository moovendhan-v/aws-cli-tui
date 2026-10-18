package screens

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/moovendhan-v/aws-cli-tui/devops/internal/app"
	"github.com/moovendhan-v/aws-cli-tui/devops/internal/aws/auth"
	"github.com/moovendhan-v/aws-cli-tui/devops/internal/aws/metrics"
	"github.com/moovendhan-v/aws-cli-tui/devops/internal/aws/networking"
	"github.com/moovendhan-v/aws-cli-tui/devops/internal/tui/core"
)

func testEnv(t *testing.T) *core.Env {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("DEVOPS_HOME", filepath.Join(dir, "home"))
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(dir, "none"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "none"))
	d, err := app.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Close)
	return core.NewEnv(d, auth.NewSession(context.Background(), "", "us-east-1"))
}

func TestTopologyView(t *testing.T) {
	e := testEnv(t)
	v := &topologyView{lb: networking.LoadBalancer{Name: "alb-prod-api", Type: "application", Scheme: "internet-facing", State: "active", DNS: "alb.example"}}
	v.data = &topology{
		Listeners: []networking.Listener{{Protocol: "HTTPS", Port: 443, Action: "forward", TargetGroups: []string{"tg1"}}},
		Groups: []tgHealth{
			{TG: networking.TargetGroup{Name: "tg-web", ARN: "tg1", Protocol: "HTTP", Port: 8080}, Targets: []networking.Target{
				{ID: "10.0.1.12", Port: 8080, State: "healthy"}, {ID: "10.0.1.13", Port: 8080, State: "unhealthy", Reason: "Target.ResponseCodeMismatch"}}},
			{TG: networking.TargetGroup{Name: "tg-admin", ARN: "tg2", Protocol: "HTTP", Port: 9000}},
		},
	}
	out := ansi.Strip(v.View(e, 100, 20))
	if testing.Verbose() {
		t.Log("\n" + out)
	}
	for _, want := range []string{"HTTPS:443", "tg-web", "1/2 healthy", "Target.ResponseCodeMismatch", "listener rules", "tg-admin"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestAlarmView(t *testing.T) {
	e := testEnv(t)
	thr := 80.0
	v := &alarmView{since: 6 * time.Hour, chartReady: true, alarm: metrics.Alarm{Name: "HighCPU", State: "ALARM", Namespace: "AWS/EC2",
		Metric: "CPUUtilization", Threshold: &thr, Comparison: "GreaterThanThreshold", Statistic: "Average", Period: 300,
		Reason: "Threshold Crossed: 1 datapoint [93.1] was greater than the threshold (80.0).", Dimensions: map[string]string{"InstanceId": "i-1"}}}
	now := time.Now()
	for i := range 40 {
		v.points = append(v.points, metrics.Point{Time: now.Add(time.Duration(i-40) * 5 * time.Minute), Average: 40 + float64(i)*1.4})
	}
	v.history = []metrics.StateChange{{At: now.Add(-time.Hour), Summary: "Alarm updated from OK to ALARM"}}
	out := ansi.Strip(v.View(e, 100, 30))
	if testing.Verbose() {
		t.Log("\n" + out)
	}
	for _, want := range []string{"Average > 80", "threshold", "OK to ALARM", "InstanceId=i-1"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q", want)
		}
	}
}
