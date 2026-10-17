// Package networking inspects VPCs, load balancers and target health.
package networking

import (
	"context"
	"sort"

	"github.com/aws/aws-sdk-go-v2/aws"
	ec2sdk "github.com/aws/aws-sdk-go-v2/service/ec2"
	elb "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"

	"devops/internal/aws/auth"
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
