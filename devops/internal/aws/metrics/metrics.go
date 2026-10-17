// Package metrics reads CloudWatch alarms and metric statistics.
package metrics

import (
	"context"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	sdk "github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"

	"devops/internal/aws/auth"
)

type Alarm struct {
	Name, State, Namespace, Metric, Reason string
	Updated                                time.Time
}

// Alarms lists metric alarms; state "" returns all states.
func Alarms(ctx context.Context, s *auth.Session, state string) ([]Alarm, error) {
	in := &sdk.DescribeAlarmsInput{AlarmTypes: []types.AlarmType{types.AlarmTypeMetricAlarm, types.AlarmTypeCompositeAlarm}}
	if state != "" {
		in.StateValue = types.StateValue(state)
	}
	var out []Alarm
	p := sdk.NewDescribeAlarmsPaginator(s.CloudWatch(), in)
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, a := range page.MetricAlarms {
			out = append(out, Alarm{
				Name: aws.ToString(a.AlarmName), State: string(a.StateValue), Namespace: aws.ToString(a.Namespace),
				Metric: aws.ToString(a.MetricName), Reason: aws.ToString(a.StateReason), Updated: aws.ToTime(a.StateUpdatedTimestamp),
			})
		}
		for _, a := range page.CompositeAlarms {
			out = append(out, Alarm{
				Name: aws.ToString(a.AlarmName), State: string(a.StateValue), Namespace: "composite",
				Reason: aws.ToString(a.StateReason), Updated: aws.ToTime(a.StateUpdatedTimestamp),
			})
		}
	}
	rank := map[string]int{"ALARM": 0, "INSUFFICIENT_DATA": 1, "OK": 2}
	sort.SliceStable(out, func(i, j int) bool {
		if rank[out[i].State] != rank[out[j].State] {
			return rank[out[i].State] < rank[out[j].State]
		}
		return out[i].Name < out[j].Name
	})
	return out, nil
}

type Point struct {
	Time             time.Time
	Average, Maximum float64
	Unit             string
}

type StatQuery struct {
	Namespace, Metric string
	Dimensions        map[string]string
	Since             time.Duration
	Period            time.Duration
}

func Statistics(ctx context.Context, s *auth.Session, q StatQuery) ([]Point, error) {
	end := time.Now()
	in := &sdk.GetMetricStatisticsInput{
		Namespace: aws.String(q.Namespace), MetricName: aws.String(q.Metric),
		StartTime: aws.Time(end.Add(-q.Since)), EndTime: aws.Time(end),
		Period:     aws.Int32(int32(q.Period.Seconds())),
		Statistics: []types.Statistic{types.StatisticAverage, types.StatisticMaximum},
	}
	for k, v := range q.Dimensions {
		in.Dimensions = append(in.Dimensions, types.Dimension{Name: aws.String(k), Value: aws.String(v)})
	}
	out, err := s.CloudWatch().GetMetricStatistics(ctx, in)
	if err != nil {
		return nil, err
	}
	pts := make([]Point, 0, len(out.Datapoints))
	for _, d := range out.Datapoints {
		pts = append(pts, Point{Time: aws.ToTime(d.Timestamp), Average: aws.ToFloat64(d.Average), Maximum: aws.ToFloat64(d.Maximum), Unit: string(d.Unit)})
	}
	sort.Slice(pts, func(i, j int) bool { return pts[i].Time.Before(pts[j].Time) })
	return pts, nil
}
