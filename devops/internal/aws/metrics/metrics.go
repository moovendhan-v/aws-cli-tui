// Package metrics reads CloudWatch alarms and metric statistics.
package metrics

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	sdk "github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"

	"github.com/moovendhan-v/aws-cli-tui/devops/internal/aws/auth"
)

type Alarm struct {
	Name, State, Namespace, Metric, Reason string
	Updated                                time.Time
	Dimensions                             map[string]string
	Threshold                              *float64
	Comparison, Statistic                  string
	Period                                 int32
	Description                            string
}

func convertAlarm(a types.MetricAlarm) Alarm {
	al := Alarm{Name: aws.ToString(a.AlarmName), State: string(a.StateValue), Namespace: aws.ToString(a.Namespace),
		Metric: aws.ToString(a.MetricName), Reason: aws.ToString(a.StateReason), Updated: aws.ToTime(a.StateUpdatedTimestamp),
		Threshold: a.Threshold, Comparison: string(a.ComparisonOperator), Statistic: string(a.Statistic),
		Period: aws.ToInt32(a.Period), Description: aws.ToString(a.AlarmDescription), Dimensions: map[string]string{}}
	for _, d := range a.Dimensions {
		al.Dimensions[aws.ToString(d.Name)] = aws.ToString(d.Value)
	}
	return al
}

type StateChange struct {
	At      time.Time
	Summary string
}

// History returns recent state changes for an alarm.
func History(ctx context.Context, s *auth.Session, name string) ([]StateChange, error) {
	out, err := s.CloudWatch().DescribeAlarmHistory(ctx, &sdk.DescribeAlarmHistoryInput{AlarmName: aws.String(name),
		HistoryItemType: types.HistoryItemTypeStateUpdate, MaxRecords: aws.Int32(20)})
	if err != nil {
		return nil, err
	}
	var h []StateChange
	for _, it := range out.AlarmHistoryItems {
		h = append(h, StateChange{At: aws.ToTime(it.Timestamp), Summary: aws.ToString(it.HistorySummary)})
	}
	return h, nil
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
			out = append(out, convertAlarm(a))
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
	// Stat selects one statistic (Average, Sum, Maximum, Minimum,
	// SampleCount); its value is returned in Point.Average. Empty = Average+Maximum.
	Stat              string
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
	if q.Stat != "" {
		in.Statistics = []types.Statistic{types.Statistic(q.Stat), types.StatisticMaximum}
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
		v := aws.ToFloat64(d.Average)
		switch q.Stat {
		case "Sum":
			v = aws.ToFloat64(d.Sum)
		case "Minimum":
			v = aws.ToFloat64(d.Minimum)
		case "Maximum":
			v = aws.ToFloat64(d.Maximum)
		case "SampleCount":
			v = aws.ToFloat64(d.SampleCount)
		}
		pts = append(pts, Point{Time: aws.ToTime(d.Timestamp), Average: v, Maximum: aws.ToFloat64(d.Maximum), Unit: string(d.Unit)})
	}
	sort.Slice(pts, func(i, j int) bool { return pts[i].Time.Before(pts[j].Time) })
	return pts, nil
}

// AlarmsPage returns up to 100 alarms per page; state "" returns all.
func AlarmsPage(ctx context.Context, s *auth.Session, state, token string) ([]Alarm, string, error) {
	in := &sdk.DescribeAlarmsInput{MaxRecords: aws.Int32(100),
		AlarmTypes: []types.AlarmType{types.AlarmTypeMetricAlarm, types.AlarmTypeCompositeAlarm}}
	if state != "" {
		in.StateValue = types.StateValue(state)
	}
	if token != "" {
		in.NextToken = aws.String(token)
	}
	page, err := s.CloudWatch().DescribeAlarms(ctx, in)
	if err != nil {
		return nil, "", err
	}
	var out []Alarm
	for _, a := range page.MetricAlarms {
		out = append(out, convertAlarm(a))
	}
	for _, a := range page.CompositeAlarms {
		out = append(out, Alarm{Name: aws.ToString(a.AlarmName), State: string(a.StateValue), Namespace: "composite",
			Reason: aws.ToString(a.StateReason), Updated: aws.ToTime(a.StateUpdatedTimestamp)})
	}
	return out, aws.ToString(page.NextToken), nil
}

// EC2CPU fetches CPUUtilization (5-minute averages) for many instances in
// batched GetMetricData calls (up to 500 metrics each).
func EC2CPU(ctx context.Context, s *auth.Session, ids []string, since time.Duration) (map[string][]float64, error) {
	out := map[string][]float64{}
	end := time.Now()
	for start := 0; start < len(ids); start += 500 {
		chunk := ids[start:min(start+500, len(ids))]
		in := &sdk.GetMetricDataInput{StartTime: aws.Time(end.Add(-since)), EndTime: aws.Time(end), ScanBy: types.ScanByTimestampAscending}
		for i, id := range chunk {
			in.MetricDataQueries = append(in.MetricDataQueries, types.MetricDataQuery{
				Id: aws.String(fmt.Sprintf("m%d", i)),
				MetricStat: &types.MetricStat{Period: aws.Int32(300), Stat: aws.String("Average"),
					Metric: &types.Metric{Namespace: aws.String("AWS/EC2"), MetricName: aws.String("CPUUtilization"),
						Dimensions: []types.Dimension{{Name: aws.String("InstanceId"), Value: aws.String(id)}}}},
			})
		}
		p := sdk.NewGetMetricDataPaginator(s.CloudWatch(), in)
		for p.HasMorePages() {
			page, err := p.NextPage(ctx)
			if err != nil {
				return out, err
			}
			for _, r := range page.MetricDataResults {
				var idx int
				fmt.Sscanf(aws.ToString(r.Id), "m%d", &idx)
				if idx < len(chunk) {
					out[chunk[idx]] = append(out[chunk[idx]], r.Values...)
				}
			}
		}
	}
	return out, nil
}

// Series is a metric over time, returned by metric commands so the UI can
// draw it as a chart.
type Series struct {
	Namespace, Metric string
	Dimensions        map[string]string
	Since, Period     time.Duration
	Points            []Point
}

// Stats returns last, average, minimum and maximum of the plotted values.
func (s Series) Stats() (last, avg, lo, hi float64) {
	if len(s.Points) == 0 {
		return
	}
	lo = s.Points[0].Average
	for _, p := range s.Points {
		avg += p.Average
		lo = min(lo, p.Average)
		hi = max(hi, p.Maximum, p.Average)
	}
	return s.Points[len(s.Points)-1].Average, avg / float64(len(s.Points)), lo, hi
}

// TextLines is the plain-text form used for raw output and history.
func (s Series) TextLines() []string {
	if len(s.Points) == 0 {
		return []string{"No datapoints for " + s.Metric + " in this period."}
	}
	lines := []string{fmt.Sprintf("%-8s %12s %12s  %s", "TIME", "AVERAGE", "MAXIMUM", "UNIT")}
	for _, p := range s.Points {
		lines = append(lines, fmt.Sprintf("%-8s %12.2f %12.2f  %s", p.Time.Local().Format("15:04"), p.Average, p.Maximum, p.Unit))
	}
	return lines
}

// Spec names one metric/statistic for Multi.
type Spec struct {
	Metric, Stat string
}

// Multi fetches several metrics of one resource in a single GetMetricData
// call. Results are keyed by "Metric/Stat"; each Point.Average holds the value.
func Multi(ctx context.Context, s *auth.Session, namespace string, dims map[string]string, specs []Spec, since, period time.Duration) (map[string][]Point, error) {
	end := time.Now()
	var dd []types.Dimension
	for k, v := range dims {
		dd = append(dd, types.Dimension{Name: aws.String(k), Value: aws.String(v)})
	}
	in := &sdk.GetMetricDataInput{StartTime: aws.Time(end.Add(-since)), EndTime: aws.Time(end), ScanBy: types.ScanByTimestampAscending}
	for i, sp := range specs {
		in.MetricDataQueries = append(in.MetricDataQueries, types.MetricDataQuery{
			Id: aws.String(fmt.Sprintf("q%d", i)),
			MetricStat: &types.MetricStat{Period: aws.Int32(int32(period.Seconds())), Stat: aws.String(sp.Stat),
				Metric: &types.Metric{Namespace: aws.String(namespace), MetricName: aws.String(sp.Metric), Dimensions: dd}},
		})
	}
	out := map[string][]Point{}
	p := sdk.NewGetMetricDataPaginator(s.CloudWatch(), in)
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return out, err
		}
		for _, r := range page.MetricDataResults {
			var i int
			fmt.Sscanf(aws.ToString(r.Id), "q%d", &i)
			if i >= len(specs) {
				continue
			}
			key := specs[i].Metric + "/" + specs[i].Stat
			for j, v := range r.Values {
				out[key] = append(out[key], Point{Time: r.Timestamps[j], Average: v, Maximum: v})
			}
		}
	}
	return out, nil
}

// AlarmByName fetches one metric alarm.
func AlarmByName(ctx context.Context, s *auth.Session, name string) (Alarm, error) {
	out, err := s.CloudWatch().DescribeAlarms(ctx, &sdk.DescribeAlarmsInput{AlarmNames: []string{name}})
	if err != nil {
		return Alarm{}, err
	}
	if len(out.MetricAlarms) == 0 {
		return Alarm{}, fmt.Errorf("alarm %q not found (composite alarms can't be traced)", name)
	}
	return convertAlarm(out.MetricAlarms[0]), nil
}
