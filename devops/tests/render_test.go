// Package tests holds end-to-end checks that need no AWS access.
package tests

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"
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
	if v := os.Getenv("DEVOPS_TEST_SPACING"); v != "" {
		d.Config.UI.NavSpacing = v
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

func TestTallSidebarSpacing(t *testing.T) {
	t.Setenv("DEVOPS_TEST_SPACING", "auto")
	m := tui.New(newDeps(t))
	out := render(m, tea.WindowSizeMsg{Width: 140, Height: 60})
	if testing.Verbose() {
		t.Log("\n" + out)
	}
	lines := strings.Split(out, "\n")
	ov, ec := -1, -1
	for i, l := range lines {
		if strings.Contains(l, "Overview") && ov < 0 {
			ov = i
		}
		if strings.Contains(l, "What Changed?") && ec < 0 {
			ec = i
		}
	}
	if ec-ov != 2 {
		t.Errorf("expected a blank line between sidebar items on a tall window (Overview line %d, EC2 line %d)", ov, ec)
	}
}

func TestClickingSidebarPaddingSelectsItem(t *testing.T) {
	t.Setenv("DEVOPS_TEST_SPACING", "auto")
	m := tui.New(newDeps(t))
	out := render(m, tea.WindowSizeMsg{Width: 140, Height: 60})
	line := -1
	for i, l := range strings.Split(out, "\n") {
		if strings.Contains(l, "Settings") {
			line = i
		}
	}
	if line < 0 {
		t.Fatal("Settings not in sidebar")
	}
	// Click the blank padding line under "Settings", near the right edge of the sidebar.
	click := tea.MouseMsg{X: 25, Y: line + 1, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft}
	out = render(m, click)
	if !strings.Contains(out, "SETTINGS") {
		t.Fatalf("clicking the padding under Settings did not open it:\n%s", out)
	}
}

func TestNarrowSidebarNamesFit(t *testing.T) {
	d := newDeps(t)
	d.Config.UI.NavWidth = 26
	out := render(tui.New(d), tea.WindowSizeMsg{Width: 140, Height: 60})
	for _, name := range []string{"CloudWatch Logs", "Metrics & Alarms", "Running Commands", "Command Library"} {
		if !strings.Contains(out, name) {
			t.Errorf("%q is cut off at sidebar width 26", name)
		}
	}
}

func TestSelectedSidebarItemIsCentredBlock(t *testing.T) {
	t.Setenv("DEVOPS_TEST_SPACING", "auto")
	lipgloss.SetColorProfile(termenv.TrueColor) // tests have no TTY; force colour output
	defer lipgloss.SetColorProfile(termenv.Ascii)
	m := tui.New(newDeps(t))
	m.Update(tea.WindowSizeMsg{Width: 140, Height: 60})
	lines := strings.Split(m.View(), "\n")
	for i, l := range lines {
		if strings.Contains(ansi.Strip(l), "Overview") {
			sidebar := func(s string) string { return ansi.Cut(s, 0, 31) }
			bg := func(s string) bool { return strings.Contains(sidebar(s), "[48;2;") || strings.Contains(sidebar(s), ";48;2;") }
			if !bg(lines[i-1]) || !bg(lines[i]) || !bg(lines[i+1]) {
				t.Fatalf("selected item should be a 3-line highlighted block:\n%q\n%q\n%q", sidebar(lines[i-1]), sidebar(lines[i]), sidebar(lines[i+1]))
			}
			if bg(lines[i+3]) {
				t.Fatalf("the next item should not be highlighted: %q / %q", sidebar(lines[i+2]), sidebar(lines[i+3]))
			}
			return
		}
	}
	t.Fatal("Overview not found")
}

func TestFooterWrapsInsteadOfCutting(t *testing.T) {
	m := tui.New(newDeps(t))
	render(m, tea.WindowSizeMsg{Width: 90, Height: 40})
	out := render(m, tea.KeyMsg{Type: tea.KeyTab}) // focus the workspace so screen hints are added
	lines := strings.Split(out, "\n")
	footer := strings.Join(lines[len(lines)-3:], "\n")
	if strings.Contains(footer, "…") {
		t.Fatalf("footer was truncated:\n%s", footer)
	}
	for _, want := range []string{"Help", "Refresh"} {
		if !strings.Contains(out, want) {
			t.Errorf("footer missing %q", want)
		}
	}
	if len(lines) != 40 {
		t.Errorf("rendered %d lines, want 40", len(lines))
	}
}
