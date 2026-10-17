package screens

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"devops/internal/aws/profiles"
	"devops/internal/aws/ssm"
	"devops/internal/tui/components"
	"devops/internal/tui/core"
	"devops/internal/tui/theme"
)

type Settings struct{ offset int }

func NewSettings() *Settings { return &Settings{} }

func (s *Settings) ID() string      { return "settings" }
func (s *Settings) Capturing() bool { return false }
func (s *Settings) Init(e *core.Env) tea.Cmd {
	return nil
}

func (s *Settings) Hints() []core.Hint {
	return []core.Hint{{Key: "L", Label: "SSO login"}, {Key: "i", Label: "Verify identity"}, {Key: "↑↓", Label: "Scroll"}}
}

func (s *Settings) Update(e *core.Env, msg tea.Msg) tea.Cmd {
	if m, ok := msg.(tea.KeyMsg); ok {
		switch m.String() {
		case "up", "k":
			s.offset = max(0, s.offset-1)
		case "down", "j":
			s.offset++
		case "i":
			return e.RunCommandID("identity.caller", nil)
		}
	}
	return nil
}

func (s *Settings) View(e *core.Env, w, h int) string {
	ctx := e.Ctx()
	cfg := e.Deps.Config
	var b strings.Builder
	section := func(t string) { b.WriteString("\n" + theme.Title.Render(t) + "\n") }

	b.WriteString(heading("Settings", "", w) + "\n")
	section("AWS CONTEXT")
	identity := ctx.ARN
	switch {
	case e.Identifying:
		identity = e.Spinner() + " verifying…"
	case e.IdentityErr != nil:
		identity = theme.Bad.Render("✗ " + e.IdentityErr.Error())
	}
	env := "non-production"
	if e.Prod() {
		env = theme.ProdBadge.Render("PRODUCTION")
	}
	b.WriteString(kv(w, [2]string{"Profile", ctx.ProfileLabel()}, [2]string{"Region", ctx.Region},
		[2]string{"Account", ctx.AccountID}, [2]string{"Environment", env}) + "\n")
	b.WriteString(theme.Dim.Render(components.Pad("Identity", 13)) + identity + "\n")
	if e.Session.LoadErr != nil {
		b.WriteString(errorView(e.Session.LoadErr, w) + "\n")
	}

	section("PROFILES")
	if len(e.Deps.Profiles) == 0 {
		b.WriteString(theme.Dim.Render("No profiles in "+profiles.ConfigPath()+" — using environment credentials.") + "\n")
	}
	for _, p := range e.Deps.Profiles {
		mark := "  "
		if p.Name == ctx.Profile {
			mark = theme.OK.Render("● ")
		}
		name := p.Name
		if e.Deps.Production.Is(p.Name, "") {
			name += " " + theme.Bad.Render("prod")
		}
		b.WriteString(mark + components.Pad(theme.Body.Render(name), 30) + theme.Dim.Render(p.Kind()+"  "+dash(p.Region)) + "\n")
	}

	section("TOOLS")
	plugin := theme.OK.Render("installed")
	if !ssm.PluginInstalled() {
		plugin = theme.Warn.Render("missing — install it for SSM sessions, ECS Exec and tunnels")
	}
	b.WriteString(theme.Dim.Render(components.Pad("SSM plugin", 13)) + plugin + "\n")

	section("PREFERENCES")
	b.WriteString(kv(w,
		[2]string{"Config file", cfg.Path()}, [2]string{"Database", e.Deps.DBPath},
		[2]string{"Concurrency", fmt.Sprint(cfg.Execution.MaxConcurrentJobs)}, [2]string{"Timeout", cfg.Execution.DefaultTimeout.String()},
		[2]string{"Confirm mut.", yesNo(cfg.Execution.ConfirmMutations)}, [2]string{"History", fmt.Sprintf("%s · %dd · output %s", yesNo(cfg.History.Enabled), cfg.History.RetentionDays, yesNo(cfg.History.StoreCommandOutput))},
		[2]string{"Fuzzy search", yesNo(cfg.Search.Fuzzy)}, [2]string{"Full-text", yesNo(e.Deps.Store != nil && e.Deps.Store.FTSEnabled())},
	) + "\n")
	b.WriteString(theme.Dim.Render(components.Pad("Prod match", 13)) + theme.Body.Render(strings.Join(cfg.Safety.ProductionPatterns, ", ")) + "\n")

	section("COMMAND LIBRARY")
	cats := e.Deps.Registry.Categories()
	names := make([]string, 0, len(cats))
	for c, n := range cats {
		names = append(names, fmt.Sprintf("%s %d", c, n))
	}
	sort.Strings(names)
	b.WriteString(theme.Body.Render(fmt.Sprintf("%d commands", len(e.Deps.Registry.All()))) + theme.Dim.Render(" · "+strings.Join(names, " · ")) + "\n")
	b.WriteString(theme.Dim.Render("Add your own YAML in "+e.Deps.Home+"/commands") + "\n")
	for _, r := range e.Deps.Registry.Rejected {
		b.WriteString(theme.Bad.Render("✗ "+r.Error()) + "\n")
	}
	for _, wmsg := range e.Deps.Warnings {
		b.WriteString(theme.Warn.Render("! "+wmsg) + "\n")
	}

	section("KEYBOARD")
	for _, k := range Shortcuts {
		b.WriteString(theme.Key.Render(components.Pad(k[0], 13)) + theme.Dim.Render(k[1]) + "\n")
	}

	all := strings.Split(b.String(), "\n")
	s.offset = min(s.offset, max(0, len(all)-h))
	return components.Fit(strings.Join(all[s.offset:], "\n"), w, h)
}

// Shortcuts is the global keymap shown in Settings and Help.
var Shortcuts = [][2]string{
	{"↑ / ↓", "Navigate resources and menus"},
	{"Enter", "Open a resource or run the selected action"},
	{"/  Ctrl+K", "Search commands, screens and resources"},
	{"Ctrl+P", "Switch AWS profile"},
	{"Ctrl+R", "Switch region"},
	{"Ctrl+J", "Open command activity"},
	{"Tab", "Switch between navigation and workspace"},
	{"f", "Filter the current table"},
	{"Esc", "Go back or close a panel"},
	{"L", "Run aws sso login for the active profile"},
	{"?", "Context help"},
	{"q", "Quit"},
}
