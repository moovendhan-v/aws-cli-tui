package logs

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	sdk "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"

	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/aws/auth"
)

type Event struct {
	Time    time.Time
	Stream  string
	Message string
	ID      string
}

type TailOptions struct {
	Group    string
	GroupARN string        // required for live mode
	Pattern  string        // CloudWatch filter pattern, empty for all events
	From     time.Time     // first event time to include
	Interval time.Duration // poll interval
	Paused   *atomic.Bool  // optional; polling is skipped while true
	Live     bool          // use StartLiveTail (billed per session minute)
}

// maxPagesPerPoll bounds API calls per poll. When a poll stops early the
// pagination token is kept, so the next poll continues where it left off
// instead of re-reading the same pages (the earlier "tail shows nothing" bug).
const maxPagesPerPoll = 10

// Tail streams new events until ctx is cancelled, emitting each event once.
func Tail(ctx context.Context, s *auth.Session, o TailOptions, emit func(Event)) error {
	if o.Live {
		return liveTail(ctx, s, o, emit)
	}
	if o.Interval <= 0 {
		o.Interval = 3 * time.Second
	}
	start := o.From.UnixMilli()
	token := ""
	seen := map[string]int64{}
	for {
		if o.Paused == nil || !o.Paused.Load() {
			in := &sdk.FilterLogEventsInput{LogGroupName: aws.String(o.Group), StartTime: aws.Int64(start)}
			if o.Pattern != "" {
				in.FilterPattern = aws.String(o.Pattern)
			}
			newest := start
			var batch []Event
			for pages := 0; pages < maxPagesPerPoll; pages++ {
				if token != "" {
					in.NextToken = aws.String(token)
				}
				page, err := s.Logs().FilterLogEvents(ctx, in)
				if err != nil {
					return err
				}
				for _, e := range page.Events {
					id, ts := aws.ToString(e.EventId), aws.ToInt64(e.Timestamp)
					if _, dup := seen[id]; dup {
						continue
					}
					seen[id] = ts
					newest = max(newest, ts)
					batch = append(batch, Event{ID: id, Time: time.UnixMilli(ts), Stream: aws.ToString(e.LogStreamName), Message: aws.ToString(e.Message)})
				}
				token = aws.ToString(page.NextToken)
				if token == "" {
					break
				}
			}
			sort.SliceStable(batch, func(i, j int) bool { return batch[i].Time.Before(batch[j].Time) })
			for _, e := range batch {
				emit(e)
			}
			// Only move the window once it was read to the end; IDs at the
			// boundary timestamp are deduplicated.
			if token == "" {
				start = newest
				for id, ts := range seen {
					if ts < start {
						delete(seen, id)
					}
				}
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(o.Interval):
		}
	}
}

func liveTail(ctx context.Context, s *auth.Session, o TailOptions, emit func(Event)) error {
	if o.GroupARN == "" {
		return errors.New("live tail needs the log group ARN")
	}
	in := &sdk.StartLiveTailInput{LogGroupIdentifiers: []string{strings.TrimSuffix(o.GroupARN, ":*")}}
	if o.Pattern != "" {
		in.LogEventFilterPattern = aws.String(o.Pattern)
	}
	out, err := s.Logs().StartLiveTail(ctx, in)
	if err != nil {
		return err
	}
	stream := out.GetStream()
	defer stream.Close()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ev, ok := <-stream.Events():
			if !ok {
				if err := stream.Err(); err != nil {
					return err
				}
				return errors.New("live tail session ended (sessions are limited by AWS; press t to restart)")
			}
			upd, ok := ev.(*types.StartLiveTailResponseStreamMemberSessionUpdate)
			if !ok || (o.Paused != nil && o.Paused.Load()) {
				continue
			}
			for _, e := range upd.Value.SessionResults {
				emit(Event{Time: time.UnixMilli(aws.ToInt64(e.Timestamp)), Stream: aws.ToString(e.LogStreamName), Message: aws.ToString(e.Message)})
			}
		}
	}
}
