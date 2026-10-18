package components

import (
	"fmt"
	"math"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/tui/theme"
)

var blocks = []rune("▁▂▃▄▅▆▇█")

// Sparkline renders values as one row of block characters scaled to [0,max].
func Sparkline(values []float64, w int, style lipgloss.Style) string {
	if len(values) == 0 || w <= 0 {
		return ""
	}
	vals := resample(values, w)
	hi := 0.0
	for _, v := range vals {
		hi = math.Max(hi, v)
	}
	var b strings.Builder
	for _, v := range vals {
		i := 0
		if hi > 0 {
			i = int(v / hi * float64(len(blocks)-1))
		}
		b.WriteRune(blocks[max(0, min(len(blocks)-1, i))])
	}
	return style.Render(b.String())
}

// resample maps values onto n columns (averaging or repeating).
func resample(values []float64, n int) []float64 {
	if len(values) == n || n <= 0 {
		return values
	}
	out := make([]float64, n)
	for i := range out {
		lo := i * len(values) / n
		hi := max(lo+1, (i+1)*len(values)/n)
		sum := 0.0
		for _, v := range values[lo:min(hi, len(values))] {
			sum += v
		}
		out[i] = sum / float64(min(hi, len(values))-lo)
	}
	return out
}

// Progress renders a capacity bar: running (green), pending (yellow,
// animated when tick >= 0), remaining (dim), e.g. ███████▒▒░░░ 7/12.
func Progress(running, pending, desired int32, w int, tick int) string {
	if w < 4 {
		return ""
	}
	total := max(desired, running+pending, 1)
	r := int(float64(running) / float64(total) * float64(w))
	p := int(float64(pending) / float64(total) * float64(w))
	if pending > 0 && p == 0 {
		p = 1
	}
	r = min(r, w)
	p = min(p, w-r)
	rest := w - r - p
	pend := strings.Repeat("▒", p)
	if p > 0 && tick >= 0 {
		// A moving highlight inside the pending segment shows activity.
		pos := tick % p
		pend = strings.Repeat("▒", pos) + "▓" + strings.Repeat("▒", p-pos-1)
	}
	bar := theme.OK.Render(strings.Repeat("█", r)) + theme.Warn.Render(pend) + theme.Dim.Render(strings.Repeat("░", rest))
	if running < desired && pending == 0 {
		bar = theme.Bad.Render(strings.Repeat("█", r)) + theme.Dim.Render(strings.Repeat("░", w-r))
	}
	return bar
}

// BarChart draws values as vertical bars h rows tall with a y-axis label.
// Each row has 8 sub-levels, so small differences stay visible.
func BarChart(values []float64, w, h int, unit string, style lipgloss.Style) []string {
	if len(values) == 0 || w < 10 || h < 2 {
		return nil
	}
	hi := 0.0
	for _, v := range values {
		hi = math.Max(hi, v)
	}
	if hi == 0 {
		hi = 1
	}
	label := func(v float64) string { return fmt.Sprintf("%6.1f%s", v, unit) }
	lw := len(label(hi)) + 1
	cols := resample(values, w-lw)
	lines := make([]string, h)
	for row := 0; row < h; row++ {
		top := float64(h-row) / float64(h) * hi
		bottom := float64(h-row-1) / float64(h) * hi
		var b strings.Builder
		for _, v := range cols {
			switch {
			case v >= top:
				b.WriteRune('█')
			case v > bottom:
				frac := (v - bottom) / (top - bottom)
				b.WriteRune(blocks[max(0, min(len(blocks)-1, int(frac*float64(len(blocks)))-1))])
			default:
				b.WriteRune(' ')
			}
		}
		axis := strings.Repeat(" ", lw)
		switch row {
		case 0:
			axis = Pad(label(hi), lw)
		case h - 1:
			axis = Pad(label(0), lw)
		}
		lines[row] = theme.Dim.Render(axis) + style.Render(b.String())
	}
	return lines
}

// Gauge renders a percentage as a short bar with colour thresholds.
func Gauge(pct float64, w int) string {
	pct = math.Max(0, math.Min(100, pct))
	n := int(pct / 100 * float64(w))
	st := theme.OK
	switch {
	case pct >= 85:
		st = theme.Bad
	case pct >= 65:
		st = theme.Warn
	}
	return st.Render(strings.Repeat("■", n)) + theme.Dim.Render(strings.Repeat("·", w-n)) + fmt.Sprintf(" %3.0f%%", pct)
}

// Series is one line on a LineChart.
type Series struct {
	Values []float64
	Style  lipgloss.Style
}

// brailleBit maps a dot inside a 2×4 braille cell to its bit.
var brailleBit = [4][2]rune{{0x01, 0x08}, {0x02, 0x10}, {0x04, 0x20}, {0x40, 0x80}}

// LineChart plots series with braille dots (2×4 dots per cell) for a smooth,
// high-resolution line. A threshold, when set, is drawn as a dashed red line.
func LineChart(series []Series, w, h int, unit string, threshold *float64) []string {
	if w < 12 || h < 2 || len(series) == 0 {
		return nil
	}
	lo, hi := math.Inf(1), math.Inf(-1)
	for _, s := range series {
		for _, v := range s.Values {
			lo, hi = math.Min(lo, v), math.Max(hi, v)
		}
	}
	if threshold != nil {
		lo, hi = math.Min(lo, *threshold), math.Max(hi, *threshold)
	}
	if math.IsInf(lo, 0) {
		return nil
	}
	lo = math.Min(lo, 0)
	if hi == lo {
		hi = lo + 1
	}
	label := func(v float64) string { return fmt.Sprintf("%7.1f%s", v, unit) }
	lw := len(label(hi)) + 1
	cw := w - lw
	pw, ph := cw*2, h*4
	cells := make([][]rune, h)
	owner := make([][]int, h) // series index that last touched a cell (for colour)
	for y := range cells {
		cells[y] = make([]rune, cw)
		owner[y] = make([]int, cw)
		for x := range owner[y] {
			owner[y][x] = -1
		}
	}
	set := func(px, py, who int) {
		if px < 0 || py < 0 || px >= pw || py >= ph {
			return
		}
		cy, cx := py/4, px/2
		cells[cy][cx] |= brailleBit[py%4][px%2]
		owner[cy][cx] = who
	}
	toY := func(v float64) int { return ph - 1 - int((v-lo)/(hi-lo)*float64(ph-1)) }
	if threshold != nil {
		ty := toY(*threshold)
		for px := 0; px < pw; px += 2 {
			set(px, ty, -2)
		}
	}
	for si, s := range series {
		n := len(s.Values)
		if n == 0 {
			continue
		}
		prevX, prevY := -1, -1
		for i, v := range s.Values {
			x := 0
			if n > 1 {
				x = i * (pw - 1) / (n - 1)
			}
			y := toY(v)
			if prevX >= 0 {
				line(prevX, prevY, x, y, func(px, py int) { set(px, py, si) })
			} else {
				set(x, y, si)
			}
			prevX, prevY = x, y
		}
	}
	out := make([]string, h)
	for y := 0; y < h; y++ {
		axis := strings.Repeat(" ", lw)
		switch y {
		case 0:
			axis = Pad(label(hi), lw)
		case h - 1:
			axis = Pad(label(lo), lw)
		}
		var b strings.Builder
		b.WriteString(theme.Dim.Render(axis))
		for x := 0; x < cw; x++ {
			r := cells[y][x]
			if r == 0 {
				b.WriteRune(' ')
				continue
			}
			ch := string(0x2800 + r)
			switch who := owner[y][x]; {
			case who == -2:
				b.WriteString(theme.Bad.Render(ch))
			case who >= 0:
				b.WriteString(series[who].Style.Render(ch))
			default:
				b.WriteString(ch)
			}
		}
		out[y] = b.String()
	}
	return out
}

// line rasterises a segment (Bresenham).
func line(x0, y0, x1, y1 int, plot func(x, y int)) {
	dx, dy := abs(x1-x0), -abs(y1-y0)
	sx, sy := 1, 1
	if x0 > x1 {
		sx = -1
	}
	if y0 > y1 {
		sy = -1
	}
	err := dx + dy
	for {
		plot(x0, y0)
		if x0 == x1 && y0 == y1 {
			return
		}
		e2 := 2 * err
		if e2 >= dy {
			err += dy
			x0 += sx
		}
		if e2 <= dx {
			err += dx
			y0 += sy
		}
	}
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
