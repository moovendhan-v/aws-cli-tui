// Package trail answers "what changed?": CloudTrail write events and alarm
// state changes for a time window.
package trail

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	ct "github.com/aws/aws-sdk-go-v2/service/cloudtrail"
	cttypes "github.com/aws/aws-sdk-go-v2/service/cloudtrail/types"
	cw "github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"

	"devops/internal/aws/auth"
	"devops/internal/safety"
)

type Change struct {
	ID, Kind             string // Kind: "api" or "alarm"
	Time                 time.Time
	Who, Action, Service string
	Resources            string
	ResourceType         string
	ResourceName         string
	Error, ErrorMessage  string
	SourceIP, Region     string
	Raw                  string // redacted CloudTrail JSON
	AlarmState           string // for alarm rows: the new state
}

// Noisy reports routine automated calls hidden by default.
func (c Change) Noisy() bool {
	if c.Kind != "api" {
		return false
	}
	switch c.Service {
	case "sts", "ssmmessages", "ec2messages", "sso", "signin":
		return true
	}
	switch c.Action {
	case "UpdateInstanceInformation", "SubmitTaskStateChange", "SubmitContainerStateChange", "SubmitAttachmentStateChanges",
		"CreateLogStream", "PutLogEvents", "ConsoleLogin", "GenerateDataKey", "Decrypt", "Encrypt", "RegisterContainerInstance",
		"UpdateContainerInstancesState", "PutMetricData", "CreateNetworkInterface", "DeleteNetworkInterface", "AttachNetworkInterface":
		return true
	}
	return false
}

// Page returns one page (≤50) of write events, newest first.
func Page(ctx context.Context, s *auth.Session, start, end time.Time, token string) ([]Change, string, error) {
	in := &ct.LookupEventsInput{StartTime: aws.Time(start), EndTime: aws.Time(end), MaxResults: aws.Int32(50),
		LookupAttributes: []cttypes.LookupAttribute{{AttributeKey: cttypes.LookupAttributeKeyReadOnly, AttributeValue: aws.String("false")}}}
	if token != "" {
		in.NextToken = aws.String(token)
	}
	out, err := s.CloudTrail().LookupEvents(ctx, in)
	if err != nil {
		return nil, "", err
	}
	cs := make([]Change, 0, len(out.Events))
	for _, e := range out.Events {
		cs = append(cs, convert(e))
	}
	return cs, aws.ToString(out.NextToken), nil
}

type rawEvent struct {
	UserIdentity struct {
		Type, Arn, UserName, InvokedBy string
		SessionContext                 struct {
			SessionIssuer struct{ UserName string }
		}
	}
	SourceIPAddress string
	AWSRegion       string
	ErrorCode       string
	ErrorMessage    string
}

func convert(e cttypes.Event) Change {
	c := Change{ID: aws.ToString(e.EventId), Kind: "api", Time: aws.ToTime(e.EventTime), Action: aws.ToString(e.EventName),
		Service: strings.TrimSuffix(aws.ToString(e.EventSource), ".amazonaws.com"), Who: aws.ToString(e.Username)}
	var names []string
	for _, r := range e.Resources {
		n := aws.ToString(r.ResourceName)
		names = append(names, n)
		if c.ResourceType == "" {
			c.ResourceType, c.ResourceName = aws.ToString(r.ResourceType), n
		}
	}
	c.Resources = strings.Join(names, ", ")
	if raw := aws.ToString(e.CloudTrailEvent); raw != "" {
		var re rawEvent
		if json.Unmarshal([]byte(raw), &re) == nil {
			c.SourceIP, c.Region, c.Error, c.ErrorMessage = re.SourceIPAddress, re.AWSRegion, re.ErrorCode, re.ErrorMessage
			ui := re.UserIdentity
			switch ui.Type {
			case "AssumedRole":
				role := ui.SessionContext.SessionIssuer.UserName
				session := ui.Arn[strings.LastIndex(ui.Arn, "/")+1:]
				c.Who = role + "/" + session
			case "IAMUser":
				c.Who = ui.UserName
			case "Root":
				c.Who = "root"
			case "AWSService":
				c.Who = ui.InvokedBy
			}
		}
		c.Raw = safety.Redact(raw)
	}
	return c
}

// AlarmChanges lists alarm state transitions in the window.
func AlarmChanges(ctx context.Context, s *auth.Session, start, end time.Time) ([]Change, error) {
	out, err := s.CloudWatch().DescribeAlarmHistory(ctx, &cw.DescribeAlarmHistoryInput{HistoryItemType: cwtypes.HistoryItemTypeStateUpdate,
		StartDate: aws.Time(start), EndDate: aws.Time(end), MaxRecords: aws.Int32(100), ScanBy: cwtypes.ScanByTimestampDescending})
	if err != nil {
		return nil, err
	}
	var cs []Change
	for _, h := range out.AlarmHistoryItems {
		sum := aws.ToString(h.HistorySummary)
		state := ""
		if i := strings.LastIndex(sum, " to "); i >= 0 {
			state = strings.TrimSpace(sum[i+4:])
		}
		cs = append(cs, Change{Kind: "alarm", Time: aws.ToTime(h.Timestamp), Who: "CloudWatch", Action: "→ " + state, AlarmState: state,
			Service: "alarm", Resources: aws.ToString(h.AlarmName), ResourceName: aws.ToString(h.AlarmName), Raw: sum})
	}
	return cs, nil
}

// SortNewest orders changes newest first.
func SortNewest(cs []Change) {
	sort.SliceStable(cs, func(i, j int) bool { return cs[i].Time.After(cs[j].Time) })
}
