// Package components contains reusable TUI building blocks.
package components

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// Fit truncates every line to w cells and pads/cuts the block to exactly h lines.
func Fit(s string, w, h int) string {
	lines := strings.Split(s, "\n")
	if h >= 0 && len(lines) > h {
		lines = lines[:h]
	}
	for i, l := range lines {
		lines[i] = Truncate(l, w)
	}
	for h >= 0 && len(lines) < h {
		lines = append(lines, "")
	}
	return strings.Join(lines, "\n")
}

// Truncate cuts a styled string to w visible cells with an ellipsis.
func Truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	return ansi.Truncate(s, w, "…")
}

// Pad right-pads a styled string to w visible cells.
func Pad(s string, w int) string {
	s = Truncate(s, w)
	if d := w - lipgloss.Width(s); d > 0 {
		s += strings.Repeat(" ", d)
	}
	return s
}

// Ago renders a compact relative time ("12s ago").
func Ago(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return Dur(time.Since(t)) + " ago"
}

// Dur renders a compact duration (850ms, 12s, 4m, 2h).
func Dur(d time.Duration) string {
	switch {
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
	return fmt.Sprintf("%dd", int(d.Hours()/24))
}

// Bytes renders a human byte size.
func Bytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(n)/float64(div), "KMGTPE"[exp])
}

// Wrap soft-wraps styled text to w cells, breaking long words when needed.
func Wrap(s string, w int) string {
	if w <= 0 {
		return s
	}
	return ansi.Wrap(s, w, " -/,:")
}

// WrapLines wraps each line and returns the visual lines.
func WrapLines(s string, w int) []string {
	return strings.Split(Wrap(s, w), "\n")
}

// Strip removes ANSI styling.
func Strip(s string) string { return ansi.Strip(s) }

// Pack joins styled items with sep, starting a new line instead of cutting an
// item off at the right edge.
func Pack(items []string, sep string, w int) []string {
	var lines []string
	cur := ""
	for _, it := range items {
		switch {
		case cur == "":
			cur = it
		case lipgloss.Width(cur)+len(sep)+lipgloss.Width(it) <= w:
			cur += sep + it
		default:
			lines = append(lines, cur)
			cur = it
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

// Hyperlink wraps text in an OSC 8 terminal hyperlink (Cmd/Ctrl+click opens
// it in terminals that support links; others just show the text).
func Hyperlink(url, text string) string {
	return "\x1b]8;;" + url + "\x1b\\" + text + "\x1b]8;;\x1b\\"
}
