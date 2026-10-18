package components

import (
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/tui/theme"
)

type Column struct {
	Title string
	Width int  // minimum width
	Flex  bool // shares leftover space
}

type Row struct {
	Key    string
	Cells  []string
	Tone   theme.Tone
	Search string // extra text for filtering (tags, ARNs…)
	Data   any
}

// Table is a keyboard-driven, filterable table that renders within any size.
type Table struct {
	Columns []Column
	rows    []Row
	view    []int // indices into rows after filtering
	cursor  int
	offset  int
	filter  string
	height  int

	more        bool   // another page exists in AWS
	loadingMore bool   // a page fetch is in flight
	spinner     string // current spinner frame for the loading row
}

// SetMore records pagination state for the trailing status row.
func (t *Table) SetMore(more, loading bool, spinner string) {
	t.more, t.loadingMore, t.spinner = more, loading, spinner
}

func (t *Table) HasMore() bool { return t.more }

// WantsMore reports whether the cursor is near the end of the loaded rows and
// the next page should be fetched.
func (t *Table) WantsMore() bool {
	return t.more && !t.loadingMore && t.filter == "" && t.cursor >= len(t.view)-5
}

// AppendRows adds a fetched page without moving the cursor.
func (t *Table) AppendRows(rows []Row) {
	t.rows = append(t.rows, rows...)
	t.apply()
	t.clamp()
}

// Scroll moves the cursor by n rows (mouse wheel).
func (t *Table) Scroll(n int) {
	t.cursor += n
	t.clamp()
}

// Click selects the row at line y of the last rendered view (0 = header).
// It reports whether a row was hit.
func (t *Table) Click(y int) bool {
	i := t.offset + y - 1
	if y < 1 || i < 0 || i >= len(t.view) {
		return false
	}
	t.cursor = i
	return true
}

func NewTable(cols ...Column) *Table { return &Table{Columns: cols} }

func (t *Table) SetRows(rows []Row) {
	var key string
	if r, ok := t.Selected(); ok {
		key = r.Key
	}
	t.rows = rows
	t.apply()
	if key != "" {
		for i, idx := range t.view {
			if t.rows[idx].Key == key {
				t.cursor = i
				break
			}
		}
	}
	t.clamp()
}

func (t *Table) Rows() []Row { return t.rows }

func (t *Table) SetFilter(f string) {
	t.filter = strings.ToLower(strings.TrimSpace(f))
	t.apply()
	t.cursor, t.offset = 0, 0
}

func (t *Table) Filter() string { return t.filter }

func (t *Table) apply() {
	t.view = t.view[:0]
	for i, r := range t.rows {
		if t.filter == "" || matches(r, t.filter) {
			t.view = append(t.view, i)
		}
	}
}

func matches(r Row, f string) bool {
	hay := strings.ToLower(strings.Join(r.Cells, " ") + " " + r.Search + " " + r.Key)
	for _, term := range strings.Fields(f) {
		if !strings.Contains(hay, term) {
			return false
		}
	}
	return true
}

func (t *Table) Len() int    { return len(t.view) }
func (t *Table) Total() int  { return len(t.rows) }
func (t *Table) Cursor() int { return t.cursor }

func (t *Table) Selected() (Row, bool) {
	if t.cursor < 0 || t.cursor >= len(t.view) {
		return Row{}, false
	}
	return t.rows[t.view[t.cursor]], true
}

// Select moves the cursor to the row with key, if visible.
func (t *Table) Select(key string) bool {
	for i, idx := range t.view {
		if t.rows[idx].Key == key {
			t.cursor = i
			t.clamp()
			return true
		}
	}
	return false
}

func (t *Table) clamp() {
	if t.cursor >= len(t.view) {
		t.cursor = len(t.view) - 1
	}
	if t.cursor < 0 {
		t.cursor = 0
	}
}

// Update handles navigation keys and reports whether the key was consumed.
func (t *Table) Update(msg tea.KeyMsg) bool {
	page := max(1, t.height-2)
	switch msg.String() {
	case "up", "k":
		t.cursor--
	case "down", "j":
		t.cursor++
	case "pgup", "ctrl+u":
		t.cursor -= page
	case "pgdown", "ctrl+d":
		t.cursor += page
	case "home", "g":
		t.cursor = 0
	case "end", "G":
		t.cursor = len(t.view) - 1
	default:
		return false
	}
	t.clamp()
	return true
}

// View renders the header and visible rows into w×h.
func (t *Table) View(w, h int) string {
	t.height = h
	widths := t.widths(w)
	var b strings.Builder

	head := make([]string, len(t.Columns))
	for i, c := range t.Columns {
		head[i] = Pad(strings.ToUpper(c.Title), widths[i])
	}
	b.WriteString(theme.TableHead.Render(strings.Join(head, " ")))

	body := h - 1
	status := ""
	switch {
	case t.loadingMore:
		status = theme.Warn.Render("  " + t.spinner + " loading more…")
	case t.more:
		status = theme.Dim.Render("  ↓ more available — scroll down to load")
	}
	if status != "" {
		body--
	}
	if body <= 0 {
		return b.String()
	}
	if t.cursor < t.offset {
		t.offset = t.cursor
	}
	if t.cursor >= t.offset+body {
		t.offset = t.cursor - body + 1
	}
	if len(t.view) == 0 {
		b.WriteString("\n" + theme.Dim.Render("  no matching rows"))
	}
	for i := t.offset; i < len(t.view) && i < t.offset+body; i++ {
		r := t.rows[t.view[i]]
		cells := make([]string, len(t.Columns))
		for c := range t.Columns {
			v := ""
			if c < len(r.Cells) {
				v = r.Cells[c]
			}
			cells[c] = Pad(v, widths[c])
		}
		line := strings.Join(cells, " ")
		b.WriteString("\n")
		if i == t.cursor {
			b.WriteString(theme.RowCursor.Render(Pad(line, w)))
		} else {
			b.WriteString(r.Tone.Style().Render(line))
		}
	}
	if status != "" {
		b.WriteString("\n" + status)
	}
	return b.String()
}

func (t *Table) widths(w int) []int {
	out := make([]int, len(t.Columns))
	used, flex := 0, 0
	for i, c := range t.Columns {
		out[i] = c.Width
		used += c.Width + 1
		if c.Flex {
			flex++
		}
	}
	extra := w - used + 1
	if extra > 0 && flex > 0 {
		for i, c := range t.Columns {
			if c.Flex {
				out[i] += extra / flex
			}
		}
	} else if extra < 0 {
		// Shrink from the right until it fits.
		for i := len(out) - 1; i >= 0 && extra < 0; i-- {
			cut := min(-extra, max(0, out[i]-4))
			out[i] -= cut
			extra += cut
		}
	}
	return out
}

// Position renders "12/340" for a panel header.
func (t *Table) Position() string {
	if len(t.view) == 0 {
		return "0/0"
	}
	pos := strconv.Itoa(t.cursor+1) + "/" + strconv.Itoa(len(t.view))
	if t.more {
		pos += "+"
	}
	return pos
}
