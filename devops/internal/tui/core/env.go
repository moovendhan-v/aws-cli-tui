// Package core defines the contract between the TUI shell and its screens:
// the shared Env, messages, modals and the command execution flow.
package core

import (
	"context"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"devops/internal/app"
	"devops/internal/aws/auth"
	"devops/internal/cache"
	"devops/internal/commands"
	"devops/internal/executor"
	"devops/internal/tui/theme"
)

// Screen is a workspace view selected from the navigation.
type Screen interface {
	ID() string
	// Init runs on first activation and after every profile/region switch.
	Init(e *Env) tea.Cmd
	Update(e *Env, msg tea.Msg) tea.Cmd
	View(e *Env, w, h int) string
	Hints() []Hint
	// Capturing is true while the screen owns text input (filters etc.), so
	// single-letter global shortcuts are not intercepted.
	Capturing() bool
}

// Focuser screens accept a query routed from the global search bar.
type Focuser interface {
	Focus(e *Env, query string) tea.Cmd
}

type Hint struct{ Key, Label string }

// Modal is an overlay that receives all input while open.
type Modal interface {
	Title() string
	Update(e *Env, msg tea.Msg) (closed bool, cmd tea.Cmd)
	View(e *Env, w, h int) string
	Size(maxW, maxH int) (w, h int)
}

type (
	// ResultMsg delivers an executor result to the screen that asked for it.
	ResultMsg struct {
		Screen, Key string
		Gen         int
		Job         *executor.Job
		Value       any
		Err         error
		// CachedAt is set when Value came from the cache instead of AWS;
		// Refreshing means a background refresh for it is in flight.
		CachedAt   time.Time
		Refreshing bool
	}
	TickMsg     struct{ Time time.Time }
	NavigateMsg struct{ Screen, Query string }
	ToastMsg    struct {
		Text string
		Tone theme.Tone
	}
	OpenModalMsg       struct{ Modal Modal }
	InteractiveDoneMsg struct {
		Job *executor.Job
		Err error
	}
	SwitchContextMsg struct{ Profile, Region string }
	// OpenTerminalMsg asks the shell to start an embedded interactive session.
	OpenTerminalMsg struct{ Prepared *commands.Prepared }
	// FocusMsg moves keyboard focus ("nav" or "workspace").
	FocusMsg struct{ Area string }
	// MouseMsg is a mouse event translated to workspace-content coordinates.
	MouseMsg struct {
		X, Y               int
		Click, Double      bool
		WheelUp, WheelDown bool
	}
	IdentityMsg struct {
		Gen          int
		Account, ARN string
		Err          error
	}
)

// IndexItem is a discovered resource the global search can jump to.
type IndexItem struct {
	Screen, Label, Detail string
}

type Env struct {
	Deps        *app.Deps
	Session     *auth.Session
	Gen         int // bumps on every context switch; stale results are dropped
	Tick        int
	Identifying bool
	IdentityErr error

	index map[string][]IndexItem
}

func NewEnv(d *app.Deps, s *auth.Session) *Env {
	return &Env{Deps: d, Session: s, index: map[string][]IndexItem{}}
}

// Ctx is the active caller context.
func (e *Env) Ctx() auth.Context { return e.Session.Ctx }

func (e *Env) Prod() bool {
	return e.Deps.Production.Is(e.Session.Ctx.Profile, e.Session.Ctx.AccountID)
}

// Run executes fn through the shared executor, bound to the current context,
// and routes the result back to screen as a ResultMsg.
func (e *Env) Run(screen, key string, spec executor.Spec, fn executor.Func) tea.Cmd {
	_, cmd := e.RunJob(screen, key, spec, fn)
	return cmd
}

// CacheKey builds a cache key bound to the active profile/region.
func (e *Env) CacheKey(kind string, parts ...string) string {
	return cache.Key(e.Session.Ctx.Profile, e.Session.Ctx.Region, kind, parts...)
}

// Cached serves a result from cache when fresh. When the entry is stale it
// returns the cached value immediately and refreshes in the background; on a
// miss, or when force is set (the user pressed r), it fetches from AWS.
// Successful fetches are written back to the cache.
func (e *Env) Cached(screen, key, cacheKey string, force bool, spec executor.Spec, fn executor.Func) tea.Cmd {
	entry, ok, fresh := e.Deps.Cache.Get(cacheKey)
	gen := e.Gen
	var cmds []tea.Cmd
	if ok && !force {
		cmds = append(cmds, Msg(ResultMsg{Screen: screen, Key: key, Gen: gen, Value: entry.Value, CachedAt: entry.At, Refreshing: !fresh}))
		if fresh {
			return cmds[0]
		}
	}
	store := e.Deps.Cache
	cmds = append(cmds, e.Run(screen, key, spec, func(ctx context.Context, j *executor.Job) (any, error) {
		v, err := fn(ctx, j)
		if err == nil {
			store.Put(cacheKey, v)
		}
		return v, err
	}))
	return tea.Sequence(cmds...)
}

// RunJob is Run that also returns the job (for live output such as log tails).
func (e *Env) RunJob(screen, key string, spec executor.Spec, fn executor.Func) (*executor.Job, tea.Cmd) {
	if spec.Ctx == (auth.Context{}) {
		spec.Ctx = e.Session.Ctx
	}
	if spec.Kind == "" {
		spec.Kind = executor.KindAPI
	}
	if spec.Timeout == 0 && spec.Kind == executor.KindAPI {
		spec.Timeout = e.Deps.Config.Execution.DefaultTimeout.Duration
	}
	job, done := e.Deps.Exec.Start(spec, fn)
	gen := e.Gen
	return job, func() tea.Msg {
		d := <-done
		return ResultMsg{Screen: screen, Key: key, Gen: gen, Job: d.Job, Value: d.Result, Err: d.Err}
	}
}

// Msg wraps a message as a command.
func Msg(m tea.Msg) tea.Cmd { return func() tea.Msg { return m } }

func (e *Env) Navigate(screen, query string) tea.Cmd { return Msg(NavigateMsg{screen, query}) }

func (e *Env) Toast(text string, tone theme.Tone) tea.Cmd { return Msg(ToastMsg{text, tone}) }

func (e *Env) Open(m Modal) tea.Cmd { return Msg(OpenModalMsg{m}) }

// SetIndex replaces the searchable resources contributed by a screen.
func (e *Env) SetIndex(screen string, items []IndexItem) { e.index[screen] = items }

func (e *Env) ClearIndex() { e.index = map[string][]IndexItem{} }

func (e *Env) Index() []IndexItem {
	var out []IndexItem
	for _, items := range e.index {
		out = append(out, items...)
	}
	return out
}

var spinner = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// Spinner returns the current spinner frame.
func (e *Env) Spinner() string { return spinner[e.Tick%len(spinner)] }

// Pulse alternates every second (ticks are 250ms), like the mock's pulsing dot.
func (e *Env) Pulse() bool { return (e.Tick/4)%2 == 0 }
