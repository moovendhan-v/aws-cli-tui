package tests

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moovendhan-v/aws-cli-tui/devops/internal/aws/auth"
	"github.com/moovendhan-v/aws-cli-tui/devops/internal/aws/logs"
	"github.com/moovendhan-v/aws-cli-tui/devops/internal/aws/meter"
)

type fakeEvent struct {
	TS  int64  `json:"timestamp"`
	Msg string `json:"message"`
}

// fakeLogs emulates the CloudWatch Logs JSON API for two streams.
type fakeLogs struct {
	mu          sync.Mutex
	streams     map[string][]fakeEvent // ascending
	emptyPages  int                    // FilterLogEvents returns this many empty pages first
	filterCalls int
}

func (f *fakeLogs) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var in map[string]any
	json.NewDecoder(r.Body).Decode(&in)
	op := r.Header.Get("X-Amz-Target")
	op = op[strings.Index(op, ".")+1:]
	out := map[string]any{}
	switch op {
	case "DescribeLogStreams":
		var ls []map[string]any
		for name, ev := range f.streams {
			ls = append(ls, map[string]any{"logStreamName": name, "lastEventTimestamp": ev[len(ev)-1].TS})
		}
		out["logStreams"] = ls
	case "GetLogEvents":
		ev := f.streams[in["logStreamName"].(string)]
		end := len(ev)
		if tok, ok := in["nextToken"].(string); ok {
			end, _ = strconv.Atoi(strings.TrimPrefix(tok, "b/"))
		}
		start := max(0, end-3) // small pages to exercise backward paging
		out["events"] = ev[start:end]
		out["nextBackwardToken"] = "b/" + strconv.Itoa(start)
		out["nextForwardToken"] = "f/" + strconv.Itoa(end)
	case "FilterLogEvents":
		f.filterCalls++
		tok, _ := in["nextToken"].(string)
		n, _ := strconv.Atoi(strings.TrimPrefix(tok, "p"))
		if n < f.emptyPages {
			out["events"] = []any{}
			out["nextToken"] = "p" + strconv.Itoa(n+1)
			break
		}
		out["events"] = []map[string]any{{"eventId": "e1", "timestamp": time.Now().UnixMilli(), "message": "hello after empty pages", "logStreamName": "a"}}
	default:
		w.WriteHeader(400)
		return
	}
	w.Header().Set("Content-Type", "application/x-amz-json-1.1")
	json.NewEncoder(w).Encode(out)
}

func fakeSession(t *testing.T, h http.Handler) *auth.Session {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "config"), []byte(""), 0o600)
	t.Setenv("AWS_CONFIG_FILE", filepath.Join(dir, "config"))
	t.Setenv("AWS_SHARED_CREDENTIALS_FILE", filepath.Join(dir, "none"))
	t.Setenv("AWS_ACCESS_KEY_ID", "AKIDEXAMPLE")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test-secret")
	t.Setenv("AWS_ENDPOINT_URL", srv.URL)
	t.Setenv("AWS_PROFILE", "")
	s := auth.NewSession(context.Background(), "", "us-east-1")
	if s.LoadErr != nil {
		t.Fatal(s.LoadErr)
	}
	return s
}

func TestHistoryMergesNewestFirstAndPagesOlder(t *testing.T) {
	base := time.Now().Add(-time.Hour).UnixMilli()
	f := &fakeLogs{streams: map[string][]fakeEvent{}}
	// Interleave 20 events across two streams: even seconds in a, odd in b.
	for i := range 20 {
		name := "a"
		if i%2 == 1 {
			name = "b"
		}
		f.streams[name] = append(f.streams[name], fakeEvent{TS: base + int64(i)*1000, Msg: fmt.Sprintf("event-%02d", i)})
	}
	s := fakeSession(t, f)
	h := logs.NewHistory(s, "/app", "", 0, time.Time{})

	first, err := h.Older(context.Background(), 5)
	if err != nil {
		t.Fatal(err)
	}
	if got := msgs(first); got != "event-15 event-16 event-17 event-18 event-19" {
		t.Fatalf("first page = %s", got)
	}
	second, _ := h.Older(context.Background(), 5)
	if got := msgs(second); got != "event-10 event-11 event-12 event-13 event-14" {
		t.Fatalf("second page = %s", got)
	}
	var rest []logs.Event
	for !h.Done {
		page, err := h.Older(context.Background(), 5)
		if err != nil {
			t.Fatal(err)
		}
		rest = append(page, rest...)
	}
	if len(rest) != 10 || rest[0].Message != "event-00" {
		t.Fatalf("remaining = %s", msgs(rest))
	}
}

func TestTailContinuesPastEmptyPages(t *testing.T) {
	f := &fakeLogs{emptyPages: 25} // more than one poll's page budget
	s := fakeSession(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	got := make(chan logs.Event, 1)
	go logs.Tail(ctx, s, logs.TailOptions{Group: "/app", From: time.Now().Add(-time.Minute), Interval: 10 * time.Millisecond},
		func(e logs.Event) { got <- e })
	select {
	case e := <-got:
		if e.Message != "hello after empty pages" {
			t.Fatalf("unexpected event %q", e.Message)
		}
	case <-ctx.Done():
		t.Fatalf("tail never delivered the event (FilterLogEvents calls: %d)", f.filterCalls)
	}
}

func TestMeterCountsRequests(t *testing.T) {
	f := &fakeLogs{streams: map[string][]fakeEvent{"a": {{TS: 1, Msg: "x"}}}}
	s := fakeSession(t, f)
	before, _ := meter.Default.Totals()
	if _, err := logs.NewHistory(s, "/app", "", 0, time.Time{}).Older(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	after, _ := meter.Default.Totals()
	if after-before < 2 {
		t.Fatalf("meter counted %d requests, want >= 2", after-before)
	}
	found := false
	for _, st := range meter.Default.Stats() {
		if st.Service == "CloudWatch Logs" && st.Operation == "GetLogEvents" {
			found = true
		}
	}
	if !found {
		t.Fatalf("GetLogEvents not recorded: %+v", meter.Default.Stats())
	}
}

func msgs(e []logs.Event) string {
	var s []string
	for _, x := range e {
		s = append(s, x.Message)
	}
	return strings.Join(s, " ")
}
