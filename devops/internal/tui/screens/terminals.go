package screens

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"devops/internal/commands"
	"devops/internal/tui/components"
	"devops/internal/tui/core"
	"devops/internal/tui/term"
	"devops/internal/tui/theme"
)

// Terminals hosts interactive sessions (SSM, ECS Exec, tunnels) inside the
// workspace. Keys go straight to the active session; Ctrl+] opens a menu.
type Terminals struct {
	sessions []*term.Session
	active   int
	nextID   int
	leader   bool
	cols     int
	rows     int
	tabHit   map[int][2]int // session index → x range on the tab line
}

func NewTerminals() *Terminals { return &Terminals{tabHit: map[int][2]int{}} }

func (t *Terminals) ID() string { return "terminals" }

func (t *Terminals) Init(e *core.Env) tea.Cmd { return nil }

// RawInput reports whether every key should go to the session.
func (t *Terminals) RawInput() bool {
	s := t.current()
	return s != nil && !s.Done()
}

func (t *Terminals) Capturing() bool { return t.RawInput() }

func (t *Terminals) current() *term.Session {
	if t.active < 0 || t.active >= len(t.sessions) {
		return nil
	}
	return t.sessions[t.active]
}

func (t *Terminals) Hints() []core.Hint {
	if s := t.current(); s != nil && !s.Done() {
		return []core.Hint{{Key: "Ctrl+]", Label: "Menu (detach · next · close)"}}
	}
	return []core.Hint{{Key: "enter", Label: "Close ended session"}, {Key: "tab", Label: "Next session"}}
}

// Open starts a prepared interactive command in a new embedded session.
func (t *Terminals) Open(e *core.Env, p *commands.Prepared) tea.Cmd {
	cmd, job, err := e.Deps.Runner.Interactive(p)
	if err != nil {
		return e.Toast(err.Error(), theme.ToneBad)
	}
	t.nextID++
	title, note, ready := sessionTitle(p)
	s, err := term.Start(t.nextID, title, cmd, job, max(t.cols, 80), max(t.rows, 24), ready, func(s *term.Session) {
		code, err := s.Exit()
		e.Deps.Exec.EndInteractive(s.Job, err, code)
	})
	if err != nil {
		e.Deps.Exec.EndInteractive(job, err, -1)
		return e.Toast("Could not start session: "+err.Error(), theme.ToneBad)
	}
	s.Note = note
	t.sessions = append(t.sessions, s)
	t.active = len(t.sessions) - 1
	return nil
}

func sessionTitle(p *commands.Prepared) (title, note, ready string) {
	v := p.Values
	label := func(k string) string {
		if l := p.Display[k]; l != "" {
			return l
		}
		return v[k]
	}
	switch p.Cmd.ID {
	case "ssm.session.start":
		return "ssm " + label("instance_id"), "", ""
	case "ecs.exec.shell":
		return "exec " + v["container"] + "@" + shortID(v["task"]), "", ""
	case "ssm.tunnel.ecs":
		return "forward :" + v["local_port"], fmt.Sprintf("localhost:%s → task %s port %s", v["local_port"], shortID(v["task"]), v["port"]),
			"Waiting for connections"
	case "ssm.tunnel.database":
		return "tunnel :" + v["local_port"], fmt.Sprintf("localhost:%s → %s:%s via %s", v["local_port"], v["db_host"], v["db_port"], label("instance_id")),
			"Waiting for connections"
	}
	return p.Cmd.Name, "", ""
}

func (t *Terminals) Update(e *core.Env, msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case core.TickMsg:
		var cmds []tea.Cmd
		for _, s := range t.sessions {
			if s.MarkReported() {
				_, err := s.Exit()
				cmds = append(cmds, core.Msg(core.InteractiveDoneMsg{Job: s.Job, Err: err}))
			}
		}
		return tea.Batch(cmds...)
	case core.MouseMsg:
		if m.Click && m.Y == 1 {
			for i, r := range t.tabHit {
				if m.X >= r[0] && m.X < r[1] {
					t.active = i
				}
			}
		}
	case tea.KeyMsg:
		return t.key(e, m)
	}
	return nil
}

func (t *Terminals) key(e *core.Env, k tea.KeyMsg) tea.Cmd {
	s := t.current()
	if t.leader {
		t.leader = false
		switch k.String() {
		case "d", "esc":
			return core.Msg(core.FocusMsg{Area: "nav"})
		case "n", "tab":
			t.cycle(1)
		case "p":
			t.cycle(-1)
		case "x":
			if s != nil {
				s.Close()
			}
		case "ctrl+]", "]":
			if s != nil {
				s.Send([]byte{0x1d})
			}
		}
		return nil
	}
	if s != nil && !s.Done() {
		if k.String() == "ctrl+]" {
			t.leader = true
			return nil
		}
		s.Send(term.KeyBytes(k, s.AppCursor()))
		return nil
	}
	switch k.String() {
	case "enter", "x":
		if s != nil {
			t.sessions = append(t.sessions[:t.active], t.sessions[t.active+1:]...)
			t.active = max(0, min(t.active, len(t.sessions)-1))
		}
	case "tab", "n":
		t.cycle(1)
	}
	return nil
}

func (t *Terminals) cycle(d int) {
	if n := len(t.sessions); n > 0 {
		t.active = (t.active + d + n) % n
	}
}

func (t *Terminals) View(e *core.Env, w, h int) string {
	out := heading("Terminals", fmt.Sprintf("%d sessions", len(t.sessions)), w) + "\n"
	if len(t.sessions) == 0 {
		return out + "\n" + theme.Dim.Render(strings.Join([]string{
			"No interactive sessions yet. Start one from:",
			"",
			"  EC2 Instances › select › s        SSM shell on the instance",
			"  ECS Clusters › service › task › x  ECS Exec into a container",
			"  Databases › select › t             SSM port-forward tunnel",
			"  / search: \"start ssm session\", \"open database tunnel\"",
			"",
			"Sessions run inside this panel. Every key goes to the remote shell;",
			"press Ctrl+] for the menu: d detach · n next · p previous · x close · ] send Ctrl+].",
		}, "\n"))
	}
	// Tab line.
	t.tabHit = map[int][2]int{}
	var tabs []string
	x := 0
	for i, s := range t.sessions {
		dot := theme.OK.Render("●")
		switch {
		case s.Done():
			dot = theme.Dim.Render("■")
		case s.Note != "" && !s.Ready():
			dot = theme.Warn.Render(e.Spinner())
		}
		label := fmt.Sprintf(" %d %s ", s.ID, s.Title)
		tab := dot + label
		if i == t.active {
			tab = theme.NavActive.Render(" ") + dot + theme.NavActive.Render(label)
		}
		width := len([]rune(label)) + 2
		t.tabHit[i] = [2]int{x, x + width}
		x += width + 1
		tabs = append(tabs, tab)
	}
	out += components.Truncate(strings.Join(tabs, " "), w) + "\n"

	s := t.current()
	if s == nil {
		return out
	}
	info := ""
	switch {
	case t.leader:
		info = theme.Warn.Render("menu: d detach · n next · p previous · x close session · ] send Ctrl+] · any other key cancels")
	case s.Note != "" && s.Ready():
		info = theme.OK.Render("● listening  ") + theme.Body.Render(s.Note)
	case s.Note != "":
		info = theme.Warn.Render(e.Spinner()+" connecting  ") + theme.Dim.Render(s.Note)
	}
	if s.Done() {
		code, err := s.Exit()
		msg := fmt.Sprintf("■ session ended (exit %d)", code)
		if err != nil && code == 0 {
			msg += ": " + err.Error()
		}
		info = theme.Dim.Render(msg + " — enter closes this tab")
	}
	if info != "" {
		out += components.Truncate(info, w) + "\n"
	}
	used := strings.Count(out, "\n")
	t.cols, t.rows = w, h-used
	s.Resize(t.cols, t.rows)
	return out + components.Fit(s.Render(!t.leader), w, t.rows)
}

// CloseAll hangs up every session (on quit).
func (t *Terminals) CloseAll() {
	for _, s := range t.sessions {
		s.Close()
	}
}
