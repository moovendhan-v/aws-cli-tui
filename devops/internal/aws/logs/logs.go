// Package logs browses CloudWatch log groups and tails log events.
package logs

import (
	"context"
	"sort"
	"sync/atomic"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	sdk "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"

	"devops/internal/aws/auth"
)

type Group struct {
	Name          string
	StoredBytes   int64
	RetentionDays int32
	Created       time.Time
}

// ListGroups returns up to max log groups (0 = no limit).
func ListGroups(ctx context.Context, s *auth.Session, prefix string, max int) ([]Group, error) {
	in := &sdk.DescribeLogGroupsInput{}
	if prefix != "" {
		in.LogGroupNamePrefix = aws.String(prefix)
	}
	var out []Group
	p := sdk.NewDescribeLogGroupsPaginator(s.Logs(), in)
	for p.HasMorePages() && (max == 0 || len(out) < max) {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, g := range page.LogGroups {
			out = append(out, Group{
				Name: aws.ToString(g.LogGroupName), StoredBytes: aws.ToInt64(g.StoredBytes),
				RetentionDays: aws.ToInt32(g.RetentionInDays), Created: time.UnixMilli(aws.ToInt64(g.CreationTime)),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

type Event struct {
	Time    time.Time
	Stream  string
	Message string
}

type TailOptions struct {
	Group    string
	Pattern  string        // CloudWatch filter pattern, empty for all events
	Since    time.Duration // initial look-back
	Interval time.Duration // poll interval
	Paused   *atomic.Bool  // optional; polling is skipped while true
}

// maxPagesPerPoll bounds API calls per poll when backfilling a busy group.
const maxPagesPerPoll = 20

// Tail polls FilterLogEvents until ctx is cancelled, emitting each event once.
func Tail(ctx context.Context, s *auth.Session, o TailOptions, emit func(Event)) error {
	if o.Interval <= 0 {
		o.Interval = 2 * time.Second
	}
	start := time.Now().Add(-o.Since).UnixMilli()
	seen := map[string]int64{}
	for {
		if o.Paused == nil || !o.Paused.Load() {
			in := &sdk.FilterLogEventsInput{LogGroupName: aws.String(o.Group), StartTime: aws.Int64(start)}
			if o.Pattern != "" {
				in.FilterPattern = aws.String(o.Pattern)
			}
			var batch []Event
			newest := start
			p := sdk.NewFilterLogEventsPaginator(s.Logs(), in)
			for pages := 0; p.HasMorePages() && pages < maxPagesPerPoll; pages++ {
				page, err := p.NextPage(ctx)
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
					batch = append(batch, Event{Time: time.UnixMilli(ts), Stream: aws.ToString(e.LogStreamName), Message: aws.ToString(e.Message)})
				}
			}
			sort.SliceStable(batch, func(i, j int) bool { return batch[i].Time.Before(batch[j].Time) })
			for _, e := range batch {
				emit(e)
			}
			// Next poll starts at the newest timestamp; IDs at that boundary dedupe.
			start = newest
			for id, ts := range seen {
				if ts < start {
					delete(seen, id)
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
