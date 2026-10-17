// Package tests holds end-to-end checks that need no AWS access.
package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"devops/internal/app"
	"devops/internal/tui"
)

// newDeps builds dependencies against an isolated home and a fake AWS config.
func newDeps(t *testing.T) *app.Deps {
	t.Helper()
	dir := t.TempDir()
	cfg := filepath.Join(dir, "aws-config")
	os.WriteFile(cfg, []byte("[profile dev-sso]\nregion = ap-southeast-2\nsso_session = corp\n\n[sso-session corp]\nsso_start_url = https://example.awsapps.com/start\nsso_region = ap-southeast-2\n\n[profile prod-admin]\nregion = us-east-1\n"), 0o600)
	t.Setenv("DEVOPS_HOME", filepath.Join(dir, "home"))
	t.Setenv("AWS_CONFIG_FILE", cfg)
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "none"))
	t.Setenv("AWS_PROFILE", "dev-sso")
	d, err := app.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(d.Close)
	return d
}

func render(m tea.Model, msgs ...tea.Msg) string {
	for _, msg := range msgs {
		m, _ = m.Update(msg)
	}
	return ansi.Strip(m.View())
}

func TestShellLayout(t *testing.T) {
	m := tui.New(newDeps(t))
	out := render(m, tea.WindowSizeMsg{Width: 120, Height: 34})
	if testing.Verbose() {
		t.Log("\n" + out)
	}
	lines := strings.Split(out, "\n")
	if len(lines) != 34 {
		t.Errorf("rendered %d lines, want 34", len(lines))
	}
	for i, l := range lines {
		if w := ansi.StringWidth(l); w > 120 {
			t.Errorf("line %d is %d cells wide", i, w)
		}
	}
	for _, want := range []string{"DEVOPS", "Profile: dev-sso", "Region: ap-southeast-2", "Search: >",
		"Overview", "EC2 Instances", "Running Commands", "AWS INFRASTRUCTURE SUMMARY", "RECENT ACTIVITY", "Ctrl+P Profile"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q", want)
		}
	}
}

func TestSearchPalette(t *testing.T) {
	m := tui.New(newDeps(t))
	msgs := []tea.Msg{tea.WindowSizeMsg{Width: 120, Height: 34}, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("/")}}
	for _, r := range "ecs errors" {
		msgs = append(msgs, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
	}
	out := render(m, msgs...)
	if testing.Verbose() {
		t.Log("\n" + out)
	}
	for _, want := range []string{"ACTIONS", "Inspect Stopped Tasks", "GO TO", "ECS Clusters"} {
		if !strings.Contains(out, want) {
			t.Errorf("palette missing %q", want)
		}
	}
}

func TestBrokenProfileShowsRecoveryPath(t *testing.T) {
	d := newDeps(t)
	t.Setenv("AWS_PROFILE", "broken")
	os.WriteFile(os.Getenv("AWS_CONFIG_FILE"), []byte("[profile broken]\nregion = eu-west-1\nsso_session = missing\n"), 0o600)
	d.Profiles[0].Name, d.Profiles[0].Region = "broken", "eu-west-1"
	out := render(tui.New(d), tea.WindowSizeMsg{Width: 120, Height: 34})
	for _, want := range []string{"Region: eu-west-1", "configuration error", "Ctrl+P"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
}

func TestProductionBadge(t *testing.T) {
	d := newDeps(t)
	t.Setenv("AWS_PROFILE", "prod-admin")
	m := tui.New(d)
	out := render(m, tea.WindowSizeMsg{Width: 120, Height: 34})
	if !strings.Contains(out, "PROD") {
		t.Error("production badge not shown for prod-admin")
	}
}
