package core

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/moovendhan-v/aws-cli-tui/devops/internal/tui/theme"
)

// Pin is a resource the user pinned to the overview.
type Pin struct {
	Screen, Key, Label string
}

func (p Pin) encode() string { return p.Screen + "\x1f" + p.Key + "\x1f" + p.Label }

// Pins returns pinned resources for the active profile/region, newest first.
func (e *Env) Pins() []Pin {
	if e.Deps.Store == nil {
		return nil
	}
	var out []Pin
	for _, v := range e.Deps.Store.Recents(e.pinKind(), 9) {
		parts := strings.SplitN(v, "\x1f", 3)
		if len(parts) == 3 {
			out = append(out, Pin{parts[0], parts[1], parts[2]})
		}
	}
	return out
}

func (e *Env) pinKind() string { return "pin:" + e.Ctx().Profile + ":" + e.Ctx().Region }

// TogglePin pins or unpins a resource.
func (e *Env) TogglePin(p Pin) tea.Cmd {
	if e.Deps.Store == nil {
		return e.Toast("Pins need history storage", theme.ToneWarn)
	}
	for _, x := range e.Pins() {
		if x.Screen == p.Screen && x.Key == p.Key {
			_ = e.Deps.Store.DeleteRecent(e.pinKind(), x.encode())
			return e.Toast("Unpinned "+p.Label, theme.ToneNormal)
		}
	}
	_ = e.Deps.Store.TouchRecent(e.pinKind(), p.encode())
	return e.Toast("★ Pinned "+p.Label+" to the overview", theme.ToneOK)
}
