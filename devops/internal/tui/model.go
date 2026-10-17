// Package tui is the application shell: header, search bar, navigation,
// workspace, footer, modals and global shortcuts.
package tui

import (
	"context"
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"devops/internal/app"
	"devops/internal/aws/auth"
	"devops/internal/aws/profiles"
	"devops/internal/executor"
	"devops/internal/safety"
	"devops/internal/tui/core"
	"devops/internal/tui/screens"
	"devops/internal/tui/theme"
)

type focusArea int

const (
	focusNav focusArea = iota
	focusWorkspace
	focusSearch
)

type identity struct{ account, arn string }

type Model struct {
	env     *core.Env
	screens map[string]core.Screen
	inited  map[string]bool
	navIdx  int
	focus   focusArea

	search  textinput.Model
	palette palette
	modal   core.Modal

	width, height int
	toast         string
	toastTone     theme.Tone
	toastUntil    time.Time
	quitArmed     time.Time
}

func New(d *app.Deps) *Model {
	cfg := d.Config
	profile := profiles.Initial(d.Profiles, cfg.UI.DefaultProfile)
	sess := newSession(d, profile, cfg.UI.DefaultRegion)

	in := textinput.New()
	in.Prompt = ""
	in.Placeholder = "search actions, screens and resources…  (e.g. ecs errors, tail logs, i-0abc)"
	in.PlaceholderStyle = theme.Dim
	in.TextStyle = theme.Body
	in.Cursor.Style = theme.Prompt

	m := &Model{
		env:    core.NewEnv(d, sess),
		inited: map[string]bool{},
		search: in,
		focus:  focusNav,
		screens: map[string]core.Screen{
			"overview": screens.NewOverview(),
			"ec2":      screens.NewEC2(),
			"ecs":      screens.NewResource("ecs", screens.ECSClusters),
			"rds":      screens.NewResource("rds", screens.RDSInstances),
			"lambda":   screens.NewResource("lambda", screens.LambdaFunctions),
			"elb":      screens.NewResource("elb", screens.LoadBalancers),
			"logs":     screens.NewLogs(),
			"alarms":   screens.NewResource("alarms", screens.Alarms),
			"network":  screens.NewResource("network", screens.VPCs),
			"commands": screens.NewLibrary(),
			"activity": screens.NewActivity(),
			"settings": screens.NewSettings(),
		},
	}
	for _, w := range d.Warnings {
		m.setToast(w, theme.ToneWarn)
	}
	return m
}

// newSession loads a session; if the shared config can't be loaded it still
// shows the profile's configured region so the header stays meaningful.
func newSession(d *app.Deps, profile, region string) *auth.Session {
	s := auth.NewSession(context.Background(), profile, region)
	if s.Ctx.Region == "" {
		for _, p := range d.Profiles {
			if p.Name == profile {
				s.Ctx.Region = p.Region
			}
		}
	}
	return s
}

func tick() tea.Cmd {
	return tea.Tick(250*time.Millisecond, func(t time.Time) tea.Msg { return core.TickMsg{Time: t} })
}

func (m *Model) Init() tea.Cmd {
	return tea.Batch(tick(), m.verifyIdentity(), m.activate())
}

func (m *Model) current() core.Screen { return m.screens[app.Navigation[m.navIdx].ID] }

// activate initialises the selected screen the first time it is shown in
// the current context.
func (m *Model) activate() tea.Cmd {
	s := m.current()
	if m.inited[s.ID()] {
		return nil
	}
	m.inited[s.ID()] = true
	return s.Init(m.env)
}

func (m *Model) verifyIdentity() tea.Cmd {
	e := m.env
	if e.Session.Ready() != nil {
		e.IdentityErr = e.Session.LoadErr
		return nil
	}
	e.Identifying, e.IdentityErr = true, nil
	s := e.Session
	return e.Run("", "identity", executor.Spec{Name: "Verify identity (sts:GetCallerIdentity)", Timeout: 20 * time.Second},
		func(ctx context.Context, j *executor.Job) (any, error) {
			acct, arn, err := s.Identity(ctx)
			if err != nil {
				return nil, err
			}
			j.Printf("%s", arn)
			return identity{acct, arn}, nil
		})
}

// switchContext rebuilds the session for a new profile/region. Running jobs
// keep the context they were started with.
func (m *Model) switchContext(profile, region string) tea.Cmd {
	e := m.env
	e.Session = newSession(e.Deps, profile, region)
	e.Gen++
	e.ClearIndex()
	m.inited = map[string]bool{}
	msg := fmt.Sprintf("Switched to %s · %s", e.Session.Ctx.ProfileLabel(), e.Session.Ctx.Region)
	tone := theme.ToneOK
	if e.Prod() {
		msg, tone = "⚠ PRODUCTION · "+msg, theme.ToneBad
	}
	m.setToast(msg, tone)
	return tea.Batch(m.verifyIdentity(), m.activate())
}

func (m *Model) setToast(text string, tone theme.Tone) {
	m.toast, m.toastTone, m.toastUntil = text, tone, time.Now().Add(5*time.Second)
}

func (m *Model) navigateTo(id string) tea.Cmd {
	for i, n := range app.Navigation {
		if n.ID == id {
			m.navIdx = i
		}
	}
	return m.activate()
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	e := m.env
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil

	case core.TickMsg:
		e.Tick++
		cmds := []tea.Cmd{tick()}
		if m.modal != nil {
			_, c := m.modal.Update(e, msg)
			cmds = append(cmds, c)
		}
		cmds = append(cmds, m.current().Update(e, msg))
		return m, tea.Batch(cmds...)

	case core.ResultMsg:
		return m, m.handleResult(msg)

	case core.NavigateMsg:
		m.focus = focusWorkspace
		m.search.Blur()
		cmd := m.navigateTo(msg.Screen)
		if f, ok := m.current().(core.Focuser); ok && msg.Query != "" {
			cmd = tea.Batch(cmd, f.Focus(e, msg.Query))
		}
		return m, cmd

	case core.ToastMsg:
		m.setToast(msg.Text, msg.Tone)
		return m, nil

	case core.OpenModalMsg:
		m.modal = msg.Modal
		m.search.Blur()
		if m.focus == focusSearch {
			m.focus = focusWorkspace
		}
		return m, nil

	case core.SwitchContextMsg:
		return m, m.switchContext(msg.Profile, msg.Region)

	case core.InteractiveDoneMsg:
		v := msg.Job.View()
		if msg.Err != nil {
			m.setToast(fmt.Sprintf("%s ended: %s", v.Name, safety.Describe(msg.Err)), theme.ToneBad)
		} else {
			m.setToast(fmt.Sprintf("%s ended after %s", v.Name, v.Elapsed().Round(time.Second)), theme.ToneOK)
		}
		// SSO login refreshes credentials, so re-verify.
		if v.CommandID == "identity.sso.login" {
			return m, tea.Batch(m.verifyIdentity(), m.reinitCurrent())
		}
		return m, nil

	case tea.KeyMsg:
		return m, m.handleKey(msg)
	}
	return m, nil
}

func (m *Model) reinitCurrent() tea.Cmd {
	m.inited = map[string]bool{}
	return m.activate()
}

func (m *Model) handleResult(r core.ResultMsg) tea.Cmd {
	e := m.env
	if r.Screen == "" {
		switch r.Key {
		case "identity":
			if r.Gen != e.Gen {
				return nil
			}
			e.Identifying = false
			e.IdentityErr = r.Err
			if id, ok := r.Value.(identity); ok {
				e.Session = e.Session.WithIdentity(id.account, id.arn)
				if e.Prod() {
					m.setToast("⚠ PRODUCTION account "+id.account+" · mutations require typed confirmation", theme.ToneBad)
				}
			}
		case "command":
			if out, ok := m.modal.(*core.Output); ok && out.Title() == fmt.Sprintf("#%d %s", r.Job.ID, r.Job.Name) {
				return nil
			}
			v := r.Job.View()
			if v.Status == executor.Completed {
				m.setToast(fmt.Sprintf("✓ %s completed — Ctrl+J to view", v.Name), theme.ToneOK)
			} else {
				m.setToast(fmt.Sprintf("✗ %s %s — Ctrl+J to view", v.Name, v.Status), theme.ToneBad)
			}
		}
		return nil
	}
	if r.Gen != e.Gen {
		return nil // result from a previous profile/region
	}
	if s, ok := m.screens[r.Screen]; ok {
		return s.Update(e, r)
	}
	return nil
}

func (m *Model) handleKey(k tea.KeyMsg) tea.Cmd {
	e := m.env
	key := k.String()
	if key == "ctrl+c" {
		e.Deps.Exec.CancelAll()
		return tea.Quit
	}

	if m.modal != nil {
		closed, cmd := m.modal.Update(e, k)
		if closed {
			m.modal = nil
		}
		return cmd
	}

	if m.focus == focusSearch {
		return m.searchKey(k)
	}

	scr := m.current()
	capturing := m.focus == focusWorkspace && scr.Capturing()

	// Control-key shortcuts work everywhere, including inside filters.
	switch key {
	case "ctrl+k":
		return m.openSearch()
	case "ctrl+p":
		return m.profilePicker()
	case "ctrl+r":
		return m.regionPicker()
	case "ctrl+j":
		m.focus = focusWorkspace
		return m.navigateTo("activity")
	}
	if capturing {
		return scr.Update(e, k)
	}

	switch key {
	case "/":
		return m.openSearch()
	case "?":
		return e.Open(core.NewText("Help · "+app.Navigation[m.navIdx].Label, helpText(scr)))
	case "tab", "shift+tab":
		if m.focus == focusNav {
			m.focus = focusWorkspace
		} else {
			m.focus = focusNav
		}
		return nil
	case "L":
		return e.RunCommandID("identity.sso.login", nil)
	case "q":
		if n := m.foregroundJobs(); n > 0 && time.Since(m.quitArmed) > 3*time.Second {
			m.quitArmed = time.Now()
			m.setToast(fmt.Sprintf("%d operation(s) still running — press q again to quit", n), theme.ToneWarn)
			return nil
		}
		e.Deps.Exec.CancelAll()
		return tea.Quit
	case "esc":
		if b, ok := scr.(screens.Backer); ok && m.focus == focusWorkspace {
			if handled, cmd := b.Back(e); handled {
				return cmd
			}
		}
		m.focus = focusNav
		return nil
	}

	if m.focus == focusNav {
		switch key {
		case "up", "k":
			m.navIdx = (m.navIdx - 1 + len(app.Navigation)) % len(app.Navigation)
			return m.activate()
		case "down", "j":
			m.navIdx = (m.navIdx + 1) % len(app.Navigation)
			return m.activate()
		case "enter", "right", "l":
			m.focus = focusWorkspace
			return nil
		}
		// Other keys (r, f, s…) act on the visible screen.
		if key != "enter" {
			return scr.Update(e, k)
		}
		return nil
	}
	return scr.Update(e, k)
}

func (m *Model) foregroundJobs() int {
	n := 0
	for _, v := range m.env.Deps.Exec.Views() {
		if !v.Status.Done() && !v.Quiet {
			n++
		}
	}
	return n
}

func (m *Model) openSearch() tea.Cmd {
	m.focus = focusSearch
	m.search.SetValue("")
	m.palette.search(m.env, "")
	return m.search.Focus()
}

func (m *Model) closeSearch() {
	m.search.Blur()
	m.search.SetValue("")
	m.focus = focusWorkspace
}

func (m *Model) searchKey(k tea.KeyMsg) tea.Cmd {
	switch k.String() {
	case "esc":
		m.closeSearch()
		return nil
	case "up", "ctrl+p", "shift+tab":
		m.palette.move(-1)
		return nil
	case "down", "ctrl+n", "tab":
		m.palette.move(1)
		return nil
	case "enter":
		cmd := m.palette.selected(m.env)
		m.closeSearch()
		return cmd
	}
	before := m.search.Value()
	var cmd tea.Cmd
	m.search, cmd = m.search.Update(k)
	if m.search.Value() != before {
		m.palette.search(m.env, m.search.Value())
	}
	return cmd
}

func (m *Model) profilePicker() tea.Cmd {
	e := m.env
	items := make([]core.PickItem, 0, len(e.Deps.Profiles))
	for _, p := range e.Deps.Profiles {
		detail := p.Kind()
		if p.Region != "" {
			detail += " · " + p.Region
		}
		tone := theme.ToneNormal
		if e.Deps.Production.Is(p.Name, "") {
			tone, detail = theme.ToneBad, detail+" · PRODUCTION"
		}
		items = append(items, core.PickItem{Label: p.Name, Detail: detail, Value: p.Name, Tone: tone})
	}
	if len(items) == 0 {
		return e.Toast("No profiles found in "+profiles.ConfigPath(), theme.ToneWarn)
	}
	return e.Open(core.NewPicker("Switch AWS profile", items, e.Ctx().Profile, func(it core.PickItem) tea.Cmd {
		// Keep an explicitly chosen region only if the user set a default; otherwise use the profile's.
		return core.Msg(core.SwitchContextMsg{Profile: it.Value, Region: e.Deps.Config.UI.DefaultRegion})
	}))
}

func (m *Model) regionPicker() tea.Cmd {
	e := m.env
	items := make([]core.PickItem, 0, len(profiles.Regions))
	for _, r := range profiles.Regions {
		items = append(items, core.PickItem{Label: r, Value: r})
	}
	return e.Open(core.NewPicker("Switch region", items, e.Ctx().Region, func(it core.PickItem) tea.Cmd {
		return core.Msg(core.SwitchContextMsg{Profile: e.Ctx().Profile, Region: it.Value})
	}))
}
