// Package ssm checks Systems Manager prerequisites for interactive sessions.
package ssm

import (
	"context"
	"os/exec"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	sdk "github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/ssm/types"

	"devops/internal/aws/auth"
)

type NodeStatus struct {
	Managed      bool
	PingStatus   string
	AgentVersion string
	Platform     string
	LastPing     time.Time
}

// Ready reports whether a Session Manager session is expected to work.
func (n NodeStatus) Ready() bool { return n.Managed && n.PingStatus == string(types.PingStatusOnline) }

func Status(ctx context.Context, s *auth.Session, instanceID string) (NodeStatus, error) {
	out, err := s.SSM().DescribeInstanceInformation(ctx, &sdk.DescribeInstanceInformationInput{
		Filters: []types.InstanceInformationStringFilter{{Key: aws.String("InstanceIds"), Values: []string{instanceID}}},
	})
	if err != nil {
		return NodeStatus{}, err
	}
	if len(out.InstanceInformationList) == 0 {
		return NodeStatus{}, nil
	}
	ii := out.InstanceInformationList[0]
	return NodeStatus{
		Managed:      true,
		PingStatus:   string(ii.PingStatus),
		AgentVersion: aws.ToString(ii.AgentVersion),
		Platform:     aws.ToString(ii.PlatformName),
		LastPing:     aws.ToTime(ii.LastPingDateTime),
	}, nil
}

// PluginInstalled reports whether session-manager-plugin is on PATH.
func PluginInstalled() bool {
	_, err := exec.LookPath("session-manager-plugin")
	return err == nil
}
