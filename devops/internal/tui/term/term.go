// Package term runs interactive commands (SSM sessions, ECS Exec, tunnels)
// on a pseudo-terminal and renders their screen inside the TUI layout.
package term

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/creack/pty"
	"github.com/hinshun/vt10x"

	"devops/internal/executor"
)

type Session struct {
	ID    int
	Title string
	Note  string // e.g. "localhost:15432 → db:5432" for tunnels
	Job   *executor.Job

	cmd  *exec.Cmd
	pty  *os.File
	vt   vt10x.Terminal
	mu   sync.Mutex
	cols int
	rows int

	readyText string
	ready     atomic.Bool
	done      atomic.Bool
	exitCode  int
	exitErr   error
	reported  bool
}

// Start launches cmd on a new PTY of the given size. readyText, when set,
// marks the session ready once it appears in the output (tunnels).
func Start(id int, title string, cmd *exec.Cmd, job *executor.Job, cols, rows int, readyText string, onExit func(*Session)) (*Session, error) {
	cols, rows = max(cols, 20), max(rows, 5)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	f, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		return nil, err
	}
	s := &Session{ID: id, Title: title, Job: job, cmd: cmd, pty: f, cols: cols, rows: rows, readyText: readyText}
	s.vt = vt10x.New(vt10x.WithWriter(f), vt10x.WithSize(cols, rows))
	go s.pump(onExit)
	return s, nil
}

func (s *Session) pump(onExit func(*Session)) {
	buf := make([]byte, 32*1024)
	for {
		n, err := s.pty.Read(buf)
		if n > 0 {
			s.vt.Write(buf[:n])
			if s.readyText != "" && !s.ready.Load() && strings.Contains(string(buf[:n]), s.readyText) {
				s.ready.Store(true)
			}
			if s.Job != nil {
				s.Job.Write(strings.TrimRight(stripControl(string(buf[:n])), "\r\n"))
			}
		}
		if err != nil {
			break
		}
	}
	err := s.cmd.Wait()
	s.mu.Lock()
	s.exitErr = err
	if s.cmd.ProcessState != nil {
		s.exitCode = s.cmd.ProcessState.ExitCode()
	}
	s.mu.Unlock()
	s.done.Store(true)
	s.pty.Close()
	if onExit != nil {
		onExit(s)
	}
}

func (s *Session) Done() bool  { return s.done.Load() }
func (s *Session) Ready() bool { return s.ready.Load() }

func (s *Session) Exit() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.exitCode, s.exitErr
}

// MarkReported returns true the first time it is called after exit.
func (s *Session) MarkReported() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.Done() || s.reported {
		return false
	}
	s.reported = true
	return true
}

// Resize adjusts the PTY and emulator when the panel size changes.
func (s *Session) Resize(cols, rows int) {
	cols, rows = max(cols, 20), max(rows, 5)
	s.mu.Lock()
	same := cols == s.cols && rows == s.rows
	s.cols, s.rows = cols, rows
	s.mu.Unlock()
	if same || s.Done() {
		return
	}
	_ = pty.Setsize(s.pty, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	s.vt.Resize(cols, rows)
}

// Send writes input to the process.
func (s *Session) Send(b []byte) {
	if !s.Done() && len(b) > 0 {
		_, _ = s.pty.Write(b)
	}
}

// Close hangs up the session, then kills it if it does not exit.
func (s *Session) Close() {
	if s.Done() || s.cmd.Process == nil {
		return
	}
	_ = s.cmd.Process.Signal(syscall.SIGHUP)
	go func() {
		time.Sleep(2 * time.Second)
		if !s.Done() {
			_ = s.cmd.Process.Kill()
		}
	}()
}

// AppCursor reports whether the remote app enabled application cursor keys.
func (s *Session) AppCursor() bool { return s.vt.Mode()&vt10x.ModeAppCursor != 0 }

// Render draws the emulator screen with SGR colours; the cursor is shown
// when focused.
func (s *Session) Render(focused bool) string {
	s.vt.Lock()
	defer s.vt.Unlock()
	cols, rows := s.vt.Size()
	cur := s.vt.Cursor()
	showCursor := focused && s.vt.CursorVisible() && !s.Done()
	var b strings.Builder
	for y := 0; y < rows; y++ {
		if y > 0 {
			b.WriteByte('\n')
		}
		var last string
		for x := 0; x < cols; x++ {
			g := s.vt.Cell(x, y)
			sgr := sgrFor(g, showCursor && x == cur.X && y == cur.Y)
			if sgr != last {
				b.WriteString(sgr)
				last = sgr
			}
			c := g.Char
			if c == 0 || c < ' ' {
				c = ' '
			}
			b.WriteRune(c)
		}
		b.WriteString("\x1b[0m")
	}
	return b.String()
}

const (
	attrReverse   = 1 << 0
	attrUnderline = 1 << 1
	attrBold      = 1 << 2
	attrItalic    = 1 << 4
)

func sgrFor(g vt10x.Glyph, cursor bool) string {
	parts := []string{"0"}
	if g.Mode&attrBold != 0 {
		parts = append(parts, "1")
	}
	if g.Mode&attrItalic != 0 {
		parts = append(parts, "3")
	}
	if g.Mode&attrUnderline != 0 {
		parts = append(parts, "4")
	}
	if cursor {
		parts = append(parts, "7")
	}
	if c := color(g.FG, true); c != "" {
		parts = append(parts, c)
	}
	if c := color(g.BG, false); c != "" {
		parts = append(parts, c)
	}
	return "\x1b[" + strings.Join(parts, ";") + "m"
}

func color(c vt10x.Color, fg bool) string {
	base := 38
	if !fg {
		base = 48
	}
	switch {
	case c >= vt10x.DefaultFG:
		return ""
	case c < 8:
		if fg {
			return strconv.Itoa(30 + int(c))
		}
		return strconv.Itoa(40 + int(c))
	case c < 16:
		if fg {
			return strconv.Itoa(90 + int(c) - 8)
		}
		return strconv.Itoa(100 + int(c) - 8)
	case c < 256:
		return fmt.Sprintf("%d;5;%d", base, c)
	}
	return fmt.Sprintf("%d;2;%d;%d;%d", base, (c>>16)&0xff, (c>>8)&0xff, c&0xff)
}

// KeyBytes translates a Bubble Tea key into the bytes a terminal would send.
func KeyBytes(k tea.KeyMsg, appCursor bool) []byte {
	arrow := func(c byte) []byte {
		if appCursor {
			return []byte{0x1b, 'O', c}
		}
		return []byte{0x1b, '[', c}
	}
	var out []byte
	switch k.Type {
	case tea.KeyRunes:
		out = []byte(string(k.Runes))
	case tea.KeySpace:
		out = []byte{' '}
	case tea.KeyUp:
		out = arrow('A')
	case tea.KeyDown:
		out = arrow('B')
	case tea.KeyRight:
		out = arrow('C')
	case tea.KeyLeft:
		out = arrow('D')
	case tea.KeyHome:
		out = []byte("\x1b[H")
	case tea.KeyEnd:
		out = []byte("\x1b[F")
	case tea.KeyPgUp:
		out = []byte("\x1b[5~")
	case tea.KeyPgDown:
		out = []byte("\x1b[6~")
	case tea.KeyDelete:
		out = []byte("\x1b[3~")
	case tea.KeyInsert:
		out = []byte("\x1b[2~")
	case tea.KeyShiftTab:
		out = []byte("\x1b[Z")
	case tea.KeyF1, tea.KeyF2, tea.KeyF3, tea.KeyF4:
		out = []byte{0x1b, 'O', byte('P' + (tea.KeyF1 - k.Type))}
	case tea.KeyF5:
		out = []byte("\x1b[15~")
	case tea.KeyF6:
		out = []byte("\x1b[17~")
	case tea.KeyF7:
		out = []byte("\x1b[18~")
	case tea.KeyF8:
		out = []byte("\x1b[19~")
	case tea.KeyF9:
		out = []byte("\x1b[20~")
	case tea.KeyF10:
		out = []byte("\x1b[21~")
	case tea.KeyF11:
		out = []byte("\x1b[23~")
	case tea.KeyF12:
		out = []byte("\x1b[24~")
	default:
		if k.Type >= 0 && k.Type <= 31 || k.Type == 127 {
			out = []byte{byte(k.Type)} // Ctrl+letter, Enter, Tab, Esc, Backspace
		}
	}
	if k.Alt && len(out) > 0 {
		out = append([]byte{0x1b}, out...)
	}
	return out
}

// stripControl removes escape sequences so job output stays readable.
func stripControl(s string) string {
	var b strings.Builder
	esc := false
	for _, r := range s {
		switch {
		case esc:
			if (r >= '@' && r <= '~') && r != '[' {
				esc = false
			}
		case r == 0x1b:
			esc = true
		case r == '\n' || r == '\t' || r >= ' ':
			b.WriteRune(r)
		}
	}
	return b.String()
}
