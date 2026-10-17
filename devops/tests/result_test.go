package tests

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"devops/internal/aws/ecs"
	"devops/internal/tui/components"
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
