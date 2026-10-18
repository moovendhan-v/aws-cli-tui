// Package rds discovers RDS database instances.
package rds

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	sdk "github.com/aws/aws-sdk-go-v2/service/rds"
	"github.com/aws/aws-sdk-go-v2/service/rds/types"

	"github.com/moovendhan-v/aws-cli-tui/devops/internal/aws/auth"
)

type Instance struct {
	ID, Engine, Version, Status, Class string
	Endpoint                           string
	Port                               int32
	MultiAZ, Public, Encrypted         bool
	AZ, Cluster                        string
	DBName, MasterUser, VPC            string
}

// Client returns the interactive client family for the engine ("postgres",
// "mysql" or "" when this tool has no client for it).
func (i Instance) Client() string {
	switch {
	case strings.Contains(i.Engine, "postgres"):
		return "postgres"
	case strings.Contains(i.Engine, "mysql"), strings.Contains(i.Engine, "mariadb"):
		return "mysql"
	}
	return ""
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
			out = append(out, convert(d))
		}
	}
	sort.Slice(out, func(a, b int) bool { return out[a].ID < out[b].ID })
	return out, nil
}

// ListPage returns up to 100 DB instances per page.
func ListPage(ctx context.Context, s *auth.Session, marker string) ([]Instance, string, error) {
	in := &sdk.DescribeDBInstancesInput{MaxRecords: aws.Int32(100)}
	if marker != "" {
		in.Marker = aws.String(marker)
	}
	page, err := s.RDS().DescribeDBInstances(ctx, in)
	if err != nil {
		return nil, "", err
	}
	var out []Instance
	for _, d := range page.DBInstances {
		out = append(out, convert(d))
	}
	return out, aws.ToString(page.Marker), nil
}

func convert(d types.DBInstance) Instance {
	i := Instance{
		ID: aws.ToString(d.DBInstanceIdentifier), Engine: aws.ToString(d.Engine),
		Version: aws.ToString(d.EngineVersion), Status: aws.ToString(d.DBInstanceStatus),
		Class: aws.ToString(d.DBInstanceClass), AZ: aws.ToString(d.AvailabilityZone),
		Cluster: aws.ToString(d.DBClusterIdentifier),
		MultiAZ: aws.ToBool(d.MultiAZ), Public: aws.ToBool(d.PubliclyAccessible), Encrypted: aws.ToBool(d.StorageEncrypted),
		DBName: aws.ToString(d.DBName), MasterUser: aws.ToString(d.MasterUsername),
	}
	if d.DBSubnetGroup != nil {
		i.VPC = aws.ToString(d.DBSubnetGroup.VpcId)
	}
	if d.Endpoint != nil {
		i.Endpoint = aws.ToString(d.Endpoint.Address)
		i.Port = aws.ToInt32(d.Endpoint.Port)
	}
	return i
}
