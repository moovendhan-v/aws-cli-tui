// Package ops is the table of AWS SDK operations that YAML command
// definitions may reference. A definition can only call what is listed here,
// with typed parameters, and can never declare a lower risk than the operation.
package ops

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	cfn "github.com/aws/aws-sdk-go-v2/service/cloudformation"
	logssdk "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cbsdk "github.com/aws/aws-sdk-go-v2/service/codebuild"
	cp "github.com/aws/aws-sdk-go-v2/service/codepipeline"
	cptypes "github.com/aws/aws-sdk-go-v2/service/codepipeline/types"
	ec2sdk "github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	ecssdk "github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/aws/auth"
	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/aws/cicd"
	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/aws/ec2"
	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/aws/ecs"
	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/aws/lambda"
	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/aws/logs"
	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/aws/meter"
	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/aws/metrics"
	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/aws/networking"
	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/aws/rds"
	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/aws/s3x"
	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/aws/ssm"
	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/safety"
)

type Params map[string]string

type Operation struct {
	Service, Name string
	Risk          safety.Risk
	Billed        string // non-empty when AWS bills per call or per data unit
	Required      []string
	Optional      []string
	Run           func(ctx context.Context, s *auth.Session, p Params) (any, error)
}

func (o Operation) Key() string { return o.Service + ":" + o.Name }

// Accepts reports whether name is a parameter the operation understands.
func (o Operation) Accepts(name string) bool {
	for _, n := range append(append([]string{}, o.Required...), o.Optional...) {
		if n == name {
			return true
		}
	}
	return false
}

type Table struct{ ops map[string]Operation }

func (t *Table) Get(service, name string) (Operation, bool) {
	o, ok := t.ops[strings.ToLower(service)+":"+name]
	return o, ok
}

func (t *Table) List() []Operation {
	out := make([]Operation, 0, len(t.ops))
	for _, o := range t.ops {
		out = append(out, o)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key() < out[j].Key() })
	return out
}

func (t *Table) add(o Operation) { t.ops[o.Key()] = o }

func (p Params) str(k string) string { return strings.TrimSpace(p[k]) }

func (p Params) duration(k string, def time.Duration) (time.Duration, error) {
	if p.str(k) == "" {
		return def, nil
	}
	return time.ParseDuration(p.str(k))
}

func (p Params) int(k string, def int) (int, error) {
	if p.str(k) == "" {
		return def, nil
	}
	return strconv.Atoi(p.str(k))
}

// Default returns the built-in operation table.
func Default() *Table {
	t := &Table{ops: map[string]Operation{}}

	t.add(Operation{Service: "sts", Name: "GetCallerIdentity", Risk: safety.ReadOnly,
		Run: func(ctx context.Context, s *auth.Session, _ Params) (any, error) {
			return s.STS().GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
		}})

	// EC2
	t.add(Operation{Service: "ec2", Name: "DescribeInstances", Risk: safety.ReadOnly, Optional: []string{"instance_id", "query"},
		Run: func(ctx context.Context, s *auth.Session, p Params) (any, error) {
			all, err := ec2.List(ctx, s)
			if err != nil {
				return nil, err
			}
			var out []ec2.Instance
			for _, i := range all {
				if (p.str("instance_id") == "" || i.ID == p.str("instance_id")) && i.Matches(p.str("query")) {
					out = append(out, i)
				}
			}
			return out, nil
		}})
	t.add(Operation{Service: "ec2", Name: "DescribeSecurityGroups", Risk: safety.ReadOnly, Optional: []string{"group_id", "instance_id"},
		Run: func(ctx context.Context, s *auth.Session, p Params) (any, error) {
			in := &ec2sdk.DescribeSecurityGroupsInput{}
			if id := p.str("group_id"); id != "" {
				in.GroupIds = strings.Split(id, ",")
			}
			if id := p.str("instance_id"); id != "" {
				res, err := s.EC2().DescribeInstances(ctx, &ec2sdk.DescribeInstancesInput{InstanceIds: []string{id}})
				if err != nil {
					return nil, err
				}
				for _, r := range res.Reservations {
					for _, i := range r.Instances {
						for _, g := range i.SecurityGroups {
							in.GroupIds = append(in.GroupIds, aws.ToString(g.GroupId))
						}
					}
				}
				if len(in.GroupIds) == 0 {
					return nil, fmt.Errorf("instance %s has no security groups", id)
				}
			}
			out, err := s.EC2().DescribeSecurityGroups(ctx, in)
			if err != nil {
				return nil, err
			}
			return out.SecurityGroups, nil
		}})
	t.add(Operation{Service: "ec2", Name: "DescribeVpcs", Risk: safety.ReadOnly,
		Run: func(ctx context.Context, s *auth.Session, _ Params) (any, error) { return networking.VPCs(ctx, s) }})
	t.add(Operation{Service: "ec2", Name: "DescribeSubnets", Risk: safety.ReadOnly, Optional: []string{"vpc_id"},
		Run: func(ctx context.Context, s *auth.Session, p Params) (any, error) {
			in := &ec2sdk.DescribeSubnetsInput{}
			if v := p.str("vpc_id"); v != "" {
				in.Filters = []ec2types.Filter{{Name: aws.String("vpc-id"), Values: []string{v}}}
			}
			out, err := s.EC2().DescribeSubnets(ctx, in)
			if err != nil {
				return nil, err
			}
			return out.Subnets, nil
		}})
	t.add(Operation{Service: "ec2", Name: "DescribeRouteTables", Risk: safety.ReadOnly, Optional: []string{"vpc_id"},
		Run: func(ctx context.Context, s *auth.Session, p Params) (any, error) {
			in := &ec2sdk.DescribeRouteTablesInput{}
			if v := p.str("vpc_id"); v != "" {
				in.Filters = []ec2types.Filter{{Name: aws.String("vpc-id"), Values: []string{v}}}
			}
			out, err := s.EC2().DescribeRouteTables(ctx, in)
			if err != nil {
				return nil, err
			}
			return out.RouteTables, nil
		}})
	t.add(Operation{Service: "ec2", Name: "RebootInstances", Risk: safety.Mutating, Required: []string{"instance_id"},
		Run: func(ctx context.Context, s *auth.Session, p Params) (any, error) {
			_, err := s.EC2().RebootInstances(ctx, &ec2sdk.RebootInstancesInput{InstanceIds: []string{p.str("instance_id")}})
			if err != nil {
				return nil, err
			}
			return map[string]string{"result": "reboot requested", "instance_id": p.str("instance_id")}, nil
		}})

	// SSM
	t.add(Operation{Service: "ssm", Name: "DescribeInstanceInformation", Risk: safety.ReadOnly, Required: []string{"instance_id"},
		Run: func(ctx context.Context, s *auth.Session, p Params) (any, error) {
			st, err := ssm.Status(ctx, s, p.str("instance_id"))
			if err != nil {
				return nil, err
			}
			return map[string]any{
				"managed": st.Managed, "ping_status": st.PingStatus, "agent_version": st.AgentVersion,
				"platform": st.Platform, "last_ping": st.LastPing, "session_ready": st.Ready(),
				"session_manager_plugin_installed": ssm.PluginInstalled(),
			}, nil
		}})

	// ECS
	t.add(Operation{Service: "ecs", Name: "ListClusters", Risk: safety.ReadOnly,
		Run: func(ctx context.Context, s *auth.Session, _ Params) (any, error) { return ecs.ListClusters(ctx, s) }})
	t.add(Operation{Service: "ecs", Name: "DescribeServices", Risk: safety.ReadOnly, Required: []string{"cluster"}, Optional: []string{"service"},
		Run: func(ctx context.Context, s *auth.Session, p Params) (any, error) {
			if sv := p.str("service"); sv != "" {
				out, err := s.ECS().DescribeServices(ctx, &ecssdk.DescribeServicesInput{Cluster: aws.String(p.str("cluster")), Services: []string{sv}})
				if err != nil {
					return nil, err
				}
				return out.Services, nil
			}
			return ecs.ListServices(ctx, s, p.str("cluster"))
		}})
	t.add(Operation{Service: "ecs", Name: "DescribeTasks", Risk: safety.ReadOnly, Required: []string{"cluster"}, Optional: []string{"service", "status"},
		Run: func(ctx context.Context, s *auth.Session, p Params) (any, error) {
			tasks, err := ecs.ListTasks(ctx, s, p.str("cluster"), p.str("service"))
			if err != nil {
				return nil, err
			}
			want := strings.ToUpper(p.str("status"))
			if want == "" {
				return tasks, nil
			}
			var out []ecs.Task
			for _, t := range tasks {
				if t.LastStatus == want {
					out = append(out, t)
				}
			}
			return out, nil
		}})
	t.add(Operation{Service: "ecs", Name: "DescribeTaskDefinition", Risk: safety.ReadOnly, Required: []string{"task_definition"},
		Run: func(ctx context.Context, s *auth.Session, p Params) (any, error) {
			out, err := s.ECS().DescribeTaskDefinition(ctx, &ecssdk.DescribeTaskDefinitionInput{TaskDefinition: aws.String(p.str("task_definition"))})
			if err != nil {
				return nil, err
			}
			return out.TaskDefinition, nil
		}})
	t.add(Operation{Service: "ecs", Name: "UpdateService", Risk: safety.Mutating, Required: []string{"cluster", "service"},
		Optional: []string{"desired_count", "task_definition", "force_new_deployment"},
		Run: func(ctx context.Context, s *auth.Session, p Params) (any, error) {
			in := &ecssdk.UpdateServiceInput{Cluster: aws.String(p.str("cluster")), Service: aws.String(p.str("service")),
				ForceNewDeployment: p.str("force_new_deployment") == "true"}
			if p.str("desired_count") != "" {
				n, err := p.int("desired_count", 0)
				if err != nil || n < 0 || n > 1000 {
					return nil, fmt.Errorf("desired_count must be between 0 and 1000")
				}
				in.DesiredCount = aws.Int32(int32(n))
			}
			if td := p.str("task_definition"); td != "" {
				in.TaskDefinition = aws.String(td)
			}
			if in.DesiredCount == nil && in.TaskDefinition == nil && !in.ForceNewDeployment {
				return nil, fmt.Errorf("nothing to change")
			}
			out, err := s.ECS().UpdateService(ctx, in)
			if err != nil {
				return nil, err
			}
			return out.Service.Deployments, nil
		}})
	t.add(Operation{Service: "ecs", Name: "StopTask", Risk: safety.Mutating, Required: []string{"cluster", "task"}, Optional: []string{"reason"},
		Run: func(ctx context.Context, s *auth.Session, p Params) (any, error) {
			reason := p.str("reason")
			if reason == "" {
				reason = "Stopped from devops CLI"
			}
			out, err := s.ECS().StopTask(ctx, &ecssdk.StopTaskInput{Cluster: aws.String(p.str("cluster")), Task: aws.String(p.str("task")), Reason: aws.String(reason)})
			if err != nil {
				return nil, err
			}
			return map[string]string{"task": aws.ToString(out.Task.TaskArn), "desired_status": aws.ToString(out.Task.DesiredStatus)}, nil
		}})

	// Logs
	t.add(Operation{Service: "logs", Name: "DescribeLogGroups", Risk: safety.ReadOnly, Optional: []string{"prefix"},
		Run: func(ctx context.Context, s *auth.Session, p Params) (any, error) {
			return logs.ListGroups(ctx, s, p.str("prefix"), 1000)
		}})
	t.add(Operation{Service: "logs", Name: "FilterLogEvents", Risk: safety.ReadOnly, Required: []string{"log_group"}, Optional: []string{"pattern", "since", "limit"},
		Run: func(ctx context.Context, s *auth.Session, p Params) (any, error) {
			since, err := p.duration("since", 15*time.Minute)
			if err != nil {
				return nil, err
			}
			limit, err := p.int("limit", 200)
			if err != nil {
				return nil, err
			}
			in := &logssdk.FilterLogEventsInput{
				LogGroupName: aws.String(p.str("log_group")), StartTime: aws.Int64(time.Now().Add(-since).UnixMilli()),
				Limit: aws.Int32(int32(min(limit, 10000))),
			}
			if pat := p.str("pattern"); pat != "" {
				in.FilterPattern = aws.String(pat)
			}
			out, err := s.Logs().FilterLogEvents(ctx, in)
			if err != nil {
				return nil, err
			}
			lines := make([]string, 0, len(out.Events))
			for _, e := range out.Events {
				ts := time.UnixMilli(aws.ToInt64(e.Timestamp)).Format("15:04:05")
				lines = append(lines, ts+"  "+strings.TrimRight(aws.ToString(e.Message), "\n"))
			}
			return lines, nil
		}})

	// CloudWatch metrics
	t.add(Operation{Service: "cloudwatch", Name: "DescribeAlarms", Risk: safety.ReadOnly, Optional: []string{"state"},
		Run: func(ctx context.Context, s *auth.Session, p Params) (any, error) {
			return metrics.Alarms(ctx, s, strings.ToUpper(p.str("state")))
		}})
	t.add(Operation{Service: "cloudwatch", Name: "GetMetricStatistics", Risk: safety.ReadOnly,
		Billed:   meter.BilledNote("CloudWatch", "GetMetricStatistics"),
		Required: []string{"namespace", "metric", "dimension_name", "dimension_value"}, Optional: []string{"since", "period"},
		Run: func(ctx context.Context, s *auth.Session, p Params) (any, error) {
			since, err := p.duration("since", 3*time.Hour)
			if err != nil {
				return nil, err
			}
			period, err := p.duration("period", 5*time.Minute)
			if err != nil {
				return nil, err
			}
			pts, err := metrics.Statistics(ctx, s, metrics.StatQuery{
				Namespace: p.str("namespace"), Metric: p.str("metric"),
				Dimensions: map[string]string{p.str("dimension_name"): p.str("dimension_value")},
				Since:      since, Period: period,
			})
			if err != nil {
				return nil, err
			}
			return metrics.Series{Namespace: p.str("namespace"), Metric: p.str("metric"),
				Dimensions: map[string]string{p.str("dimension_name"): p.str("dimension_value")},
				Since:      since, Period: period, Points: pts}, nil
		}})

	// RDS
	t.add(Operation{Service: "rds", Name: "DescribeDBInstances", Risk: safety.ReadOnly, Optional: []string{"db_instance_id"},
		Run: func(ctx context.Context, s *auth.Session, p Params) (any, error) {
			all, err := rds.List(ctx, s)
			if err != nil || p.str("db_instance_id") == "" {
				return all, err
			}
			for _, d := range all {
				if d.ID == p.str("db_instance_id") {
					return d, nil
				}
			}
			return nil, fmt.Errorf("DB instance %q not found", p.str("db_instance_id"))
		}})

	// ELB
	t.add(Operation{Service: "elbv2", Name: "DescribeLoadBalancers", Risk: safety.ReadOnly,
		Run: func(ctx context.Context, s *auth.Session, _ Params) (any, error) {
			return networking.LoadBalancers(ctx, s)
		}})
	t.add(Operation{Service: "elbv2", Name: "DescribeTargetGroups", Risk: safety.ReadOnly, Optional: []string{"load_balancer_arn"},
		Run: func(ctx context.Context, s *auth.Session, p Params) (any, error) {
			return networking.TargetGroups(ctx, s, p.str("load_balancer_arn"))
		}})
	t.add(Operation{Service: "elbv2", Name: "DescribeTargetHealth", Risk: safety.ReadOnly, Required: []string{"target_group_arn"},
		Run: func(ctx context.Context, s *auth.Session, p Params) (any, error) {
			return networking.TargetHealth(ctx, s, p.str("target_group_arn"))
		}})

	// Lambda
	t.add(Operation{Service: "lambda", Name: "ListFunctions", Risk: safety.ReadOnly,
		Run: func(ctx context.Context, s *auth.Session, _ Params) (any, error) { return lambda.List(ctx, s) }})
	t.add(Operation{Service: "lambda", Name: "GetFunctionConfiguration", Risk: safety.ReadOnly, Required: []string{"function_name"},
		Run: func(ctx context.Context, s *auth.Session, p Params) (any, error) {
			// Environment values can hold secrets, so only names are returned.
			return lambda.Describe(ctx, s, p.str("function_name"))
		}})
	t.add(Operation{Service: "lambda", Name: "Invoke", Risk: safety.Mutating, Required: []string{"function_name"}, Optional: []string{"payload"},
		Run: func(ctx context.Context, s *auth.Session, p Params) (any, error) {
			payload := p.str("payload")
			if payload == "" {
				payload = "{}"
			}
			if !json.Valid([]byte(payload)) {
				return nil, fmt.Errorf("payload must be valid JSON")
			}
			r, err := lambda.Invoke(ctx, s, p.str("function_name"), payload)
			if err != nil {
				return nil, err
			}
			lines := []string{fmt.Sprintf("status %d · %s", r.StatusCode, r.Duration.Round(time.Millisecond))}
			if r.FunctionError != "" {
				lines = append(lines, "✗ function error: "+r.FunctionError)
			}
			lines = append(lines, "", "━━ Response")
			lines = append(lines, strings.Split(r.Payload, "\n")...)
			if r.LogTail != "" {
				lines = append(lines, "", "━━ Log (last 4 KB)")
				lines = append(lines, strings.Split(strings.TrimRight(r.LogTail, "\n"), "\n")...)
			}
			return lines, nil
		}})

	// CloudFormation
	t.add(Operation{Service: "cloudformation", Name: "DescribeStackEvents", Risk: safety.ReadOnly, Required: []string{"stack_name"}, Optional: []string{"failed_only"},
		Run: func(ctx context.Context, s *auth.Session, p Params) (any, error) {
			out, err := s.CloudFormation().DescribeStackEvents(ctx, &cfn.DescribeStackEventsInput{StackName: aws.String(p.str("stack_name"))})
			if err != nil {
				return nil, err
			}
			var lines []string
			for _, e := range out.StackEvents {
				st := string(e.ResourceStatus)
				if p.str("failed_only") == "true" && !strings.Contains(st, "FAILED") {
					continue
				}
				lines = append(lines, fmt.Sprintf("%s  %-28s %-40s %s", aws.ToTime(e.Timestamp).Format("2006-01-02 15:04:05"),
					st, aws.ToString(e.LogicalResourceId), aws.ToString(e.ResourceStatusReason)))
			}
			return lines, nil
		}})

	addCICD(t)
	t.add(Operation{Service: "s3", Name: "GetPublicAccessBlock", Risk: safety.ReadOnly, Required: []string{"bucket"},
		Run: func(ctx context.Context, s *auth.Session, p Params) (any, error) {
			region, err := s3x.Region(ctx, s, p.str("bucket"))
			if err != nil {
				return nil, err
			}
			sec := s3x.BucketSecurity(ctx, s, p.str("bucket"), region)
			public := "not fully blocked — review bucket policy and ACLs"
			switch {
			case !sec.BlockKnown:
				public = "no public access block configured"
			case sec.PublicBlocked:
				public = "all public access blocked"
			}
			enc := sec.Encryption
			if enc == "" {
				enc = "no default encryption reported"
			}
			return map[string]string{"bucket": p.str("bucket"), "region": region, "public_access": public, "default_encryption": enc}, nil
		}})
	return t
}

func addCICD(t *Table) {
	t.add(Operation{Service: "codepipeline", Name: "GetPipelineState", Risk: safety.ReadOnly, Required: []string{"pipeline"},
		Run: func(ctx context.Context, s *auth.Session, p Params) (any, error) {
			return cicd.State(ctx, s, p.str("pipeline"))
		}})
	t.add(Operation{Service: "codepipeline", Name: "ListPipelineExecutions", Risk: safety.ReadOnly, Required: []string{"pipeline"},
		Run: func(ctx context.Context, s *auth.Session, p Params) (any, error) {
			return cicd.Executions(ctx, s, p.str("pipeline"))
		}})
	t.add(Operation{Service: "codepipeline", Name: "StartPipelineExecution", Risk: safety.Mutating, Required: []string{"pipeline"},
		Run: func(ctx context.Context, s *auth.Session, p Params) (any, error) {
			out, err := s.CodePipeline().StartPipelineExecution(ctx, &cp.StartPipelineExecutionInput{Name: aws.String(p.str("pipeline"))})
			if err != nil {
				return nil, err
			}
			return map[string]string{"execution_id": aws.ToString(out.PipelineExecutionId), "pipeline": p.str("pipeline")}, nil
		}})
	t.add(Operation{Service: "codepipeline", Name: "StopPipelineExecution", Risk: safety.Mutating, Required: []string{"pipeline", "execution_id"},
		Optional: []string{"reason", "abandon"},
		Run: func(ctx context.Context, s *auth.Session, p Params) (any, error) {
			_, err := s.CodePipeline().StopPipelineExecution(ctx, &cp.StopPipelineExecutionInput{PipelineName: aws.String(p.str("pipeline")),
				PipelineExecutionId: aws.String(p.str("execution_id")), Reason: aws.String(p.str("reason")), Abandon: p.str("abandon") == "true"})
			if err != nil {
				return nil, err
			}
			return map[string]string{"result": "stop requested", "execution_id": p.str("execution_id")}, nil
		}})
	t.add(Operation{Service: "codepipeline", Name: "RetryStageExecution", Risk: safety.Mutating, Required: []string{"pipeline", "stage", "execution_id"},
		Run: func(ctx context.Context, s *auth.Session, p Params) (any, error) {
			out, err := s.CodePipeline().RetryStageExecution(ctx, &cp.RetryStageExecutionInput{PipelineName: aws.String(p.str("pipeline")),
				StageName: aws.String(p.str("stage")), PipelineExecutionId: aws.String(p.str("execution_id")), RetryMode: cptypes.StageRetryModeFailedActions})
			if err != nil {
				return nil, err
			}
			return map[string]string{"execution_id": aws.ToString(out.PipelineExecutionId), "result": "retrying failed actions"}, nil
		}})
	t.add(Operation{Service: "codepipeline", Name: "PutApprovalResult", Risk: safety.Mutating,
		Required: []string{"pipeline", "stage", "action", "token", "decision"}, Optional: []string{"summary"},
		Run: func(ctx context.Context, s *auth.Session, p Params) (any, error) {
			status := cptypes.ApprovalStatusRejected
			if p.str("decision") == "Approved" {
				status = cptypes.ApprovalStatusApproved
			}
			summary := p.str("summary")
			if summary == "" {
				summary = string(status) + " from devops CLI"
			}
			_, err := s.CodePipeline().PutApprovalResult(ctx, &cp.PutApprovalResultInput{PipelineName: aws.String(p.str("pipeline")),
				StageName: aws.String(p.str("stage")), ActionName: aws.String(p.str("action")), Token: aws.String(p.str("token")),
				Result: &cptypes.ApprovalResult{Status: status, Summary: aws.String(summary)}})
			if err != nil {
				return nil, err
			}
			return map[string]string{"result": string(status), "stage": p.str("stage"), "action": p.str("action")}, nil
		}})
	t.add(Operation{Service: "codebuild", Name: "BatchGetBuilds", Risk: safety.ReadOnly, Required: []string{"build_id"},
		Run: func(ctx context.Context, s *auth.Session, p Params) (any, error) {
			return cicd.Builds(ctx, s, []string{p.str("build_id")})
		}})
	t.add(Operation{Service: "codebuild", Name: "StartBuild", Risk: safety.Mutating, Required: []string{"project"},
		Run: func(ctx context.Context, s *auth.Session, p Params) (any, error) {
			out, err := s.CodeBuild().StartBuild(ctx, &cbsdk.StartBuildInput{ProjectName: aws.String(p.str("project"))})
			if err != nil {
				return nil, err
			}
			return map[string]string{"build_id": aws.ToString(out.Build.Id), "status": string(out.Build.BuildStatus)}, nil
		}})
	t.add(Operation{Service: "codebuild", Name: "StopBuild", Risk: safety.Mutating, Required: []string{"build_id"},
		Run: func(ctx context.Context, s *auth.Session, p Params) (any, error) {
			out, err := s.CodeBuild().StopBuild(ctx, &cbsdk.StopBuildInput{Id: aws.String(p.str("build_id"))})
			if err != nil {
				return nil, err
			}
			return map[string]string{"build_id": aws.ToString(out.Build.Id), "status": string(out.Build.BuildStatus)}, nil
		}})
	t.add(Operation{Service: "codebuild", Name: "RetryBuild", Risk: safety.Mutating, Required: []string{"build_id"},
		Run: func(ctx context.Context, s *auth.Session, p Params) (any, error) {
			out, err := s.CodeBuild().RetryBuild(ctx, &cbsdk.RetryBuildInput{Id: aws.String(p.str("build_id"))})
			if err != nil {
				return nil, err
			}
			return map[string]string{"build_id": aws.ToString(out.Build.Id), "status": string(out.Build.BuildStatus)}, nil
		}})
}
