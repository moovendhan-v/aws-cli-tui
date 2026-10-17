package term

import (
	"os/exec"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

func TestSessionRoundTrip(t *testing.T) {
	cmd := exec.Command("sh", "-c", `printf '\033[31mred\033[0m ready\n'; read x; echo "got:$x"; sleep 0.2`)
	exited := make(chan struct{})
	s, err := Start(1, "test", cmd, nil, 60, 10, "ready", func(*Session) { close(exited) })
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return s.Ready() })
	for _, r := range "abc" {
		s.Send(KeyBytes(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}}, false))
	}
	s.Send(KeyBytes(tea.KeyMsg{Type: tea.KeyEnter}, false))
	waitFor(t, func() bool { return strings.Contains(ansi.Strip(s.Render(false)), "got:abc") })
	if out := s.Render(false); !strings.Contains(out, "\x1b[0;31m") {
		t.Errorf("red SGR not rendered: %q", out[:80])
	}
	select {
	case <-exited:
	case <-time.After(5 * time.Second):
		t.Fatal("session did not exit")
	}
	if code, _ := s.Exit(); code != 0 || !s.Done() {
		t.Fatalf("exit code %d done=%v", code, s.Done())
	}
	s.Resize(80, 20) // must not panic after exit
}

func TestKeyBytes(t *testing.T) {
	cases := map[string]tea.KeyMsg{
		"\x1b[A":  {Type: tea.KeyUp},
		"\x03":    {Type: tea.KeyCtrlC},
		"\x12":    {Type: tea.KeyCtrlR},
		"\r":      {Type: tea.KeyEnter},
		"\x7f":    {Type: tea.KeyBackspace},
		"\x1bOQ":  {Type: tea.KeyF2},
		"\x1bx":   {Type: tea.KeyRunes, Runes: []rune{'x'}, Alt: true},
		"\x1b[3~": {Type: tea.KeyDelete},
	}
	for want, k := range cases {
		if got := string(KeyBytes(k, false)); got != want {
			t.Errorf("%v → %q, want %q", k, got, want)
		}
	}
	if got := string(KeyBytes(tea.KeyMsg{Type: tea.KeyUp}, true)); got != "\x1bOA" {
		t.Errorf("app cursor up = %q", got)
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatal("timed out")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
