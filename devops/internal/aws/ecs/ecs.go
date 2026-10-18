// Package ecs navigates cluster → service → task → container.
package ecs

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	sdk "github.com/aws/aws-sdk-go-v2/service/ecs"
	"github.com/aws/aws-sdk-go-v2/service/ecs/types"

	"devops/internal/aws/auth"
)

type Cluster struct {
	Name, ARN, Status                 string
	Running, Pending, Services, Hosts int32
}

type Service struct {
	Name, ARN, Status, LaunchType, TaskDef, Rollout string
	Desired, Running, Pending, FailedTasks          int32
	ExecEnabled                                     bool
	LastEvent                                       string
	TargetGroups                                    []string
}

type Container struct {
	Name, Status, Health, Reason string
	RuntimeID, Image             string
	ExitCode                     *int32
}

type Task struct {
	ID, ARN, LastStatus, Desired, Health string
	StoppedReason, StopCode, TaskDef     string
	StartedAt, StoppedAt                 time.Time
	ExecEnabled                          bool
	Containers                           []Container
}

func ListClusters(ctx context.Context, s *auth.Session) ([]Cluster, error) {
	var arns []string
	p := sdk.NewListClustersPaginator(s.ECS(), &sdk.ListClustersInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		arns = append(arns, page.ClusterArns...)
	}
	var out []Cluster
	for _, chunk := range chunks(arns, 100) {
		res, err := s.ECS().DescribeClusters(ctx, &sdk.DescribeClustersInput{Clusters: chunk})
		if err != nil {
			return nil, err
		}
		for _, c := range res.Clusters {
			out = append(out, Cluster{
				Name: aws.ToString(c.ClusterName), ARN: aws.ToString(c.ClusterArn), Status: aws.ToString(c.Status),
				Running: c.RunningTasksCount, Pending: c.PendingTasksCount,
				Services: c.ActiveServicesCount, Hosts: c.RegisteredContainerInstancesCount,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func ListServices(ctx context.Context, s *auth.Session, cluster string) ([]Service, error) {
	var arns []string
	p := sdk.NewListServicesPaginator(s.ECS(), &sdk.ListServicesInput{Cluster: aws.String(cluster)})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		arns = append(arns, page.ServiceArns...)
	}
	var out []Service
	for _, chunk := range chunks(arns, 10) {
		res, err := s.ECS().DescribeServices(ctx, &sdk.DescribeServicesInput{Cluster: aws.String(cluster), Services: chunk})
		if err != nil {
			return nil, err
		}
		for _, sv := range res.Services {
			out = append(out, convertService(sv))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

func convertService(sv types.Service) Service {
	o := Service{
		Name: aws.ToString(sv.ServiceName), ARN: aws.ToString(sv.ServiceArn), Status: aws.ToString(sv.Status),
		LaunchType: string(sv.LaunchType), TaskDef: shortARN(aws.ToString(sv.TaskDefinition)),
		Desired: sv.DesiredCount, Running: sv.RunningCount, Pending: sv.PendingCount,
		ExecEnabled: sv.EnableExecuteCommand,
	}
	for _, d := range sv.Deployments {
		if aws.ToString(d.Status) == "PRIMARY" {
			o.Rollout = string(d.RolloutState)
			o.FailedTasks = d.FailedTasks
		}
	}
	if len(sv.Events) > 0 {
		o.LastEvent = aws.ToString(sv.Events[0].Message)
	}
	for _, lb := range sv.LoadBalancers {
		if a := aws.ToString(lb.TargetGroupArn); a != "" {
			o.TargetGroups = append(o.TargetGroups, a)
		}
	}
	return o
}

// ListTasks returns running and recently stopped tasks for a service.
func ListTasks(ctx context.Context, s *auth.Session, cluster, service string) ([]Task, error) {
	var arns []string
	for _, st := range []types.DesiredStatus{types.DesiredStatusRunning, types.DesiredStatusStopped} {
		in := &sdk.ListTasksInput{Cluster: aws.String(cluster), DesiredStatus: st}
		if service != "" {
			in.ServiceName = aws.String(service)
		}
		p := sdk.NewListTasksPaginator(s.ECS(), in)
		for p.HasMorePages() {
			page, err := p.NextPage(ctx)
			if err != nil {
				return nil, err
			}
			arns = append(arns, page.TaskArns...)
		}
	}
	var out []Task
	for _, chunk := range chunks(arns, 100) {
		res, err := s.ECS().DescribeTasks(ctx, &sdk.DescribeTasksInput{Cluster: aws.String(cluster), Tasks: chunk})
		if err != nil {
			return nil, err
		}
		for _, t := range res.Tasks {
			out = append(out, convertTask(t))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if (out[i].LastStatus == "STOPPED") != (out[j].LastStatus == "STOPPED") {
			return out[i].LastStatus != "STOPPED"
		}
		return out[i].StartedAt.After(out[j].StartedAt)
	})
	return out, nil
}

func convertTask(t types.Task) Task {
	arn := aws.ToString(t.TaskArn)
	o := Task{
		ID: arn[strings.LastIndex(arn, "/")+1:], ARN: arn,
		LastStatus: aws.ToString(t.LastStatus), Desired: aws.ToString(t.DesiredStatus),
		Health: string(t.HealthStatus), StoppedReason: aws.ToString(t.StoppedReason), StopCode: string(t.StopCode),
		TaskDef: shortARN(aws.ToString(t.TaskDefinitionArn)), StartedAt: aws.ToTime(t.StartedAt), StoppedAt: aws.ToTime(t.StoppedAt),
		ExecEnabled: t.EnableExecuteCommand,
	}
	for _, c := range t.Containers {
		o.Containers = append(o.Containers, Container{
			Name: aws.ToString(c.Name), Status: aws.ToString(c.LastStatus), Health: string(c.HealthStatus),
			Reason: aws.ToString(c.Reason), ExitCode: c.ExitCode,
			RuntimeID: aws.ToString(c.RuntimeId), Image: aws.ToString(c.Image),
		})
	}
	return o
}

func shortARN(arn string) string { return arn[strings.LastIndex(arn, "/")+1:] }

func chunks(s []string, n int) [][]string {
	var out [][]string
	for len(s) > 0 {
		k := min(n, len(s))
		out = append(out, s[:k])
		s = s[k:]
	}
	return out
}

// ListClustersPage lists up to 100 clusters per page with details.
func ListClustersPage(ctx context.Context, s *auth.Session, token string) ([]Cluster, string, error) {
	in := &sdk.ListClustersInput{MaxResults: aws.Int32(100)}
	if token != "" {
		in.NextToken = aws.String(token)
	}
	page, err := s.ECS().ListClusters(ctx, in)
	if err != nil || len(page.ClusterArns) == 0 {
		return nil, "", err
	}
	res, err := s.ECS().DescribeClusters(ctx, &sdk.DescribeClustersInput{Clusters: page.ClusterArns})
	if err != nil {
		return nil, "", err
	}
	out := make([]Cluster, 0, len(res.Clusters))
	for _, c := range res.Clusters {
		out = append(out, Cluster{
			Name: aws.ToString(c.ClusterName), ARN: aws.ToString(c.ClusterArn), Status: aws.ToString(c.Status),
			Running: c.RunningTasksCount, Pending: c.PendingTasksCount,
			Services: c.ActiveServicesCount, Hosts: c.RegisteredContainerInstancesCount,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, aws.ToString(page.NextToken), nil
}

// ListServicesPage lists up to 50 services per page with details.
func ListServicesPage(ctx context.Context, s *auth.Session, cluster, token string) ([]Service, string, error) {
	in := &sdk.ListServicesInput{Cluster: aws.String(cluster), MaxResults: aws.Int32(50)}
	if token != "" {
		in.NextToken = aws.String(token)
	}
	page, err := s.ECS().ListServices(ctx, in)
	if err != nil {
		return nil, "", err
	}
	var out []Service
	for _, chunk := range chunks(page.ServiceArns, 10) {
		res, err := s.ECS().DescribeServices(ctx, &sdk.DescribeServicesInput{Cluster: aws.String(cluster), Services: chunk})
		if err != nil {
			return nil, "", err
		}
		for _, sv := range res.Services {
			out = append(out, convertService(sv))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, aws.ToString(page.NextToken), nil
}
