package logs

import (
	"context"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	sdk "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"

	"github.com/moovendhan-v/aws-cli-tui/devops/internal/aws/auth"
)

// History pages backwards through a log group, newest events first.
//
// Without a filter it merges the most recently active streams with
// GetLogEvents (backward tokens), which reads exactly the newest events even
// on very busy groups. With a filter pattern it scans time windows backwards
// with FilterLogEvents.
type History struct {
	s       *auth.Session
	group   string
	pattern string

	// stream merge
	active       []*cursor
	pending      []stream
	streamsToken string
	streamsDone  bool

	// filtered windows
	windowEnd time.Time
	window    time.Duration
	created   time.Time
	Truncated bool

	Done  bool
	Calls int
}

type stream struct {
	name string
	last int64 // upper bound for any event time in the stream
}

type cursor struct {
	name  string
	token string
	buf   []Event // ascending; pop from the end
	done  bool
}

func NewHistory(s *auth.Session, group, pattern string, window time.Duration, created time.Time) *History {
	if window <= 0 {
		window = 15 * time.Minute
	}
	return &History{s: s, group: group, pattern: pattern, window: window, created: created}
}

const (
	maxCallsPerLoad  = 30
	activeStreamsMax = 5
	eventsPerFetch   = 200
)

// Older returns up to n events older than everything returned so far, in
// ascending time order.
func (h *History) Older(ctx context.Context, n int) ([]Event, error) {
	if h.Done {
		return nil, nil
	}
	if h.pattern != "" {
		return h.olderFiltered(ctx, n)
	}
	return h.olderMerged(ctx, n)
}

func (h *History) olderMerged(ctx context.Context, n int) ([]Event, error) {
	calls := 0
	if len(h.active) == 0 && len(h.pending) == 0 && !h.streamsDone {
		if err := h.loadStreams(ctx); err != nil {
			return nil, err
		}
		calls++
		for len(h.active) < activeStreamsMax && len(h.pending) > 0 {
			h.activate()
		}
	}
	var out []Event
	for len(out) < n && calls < maxCallsPerLoad {
		for _, c := range h.active {
			if len(c.buf) == 0 && !c.done {
				if err := h.fetch(ctx, c); err != nil {
					return reverse(out), err
				}
				calls++
			}
		}
		if len(h.pending) == 0 && !h.streamsDone {
			if err := h.loadStreams(ctx); err != nil {
				return reverse(out), err
			}
			calls++
		}
		best := h.best()
		// A pending stream may hold events newer than the best candidate.
		if len(h.pending) > 0 && (best == nil || best.buf[len(best.buf)-1].Time.UnixMilli() < h.pending[0].last) {
			h.activate()
			continue
		}
		if best == nil {
			h.Done = true
			break
		}
		e := best.buf[len(best.buf)-1]
		best.buf = best.buf[:len(best.buf)-1]
		out = append(out, e)
	}
	h.Calls += calls
	return reverse(out), nil
}

func (h *History) best() *cursor {
	var b *cursor
	for _, c := range h.active {
		if len(c.buf) == 0 {
			continue
		}
		if b == nil || c.buf[len(c.buf)-1].Time.After(b.buf[len(b.buf)-1].Time) {
			b = c
		}
	}
	return b
}

func (h *History) activate() {
	p := h.pending[0]
	h.pending = h.pending[1:]
	h.active = append(h.active, &cursor{name: p.name})
}

func (h *History) loadStreams(ctx context.Context) error {
	in := &sdk.DescribeLogStreamsInput{LogGroupName: aws.String(h.group), OrderBy: types.OrderByLastEventTime,
		Descending: aws.Bool(true), Limit: aws.Int32(50)}
	if h.streamsToken != "" {
		in.NextToken = aws.String(h.streamsToken)
	}
	out, err := h.s.Logs().DescribeLogStreams(ctx, in)
	if err != nil {
		return err
	}
	for _, st := range out.LogStreams {
		last := max(aws.ToInt64(st.LastEventTimestamp), aws.ToInt64(st.LastIngestionTime))
		h.pending = append(h.pending, stream{name: aws.ToString(st.LogStreamName), last: last})
	}
	sort.SliceStable(h.pending, func(i, j int) bool { return h.pending[i].last > h.pending[j].last })
	h.streamsToken = aws.ToString(out.NextToken)
	h.streamsDone = h.streamsToken == ""
	return nil
}

func (h *History) fetch(ctx context.Context, c *cursor) error {
	in := &sdk.GetLogEventsInput{LogGroupName: aws.String(h.group), LogStreamName: aws.String(c.name),
		StartFromHead: aws.Bool(false), Limit: aws.Int32(eventsPerFetch)}
	if c.token != "" {
		in.NextToken = aws.String(c.token)
	}
	out, err := h.s.Logs().GetLogEvents(ctx, in)
	if err != nil {
		return err
	}
	next := aws.ToString(out.NextBackwardToken)
	if len(out.Events) == 0 || next == c.token {
		c.done = true
	}
	c.token = next
	for _, e := range out.Events {
		c.buf = append(c.buf, Event{Time: time.UnixMilli(aws.ToInt64(e.Timestamp)), Stream: c.name, Message: aws.ToString(e.Message)})
	}
	return nil
}

func (h *History) olderFiltered(ctx context.Context, n int) ([]Event, error) {
	if h.windowEnd.IsZero() {
		h.windowEnd = time.Now()
	}
	var out []Event
	window := h.window
	for scanned := 0; len(out) < n && scanned < 6 && !h.Done; scanned++ {
		start := h.windowEnd.Add(-window)
		in := &sdk.FilterLogEventsInput{LogGroupName: aws.String(h.group), FilterPattern: aws.String(h.pattern),
			StartTime: aws.Int64(start.UnixMilli()), EndTime: aws.Int64(h.windowEnd.UnixMilli())}
		var chunk []Event
		for pages := 0; pages < 10; pages++ {
			page, err := h.s.Logs().FilterLogEvents(ctx, in)
			h.Calls++
			if err != nil {
				return out, err
			}
			for _, e := range page.Events {
				chunk = append(chunk, Event{ID: aws.ToString(e.EventId), Time: time.UnixMilli(aws.ToInt64(e.Timestamp)),
					Stream: aws.ToString(e.LogStreamName), Message: aws.ToString(e.Message)})
			}
			if page.NextToken == nil {
				break
			}
			if pages == 9 {
				h.Truncated = true
			}
			in.NextToken = page.NextToken
		}
		sort.SliceStable(chunk, func(i, j int) bool { return chunk[i].Time.Before(chunk[j].Time) })
		out = append(chunk, out...)
		h.windowEnd = start
		if len(chunk) == 0 {
			window = min(window*2, 24*time.Hour) // speed through quiet periods
		}
		if (!h.created.IsZero() && start.Before(h.created)) || time.Since(start) > 30*24*time.Hour {
			h.Done = true
		}
	}
	h.window = window
	return out, nil
}

func reverse(e []Event) []Event {
	for i, j := 0, len(e)-1; i < j; i, j = i+1, j-1 {
		e[i], e[j] = e[j], e[i]
	}
	return e
}

// StreamReader follows one log stream: the first call returns the latest
// events, later calls return only new ones (forward token).
type StreamReader struct {
	s             *auth.Session
	group, stream string
	fwd           string
}

func NewStreamReader(s *auth.Session, group, stream string) *StreamReader {
	return &StreamReader{s: s, group: group, stream: stream}
}

func (r *StreamReader) Next(ctx context.Context) ([]Event, error) {
	in := &sdk.GetLogEventsInput{LogGroupName: aws.String(r.group), LogStreamName: aws.String(r.stream), Limit: aws.Int32(500)}
	if r.fwd == "" {
		in.StartFromHead = aws.Bool(false)
	} else {
		in.StartFromHead = aws.Bool(true)
		in.NextToken = aws.String(r.fwd)
	}
	out, err := r.s.Logs().GetLogEvents(ctx, in)
	if err != nil {
		return nil, err
	}
	r.fwd = aws.ToString(out.NextForwardToken)
	ev := make([]Event, 0, len(out.Events))
	for _, e := range out.Events {
		ev = append(ev, Event{Time: time.UnixMilli(aws.ToInt64(e.Timestamp)), Stream: r.stream, Message: aws.ToString(e.Message)})
	}
	return ev, nil
}
