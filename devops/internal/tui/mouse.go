package tui

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"devops/internal/tui/core"
)

type clickMemo struct {
	x, y int
	at   time.Time
}

// handleMouse routes clicks and wheel events using the geometry recorded by
// the last View.
func (m *Model) handleMouse(msg tea.MouseMsg) tea.Cmd {
	ev := tea.MouseEvent(msg)
	wheelUp := ev.Button == tea.MouseButtonWheelUp
	wheelDown := ev.Button == tea.MouseButtonWheelDown
	press := ev.Action == tea.MouseActionPress && ev.Button == tea.MouseButtonLeft
	if !press && !wheelUp && !wheelDown {
		return nil
	}
	x, y := ev.X, ev.Y
	double := false
	if press {
		double = m.lastClick.x == x && m.lastClick.y == y && time.Since(m.lastClick.at) < 450*time.Millisecond
		m.lastClick = clickMemo{x, y, time.Now()}
	}
	g := m.g

	// Header: click the profile or region label to open its dropdown.
	if y == 0 && press && m.modal == nil {
		switch {
		case x >= g.header[0][0] && x < g.header[0][1]:
			return m.openDropdown("profile")
		case x >= g.header[1][0] && x < g.header[1][1]:
			return m.openDropdown("region")
		}
		return nil
	}

	if press && y == g.footerY && g.link != "" && x >= g.linkX[0] && x < g.linkX[1] {
		return m.env.OpenURL(g.link, false)
	}

	if g.search.contains(x, y) && press && m.modal == nil {
		m.drop = nil
		return m.openSearch()
	}

	if g.nav.contains(x, y) {
		return m.mouseNav(x, y, press, double, wheelUp, wheelDown)
	}

	if g.ws.contains(x, y) {
		if m.drop != nil && press {
			m.drop = nil // clicking elsewhere closes the dropdown
		}
		return m.mouseWorkspace(x, y, press, double, wheelUp, wheelDown)
	}
	return nil
}

func (m *Model) mouseNav(x, y int, press, double, wheelUp, wheelDown bool) tea.Cmd {
	line := y - m.g.navBody.y
	if m.drop != nil {
		switch {
		case wheelUp:
			m.drop.move(-1)
		case wheelDown:
			m.drop.move(1)
		case press:
			if i, ok := m.g.dropLine[line]; ok {
				m.drop.cursor = i
				it, _ := m.drop.selected()
				return m.chooseDropdown(it)
			}
			if line == m.g.profileLine || line == m.g.regionLine {
				m.drop = nil
			}
		}
		return nil
	}
	if m.modal != nil {
		return nil
	}
	switch {
	case wheelUp:
		return m.moveNav(-1)
	case wheelDown:
		return m.moveNav(1)
	case press && (line == m.g.profileLine || line == m.g.profileLine-1):
		return m.openDropdown("profile")
	case press && (line == m.g.regionLine || line == m.g.regionLine-1):
		return m.openDropdown("region")
	case press:
		if i, ok := m.g.navLine[line]; ok {
			if m.focus == focusSearch {
				m.closeSearch()
			}
			m.navIdx = i
			m.focus = focusNav
			if double {
				m.focus = focusWorkspace
			}
			return m.activate()
		}
	}
	return nil
}

func (m *Model) mouseWorkspace(x, y int, press, double, wheelUp, wheelDown bool) tea.Cmd {
	e := m.env
	if m.modal != nil {
		r := m.g.modal
		if !r.contains(x, y) && press {
			return nil
		}
		mm := core.MouseMsg{X: x - r.x, Y: y - r.y, Click: press, Double: double, WheelUp: wheelUp, WheelDown: wheelDown}
		closed, cmd := m.modal.Update(e, mm)
		if closed {
			m.modal = nil
		}
		return cmd
	}
	b := m.g.wsBody
	if m.focus == focusSearch {
		switch {
		case wheelUp:
			m.palette.move(-1)
		case wheelDown:
			m.palette.move(1)
		case press:
			if i, ok := m.palette.hit[y-b.y]; ok {
				m.palette.cursor = i
				cmd := m.palette.selected(e)
				m.closeSearch()
				return cmd
			}
		}
		return nil
	}
	if press {
		m.focus = focusWorkspace
	}
	return m.current().Update(e, core.MouseMsg{X: x - b.x, Y: y - b.y, Click: press, Double: double, WheelUp: wheelUp, WheelDown: wheelDown})
}
