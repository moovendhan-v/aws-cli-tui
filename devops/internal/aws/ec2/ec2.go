// Package ec2 discovers and inspects EC2 instances.
package ec2

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	sdk "github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"devops/internal/aws/auth"
)

type Instance struct {
	ID, Name, State, Type    string
	PrivateIP, PublicIP, AZ  string
	VPC, Subnet, Platform    string
	IAMProfile, ImageID, Key string
	LaunchTime               time.Time
	SecurityGroups           []string // "sg-123 (name)"
	SecurityGroupIDs         []string
	Tags                     map[string]string
}

// Matches supports search by name, ID, private/public IP, state, type and tags.
func (i Instance) Matches(q string) bool {
	q = strings.ToLower(strings.TrimSpace(q))
	if q == "" {
		return true
	}
	for _, f := range []string{i.ID, i.Name, i.PrivateIP, i.PublicIP, i.State, i.Type, i.AZ, i.VPC} {
		if strings.Contains(strings.ToLower(f), q) {
			return true
		}
	}
	for k, v := range i.Tags {
		if strings.Contains(strings.ToLower(k+"="+v), q) {
			return true
		}
	}
	return false
}

func List(ctx context.Context, s *auth.Session) ([]Instance, error) {
	var out []Instance
	p := sdk.NewDescribeInstancesPaginator(s.EC2(), &sdk.DescribeInstancesInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, r := range page.Reservations {
			for _, in := range r.Instances {
				out = append(out, convert(in))
			}
		}
	}
	sort.Slice(out, func(a, b int) bool {
		if out[a].Name != out[b].Name {
			return strings.ToLower(out[a].Name) < strings.ToLower(out[b].Name)
		}
		return out[a].ID < out[b].ID
	})
	return out, nil
}

func convert(in types.Instance) Instance {
	i := Instance{
		ID:         aws.ToString(in.InstanceId),
		Type:       string(in.InstanceType),
		PrivateIP:  aws.ToString(in.PrivateIpAddress),
		PublicIP:   aws.ToString(in.PublicIpAddress),
		VPC:        aws.ToString(in.VpcId),
		Subnet:     aws.ToString(in.SubnetId),
		Platform:   aws.ToString(in.PlatformDetails),
		ImageID:    aws.ToString(in.ImageId),
		Key:        aws.ToString(in.KeyName),
		LaunchTime: aws.ToTime(in.LaunchTime),
		Tags:       map[string]string{},
	}
	if in.State != nil {
		i.State = string(in.State.Name)
	}
	if in.Placement != nil {
		i.AZ = aws.ToString(in.Placement.AvailabilityZone)
	}
	if in.IamInstanceProfile != nil {
		arn := aws.ToString(in.IamInstanceProfile.Arn)
		i.IAMProfile = arn[strings.LastIndex(arn, "/")+1:]
	}
	for _, g := range in.SecurityGroups {
		id := aws.ToString(g.GroupId)
		i.SecurityGroupIDs = append(i.SecurityGroupIDs, id)
		i.SecurityGroups = append(i.SecurityGroups, id+" ("+aws.ToString(g.GroupName)+")")
	}
	for _, t := range in.Tags {
		i.Tags[aws.ToString(t.Key)] = aws.ToString(t.Value)
	}
	i.Name = i.Tags["Name"]
	return i
}

// ListPage returns one page (up to 100 instances) and the next token ("" = done).
func ListPage(ctx context.Context, s *auth.Session, token string) ([]Instance, string, error) {
	in := &sdk.DescribeInstancesInput{MaxResults: aws.Int32(100)}
	if token != "" {
		in.NextToken = aws.String(token)
	}
	page, err := s.EC2().DescribeInstances(ctx, in)
	if err != nil {
		return nil, "", err
	}
	var out []Instance
	for _, r := range page.Reservations {
		for _, i := range r.Instances {
			out = append(out, convert(i))
		}
	}
	return out, aws.ToString(page.NextToken), nil
}
