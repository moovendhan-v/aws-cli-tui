// Package networking inspects VPCs, load balancers and target health.
package networking

import (
	"context"
	"sort"

	"github.com/aws/aws-sdk-go-v2/aws"
	ec2sdk "github.com/aws/aws-sdk-go-v2/service/ec2"
	elb "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"

	"github.com/moovendhan-v/aws-cli-tui/devops/internal/aws/auth"
)

type VPC struct {
	ID, Name, CIDR, State string
	Default               bool
}

func VPCs(ctx context.Context, s *auth.Session) ([]VPC, error) {
	var out []VPC
	p := ec2sdk.NewDescribeVpcsPaginator(s.EC2(), &ec2sdk.DescribeVpcsInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, v := range page.Vpcs {
			o := VPC{ID: aws.ToString(v.VpcId), CIDR: aws.ToString(v.CidrBlock), State: string(v.State), Default: aws.ToBool(v.IsDefault)}
			for _, t := range v.Tags {
				if aws.ToString(t.Key) == "Name" {
					o.Name = aws.ToString(t.Value)
				}
			}
			out = append(out, o)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

type LoadBalancer struct {
	Name, ARN, Type, Scheme, State, DNS, VPC string
}

func LoadBalancers(ctx context.Context, s *auth.Session) ([]LoadBalancer, error) {
	var out []LoadBalancer
	p := elb.NewDescribeLoadBalancersPaginator(s.ELB(), &elb.DescribeLoadBalancersInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, lb := range page.LoadBalancers {
			o := LoadBalancer{
				Name: aws.ToString(lb.LoadBalancerName), ARN: aws.ToString(lb.LoadBalancerArn), Type: string(lb.Type),
				Scheme: string(lb.Scheme), DNS: aws.ToString(lb.DNSName), VPC: aws.ToString(lb.VpcId),
			}
			if lb.State != nil {
				o.State = string(lb.State.Code)
			}
			out = append(out, o)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

type TargetGroup struct {
	Name, ARN, Protocol, TargetType, HealthPath string
	Port                                        int32
	LoadBalancerARNs                            []string
}

// TargetGroups lists target groups, optionally for one load balancer.
func TargetGroups(ctx context.Context, s *auth.Session, lbARN string) ([]TargetGroup, error) {
	in := &elb.DescribeTargetGroupsInput{}
	if lbARN != "" {
		in.LoadBalancerArn = aws.String(lbARN)
	}
	var out []TargetGroup
	p := elb.NewDescribeTargetGroupsPaginator(s.ELB(), in)
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, tg := range page.TargetGroups {
			out = append(out, TargetGroup{
				Name: aws.ToString(tg.TargetGroupName), ARN: aws.ToString(tg.TargetGroupArn), Protocol: string(tg.Protocol),
				TargetType: string(tg.TargetType), HealthPath: aws.ToString(tg.HealthCheckPath), Port: aws.ToInt32(tg.Port),
				LoadBalancerARNs: tg.LoadBalancerArns,
			})
		}
	}
	return out, nil
}

type Target struct {
	ID, State, Reason, Description string
	Port                           int32
}

func (t Target) Healthy() bool { return t.State == "healthy" }

func TargetHealth(ctx context.Context, s *auth.Session, tgARN string) ([]Target, error) {
	res, err := s.ELB().DescribeTargetHealth(ctx, &elb.DescribeTargetHealthInput{TargetGroupArn: aws.String(tgARN)})
	if err != nil {
		return nil, err
	}
	var out []Target
	for _, d := range res.TargetHealthDescriptions {
		t := Target{}
		if d.Target != nil {
			t.ID, t.Port = aws.ToString(d.Target.Id), aws.ToInt32(d.Target.Port)
		}
		if d.TargetHealth != nil {
			t.State, t.Reason, t.Description = string(d.TargetHealth.State), string(d.TargetHealth.Reason), aws.ToString(d.TargetHealth.Description)
		}
		out = append(out, t)
	}
	return out, nil
}

// LoadBalancersPage returns up to 100 load balancers per page.
func LoadBalancersPage(ctx context.Context, s *auth.Session, marker string) ([]LoadBalancer, string, error) {
	in := &elb.DescribeLoadBalancersInput{PageSize: aws.Int32(100)}
	if marker != "" {
		in.Marker = aws.String(marker)
	}
	page, err := s.ELB().DescribeLoadBalancers(ctx, in)
	if err != nil {
		return nil, "", err
	}
	var out []LoadBalancer
	for _, lb := range page.LoadBalancers {
		o := LoadBalancer{Name: aws.ToString(lb.LoadBalancerName), ARN: aws.ToString(lb.LoadBalancerArn), Type: string(lb.Type),
			Scheme: string(lb.Scheme), DNS: aws.ToString(lb.DNSName), VPC: aws.ToString(lb.VpcId)}
		if lb.State != nil {
			o.State = string(lb.State.Code)
		}
		out = append(out, o)
	}
	return out, aws.ToString(page.NextMarker), nil
}

// VPCsPage returns up to 100 VPCs per page.
func VPCsPage(ctx context.Context, s *auth.Session, token string) ([]VPC, string, error) {
	in := &ec2sdk.DescribeVpcsInput{MaxResults: aws.Int32(100)}
	if token != "" {
		in.NextToken = aws.String(token)
	}
	page, err := s.EC2().DescribeVpcs(ctx, in)
	if err != nil {
		return nil, "", err
	}
	var out []VPC
	for _, v := range page.Vpcs {
		o := VPC{ID: aws.ToString(v.VpcId), CIDR: aws.ToString(v.CidrBlock), State: string(v.State), Default: aws.ToBool(v.IsDefault)}
		for _, t := range v.Tags {
			if aws.ToString(t.Key) == "Name" {
				o.Name = aws.ToString(t.Value)
			}
		}
		out = append(out, o)
	}
	return out, aws.ToString(page.NextToken), nil
}

type Listener struct {
	Protocol     string
	Port         int32
	TargetGroups []string // default action target group ARNs
	Action       string   // default action type (forward, redirect, fixed-response…)
}

func Listeners(ctx context.Context, s *auth.Session, lbARN string) ([]Listener, error) {
	out, err := s.ELB().DescribeListeners(ctx, &elb.DescribeListenersInput{LoadBalancerArn: aws.String(lbARN)})
	if err != nil {
		return nil, err
	}
	var ls []Listener
	for _, l := range out.Listeners {
		x := Listener{Protocol: string(l.Protocol), Port: aws.ToInt32(l.Port)}
		for _, a := range l.DefaultActions {
			x.Action = string(a.Type)
			if arn := aws.ToString(a.TargetGroupArn); arn != "" {
				x.TargetGroups = append(x.TargetGroups, arn)
			}
			if a.ForwardConfig != nil {
				for _, tg := range a.ForwardConfig.TargetGroups {
					if arn := aws.ToString(tg.TargetGroupArn); arn != "" && !contains(x.TargetGroups, arn) {
						x.TargetGroups = append(x.TargetGroups, arn)
					}
				}
			}
		}
		ls = append(ls, x)
	}
	sort.Slice(ls, func(i, j int) bool { return ls[i].Port < ls[j].Port })
	return ls, nil
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
