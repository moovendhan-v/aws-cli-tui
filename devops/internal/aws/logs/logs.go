// Package logs browses CloudWatch log groups and tails log events.
package logs

import (
	"context"
	"sort"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	sdk "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"

	"devops/internal/aws/auth"
)

type Group struct {
	Name          string
	ARN           string
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
				Name: aws.ToString(g.LogGroupName), ARN: aws.ToString(g.LogGroupArn), StoredBytes: aws.ToInt64(g.StoredBytes),
				RetentionDays: aws.ToInt32(g.RetentionInDays), Created: time.UnixMilli(aws.ToInt64(g.CreationTime)),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// ListGroupsPage returns one page (up to 50) of log groups.
func ListGroupsPage(ctx context.Context, s *auth.Session, prefix, token string) ([]Group, string, error) {
	in := &sdk.DescribeLogGroupsInput{Limit: aws.Int32(50)}
	if prefix != "" {
		in.LogGroupNamePrefix = aws.String(prefix)
	}
	if token != "" {
		in.NextToken = aws.String(token)
	}
	page, err := s.Logs().DescribeLogGroups(ctx, in)
	if err != nil {
		return nil, "", err
	}
	out := make([]Group, 0, len(page.LogGroups))
	for _, g := range page.LogGroups {
		out = append(out, Group{
			Name: aws.ToString(g.LogGroupName), ARN: aws.ToString(g.LogGroupArn), StoredBytes: aws.ToInt64(g.StoredBytes),
			RetentionDays: aws.ToInt32(g.RetentionInDays), Created: time.UnixMilli(aws.ToInt64(g.CreationTime)),
		})
	}
	return out, aws.ToString(page.NextToken), nil
}
