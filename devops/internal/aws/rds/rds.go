// Package rds discovers RDS database instances.
package rds

import (
	"context"
	"fmt"
	"sort"

	"github.com/aws/aws-sdk-go-v2/aws"
	sdk "github.com/aws/aws-sdk-go-v2/service/rds"

	"devops/internal/aws/auth"
)

type Instance struct {
	ID, Engine, Version, Status, Class string
	Endpoint                           string
	Port                               int32
	MultiAZ, Public, Encrypted         bool
	AZ, Cluster                        string
}

func (i Instance) EndpointLabel() string {
	if i.Endpoint == "" {
		return "-"
	}
	return fmt.Sprintf("%s:%d", i.Endpoint, i.Port)
}

func List(ctx context.Context, s *auth.Session) ([]Instance, error) {
	var out []Instance
	p := sdk.NewDescribeDBInstancesPaginator(s.RDS(), &sdk.DescribeDBInstancesInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, d := range page.DBInstances {
			i := Instance{
				ID: aws.ToString(d.DBInstanceIdentifier), Engine: aws.ToString(d.Engine),
				Version: aws.ToString(d.EngineVersion), Status: aws.ToString(d.DBInstanceStatus),
				Class: aws.ToString(d.DBInstanceClass), AZ: aws.ToString(d.AvailabilityZone),
				Cluster: aws.ToString(d.DBClusterIdentifier),
				MultiAZ: aws.ToBool(d.MultiAZ), Public: aws.ToBool(d.PubliclyAccessible), Encrypted: aws.ToBool(d.StorageEncrypted),
			}
			if d.Endpoint != nil {
				i.Endpoint = aws.ToString(d.Endpoint.Address)
				i.Port = aws.ToInt32(d.Endpoint.Port)
			}
			out = append(out, i)
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].ID < out[b].ID })
	return out, nil
}
