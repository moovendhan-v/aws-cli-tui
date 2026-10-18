package tests

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/moovendhan-v/aws-cli-tui/devops/internal/aws/ecs"
	"github.com/moovendhan-v/aws-cli-tui/devops/internal/aws/metrics"
	"github.com/moovendhan-v/aws-cli-tui/devops/internal/tui/components"
)

func TestResultViews(t *testing.T) {
	clusters := []ecs.Cluster{
		{Name: "prod-core", ARN: "arn:aws:ecs:us-east-1:111122223333:cluster/prod-core", Status: "ACTIVE", Running: 12, Services: 4},
		{Name: "batch", ARN: "arn:aws:ecs:us-east-1:111122223333:cluster/batch", Status: "ACTIVE", Running: 0, Services: 1},
	}
	table := ansi.Strip(strings.Join(components.RenderResult(clusters, "table", 100), "\n"))
	if testing.Verbose() {
		t.Log("\n" + table)
	}
	for _, want := range []string{"NAME", "STATUS", "prod-core", "batch", "2 rows"} {
		if !strings.Contains(table, want) {
			t.Errorf("table missing %q", want)
		}
	}
	if strings.Contains(table, "{") {
		t.Errorf("table should not contain JSON:\n%s", table)
	}
	cards := ansi.Strip(strings.Join(components.RenderResult(clusters[0], "cards", 80), "\n"))
	if !strings.Contains(cards, "Running") || !strings.Contains(cards, "12") {
		t.Errorf("cards missing fields:\n%s", cards)
	}
	yaml := strings.Join(components.RenderResult(clusters, "yaml", 80), "\n")
	if !strings.Contains(yaml, "- ARN:") && !strings.Contains(yaml, "Name: prod-core") {
		t.Errorf("yaml unexpected:\n%s", yaml)
	}
	lines := components.RenderResult([]string{"a", "b"}, "table", 80)
	if len(lines) != 2 {
		t.Errorf("string results should pass through, got %v", lines)
	}
}

func TestMetricSeriesRendersChart(t *testing.T) {
	now := time.Now()
	s := metrics.Series{Namespace: "AWS/EC2", Metric: "CPUUtilization", Dimensions: map[string]string{"InstanceId": "i-1"},
		Since: 3 * time.Hour, Period: 5 * time.Minute}
	for i := range 36 {
		v := 20 + 30*math.Sin(float64(i)/5)
		s.Points = append(s.Points, metrics.Point{Time: now.Add(time.Duration(i-36) * 5 * time.Minute), Average: v, Maximum: v + 5, Unit: "Percent"})
	}
	out := ansi.Strip(strings.Join(components.RenderResult(s, "table", 90), "\n"))
	if testing.Verbose() {
		t.Log("\n" + out)
	}
	for _, want := range []string{"AWS/EC2 / CPUUtilization", "now ", "peak", "current"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q", want)
		}
	}
	braille := false
	for _, r := range out {
		if r > 0x2800 && r <= 0x28FF {
			braille = true
		}
	}
	if !braille {
		t.Error("expected a braille line chart")
	}
}
