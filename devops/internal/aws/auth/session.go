// Package auth resolves AWS configuration for a profile/region pair and caches
// service clients for that credential context.
package auth

import (
	"context"
	"fmt"
	"sync"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudformation"
	"github.com/aws/aws-sdk-go-v2/service/cloudtrail"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	"github.com/aws/aws-sdk-go-v2/service/codebuild"
	"github.com/aws/aws-sdk-go-v2/service/codepipeline"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	elbv2 "github.com/aws/aws-sdk-go-v2/service/elasticloadbalancingv2"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/rds"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/aws/meter"
)

// Context is the caller context every operation is bound to.
type Context struct {
	Profile   string
	Region    string
	AccountID string
	ARN       string
}

func (c Context) ProfileLabel() string {
	if c.Profile == "" {
		return "(environment)"
	}
	return c.Profile
}

// Session is an immutable credential/region context with lazily created clients.
type Session struct {
	Ctx     Context
	Cfg     aws.Config
	LoadErr error

	cache *clientCache
}

// clientCache is shared by sessions with the same credential context.
type clientCache struct {
	mu      sync.Mutex
	clients map[string]any
}

// NewSession loads shared config for profile/region. It does not call AWS.
func NewSession(ctx context.Context, profile, region string) *Session {
	opts := []func(*config.LoadOptions) error{config.WithRetryMaxAttempts(5)}
	if profile != "" {
		opts = append(opts, config.WithSharedConfigProfile(profile))
	}
	if region != "" {
		opts = append(opts, config.WithRegion(region))
	}
	s := &Session{Ctx: Context{Profile: profile, Region: region}, cache: &clientCache{clients: map[string]any{}}}
	cfg, err := config.LoadDefaultConfig(ctx, opts...)
	if err != nil {
		s.LoadErr = err
		return s
	}
	if cfg.Region == "" {
		cfg.Region = "us-east-1"
	}
	cfg.APIOptions = append(cfg.APIOptions, meter.Default.Middleware())
	s.Cfg = cfg
	s.Ctx.Region = cfg.Region
	return s
}

func (s *Session) Ready() error {
	if s == nil {
		return fmt.Errorf("no AWS session")
	}
	return s.LoadErr
}

// Identity calls sts:GetCallerIdentity.
func (s *Session) Identity(ctx context.Context) (account, arn string, err error) {
	if err := s.Ready(); err != nil {
		return "", "", err
	}
	out, err := s.STS().GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return "", "", err
	}
	return aws.ToString(out.Account), aws.ToString(out.Arn), nil
}

// WithIdentity returns a copy of the session context with identity filled in.
// Clients are shared because the credential context is unchanged.
func (s *Session) WithIdentity(account, arn string) *Session {
	n := &Session{Ctx: s.Ctx, Cfg: s.Cfg, LoadErr: s.LoadErr, cache: s.cache}
	n.Ctx.AccountID, n.Ctx.ARN = account, arn
	return n
}

func client[T any](s *Session, key string, mk func(aws.Config) T) T {
	s.cache.mu.Lock()
	defer s.cache.mu.Unlock()
	if c, ok := s.cache.clients[key]; ok {
		return c.(T)
	}
	c := mk(s.Cfg)
	s.cache.clients[key] = c
	return c
}

func (s *Session) STS() *sts.Client {
	return client(s, "sts", func(c aws.Config) *sts.Client { return sts.NewFromConfig(c) })
}
func (s *Session) EC2() *ec2.Client {
	return client(s, "ec2", func(c aws.Config) *ec2.Client { return ec2.NewFromConfig(c) })
}
func (s *Session) SSM() *ssm.Client {
	return client(s, "ssm", func(c aws.Config) *ssm.Client { return ssm.NewFromConfig(c) })
}
func (s *Session) ECS() *ecs.Client {
	return client(s, "ecs", func(c aws.Config) *ecs.Client { return ecs.NewFromConfig(c) })
}
func (s *Session) RDS() *rds.Client {
	return client(s, "rds", func(c aws.Config) *rds.Client { return rds.NewFromConfig(c) })
}
func (s *Session) Lambda() *lambda.Client {
	return client(s, "lambda", func(c aws.Config) *lambda.Client { return lambda.NewFromConfig(c) })
}
func (s *Session) ELB() *elbv2.Client {
	return client(s, "elbv2", func(c aws.Config) *elbv2.Client { return elbv2.NewFromConfig(c) })
}
func (s *Session) CloudWatch() *cloudwatch.Client {
	return client(s, "cloudwatch", func(c aws.Config) *cloudwatch.Client { return cloudwatch.NewFromConfig(c) })
}
func (s *Session) Logs() *cloudwatchlogs.Client {
	return client(s, "logs", func(c aws.Config) *cloudwatchlogs.Client { return cloudwatchlogs.NewFromConfig(c) })
}
func (s *Session) CloudFormation() *cloudformation.Client {
	return client(s, "cfn", func(c aws.Config) *cloudformation.Client { return cloudformation.NewFromConfig(c) })
}
func (s *Session) CodePipeline() *codepipeline.Client {
	return client(s, "codepipeline", func(c aws.Config) *codepipeline.Client { return codepipeline.NewFromConfig(c) })
}
func (s *Session) CodeBuild() *codebuild.Client {
	return client(s, "codebuild", func(c aws.Config) *codebuild.Client { return codebuild.NewFromConfig(c) })
}
func (s *Session) S3() *s3.Client {
	return client(s, "s3", func(c aws.Config) *s3.Client { return s3.NewFromConfig(c) })
}

// S3For returns an S3 client pinned to a bucket's region.
func (s *Session) S3For(region string) *s3.Client {
	if region == "" || region == s.Cfg.Region {
		return s.S3()
	}
	return client(s, "s3:"+region, func(c aws.Config) *s3.Client {
		return s3.NewFromConfig(c, func(o *s3.Options) { o.Region = region })
	})
}

func (s *Session) CloudTrail() *cloudtrail.Client {
	return client(s, "cloudtrail", func(c aws.Config) *cloudtrail.Client { return cloudtrail.NewFromConfig(c) })
}
