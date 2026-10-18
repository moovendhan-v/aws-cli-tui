// Package summary builds the infrastructure overview shown on the home screen.
package summary

import (
	"context"
	"fmt"
	"sync"

	"devops/internal/aws/auth"
	"devops/internal/aws/ec2"
	"devops/internal/aws/ecs"
	"devops/internal/aws/lambda"
	"devops/internal/aws/metrics"
	"devops/internal/aws/networking"
	"devops/internal/aws/rds"
)

// Count is one overview figure; Err is set when that part failed.
type Count struct {
	Value  int
	Detail string
	Key    string // e.g. the first firing alarm's name
	Err    error
}

type Summary struct {
	EC2, ECS, RDS, Lambda Count
	UnhealthyTargets      Count
	ActiveAlarms          Count
}

// AllFailed returns the first error when every figure failed, else nil.
func (s Summary) AllFailed() error {
	all := []Count{s.EC2, s.ECS, s.RDS, s.Lambda, s.UnhealthyTargets, s.ActiveAlarms}
	for _, c := range all {
		if c.Err == nil {
			return nil
		}
	}
	return all[0].Err
}

// maxTargetGroups bounds DescribeTargetHealth calls on large accounts.
const maxTargetGroups = 40

// Load gathers all figures concurrently; individual failures don't fail the summary.
func Load(ctx context.Context, s *auth.Session, targetHealth bool) Summary {
	var (
		out Summary
		wg  sync.WaitGroup
	)
	run := func(f func()) { wg.Add(1); go func() { defer wg.Done(); f() }() }

	run(func() {
		l, err := ec2.List(ctx, s)
		running := 0
		for _, i := range l {
			if i.State == "running" {
				running++
			}
		}
		out.EC2 = Count{Value: len(l), Detail: fmt.Sprintf("%d running", running), Err: err}
	})
	run(func() {
		l, err := ecs.ListClusters(ctx, s)
		tasks := int32(0)
		for _, c := range l {
			tasks += c.Running
		}
		out.ECS = Count{Value: len(l), Detail: fmt.Sprintf("%d running tasks", tasks), Err: err}
	})
	run(func() {
		l, err := rds.List(ctx, s)
		avail := 0
		for _, d := range l {
			if d.Status == "available" {
				avail++
			}
		}
		out.RDS = Count{Value: len(l), Detail: fmt.Sprintf("%d available", avail), Err: err}
	})
	run(func() {
		l, err := lambda.List(ctx, s)
		out.Lambda = Count{Value: len(l), Detail: "functions", Err: err}
	})
	run(func() {
		l, err := metrics.Alarms(ctx, s, "ALARM")
		c := Count{Value: len(l), Err: err}
		if len(l) > 0 {
			c.Key = l[0].Name
			c.Detail = l[0].Name
			if l[0].Metric != "" {
				c.Detail += " (" + l[0].Metric + ")"
			}
		}
		out.ActiveAlarms = c
	})
	if targetHealth {
		run(func() { out.UnhealthyTargets = unhealthy(ctx, s) })
	} else {
		out.UnhealthyTargets = Count{Detail: "check disabled (overview.target_health)"}
	}

	wg.Wait()
	return out
}

func unhealthy(ctx context.Context, s *auth.Session) Count {
	tgs, err := networking.TargetGroups(ctx, s, "")
	if err != nil {
		return Count{Err: err}
	}
	if len(tgs) > maxTargetGroups {
		tgs = tgs[:maxTargetGroups]
	}
	var (
		mu     sync.Mutex
		wg     sync.WaitGroup
		total  int
		detail string
		first  error
		sem    = make(chan struct{}, 6)
	)
	for _, tg := range tgs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			ts, err := networking.TargetHealth(ctx, s, tg.ARN)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if first == nil {
					first = err
				}
				return
			}
			for _, t := range ts {
				if t.State == "unhealthy" || t.State == "unavailable" {
					total++
					if detail == "" {
						detail = fmt.Sprintf("%s (%s:%d)", tg.Name, t.ID, t.Port)
					}
				}
			}
		}()
	}
	wg.Wait()
	return Count{Value: total, Detail: detail, Err: first}
}
