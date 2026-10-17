package ecs

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	sdk "github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/aws/aws-sdk-go-v2/service/ecs/types"

	"devops/internal/aws/auth"
)

type Deployment struct {
	ID, Status, Rollout, Reason, TaskDef string
	Desired, Running, Pending, Failed    int32
	Created, Updated                     time.Time
}

type Event struct {
	At      time.Time
	Message string
}

// ServiceDetail is everything the service view shows.
type ServiceDetail struct {
	Service
	Cluster           string
	TaskDefARN        string
	Deployments       []Deployment
	Events            []Event
	TargetGroups      []string
	CircuitBreaker    bool
	AutoRollback      bool
	MinHealthy        int32
	MaxPercent        int32
	CapacityProviders []string
	Subnets           []string
	SecurityGroups    []string
	PublicIP          bool
	GracePeriod       int32
	Created           time.Time
}

func DescribeService(ctx context.Context, s *auth.Session, cluster, service string) (ServiceDetail, error) {
	out, err := s.ECS().DescribeServices(ctx, &sdk.DescribeServicesInput{Cluster: aws.String(cluster), Services: []string{service}})
	if err != nil {
		return ServiceDetail{}, err
	}
	if len(out.Services) == 0 {
		return ServiceDetail{}, fmt.Errorf("service %s not found in %s", service, cluster)
	}
	sv := out.Services[0]
	d := ServiceDetail{Service: convertService(sv), Cluster: cluster, TaskDefARN: aws.ToString(sv.TaskDefinition),
		Created: aws.ToTime(sv.CreatedAt), GracePeriod: aws.ToInt32(sv.HealthCheckGracePeriodSeconds)}
	for _, dp := range sv.Deployments {
		d.Deployments = append(d.Deployments, Deployment{
			ID: aws.ToString(dp.Id), Status: aws.ToString(dp.Status), Rollout: string(dp.RolloutState),
			Reason: aws.ToString(dp.RolloutStateReason), TaskDef: shortARN(aws.ToString(dp.TaskDefinition)),
			Desired: dp.DesiredCount, Running: dp.RunningCount, Pending: dp.PendingCount, Failed: dp.FailedTasks,
			Created: aws.ToTime(dp.CreatedAt), Updated: aws.ToTime(dp.UpdatedAt),
		})
	}
	for _, ev := range sv.Events {
		d.Events = append(d.Events, Event{At: aws.ToTime(ev.CreatedAt), Message: aws.ToString(ev.Message)})
	}
	for _, lb := range sv.LoadBalancers {
		if arn := aws.ToString(lb.TargetGroupArn); arn != "" {
			d.TargetGroups = append(d.TargetGroups, arn)
		}
	}
	if dc := sv.DeploymentConfiguration; dc != nil {
		d.MinHealthy, d.MaxPercent = aws.ToInt32(dc.MinimumHealthyPercent), aws.ToInt32(dc.MaximumPercent)
		if cb := dc.DeploymentCircuitBreaker; cb != nil {
			d.CircuitBreaker, d.AutoRollback = cb.Enable, cb.Rollback
		}
	}
	for _, cp := range sv.CapacityProviderStrategy {
		d.CapacityProviders = append(d.CapacityProviders, fmt.Sprintf("%s (base %d, weight %d)", aws.ToString(cp.CapacityProvider), cp.Base, cp.Weight))
	}
	if nc := sv.NetworkConfiguration; nc != nil && nc.AwsvpcConfiguration != nil {
		d.Subnets = nc.AwsvpcConfiguration.Subnets
		d.SecurityGroups = nc.AwsvpcConfiguration.SecurityGroups
		d.PublicIP = nc.AwsvpcConfiguration.AssignPublicIp == types.AssignPublicIpEnabled
	}
	return d, nil
}

type ContainerDef struct {
	Name, Image                  string
	CPU, Memory, MemoryReserved  int32
	Essential                    bool
	Ports                        []string
	EnvNames                     []string // values are never shown
	Secrets                      []string // "NAME ← service" (the reference, not the value)
	LogGroup, LogPrefix, LogArea string
	HealthCheck                  string
	Command                      string
}

type TaskDef struct {
	ARN, Family, CPU, Memory string
	Revision                 int32
	NetworkMode, TaskRole    string
	ExecRole                 string
	Compat                   []string
	Registered               time.Time
	Containers               []ContainerDef
	Raw                      types.TaskDefinition
}

func (t TaskDef) FamilyRev() string { return fmt.Sprintf("%s:%d", t.Family, t.Revision) }

func DescribeTaskDef(ctx context.Context, s *auth.Session, td string) (TaskDef, error) {
	out, err := s.ECS().DescribeTaskDefinition(ctx, &sdk.DescribeTaskDefinitionInput{TaskDefinition: aws.String(td)})
	if err != nil {
		return TaskDef{}, err
	}
	t := out.TaskDefinition
	d := TaskDef{ARN: aws.ToString(t.TaskDefinitionArn), Family: aws.ToString(t.Family), Revision: t.Revision,
		CPU: aws.ToString(t.Cpu), Memory: aws.ToString(t.Memory), NetworkMode: string(t.NetworkMode),
		TaskRole: shortARN(aws.ToString(t.TaskRoleArn)), ExecRole: shortARN(aws.ToString(t.ExecutionRoleArn)),
		Registered: aws.ToTime(t.RegisteredAt), Raw: redactEnv(*t)}
	for _, c := range t.RequiresCompatibilities {
		d.Compat = append(d.Compat, string(c))
	}
	for _, c := range t.ContainerDefinitions {
		cd := ContainerDef{Name: aws.ToString(c.Name), Image: aws.ToString(c.Image), CPU: c.Cpu,
			Memory: aws.ToInt32(c.Memory), MemoryReserved: aws.ToInt32(c.MemoryReservation), Essential: aws.ToBool(c.Essential),
			Command: strings.Join(c.Command, " ")}
		for _, p := range c.PortMappings {
			cd.Ports = append(cd.Ports, fmt.Sprintf("%d/%s", aws.ToInt32(p.ContainerPort), p.Protocol))
		}
		for _, e := range c.Environment {
			cd.EnvNames = append(cd.EnvNames, aws.ToString(e.Name))
		}
		sort.Strings(cd.EnvNames)
		for _, sc := range c.Secrets {
			src := "Parameter Store"
			if strings.Contains(aws.ToString(sc.ValueFrom), ":secretsmanager:") {
				src = "Secrets Manager"
			}
			cd.Secrets = append(cd.Secrets, aws.ToString(sc.Name)+" ← "+src)
		}
		if lc := c.LogConfiguration; lc != nil && lc.LogDriver == types.LogDriverAwslogs {
			cd.LogGroup, cd.LogPrefix, cd.LogArea = lc.Options["awslogs-group"], lc.Options["awslogs-stream-prefix"], lc.Options["awslogs-region"]
		}
		if hc := c.HealthCheck; hc != nil {
			cd.HealthCheck = strings.Join(hc.Command, " ")
		}
		d.Containers = append(d.Containers, cd)
	}
	return d, nil
}

// redactEnv hides environment values in the raw definition used for
// YAML views and revision diffs (names stay visible).
func redactEnv(t types.TaskDefinition) types.TaskDefinition {
	cds := make([]types.ContainerDefinition, len(t.ContainerDefinitions))
	for i, c := range t.ContainerDefinitions {
		env := make([]types.KeyValuePair, len(c.Environment))
		for j, kv := range c.Environment {
			env[j] = types.KeyValuePair{Name: kv.Name, Value: aws.String("•••")}
		}
		c.Environment = env
		cds[i] = c
	}
	t.ContainerDefinitions = cds
	return t
}

// Revisions lists recent revisions of a family, newest first ("family:rev").
func Revisions(ctx context.Context, s *auth.Session, family string) ([]string, error) {
	out, err := s.ECS().ListTaskDefinitions(ctx, &sdk.ListTaskDefinitionsInput{FamilyPrefix: aws.String(family),
		Sort: types.SortOrderDesc, MaxResults: aws.Int32(25)})
	if err != nil {
		return nil, err
	}
	var revs []string
	for _, arn := range out.TaskDefinitionArns {
		r := shortARN(arn)
		if strings.HasPrefix(r, family+":") {
			revs = append(revs, r)
		}
	}
	return revs, nil
}
