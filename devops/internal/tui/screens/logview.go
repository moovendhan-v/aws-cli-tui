package screens

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"

	"devops/internal/aws/console"
	"devops/internal/aws/logs"
	"devops/internal/aws/meter"
	"devops/internal/executor"
	"devops/internal/safety"
	"devops/internal/tui/components"
	"devops/internal/tui/core"
	"devops/internal/tui/theme"
)

const errorPattern = "?ERROR ?Error ?error ?Exception ?exception ?timeout ?Timeout ?FATAL"

// eventBuf receives tail events from the executor goroutine.
type eventBuf struct {
	mu sync.Mutex
	ev []logs.Event
}

func (b *eventBuf) push(e logs.Event) { b.mu.Lock(); b.ev = append(b.ev, e); b.mu.Unlock() }
func (b *eventBuf) drain() []logs.Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := b.ev
	b.ev = nil
	return out
}

// logViewer shows recent history for a log group, loads older events on
// demand (scroll up), and can follow new events live.
type logViewer struct {
	group   logs.Group
	pattern string
	seq     int // bumps when the pattern changes; stale history results are dropped

	events       []logs.Event // ascending
	history      *logs.History
	loadingOlder bool
	historyErr   error
	firstLoad    bool

	tailJob  *executor.Job
	tailBuf  *eventBuf
	paused   *atomic.Bool
	live     bool
	boundary map[string]bool // events at the tail start timestamp (dedupe)

	offset   int
	follow   bool
	wrap     bool
	streams  bool
	pretty   bool
	hoff     int
	height   int
	width    int
	rendered []string
	renderID string

	editing   bool
	patternIn filterBar
}

func newLogViewer(g logs.Group) *logViewer {
	v := &logViewer{group: g, follow: true, patternIn: newFilter(), paused: &atomic.Bool{}, tailBuf: &eventBuf{}}
	v.patternIn.input.Placeholder = `CloudWatch filter pattern, e.g. ERROR or { $.level = "error" }`
	return v
}

func (v *logViewer) key(kind string) string { return "log:" + kind + ":" + strconv.Itoa(v.seq) }

func (v *logViewer) start(e *core.Env) tea.Cmd {
	v.wrap = e.Deps.Config.UI.Wrap
	v.live = e.Deps.Config.Logs.TailMode == "live"
	v.history = logs.NewHistory(e.Session, v.group.Name, v.pattern, e.Deps.Config.Logs.HistoryWindow.Duration, v.group.Created)
	v.events, v.firstLoad, v.historyErr = nil, true, nil
	v.rendered, v.renderID = nil, ""
	return v.loadOlder(e)
}

func (v *logViewer) loadOlder(e *core.Env) tea.Cmd {
	if v.loadingOlder || v.history == nil || v.history.Done {
		return nil
	}
	v.loadingOlder = true
	h, n := v.history, e.Deps.Config.Logs.PageEvents
	name := "Load log history"
	if !v.firstLoad {
		name = "Load older log events"
	}
	return e.Run("logs", v.key("older"), executor.Spec{Name: name, Quiet: true, Resources: []string{v.group.Name}, Timeout: 60 * time.Second},
		func(ctx context.Context, _ *executor.Job) (any, error) { return h.Older(ctx, n) })
}

func (v *logViewer) startTail(e *core.Env) tea.Cmd {
	from := time.Now().Add(-time.Minute)
	v.boundary = map[string]bool{}
	if n := len(v.events); n > 0 {
		from = v.events[n-1].Time
		for i := n - 1; i >= 0 && v.events[i].Time.Equal(from); i-- {
			v.boundary[evKey(v.events[i])] = true
		}
	}
	sess, buf, paused := e.Session, v.tailBuf, v.paused
	opts := logs.TailOptions{Group: v.group.Name, GroupARN: v.group.ARN, Pattern: v.pattern, From: from,
		Interval: e.Deps.Config.Logs.PollInterval.Duration, Paused: paused, Live: v.live && v.group.ARN != ""}
	paused.Store(false)
	label := "Tail " + v.group.Name
	if opts.Live {
		label = "Live Tail " + v.group.Name
	}
	job, cmd := e.RunJob("logs", v.key("tail"), executor.Spec{Name: label, Kind: executor.KindStream, Resources: []string{v.group.Name}},
		func(ctx context.Context, j *executor.Job) (any, error) {
			return nil, logs.Tail(ctx, sess, opts, func(ev logs.Event) {
				buf.push(ev)
				j.Write(ev.Time.Local().Format("15:04:05") + "  " + strings.TrimRight(ev.Message, "\n"))
			})
		})
	v.tailJob = job
	v.follow = true
	if opts.Live {
		return tea.Batch(cmd, e.Toast("Live Tail started — "+meter.BilledNote("CloudWatch Logs", "StartLiveTail"), theme.ToneWarn))
	}
	return cmd
}

func (v *logViewer) tailing() bool { return v.tailJob != nil && !v.tailJob.View().Status.Done() }

func (v *logViewer) stopTail(e *core.Env) {
	if v.tailJob != nil {
		e.Deps.Exec.Cancel(v.tailJob.ID)
		v.tailJob = nil
	}
}

func (v *logViewer) stop(e *core.Env) { v.stopTail(e) }

// detach leaves the tail running in the background (visible in activity).
func (v *logViewer) detach(e *core.Env) tea.Cmd {
	if !v.tailing() {
		return nil
	}
	return e.Toast("Tail continues in the background — Running Commands (Ctrl+J)", theme.ToneNormal)
}

func (v *logViewer) back() bool {
	if v.editing {
		v.editing = false
		return true
	}
	return false
}

func (v *logViewer) hints() []core.Hint {
	tail := "Live tail"
	if v.tailing() {
		tail = "Stop tail"
	}
	h := []core.Hint{{Key: "t", Label: tail}}
	if v.tailing() {
		pause := "Pause"
		if v.paused.Load() {
			pause = "Resume"
		}
		h = append(h, core.Hint{Key: "space", Label: pause})
	}
	return append(h, core.Hint{Key: "↑/o", Label: "Older"}, core.Hint{Key: "e", Label: "Errors"}, core.Hint{Key: "f", Label: "Filter"},
		core.Hint{Key: "w", Label: "Wrap"}, core.Hint{Key: "j", Label: "JSON"}, core.Hint{Key: "s", Label: "Streams"},
		core.Hint{Key: "m", Label: "Poll/Live"}, core.Hint{Key: "O", Label: "Console"}, core.Hint{Key: "b", Label: "Background"}, core.Hint{Key: "esc", Label: "Close"})
}

func (v *logViewer) setPattern(e *core.Env, p string) tea.Cmd {
	wasTailing := v.tailing()
	v.stopTail(e)
	v.pattern = p
	v.seq++
	v.loadingOlder = false
	v.follow = true
	cmd := v.start(e)
	if wasTailing {
		return tea.Batch(cmd, v.startTail(e))
	}
	return cmd
}

func (v *logViewer) update(e *core.Env, msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case core.TickMsg:
		if ev := v.tailBuf.drain(); len(ev) > 0 {
			for _, x := range ev {
				if v.boundary[evKey(x)] {
					continue
				}
				v.events = append(v.events, x)
			}
		}
	case core.ResultMsg:
		if m.Key != v.key("older") {
			return nil
		}
		v.loadingOlder = false
		v.historyErr = m.Err
		older, _ := m.Value.([]logs.Event)
		if len(older) > 0 {
			before := len(v.renderAll(v.lastW()))
			v.events = append(older, v.events...)
			v.renderID = ""
			if !v.follow && !v.firstLoad {
				v.offset += len(v.renderAll(v.lastW())) - before
			}
		}
		v.firstLoad = false
	case core.MouseMsg:
		switch {
		case m.WheelUp:
			return v.scroll(e, -3)
		case m.WheelDown:
			return v.scroll(e, 3)
		}
	case tea.KeyMsg:
		return v.key2(e, m)
	}
	return nil
}

func (v *logViewer) key2(e *core.Env, m tea.KeyMsg) tea.Cmd {
	if v.editing {
		switch m.String() {
		case "enter":
			v.editing = false
			v.patternIn.input.Blur()
			return v.setPattern(e, strings.TrimSpace(v.patternIn.value()))
		case "esc":
			v.editing = false
			v.patternIn.set(v.pattern)
			return nil
		}
		_, cmd := v.patternIn.update(m)
		v.patternIn.active = true
		return cmd
	}
	page := max(1, v.height-1)
	switch m.String() {
	case "t":
		if v.tailing() {
			v.stopTail(e)
			return nil
		}
		return v.startTail(e)
	case " ", "p":
		if v.tailing() {
			v.paused.Store(!v.paused.Load())
		}
	case "m":
		v.live = !v.live
		mode := "polling (FilterLogEvents)"
		if v.live {
			mode = "Live Tail — billed per session minute"
			if v.group.ARN == "" {
				mode = "Live Tail needs the group ARN; open the group from the list"
			}
		}
		cmds := []tea.Cmd{e.Toast("Tail mode: "+mode, theme.ToneNormal)}
		if v.tailing() {
			v.stopTail(e)
			cmds = append(cmds, v.startTail(e))
		}
		return tea.Batch(cmds...)
	case "e":
		p := errorPattern
		if v.pattern == errorPattern {
			p = ""
		}
		return v.setPattern(e, p)
	case "f":
		v.editing = true
		v.patternIn.set(v.pattern)
		return v.patternIn.start()
	case "w":
		v.wrap = !v.wrap
		v.renderID = ""
	case "j":
		v.pretty = !v.pretty
		v.renderID = ""
	case "s":
		v.streams = !v.streams
		v.renderID = ""
	case "o":
		return v.loadOlder(e)
	case "O":
		return e.OpenURL(console.LogGroup(e.Ctx().Region, v.group.Name), false)
	case "left", "h":
		if !v.wrap {
			v.hoff = max(0, v.hoff-10)
			v.renderID = ""
		}
	case "right", "l":
		if !v.wrap {
			v.hoff += 10
			v.renderID = ""
		}
	case "up", "k":
		return v.scroll(e, -1)
	case "down":
		return v.scroll(e, 1)
	case "pgup", "ctrl+u":
		return v.scroll(e, -page)
	case "pgdown", "ctrl+d":
		return v.scroll(e, page)
	case "g", "home":
		v.follow, v.offset = false, 0
		return v.loadOlder(e)
	case "G", "end":
		v.follow = true
	}
	return nil
}

func (v *logViewer) scroll(e *core.Env, n int) tea.Cmd {
	v.offset += n
	if n < 0 {
		v.follow = false
	}
	if v.offset <= 0 {
		v.offset = 0
		return v.loadOlder(e) // reached the top: fetch older events
	}
	return nil
}

func (v *logViewer) lastW() int { return max(20, v.width) }

func evKey(e logs.Event) string { return e.Time.String() + "\x1f" + e.Stream + "\x1f" + e.Message }

// renderAll turns events into visual lines for width w (cached).
func (v *logViewer) renderAll(w int) []string {
	id := fmt.Sprint(w, v.wrap, v.streams, v.pretty, v.hoff, len(v.events))
	if len(v.events) > 0 {
		id += v.events[0].Time.String()
	}
	if id == v.renderID {
		return v.rendered
	}
	var out []string
	for _, ev := range v.events {
		prefix := theme.Dim.Render(ev.Time.Local().Format("01-02 15:04:05") + " ")
		pw := 15
		if v.streams {
			s := ev.Stream
			if len(s) > 22 {
				s = "…" + s[len(s)-21:]
			}
			prefix += theme.Dim.Render(components.Pad(s, 22) + " ")
			pw += 23
		}
		msg := safety.Redact(strings.TrimRight(ev.Message, "\n"))
		if v.pretty {
			msg = prettyJSON(msg)
		}
		indent := strings.Repeat(" ", pw)
		first := true
		for _, raw := range strings.Split(msg, "\n") {
			var parts []string
			if v.wrap {
				parts = components.WrapLines(raw, max(10, w-pw))
			} else {
				parts = []string{components.Truncate(ansi.Cut(raw, v.hoff, v.hoff+w-pw+1), w-pw)}
			}
			for _, p := range parts {
				lead := indent
				if first {
					lead, first = prefix, false
				}
				out = append(out, lead+core.ColorLine(p))
			}
		}
	}
	v.rendered, v.renderID = out, id
	return out
}

func prettyJSON(s string) string {
	t := strings.TrimSpace(s)
	if !strings.HasPrefix(t, "{") && !strings.HasPrefix(t, "[") {
		return s
	}
	var x any
	if json.Unmarshal([]byte(t), &x) != nil {
		return s
	}
	b, err := json.MarshalIndent(x, "", "  ")
	if err != nil {
		return s
	}
	return string(b)
}

func (v *logViewer) view(e *core.Env, w, h int) string {
	v.width = w
	var b strings.Builder
	state := theme.Dim.Render("■ history")
	if v.tailing() {
		mode := "tailing"
		if v.live && v.group.ARN != "" {
			mode = "live tail $"
		}
		switch {
		case v.paused.Load():
			state = theme.Warn.Render("❚❚ paused")
		case e.Pulse():
			state = theme.OK.Render("● " + mode)
		default:
			state = theme.Dim.Render("● " + mode)
		}
	} else if v.tailJob != nil {
		if tv := v.tailJob.View(); tv.Err != nil {
			state = theme.Bad.Render("✗ tail " + string(tv.ErrKind))
		}
	}
	b.WriteString(headingRaw(v.group.Name, fmt.Sprintf("%d events loaded", len(v.events)), w) + "\n")

	pat := "all events"
	switch v.pattern {
	case "":
	case errorPattern:
		pat = "errors only"
	default:
		pat = "filter: " + v.pattern
	}
	meta := state + theme.Dim.Render("  ·  "+pat)
	if v.loadingOlder {
		meta += theme.Warn.Render("  ·  " + e.Spinner() + " loading older…")
	} else if v.history != nil && v.history.Done {
		meta += theme.Dim.Render("  ·  start of history")
	}
	if !v.follow {
		meta += theme.Warn.Render("  ·  scrolled (G to follow)")
	}
	if !v.wrap {
		meta += theme.Dim.Render("  ·  nowrap ←→")
	}
	b.WriteString(components.Truncate(meta, w) + "\n")
	b.WriteString(histogram(v.events, w) + "\n")
	if v.editing {
		b.WriteString(theme.Prompt.Render("Pattern: > ") + v.patternIn.input.View() + "\n")
	}
	if v.historyErr != nil {
		b.WriteString(components.Wrap(theme.Bad.Render("✗ "+safety.Describe(v.historyErr))+"  "+theme.Warn.Render(safety.Hint(safety.Classify(v.historyErr))), w) + "\n")
	}
	if tv := v.tailJobView(); tv != nil && tv.Err != nil && tv.Status.Done() {
		b.WriteString(components.Wrap(theme.Bad.Render("✗ tail: "+safety.Describe(tv.Err)), w) + "\n")
	}
	head := b.String()
	body := h - strings.Count(head, "\n")
	v.height = body

	lines := v.renderAll(w)
	maxOff := max(0, len(lines)-body)
	if v.follow || v.offset > maxOff {
		v.offset = maxOff
	}
	if v.offset == maxOff && !v.follow && len(lines) > 0 && v.offset > 0 {
		v.follow = true
	}
	var out []string
	switch {
	case len(lines) == 0 && (v.loadingOlder || v.firstLoad):
		out = []string{theme.Dim.Render(e.Spinner() + " loading recent events…")}
	case len(lines) == 0:
		msg := "No events found"
		if v.pattern != "" {
			msg += " matching the filter"
		}
		out = []string{theme.Dim.Render(msg + ". Press t to follow new events, or e/f to change the filter.")}
	default:
		out = lines[v.offset:min(len(lines), v.offset+body)]
	}
	return head + components.Fit(strings.Join(out, "\n"), w, body)
}

func (v *logViewer) tailJobView() *executor.View {
	if v.tailJob == nil {
		return nil
	}
	tv := v.tailJob.View()
	return &tv
}

// histogram renders events per time bucket across the loaded range; buckets
// containing error-looking events are red.
func histogram(events []logs.Event, w int) string {
	if len(events) < 2 {
		return theme.Dim.Render(strings.Repeat("─", w))
	}
	first, last := events[0].Time, events[len(events)-1].Time
	label := " " + first.Local().Format("15:04") + " "
	end := " " + last.Local().Format("15:04")
	n := w - len(label) - len(end)
	if n < 8 {
		return ""
	}
	span := last.Sub(first)
	if span <= 0 {
		span = time.Second
	}
	counts := make([]int, n)
	errs := make([]bool, n)
	for _, e := range events {
		i := int(float64(e.Time.Sub(first)) / float64(span) * float64(n-1))
		i = max(0, min(n-1, i))
		counts[i]++
		u := strings.ToUpper(e.Message)
		if strings.Contains(u, "ERROR") || strings.Contains(u, "EXCEPTION") || strings.Contains(u, "FATAL") {
			errs[i] = true
		}
	}
	peak := 0
	for _, c := range counts {
		peak = max(peak, c)
	}
	bars := []rune("▁▂▃▄▅▆▇█")
	var b strings.Builder
	for i, c := range counts {
		if c == 0 {
			b.WriteString(theme.Dim.Render("·"))
			continue
		}
		r := string(bars[min(len(bars)-1, c*(len(bars)-1)/max(1, peak))])
		if errs[i] {
			b.WriteString(theme.Bad.Render(r))
		} else {
			b.WriteString(theme.Title.Render(r))
		}
	}
	return theme.Dim.Render(label) + b.String() + theme.Dim.Render(end)
}
