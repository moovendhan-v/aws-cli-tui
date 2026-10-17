package core

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	cfn "github.com/aws/aws-sdk-go-v2/service/cloudformation"
	cfntypes "github.com/aws/aws-sdk-go-v2/service/cloudformation/types"
	ecssdk "github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"

	"devops/internal/aws/auth"
	"devops/internal/aws/cicd"
	"devops/internal/aws/ec2"
	"devops/internal/aws/ecs"
	"devops/internal/aws/lambda"
	"devops/internal/aws/logs"
	"devops/internal/aws/networking"
	"devops/internal/aws/rds"
	"devops/internal/aws/s3x"
	"devops/internal/commands"
	"devops/internal/tui/theme"
)

// Option is one choice for a form field: Value is what the command gets,
// Label/Detail are what people recognise (names, states, IPs).
type Option struct {
	Value, Label, Detail string
	Tone                 theme.Tone
	Recent               bool
}

// Source lists options for a resource kind. deps holds the other field values
// (for example a service picker needs the chosen cluster).
type Source struct {
	Kind  string   // cache bucket
	Needs []string // field names that must be filled first
	Load  func(ctx context.Context, s *auth.Session, deps map[string]string) ([]Option, error)
}

var errNeedDep = errors.New("fill the fields above first")

// SourceFor returns the option source for a parameter, if it has one.
func SourceFor(p commands.Param) (Source, bool) {
	switch p.Type {
	case "enum":
		opts := make([]Option, len(p.Options))
		for i, o := range p.Options {
			opts[i] = Option{Value: o, Label: o}
		}
		return static(opts), true
	case "bool":
		return static([]Option{{Value: "true", Label: "true"}, {Value: "false", Label: "false"}}), true
	case "resource":
		s, ok := sources[p.Resource]
		return s, ok
	}
	return Source{}, false
}

func static(o []Option) Source {
	return Source{Load: func(context.Context, *auth.Session, map[string]string) ([]Option, error) { return o, nil }}
}

var sources = map[string]Source{
	"ec2_instance": {Kind: "ec2", Load: func(ctx context.Context, s *auth.Session, _ map[string]string) ([]Option, error) {
		list, err := ec2.List(ctx, s)
		out := make([]Option, 0, len(list))
		for _, i := range list {
			name := i.Name
			if name == "" {
				name = "(no name)"
			}
			out = append(out, Option{Value: i.ID, Label: name, Detail: strings.Join(nonEmpty(i.ID, i.State, i.PrivateIP, i.Type), " · "),
				Tone: theme.StatusTone(i.State)})
		}
		return out, err
	}},
	"ecs_cluster": {Kind: "ecs", Load: func(ctx context.Context, s *auth.Session, _ map[string]string) ([]Option, error) {
		list, err := ecs.ListClusters(ctx, s)
		out := make([]Option, 0, len(list))
		for _, c := range list {
			out = append(out, Option{Value: c.Name, Label: c.Name, Detail: fmt.Sprintf("%d services · %d running", c.Services, c.Running)})
		}
		return out, err
	}},
	"ecs_service": {Kind: "ecs", Needs: []string{"cluster"}, Load: func(ctx context.Context, s *auth.Session, d map[string]string) ([]Option, error) {
		if d["cluster"] == "" {
			return nil, errNeedDep
		}
		list, err := ecs.ListServices(ctx, s, d["cluster"])
		out := make([]Option, 0, len(list))
		for _, sv := range list {
			tone := theme.ToneOK
			if sv.Running < sv.Desired {
				tone = theme.ToneBad
			}
			out = append(out, Option{Value: sv.Name, Label: sv.Name, Detail: fmt.Sprintf("%d/%d running · %s", sv.Running, sv.Desired, sv.Rollout), Tone: tone})
		}
		return out, err
	}},
	"ecs_task": {Kind: "ecs", Needs: []string{"cluster"}, Load: func(ctx context.Context, s *auth.Session, d map[string]string) ([]Option, error) {
		if d["cluster"] == "" {
			return nil, errNeedDep
		}
		list, err := ecs.ListTasks(ctx, s, d["cluster"], d["service"])
		out := make([]Option, 0, len(list))
		for _, t := range list {
			out = append(out, Option{Value: t.ID, Label: t.ID[:min(12, len(t.ID))], Detail: t.LastStatus + " · " + t.TaskDef, Tone: theme.StatusTone(t.LastStatus)})
		}
		return out, err
	}},
	"ecs_taskdef": {Kind: "taskdef", Load: func(ctx context.Context, s *auth.Session, _ map[string]string) ([]Option, error) {
		out, err := s.ECS().ListTaskDefinitions(ctx, &ecssdk.ListTaskDefinitionsInput{Sort: ecstypes.SortOrderDesc, MaxResults: aws.Int32(100)})
		if err != nil {
			return nil, err
		}
		opts := make([]Option, 0, len(out.TaskDefinitionArns))
		for _, arn := range out.TaskDefinitionArns {
			short := arn[strings.LastIndex(arn, "/")+1:]
			opts = append(opts, Option{Value: short, Label: short})
		}
		return opts, nil
	}},
	"log_group": {Kind: "logs", Load: func(ctx context.Context, s *auth.Session, _ map[string]string) ([]Option, error) {
		list, err := logs.ListGroups(ctx, s, "", 1000)
		out := make([]Option, 0, len(list))
		for _, g := range list {
			out = append(out, Option{Value: g.Name, Label: g.Name})
		}
		return out, err
	}},
	"rds_instance": {Kind: "rds", Load: func(ctx context.Context, s *auth.Session, _ map[string]string) ([]Option, error) {
		list, err := rds.List(ctx, s)
		out := make([]Option, 0, len(list))
		for _, d := range list {
			out = append(out, Option{Value: d.ID, Label: d.ID, Detail: d.Engine + " · " + d.Status, Tone: theme.StatusTone(d.Status)})
		}
		return out, err
	}},
	"lambda_function": {Kind: "lambda", Load: func(ctx context.Context, s *auth.Session, _ map[string]string) ([]Option, error) {
		list, err := lambda.List(ctx, s)
		out := make([]Option, 0, len(list))
		for _, f := range list {
			out = append(out, Option{Value: f.Name, Label: f.Name, Detail: f.Runtime})
		}
		return out, err
	}},
	"load_balancer": {Kind: "elb", Load: func(ctx context.Context, s *auth.Session, _ map[string]string) ([]Option, error) {
		list, err := networking.LoadBalancers(ctx, s)
		out := make([]Option, 0, len(list))
		for _, lb := range list {
			out = append(out, Option{Value: lb.ARN, Label: lb.Name, Detail: lb.Type + " · " + lb.State})
		}
		return out, err
	}},
	"target_group": {Kind: "elb", Load: func(ctx context.Context, s *auth.Session, _ map[string]string) ([]Option, error) {
		list, err := networking.TargetGroups(ctx, s, "")
		out := make([]Option, 0, len(list))
		for _, tg := range list {
			out = append(out, Option{Value: tg.ARN, Label: tg.Name, Detail: fmt.Sprintf("%s:%d · %s", tg.Protocol, tg.Port, tg.TargetType)})
		}
		return out, err
	}},
	"vpc": {Kind: "network", Load: func(ctx context.Context, s *auth.Session, _ map[string]string) ([]Option, error) {
		list, err := networking.VPCs(ctx, s)
		out := make([]Option, 0, len(list))
		for _, v := range list {
			label := v.Name
			if label == "" {
				label = v.ID
			}
			out = append(out, Option{Value: v.ID, Label: label, Detail: v.ID + " · " + v.CIDR})
		}
		return out, err
	}},
	"pipeline": {Kind: "pipelines", Load: func(ctx context.Context, s *auth.Session, _ map[string]string) ([]Option, error) {
		var out []Option
		token := ""
		for range 8 {
			ps, next, err := cicd.PipelinesPage(ctx, s, token)
			for _, p := range ps {
				st := p.Overall()
				out = append(out, Option{Value: p.Name, Label: p.Name, Detail: dashOr(st, "never run"), Tone: theme.StatusTone(st)})
			}
			if err != nil || next == "" {
				return out, err
			}
			token = next
		}
		return out, nil
	}},
	"build_project": {Kind: "pipelines", Load: func(ctx context.Context, s *auth.Session, _ map[string]string) ([]Option, error) {
		var out []Option
		token := ""
		for range 10 {
			names, next, err := cicd.ProjectsPage(ctx, s, token)
			for _, n := range names {
				out = append(out, Option{Value: n, Label: n})
			}
			if err != nil || next == "" {
				return out, err
			}
			token = next
		}
		return out, nil
	}},
	"s3_bucket": {Kind: "s3", Load: func(ctx context.Context, s *auth.Session, _ map[string]string) ([]Option, error) {
		var out []Option
		token := ""
		for range 10 {
			bs, next, err := s3x.BucketsPage(ctx, s, token)
			for _, b := range bs {
				out = append(out, Option{Value: b.Name, Label: b.Name, Detail: b.Region})
			}
			if err != nil || next == "" {
				return out, err
			}
			token = next
		}
		return out, nil
	}},
	"cfn_stack": {Kind: "cfn", Load: func(ctx context.Context, s *auth.Session, _ map[string]string) ([]Option, error) {
		var out []Option
		p := cfn.NewListStacksPaginator(s.CloudFormation(), &cfn.ListStacksInput{})
		for p.HasMorePages() && len(out) < 500 {
			page, err := p.NextPage(ctx)
			if err != nil {
				return out, err
			}
			for _, st := range page.StackSummaries {
				if st.StackStatus == cfntypes.StackStatusDeleteComplete {
					continue
				}
				out = append(out, Option{Value: aws.ToString(st.StackName), Label: aws.ToString(st.StackName),
					Detail: string(st.StackStatus), Tone: theme.StatusTone(string(st.StackStatus))})
			}
		}
		return out, nil
	}},
}

func nonEmpty(v ...string) []string {
	var out []string
	for _, s := range v {
		if s != "" {
			out = append(out, s)
		}
	}
	return out
}

func dashOr(s, alt string) string {
	if s == "" {
		return alt
	}
	return s
}
