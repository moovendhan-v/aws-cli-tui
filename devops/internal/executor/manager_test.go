package executor

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moovendhan-v/aws-cli-tui/devops/internal/aws/auth"
)

func TestBoundedConcurrency(t *testing.T) {
	m := NewManager(2, nil)
	var running, peak atomic.Int32
	var dones []<-chan Done
	for range 6 {
		_, d := m.Start(Spec{Name: "x", Kind: KindAPI}, func(ctx context.Context, j *Job) (any, error) {
			n := running.Add(1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			time.Sleep(20 * time.Millisecond)
			running.Add(-1)
			return nil, nil
		})
		dones = append(dones, d)
	}
	for _, d := range dones {
		<-d
	}
	if peak.Load() > 2 {
		t.Fatalf("peak concurrency %d > 2", peak.Load())
	}
}

func TestCancelAndTimeout(t *testing.T) {
	m := NewManager(4, nil)
	block := func(ctx context.Context, j *Job) (any, error) { <-ctx.Done(); return nil, ctx.Err() }

	j, d := m.Start(Spec{Name: "cancel", Kind: KindAPI}, block)
	time.Sleep(10 * time.Millisecond)
	if !m.Cancel(j.ID) {
		t.Fatal("cancel returned false")
	}
	<-d
	if s := j.View().Status; s != Cancelled {
		t.Fatalf("status = %s, want cancelled", s)
	}

	j2, d2 := m.Start(Spec{Name: "timeout", Kind: KindAPI, Timeout: 20 * time.Millisecond}, block)
	<-d2
	if s := j2.View().Status; s != TimedOut {
		t.Fatalf("status = %s, want timed out", s)
	}

	j3, d3 := m.Start(Spec{Name: "fail", Kind: KindAPI}, func(context.Context, *Job) (any, error) { return nil, errors.New("boom") })
	<-d3
	if s := j3.View().Status; s != Failed {
		t.Fatalf("status = %s, want failed", s)
	}
}

func TestJobsKeepTheirContext(t *testing.T) {
	m := NewManager(1, nil)
	j, d := m.Start(Spec{Name: "a", Kind: KindAPI, Ctx: auth.Context{Profile: "dev", Region: "us-east-1"}},
		func(context.Context, *Job) (any, error) { return nil, nil })
	<-d
	if v := j.View(); v.Ctx.Profile != "dev" || v.Ctx.Region != "us-east-1" {
		t.Fatalf("context changed: %+v", v.Ctx)
	}
}

func TestOutputIsRedactedAndBounded(t *testing.T) {
	j := &Job{out: newRing(3)}
	j.Write("key AKIAABCDEFGHIJKLMNOP here")
	if got := j.Lines(0)[0]; got != "key [REDACTED] here" {
		t.Fatalf("not redacted: %q", got)
	}
	for _, s := range []string{"a", "b", "c", "d"} {
		j.Write(s)
	}
	if got := j.Lines(0); len(got) != 3 || got[0] != "b" {
		t.Fatalf("ring = %v", got)
	}
	if got := j.Lines(4); len(got) != 1 || got[0] != "d" {
		t.Fatalf("since(4) = %v", got)
	}
}

func TestProcessAllowlist(t *testing.T) {
	if _, err := Command(context.Background(), []string{"sh", "-c", "id"}); err == nil {
		t.Fatal("sh should not be allowlisted")
	}
}
