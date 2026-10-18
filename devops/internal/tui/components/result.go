package components

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"devops/internal/aws/metrics"
	"devops/internal/safety"
	"devops/internal/tui/theme"
)

// Views a structured result can be shown in.
var ResultViews = []string{"table", "cards", "yaml", "json"}

// Normalize turns SDK structs into plain JSON-shaped data (maps, slices,
// scalars) so every view works the same way for any result type.
func Normalize(v any) any {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	var out any
	if json.Unmarshal(b, &out) != nil {
		return string(b)
	}
	return prune(out)
}

// prune drops nulls, empty strings and empty collections so views and diffs
// only show fields that carry information.
func prune(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := map[string]any{}
		for k, e := range x {
			if p := prune(e); !empty(p) {
				out[k] = p
			}
		}
		return out
	case []any:
		out := make([]any, 0, len(x))
		for _, e := range x {
			out = append(out, prune(e))
		}
		return out
	}
	return v
}

func empty(v any) bool {
	switch x := v.(type) {
	case nil:
		return true
	case string:
		return x == ""
	case map[string]any:
		return len(x) == 0
	case []any:
		return len(x) == 0
	}
	return false
}

// RenderResult renders v in the given view as display lines of width w.
func RenderResult(v any, view string, w int) []string {
	if s, ok := v.(metrics.Series); ok && (view == "table" || view == "cards") {
		return seriesView(s, view, w)
	}
	data := Normalize(v)
	if s, ok := data.([]any); ok && allStrings(s) {
		lines := make([]string, len(s))
		for i, x := range s {
			lines[i] = x.(string)
		}
		return lines // text results (log lines, events, metric series)
	}
	switch view {
	case "json":
		b, _ := json.MarshalIndent(data, "", "  ")
		return strings.Split(safety.Redact(string(b)), "\n")
	case "yaml":
		b, _ := yaml.Marshal(data)
		return strings.Split(strings.TrimRight(safety.Redact(string(b)), "\n"), "\n")
	case "cards":
		return cards(data, w)
	}
	if lines, ok := table(data, w); ok {
		return lines
	}
	return cards(data, w)
}

func allStrings(s []any) bool {
	if len(s) == 0 {
		return false
	}
	for _, x := range s {
		if _, ok := x.(string); !ok {
			return false
		}
	}
	return true
}

// priority orders well-known columns first.
func priority(k string) int {
	lk := strings.ToLower(k)
	switch {
	case lk == "name" || strings.HasSuffix(lk, "name"):
		return 0
	case lk == "id" || strings.HasSuffix(lk, "id") || strings.HasSuffix(lk, "identifier"):
		return 1
	case strings.Contains(lk, "status") || strings.Contains(lk, "state"):
		return 2
	case strings.Contains(lk, "type") || strings.Contains(lk, "engine") || strings.Contains(lk, "runtime"):
		return 3
	case strings.HasSuffix(lk, "arn"):
		return 8
	}
	return 5
}

func table(data any, w int) ([]string, bool) {
	rows, ok := data.([]any)
	if !ok {
		if m, isMap := data.(map[string]any); isMap {
			rows = []any{m}
		} else {
			return nil, false
		}
	}
	if len(rows) == 0 {
		return []string{theme.Dim.Render("(no results)")}, true
	}
	count := map[string]int{}
	for _, r := range rows {
		m, ok := r.(map[string]any)
		if !ok {
			return nil, false
		}
		for k, v := range m {
			if scalar(v) && fmt.Sprint(v) != "" {
				count[k]++
			}
		}
	}
	var cols []string
	for k := range count {
		cols = append(cols, k)
	}
	sort.Slice(cols, func(i, j int) bool {
		pi, pj := priority(cols[i]), priority(cols[j])
		if pi != pj {
			return pi < pj
		}
		if count[cols[i]] != count[cols[j]] {
			return count[cols[i]] > count[cols[j]]
		}
		return cols[i] < cols[j]
	})
	maxCols := max(2, w/14)
	if len(cols) > maxCols {
		cols = cols[:maxCols]
	}
	if len(cols) == 0 {
		return nil, false
	}
	cells := make([][]string, len(rows))
	widths := make([]int, len(cols))
	for i, c := range cols {
		widths[i] = len(c)
	}
	for r, row := range rows {
		m := row.(map[string]any)
		cells[r] = make([]string, len(cols))
		for i, c := range cols {
			s := cell(m[c])
			cells[r][i] = s
			widths[i] = max(widths[i], len([]rune(s)))
		}
	}
	// Fit to width: cap every column, then give the last column the rest.
	total := 0
	for i := range widths {
		widths[i] = min(widths[i], 40)
		total += widths[i] + 2
	}
	for total > w && total > 0 {
		i := widest(widths)
		if widths[i] <= 6 {
			break
		}
		widths[i]--
		total--
	}
	var out []string
	head := make([]string, len(cols))
	for i, c := range cols {
		head[i] = Pad(strings.ToUpper(c), widths[i])
	}
	out = append(out, theme.TableHead.Render(strings.Join(head, "  ")))
	for _, row := range cells {
		parts := make([]string, len(cols))
		for i, v := range row {
			parts[i] = Pad(v, widths[i])
		}
		out = append(out, theme.StatusTone(statusOf(row)).Style().Render(strings.Join(parts, "  ")))
	}
	out = append(out, theme.Dim.Render(fmt.Sprintf("%d rows · v for cards/YAML/JSON (all fields)", len(rows))))
	return out, true
}

func widest(w []int) int {
	best := 0
	for i := range w {
		if w[i] > w[best] {
			best = i
		}
	}
	return best
}

// statusOf finds a status-like cell to colour the row.
func statusOf(row []string) string {
	for _, c := range row {
		if t := theme.StatusTone(c); t != theme.ToneNormal {
			return c
		}
	}
	return ""
}

func scalar(v any) bool {
	switch v.(type) {
	case string, float64, bool, nil:
		return true
	}
	return false
}

func cell(v any) string {
	switch x := v.(type) {
	case nil:
		return "-"
	case string:
		if t, err := time.Parse(time.RFC3339Nano, x); err == nil {
			return t.Local().Format("2006-01-02 15:04")
		}
		return safety.Redact(strings.ReplaceAll(x, "\n", " "))
	case float64:
		if x == float64(int64(x)) {
			return fmt.Sprint(int64(x))
		}
		return fmt.Sprintf("%.2f", x)
	case []any:
		return fmt.Sprintf("[%d]", len(x))
	case map[string]any:
		return "{…}"
	}
	return fmt.Sprint(v)
}

// cards renders each object as aligned key/value lines, flattening nesting.
func cards(data any, w int) []string {
	items, ok := data.([]any)
	if !ok {
		items = []any{data}
	}
	var out []string
	for i, it := range items {
		if i > 0 {
			out = append(out, theme.Dim.Render(strings.Repeat("─", min(w, 60))))
		}
		var kv [][2]string
		flatten("", it, &kv)
		kw := 0
		for _, p := range kv {
			kw = max(kw, len(p[0]))
		}
		kw = min(kw, max(12, w/3))
		for _, p := range kv {
			val := p[1]
			for j, l := range WrapLines(val, max(10, w-kw-2)) {
				key := ""
				if j == 0 {
					key = p[0]
				}
				out = append(out, theme.Dim.Render(Pad(key, kw))+"  "+theme.StatusTone(val).Style().Render(l))
			}
		}
	}
	if len(out) == 0 {
		return []string{theme.Dim.Render("(no results)")}
	}
	return out
}

func flatten(prefix string, v any, out *[][2]string) {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool {
			pi, pj := priority(keys[i]), priority(keys[j])
			if pi != pj {
				return pi < pj
			}
			return keys[i] < keys[j]
		})
		for _, k := range keys {
			flatten(join(prefix, k), x[k], out)
		}
	case []any:
		if len(x) == 0 {
			return
		}
		for i, e := range x {
			flatten(fmt.Sprintf("%s[%d]", prefix, i), e, out)
		}
	case nil:
	default:
		s := cell(x)
		if s == "" {
			return
		}
		*out = append(*out, [2]string{prefix, s})
	}
}

func join(prefix, k string) string {
	if prefix == "" {
		return k
	}
	return prefix + "." + k
}

// seriesView draws a metric as a chart (table view) or a datapoint table (cards).
func seriesView(s metrics.Series, view string, w int) []string {
	var dims []string
	for k, v := range s.Dimensions {
		dims = append(dims, k+"="+v)
	}
	sort.Strings(dims)
	title := theme.Title.Render(s.Namespace+" / "+s.Metric) + theme.Dim.Render("  "+strings.Join(dims, " ")+
		fmt.Sprintf("  · last %s · %s periods", s.Since, s.Period))
	if len(s.Points) == 0 {
		return []string{title, "", theme.Dim.Render("No datapoints in this period (the resource may be stopped or the metric not published).")}
	}
	unit := s.Points[0].Unit
	suffix := ""
	if unit == "Percent" {
		suffix = "%"
	}
	if view == "cards" {
		out := []string{title, ""}
		for _, l := range s.TextLines() {
			out = append(out, theme.Body.Render(l))
		}
		return out
	}
	last, avg, lo, hi := s.Stats()
	st := theme.Title
	if suffix == "%" {
		switch {
		case hi >= 85:
			st = theme.Bad
		case hi >= 65:
			st = theme.Warn
		default:
			st = theme.OK
		}
	}
	vals := make([]float64, len(s.Points))
	peakAt := s.Points[0]
	for i, p := range s.Points {
		vals[i] = p.Average
		if p.Maximum > peakAt.Maximum {
			peakAt = p
		}
	}
	f := func(v float64) string { return fmt.Sprintf("%.1f%s", v, suffix) }
	stats := theme.Dim.Render("now ") + theme.Strong.Render(f(last)) + theme.Dim.Render("   avg ") + theme.Body.Render(f(avg)) +
		theme.Dim.Render("   min ") + theme.Body.Render(f(lo)) + theme.Dim.Render("   peak ") + st.Render(f(hi)) +
		theme.Dim.Render(" at "+peakAt.Time.Local().Format("15:04"))
	out := []string{title, stats, ""}
	out = append(out, LineChart([]Series{{Values: vals, Style: st}}, w, 12, suffix, nil)...)
	out = append(out, theme.Dim.Render(Pad(s.Points[0].Time.Local().Format("01-02 15:04"), w-11)+s.Points[len(s.Points)-1].Time.Local().Format("01-02 15:04")))
	if suffix == "%" {
		out = append(out, "", theme.Dim.Render("current  ")+Gauge(last, min(40, w-20)))
	}
	out = append(out, "", theme.Dim.Render("v shows the datapoints as a table"))
	return out
}
