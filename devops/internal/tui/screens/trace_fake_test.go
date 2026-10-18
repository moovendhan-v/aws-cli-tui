package screens

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"devops/internal/aws/auth"
)

// TestTraceAgainstFake runs only when the local fake AWS server is up
// (DEVOPS_FAKE_AWS=http://127.0.0.1:8977).
func TestTraceAgainstFake(t *testing.T) {
	ep := os.Getenv("DEVOPS_FAKE_AWS")
	if ep == "" {
		t.Skip("set DEVOPS_FAKE_AWS to run")
	}
	if c, err := net.Dial("tcp", strings.TrimPrefix(ep, "http://")); err != nil {
		t.Skip("fake server not running")
	} else {
		c.Close()
	}
	dir := t.TempDir()
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(dir, "none"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "none"))
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIDEXAMPLE")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "x")
	t.Setenv("AWS_ENDPOINT_URL", ep)
	s := auth.NewSession(context.Background(), "", "us-east-1")
	hops := ecsHops(context.Background(), s, "prod-core", "web-api")
	for _, h := range hops {
		t.Logf("%-14s %-30s %s  %v", h.Kind, h.Title, h.Status, h.Lines)
	}
	if len(hops) < 3 || hops[0].Kind != "ECS service" || hops[1].Kind != "Stopped tasks" || hops[len(hops)-1].Kind != "Logs" {
		t.Fatalf("unexpected chain: %+v", hops)
	}
}
