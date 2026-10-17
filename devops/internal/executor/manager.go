// Package executor runs every operation the tool launches: bounded
// concurrency, cancellation, timeouts, output capture and activity tracking.
package executor

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"devops/internal/aws/auth"
	"devops/internal/safety"
)

type Status string

const (
	Queued    Status = "queued"
	Running   Status = "running"
	Completed Status = "completed"
	Failed    Status = "failed"
	Cancelled Status = "cancelled"
	TimedOut  Status = "timed out"
)

func (s Status) Done() bool { return s != Queued && s != Running }

type Kind string

const (
	KindAPI         Kind = "api"
	KindStream      Kind = "stream"
	KindProcess     Kind = "process"
	KindInteractive Kind = "interactive"
	KindWorkflow    Kind = "workflow"
)

// Spec describes an operation before it runs. Ctx is captured at submission so
// a later profile/region switch never changes where a running job points.
type Spec struct {
	Name      string
	CommandID string
	Args      map[string]string // already sanitized
	Ctx       auth.Context
	Kind      Kind
	Risk      safety.Risk
	Timeout   time.Duration
	Resources []string
	// Quiet jobs (background resource discovery) are tracked but de-emphasised.
	Quiet bool
}

type Job struct {
	ID int64
	Spec

	mu       sync.Mutex
	status   Status
	started  time.Time
	ended    time.Time
	exitCode *int
	err      error
	out      *ring
	cancel   context.CancelFunc
	userStop bool
	results  []Result
}

// Result is a structured value produced by a job (one per workflow step).
type Result struct {
	Title string
	Value any
	Err   error
}

// AddResult records a structured result for table/cards/YAML views.
func (j *Job) AddResult(title string, v any, err error) {
	j.mu.Lock()
	j.results = append(j.results, Result{Title: title, Value: v, Err: err})
	j.mu.Unlock()
}

// Results returns the structured results recorded so far.
func (j *Job) Results() []Result {
	j.mu.Lock()
	defer j.mu.Unlock()
	return append([]Result(nil), j.results...)
}

// View is an immutable snapshot of a job.
type View struct {
	ID int64
	Spec
	Status   Status
	Started  time.Time
	Ended    time.Time
	ExitCode *int
	Err      error
	ErrKind  safety.ErrorKind
	Lines    int
}

func (v View) Elapsed() time.Duration {
	if v.Started.IsZero() {
		return 0
	}
	if v.Ended.IsZero() {
		return time.Since(v.Started)
	}
	return v.Ended.Sub(v.Started)
}

func (j *Job) View() View {
	j.mu.Lock()
	defer j.mu.Unlock()
	return View{ID: j.ID, Spec: j.Spec, Status: j.status, Started: j.started, Ended: j.ended,
		ExitCode: j.exitCode, Err: j.err, ErrKind: safety.Classify(j.err), Lines: j.out.len()}
}

// Printf appends a redacted output line.
func (j *Job) Printf(format string, a ...any) { j.Write(fmt.Sprintf(format, a...)) }

// Write appends redacted output, splitting on newlines.
func (j *Job) Write(s string) {
	s = safety.Redact(strings.TrimRight(s, "\n"))
	j.mu.Lock()
	defer j.mu.Unlock()
	for _, line := range strings.Split(s, "\n") {
		j.out.push(strings.TrimRight(line, "\r"))
	}
}

// WriteValue renders a structured result as indented JSON (or lines for []string).
func (j *Job) WriteValue(v any) {
	switch t := v.(type) {
	case nil:
		return
	case []string:
		if len(t) == 0 {
			j.Write("(no results)")
		}
		for _, l := range t {
			j.Write(l)
		}
		return
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		j.Printf("%v", v)
		return
	}
	if string(b) == "null" || string(b) == "[]" {
		j.Write("(no results)")
		return
	}
	j.Write(string(b))
}

func (j *Job) Lines(from int) []string {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.out.since(from)
}

func (j *Job) Output() string { return strings.Join(j.Lines(0), "\n") }

// Recorder persists finished jobs (implemented by storage).
type Recorder interface {
	RecordJob(v View, output string)
}

type Manager struct {
	mu       sync.Mutex
	jobs     []*Job
	nextID   int64
	sem      chan struct{}
	recorder Recorder
}

func NewManager(maxConcurrent int, r Recorder) *Manager {
	return &Manager{sem: make(chan struct{}, max(1, maxConcurrent)), recorder: r}
}

type Func func(ctx context.Context, j *Job) (any, error)

type Done struct {
	Job    *Job
	Result any
	Err    error
}

func (m *Manager) newJob(spec Spec) *Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextID++
	j := &Job{ID: m.nextID, Spec: spec, status: Queued, out: newRing(5000)}
	m.jobs = append(m.jobs, j)
	return j
}

// Start runs fn asynchronously. API and workflow jobs share the bounded
// concurrency pool; streams hold no slot so they can't starve other work.
func (m *Manager) Start(spec Spec, fn Func) (*Job, <-chan Done) {
	j := m.newJob(spec)
	done := make(chan Done, 1)

	ctx, cancel := context.WithCancel(context.Background())
	j.mu.Lock()
	j.cancel = cancel
	j.mu.Unlock()

	go func() {
		defer cancel()
		pooled := spec.Kind == KindAPI || spec.Kind == KindWorkflow || spec.Kind == KindProcess
		if pooled {
			select {
			case m.sem <- struct{}{}:
				defer func() { <-m.sem }()
			case <-ctx.Done():
				m.finish(j, ctx, ctx.Err())
				done <- Done{Job: j, Err: ctx.Err()}
				return
			}
		}
		runCtx := ctx
		if spec.Timeout > 0 && spec.Kind != KindStream {
			var c context.CancelFunc
			runCtx, c = context.WithTimeout(ctx, spec.Timeout)
			defer c()
		}
		j.mu.Lock()
		j.status, j.started = Running, time.Now()
		j.mu.Unlock()

		res, err := fn(runCtx, j)
		m.finish(j, runCtx, err)
		done <- Done{Job: j, Result: res, Err: err}
	}()
	return j, done
}

func (m *Manager) finish(j *Job, ctx context.Context, err error) {
	j.mu.Lock()
	if j.started.IsZero() {
		j.started = time.Now()
	}
	j.ended = time.Now()
	j.err = err
	switch {
	case err == nil:
		j.status = Completed
	case j.userStop:
		j.status = Cancelled
		if j.Kind == KindStream {
			j.err = nil // stopping a tail is the normal way to end it
		}
	case errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.DeadlineExceeded):
		j.status = TimedOut
	default:
		j.status = Failed
	}
	j.mu.Unlock()
	m.record(j)
}

func (m *Manager) record(j *Job) {
	if m.recorder == nil || j.Quiet {
		return
	}
	m.recorder.RecordJob(j.View(), j.Output())
}

// BeginInteractive registers a session that owns the terminal (SSM, ECS Exec).
func (m *Manager) BeginInteractive(spec Spec) *Job {
	spec.Kind = KindInteractive
	j := m.newJob(spec)
	j.mu.Lock()
	j.status, j.started = Running, time.Now()
	j.mu.Unlock()
	return j
}

// EndInteractive records the session result once the terminal is returned.
func (m *Manager) EndInteractive(j *Job, err error, exitCode int) {
	j.mu.Lock()
	j.ended = time.Now()
	j.exitCode = &exitCode
	j.err = err
	if err == nil {
		j.status = Completed
	} else {
		j.status = Failed
	}
	j.mu.Unlock()
	m.record(j)
}

func (j *Job) setExit(code int) {
	j.mu.Lock()
	j.exitCode = &code
	j.mu.Unlock()
}

// Cancel stops a job. It reports false if the job already finished.
func (m *Manager) Cancel(id int64) bool {
	j := m.Get(id)
	if j == nil {
		return false
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.status.Done() || j.cancel == nil {
		return false
	}
	j.userStop = true
	j.cancel()
	return true
}

func (m *Manager) Get(id int64) *Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, j := range m.jobs {
		if j.ID == id {
			return j
		}
	}
	return nil
}

// Views returns snapshots, newest first.
func (m *Manager) Views() []View {
	m.mu.Lock()
	jobs := append([]*Job(nil), m.jobs...)
	m.mu.Unlock()
	out := make([]View, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, j.View())
	}
	sort.Slice(out, func(a, b int) bool { return out[a].ID > out[b].ID })
	return out
}

func (m *Manager) RunningCount() int {
	n := 0
	for _, v := range m.Views() {
		if !v.Status.Done() {
			n++
		}
	}
	return n
}

// CancelAll stops every running job (used on quit).
func (m *Manager) CancelAll() {
	for _, v := range m.Views() {
		if !v.Status.Done() && v.Kind != KindInteractive {
			m.Cancel(v.ID)
		}
	}
}
