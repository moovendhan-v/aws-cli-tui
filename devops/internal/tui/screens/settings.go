package screens

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"devops/internal/aws/profiles"
	"devops/internal/aws/ssm"
	"devops/internal/config"
	"devops/internal/tui/components"
	"devops/internal/tui/core"
	"devops/internal/tui/theme"
)

type Settings struct {
	tab    int // 0 preferences, 1 status
	cursor int
	offset int
	hit    map[int]int // rendered line → setting index (mouse)
}

func NewSettings() *Settings { return &Settings{} }

func (s *Settings) ID() string      { return "settings" }
func (s *Settings) Capturing() bool { return false }
func (s *Settings) Init(e *core.Env) tea.Cmd {
	return nil
}

func (s *Settings) Hints() []core.Hint {
	if s.tab == 0 {
		return []core.Hint{{Key: "↑↓", Label: "Select"}, {Key: "enter/←→", Label: "Change"}, {Key: "tab", Label: "Status"}}
	}
	return []core.Hint{{Key: "L", Label: "SSO login"}, {Key: "i", Label: "Verify identity"}, {Key: "↑↓", Label: "Scroll"}, {Key: "tab", Label: "Preferences"}}
}

func (s *Settings) Update(e *core.Env, msg tea.Msg) tea.Cmd {
	prefs := preferences()
	switch m := msg.(type) {
	case core.MouseMsg:
		switch {
		case m.WheelUp:
			s.move(-3, len(prefs))
		case m.WheelDown:
			s.move(3, len(prefs))
		case m.Click && m.Y == 1:
			s.tab = 0
			if m.X >= 16 {
				s.tab = 1
			}
			s.offset = 0
		case m.Click && s.tab == 0:
			if i, ok := s.hit[m.Y]; ok {
				if i == s.cursor {
					return s.change(e, prefs[i], 1)
				}
				s.cursor = i
			}
		}
	case tea.KeyMsg:
		switch m.String() {
		case "tab", "shift+tab", "]", "[":
			s.tab = 1 - s.tab
			s.offset = 0
			return nil
		case "i":
			return e.RunCommandID("identity.caller", nil)
		}
		if s.tab == 1 {
			switch m.String() {
			case "up", "k":
				s.offset = max(0, s.offset-1)
			case "down", "j":
				s.offset++
			}
			return nil
		}
		switch m.String() {
		case "up", "k":
			s.move(-1, len(prefs))
		case "down", "j":
			s.move(1, len(prefs))
		case "enter", " ", "right", "l":
			return s.change(e, prefs[s.cursor], 1)
		case "left", "h":
			return s.change(e, prefs[s.cursor], -1)
		}
	}
	return nil
}

func (s *Settings) move(d, n int) {
	if s.tab == 1 {
		s.offset = max(0, s.offset+d)
		return
	}
	s.cursor = max(0, min(n-1, s.cursor+d))
}

func (s *Settings) change(e *core.Env, p pref, dir int) tea.Cmd {
	cfg := e.Deps.Config
	p.change(cfg, dir)
	var cmds []tea.Cmd
	if p.apply != nil {
		cmds = append(cmds, p.apply(e))
	}
	if err := cfg.Save(); err != nil {
		return e.Toast("Could not save config: "+err.Error(), theme.ToneBad)
	}
	cmds = append(cmds, e.Toast(fmt.Sprintf("%s → %s  (saved to config.yaml)", p.label, p.get(cfg)), theme.ToneOK))
	return tea.Batch(cmds...)
}

func (s *Settings) View(e *core.Env, w, h int) string {
	tabs := []string{" Preferences ", " Status "}
	var bar []string
	for i, t := range tabs {
		if i == s.tab {
			bar = append(bar, theme.NavActive.Render(t))
		} else {
			bar = append(bar, theme.InfoBadge.Render(t))
		}
	}
	head := heading("Settings", "changes apply immediately", w) + "\n" + strings.Join(bar, " ") + "\n"
	if s.tab == 0 {
		return head + s.prefsView(e, w, h-2)
	}
	return head + s.statusView(e, w, h-2)
}

func (s *Settings) prefsView(e *core.Env, w, h int) string {
	cfg := e.Deps.Config
	prefs := preferences()
	var lines []string
	idxLine := map[int]int{}
	group := ""
	for i, p := range prefs {
		if p.group != group {
			group = p.group
			lines = append(lines, "", theme.Title.Render(strings.ToUpper(group)))
		}
		idxLine[i] = len(lines)
		label := components.Pad(p.label, 30)
		val := renderValue(p, cfg)
		line := label + val
		if i == s.cursor {
			lines = append(lines, theme.RowCursor.Render(components.Pad(" "+line, w)))
			if p.help != "" {
				lines = append(lines, theme.Dim.Render(components.Truncate("   "+p.help, w)))
			}
		} else {
			lines = append(lines, " "+theme.Body.Render(label)+val)
		}
	}
	// Keep the cursor visible.
	cl := idxLine[s.cursor]
	if cl < s.offset {
		s.offset = max(0, cl-2)
	}
	if cl >= s.offset+h-1 {
		s.offset = cl - h + 3
	}
	s.offset = max(0, min(s.offset, max(0, len(lines)-h)))
	s.hit = map[int]int{}
	for i, l := range idxLine {
		if y := l - s.offset; y >= 0 && y < h {
			s.hit[y+2] = i // +2: heading and tab bar
		}
	}
	return components.Fit(strings.Join(lines[s.offset:], "\n"), w, h)
}

func renderValue(p pref, cfg *config.Config) string {
	v := p.get(cfg)
	if p.toggle {
		if v == "on" {
			return theme.OK.Render("[●] on")
		}
		return theme.Dim.Render("[ ] off")
	}
	return theme.Dim.Render("‹ ") + theme.Strong.Render(v) + theme.Dim.Render(" ›")
}

// pref is one editable setting.
type pref struct {
	group, label, help string
	toggle             bool
	get                func(c *config.Config) string
	change             func(c *config.Config, dir int)
	apply              func(e *core.Env) tea.Cmd
}

func boolPref(group, label, help string, ptr func(c *config.Config) *bool, apply func(e *core.Env) tea.Cmd) pref {
	return pref{group: group, label: label, help: help, toggle: true, apply: apply,
		get: func(c *config.Config) string {
			if *ptr(c) {
				return "on"
			}
			return "off"
		},
		change: func(c *config.Config, _ int) { *ptr(c) = !*ptr(c) }}
}

func cycle(opts []string, cur string, dir int) string {
	for i, o := range opts {
		if o == cur {
			return opts[(i+dir+len(opts))%len(opts)]
		}
	}
	return opts[0]
}

func choicePref(group, label, help string, opts []string, ptr func(c *config.Config) *string, apply func(e *core.Env) tea.Cmd) pref {
	return pref{group: group, label: label, help: help, apply: apply,
		get:    func(c *config.Config) string { return *ptr(c) },
		change: func(c *config.Config, dir int) { *ptr(c) = cycle(opts, *ptr(c), dir) }}
}

func intPref(group, label, help string, opts []int, ptr func(c *config.Config) *int, show func(int) string, apply func(e *core.Env) tea.Cmd) pref {
	strs := make([]string, len(opts))
	for i, o := range opts {
		strs[i] = strconv.Itoa(o)
	}
	return pref{group: group, label: label, help: help, apply: apply,
		get: func(c *config.Config) string { return show(*ptr(c)) },
		change: func(c *config.Config, dir int) {
			n, _ := strconv.Atoi(cycle(strs, strconv.Itoa(*ptr(c)), dir))
			*ptr(c) = n
		}}
}

func durPref(group, label, help string, opts []time.Duration, ptr func(c *config.Config) *config.Duration) pref {
	strs := make([]string, len(opts))
	for i, o := range opts {
		strs[i] = o.String()
	}
	return pref{group: group, label: label, help: help,
		get: func(c *config.Config) string { return ptr(c).String() },
		change: func(c *config.Config, dir int) {
			d, _ := time.ParseDuration(cycle(strs, ptr(c).String(), dir))
			ptr(c).Duration = d
		}}
}

func plain(n int) string { return strconv.Itoa(n) }

func zeroOff(n int) string {
	if n == 0 {
		return "off"
	}
	return strconv.Itoa(n)
}

func preferences() []pref {
	limits := func(e *core.Env) tea.Cmd { e.Deps.ApplyLimits(); return nil }
	return []pref{
		choicePref("Appearance", "Density", "Spacing inside panels: compact, comfortable (default) or spacious.",
			[]string{"compact", "comfortable", "spacious"}, func(c *config.Config) *string { return &c.UI.Density }, nil),
		intPref("Appearance", "Panel padding", "Horizontal padding in panels; auto follows the density.",
			[]int{-1, 0, 1, 2, 3, 4, 5, 6}, func(c *config.Config) *int { return &c.UI.Padding },
			func(n int) string {
				if n < 0 {
					return "auto"
				}
				return strconv.Itoa(n)
			}, nil),
		choicePref("Appearance", "Sidebar spacing", "auto adds a blank line between sidebar items when the window is tall enough.",
			[]string{"auto", "spacious", "compact"}, func(c *config.Config) *string { return &c.UI.NavSpacing }, nil),
		intPref("Appearance", "Sidebar width", "Width of the left navigation.", []int{22, 24, 26, 28, 30, 32, 36, 40},
			func(c *config.Config) *int { return &c.UI.NavWidth }, plain, nil),
		boolPref("Appearance", "Mouse", "Click and scroll with the mouse. Hold Option/Shift to select text while it is on.",
			func(c *config.Config) *bool { return &c.UI.Mouse }, func(e *core.Env) tea.Cmd {
				if e.Deps.Config.UI.Mouse {
					return tea.EnableMouseCellMotion
				}
				return tea.DisableMouse
			}),
		boolPref("Appearance", "Wrap long lines", "Wrap output and logs instead of cutting them off (w toggles per view).",
			func(c *config.Config) *bool { return &c.UI.Wrap }, nil),
		boolPref("Appearance", "Sessions inside the layout", "Run SSM, ECS Exec and tunnels in the Terminals panel (off = full screen).",
			func(c *config.Config) *bool { return &c.UI.EmbeddedSessions }, nil),
		choicePref("Appearance", "Result view", "How command results are shown first (v cycles in the output panel).",
			[]string{"table", "cards", "yaml", "json"}, func(c *config.Config) *string { return &c.UI.OutputFormat }, nil),

		boolPref("Safety & cost", "Confirm changes", "Ask before mutating commands (production always asks).",
			func(c *config.Config) *bool { return &c.Execution.ConfirmMutations }, nil),
		boolPref("Safety & cost", "Confirm billed operations", "Ask before operations AWS bills per call or per data unit ($).",
			func(c *config.Config) *bool { return &c.Limits.ConfirmBilled }, nil),
		{group: "Safety & cost", label: "Requests / second / service", help: "Client-side rate limit per AWS service; protects quotas.",
			get: func(c *config.Config) string {
				if c.Limits.RequestsPerSecond == 0 {
					return "off"
				}
				return strconv.FormatFloat(c.Limits.RequestsPerSecond, 'f', -1, 64)
			},
			change: func(c *config.Config, dir int) {
				opts := []string{"0", "2", "5", "10", "20", "50"}
				v, _ := strconv.ParseFloat(cycle(opts, strconv.FormatFloat(c.Limits.RequestsPerSecond, 'f', -1, 64), dir), 64)
				c.Limits.RequestsPerSecond = v
			},
			apply: limits},
		intPref("Safety & cost", "Warn after API calls", "Show a warning once a session makes this many requests.",
			[]int{0, 500, 1000, 2000, 5000, 10000}, func(c *config.Config) *int { return &c.Limits.WarnAPICalls }, zeroOff, limits),
		intPref("Safety & cost", "Hard cap on API calls", "Refuse new requests after this many in a session.",
			[]int{0, 1000, 5000, 10000, 50000}, func(c *config.Config) *int { return &c.Limits.MaxAPICalls }, zeroOff, limits),
		intPref("Safety & cost", "S3 preview limit (MB)", "Previews larger than this ask before downloading.",
			[]int{5, 10, 20, 50, 100, 250}, func(c *config.Config) *int { return &c.Limits.MaxPreviewMB }, plain, nil),

		boolPref("Data & caching", "Cache AWS data", "Reuse recent results; r always refreshes.",
			func(c *config.Config) *bool { return &c.Cache.Enabled }, func(e *core.Env) tea.Cmd {
				e.Deps.Cache.SetEnabled(e.Deps.Config.Cache.Enabled)
				return nil
			}),
		durPref("Data & caching", "Default cache lifetime", "Per-type lifetimes are in config.yaml (cache.ttls).",
			[]time.Duration{30 * time.Second, time.Minute, 2 * time.Minute, 5 * time.Minute, 10 * time.Minute},
			func(c *config.Config) *config.Duration { return &c.Cache.TTL }),
		boolPref("Data & caching", "Overview target health", "Check load balancer target health on the overview (up to 40 groups).",
			func(c *config.Config) *bool { return &c.Overview.TargetHealth }, nil),
		choicePref("Logs", "Log line format", "readable turns JSON logs into LEVEL message key=value; j cycles in the viewer.",
			[]string{"readable", "json", "raw"}, func(c *config.Config) *string { return &c.Logs.Format }, nil),
		choicePref("Logs", "Tail mode", "poll uses FilterLogEvents; live uses Live Tail (billed per session minute).",
			[]string{"poll", "live"}, func(c *config.Config) *string { return &c.Logs.TailMode }, nil),
		durPref("Logs", "Poll interval", "How often polling tails ask for new events.",
			[]time.Duration{time.Second, 2 * time.Second, 3 * time.Second, 5 * time.Second, 10 * time.Second},
			func(c *config.Config) *config.Duration { return &c.Logs.PollInterval }),
		durPref("Logs", "Filtered history window", "Time window scanned per step when a filter pattern is set.",
			[]time.Duration{5 * time.Minute, 15 * time.Minute, time.Hour, 3 * time.Hour},
			func(c *config.Config) *config.Duration { return &c.Logs.HistoryWindow }),
		intPref("Logs", "Events per page", "Events loaded per history page.", []int{200, 500, 1000, 2000},
			func(c *config.Config) *int { return &c.Logs.PageEvents }, plain, nil),

		boolPref("History", "Record history", "Keep a redacted record of commands you ran.",
			func(c *config.Config) *bool { return &c.History.Enabled }, nil),
		boolPref("History", "Store command output", "Also keep (redacted) output in history.",
			func(c *config.Config) *bool { return &c.History.StoreCommandOutput }, nil),
		intPref("History", "Retention (days)", "History older than this is deleted at startup.",
			[]int{1, 3, 7, 14, 30, 90}, func(c *config.Config) *int { return &c.History.RetentionDays }, plain, nil),
	}
}

func (s *Settings) statusView(e *core.Env, w, h int) string {
	ctx := e.Ctx()
	cfg := e.Deps.Config
	var b strings.Builder
	section := func(t string) { b.WriteString("\n" + theme.Title.Render(t) + "\n") }

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

	section("API USAGE (THIS SESSION)")
	total, billed := e.Deps.Meter.Totals()
	lim := e.Deps.Config.Limits
	capTxt := "no hard cap"
	if lim.MaxAPICalls > 0 {
		capTxt = fmt.Sprintf("cap %d", lim.MaxAPICalls)
	}
	b.WriteString(theme.Body.Render(fmt.Sprintf("%d requests · %d billed · ≤%.0f req/s per service · %s", total, billed, lim.RequestsPerSecond, capTxt)) + "\n")
	for i, st := range e.Deps.Meter.Stats() {
		if i >= 8 {
			break
		}
		mark := ""
		if st.Billed {
			mark = theme.Warn.Render(" $")
		}
		b.WriteString(theme.Dim.Render(components.Pad(fmt.Sprintf("%5d", st.Calls), 7)) + theme.Body.Render(st.Service+" "+st.Operation) + mark + "\n")
	}
	entries, hits, misses := e.Deps.Cache.Stats()
	b.WriteString(theme.Dim.Render(fmt.Sprintf("cache: %d entries · %d hits · %d misses", entries, hits, misses)) + "\n")

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
	{"↑ / ↓", "Navigate resources and menus (or scroll the mouse wheel)"},
	{"Enter", "Open a resource or run the selected action (or double-click)"},
	{"/  Ctrl+K", "Search commands, screens and resources"},
	{"Ctrl+P", "Switch AWS profile (or click the PROFILE select, bottom-left)"},
	{"Ctrl+R", "Switch region (or click the REGION select, bottom-left)"},
	{"Ctrl+J", "Open command activity"},
	{"Tab", "Switch between navigation and workspace"},
	{"f", "Filter the current table"},
	{"r", "Refresh (bypasses the cache)"},
	{"o / y / Y", "Open in AWS console · copy ID · copy console link"},
	{"*", "Pin the selected resource to the overview"},
	{"W", "Watch: auto-refresh this list every 10s"},
	{"v / w", "Output panel: cycle table/cards/YAML/JSON · toggle wrap"},
	{"Ctrl+]", "Inside an embedded session: menu (detach, next, close)"},
	{"Esc", "Go back or close a panel"},
	{"L", "Run aws sso login for the active profile"},
	{"?", "Context help"},
	{"q", "Quit"},
}
