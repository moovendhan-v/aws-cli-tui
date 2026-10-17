// Package theme holds the color tokens and styles from the design mock
// (GitHub-dark palette: #0d1117 canvas, #161b22 panels, #30363d borders).
package theme

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var (
	Canvas      = lipgloss.Color("#0d1117")
	Panel       = lipgloss.Color("#161b22")
	Bar         = lipgloss.Color("#21262d")
	Border      = lipgloss.Color("#30363d")
	Blue        = lipgloss.Color("#58a6ff")
	Accent      = lipgloss.Color("#1f6feb")
	Text        = lipgloss.Color("#c9d1d9")
	Bright      = lipgloss.Color("#f0f6fc")
	Muted       = lipgloss.Color("#8b949e")
	Green       = lipgloss.Color("#3fb950")
	Red         = lipgloss.Color("#f85149")
	RedBorder   = lipgloss.Color("#da3633")
	Yellow      = lipgloss.Color("#e3b341")
	YellowEdge  = lipgloss.Color("#d29922")
	White       = lipgloss.Color("#ffffff")
	SelectionBg = lipgloss.Color("#1c2d41")
)

var (
	Brand      = lipgloss.NewStyle().Foreground(Blue).Bold(true)
	Title      = lipgloss.NewStyle().Foreground(Blue).Bold(true)
	Body       = lipgloss.NewStyle().Foreground(Text)
	Strong     = lipgloss.NewStyle().Foreground(Bright).Bold(true)
	Dim        = lipgloss.NewStyle().Foreground(Muted)
	OK         = lipgloss.NewStyle().Foreground(Green)
	Bad        = lipgloss.NewStyle().Foreground(Red)
	Warn       = lipgloss.NewStyle().Foreground(Yellow)
	Prompt     = lipgloss.NewStyle().Foreground(Green)
	Key        = lipgloss.NewStyle().Foreground(Text).Bold(true)
	NavActive  = lipgloss.NewStyle().Background(Accent).Foreground(White).Bold(true)
	NavItem    = lipgloss.NewStyle().Foreground(Muted)
	NavHover   = lipgloss.NewStyle().Foreground(Text)
	RowCursor  = lipgloss.NewStyle().Background(SelectionBg).Foreground(Bright)
	TableHead  = lipgloss.NewStyle().Foreground(Muted).Bold(true)
	ProdBadge  = lipgloss.NewStyle().Background(RedBorder).Foreground(White).Bold(true).Padding(0, 1)
	InfoBadge  = lipgloss.NewStyle().Background(Bar).Foreground(Text).Padding(0, 1)
	HeaderBar  = lipgloss.NewStyle().Background(Bar).Foreground(Text)
	ErrorBlock = lipgloss.NewStyle().Foreground(Red)
)

// Box returns a rounded panel with the given border color.
func Box(border lipgloss.Color) lipgloss.Style {
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(border).Padding(0, 1)
}

// PanelBox returns a rounded panel with explicit padding (density-dependent).
func PanelBox(border lipgloss.Color, top, side, bottom int) lipgloss.Style {
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(border).Padding(top, side, bottom, side)
}

// Select renders a dropdown-looking field: " value      ▾ ".
func Select(value string, w int, active bool) string {
	st := lipgloss.NewStyle().Background(Bar).Foreground(Text)
	if active {
		st = lipgloss.NewStyle().Background(Accent).Foreground(White).Bold(true)
	}
	inner := max(1, w-4)
	v := []rune(value)
	if len(v) > inner {
		v = append(v[:inner-1], '…')
	}
	pad := inner - len(v)
	return st.Render(" " + string(v) + strings.Repeat(" ", max(0, pad)) + " ▾ ")
}

// Tone is a semantic row/card severity.
type Tone int

const (
	ToneNormal Tone = iota
	ToneOK
	ToneWarn
	ToneBad
	ToneDim
)

func (t Tone) Style() lipgloss.Style {
	switch t {
	case ToneOK:
		return OK
	case ToneWarn:
		return Warn
	case ToneBad:
		return Bad
	case ToneDim:
		return Dim
	}
	return Body
}

func (t Tone) Border() lipgloss.Color {
	switch t {
	case ToneWarn:
		return YellowEdge
	case ToneBad:
		return RedBorder
	}
	return Border
}

// StatusTone maps common AWS state strings to a tone.
func StatusTone(s string) Tone {
	switch s {
	case "running", "available", "ACTIVE", "RUNNING", "healthy", "OK", "active", "COMPLETED", "Active", "Online",
		"completed", "Successful", "CREATE_COMPLETE", "UPDATE_COMPLETE", "Succeeded", "SUCCEEDED":
		return ToneOK
	case "pending", "stopping", "PENDING", "PROVISIONING", "DEACTIVATING", "draining", "initial", "IN_PROGRESS",
		"INSUFFICIENT_DATA", "modifying", "backing-up", "provisioning", "queued", "Pending", "running…", "InProgress",
		"Stopping", "STOPPING":
		return ToneWarn
	case "stopped", "terminated", "STOPPED", "INACTIVE", "unhealthy", "unavailable", "ALARM", "FAILED", "failed",
		"Failed", "timed out", "Inactive", "ConnectionLost", "FAULT", "TIMED_OUT", "Superseded", "Stopped":
		return ToneBad
	case "cancelled", "shutting-down", "unused":
		return ToneDim
	}
	return ToneNormal
}
