// Package console builds AWS Management Console deep links for resources so
// users can jump from the terminal to the matching console page.
//
// Links target the standard "aws" partition. The console still requires the
// user to be signed in to the same account in their browser.
package console

import (
	"fmt"
	"net/url"
	"strings"
)

func host(region string) string { return "https://" + region + ".console.aws.amazon.com" }

// logEscape encodes a log group/stream name the way the CloudWatch console expects.
func logEscape(s string) string {
	return strings.ReplaceAll(url.QueryEscape(url.QueryEscape(s)), "%", "$")
}

func q(s string) string { return url.QueryEscape(s) }

func EC2Instance(region, id string) string {
	return fmt.Sprintf("%s/ec2/home?region=%s#InstanceDetails:instanceId=%s", host(region), region, q(id))
}

func ECSCluster(region, cluster string) string {
	return fmt.Sprintf("%s/ecs/v2/clusters/%s/services?region=%s", host(region), url.PathEscape(cluster), region)
}

func ECSService(region, cluster, service string) string {
	return fmt.Sprintf("%s/ecs/v2/clusters/%s/services/%s/health?region=%s", host(region), url.PathEscape(cluster), url.PathEscape(service), region)
}

func ECSTask(region, cluster, task string) string {
	return fmt.Sprintf("%s/ecs/v2/clusters/%s/tasks/%s/configuration?region=%s", host(region), url.PathEscape(cluster), url.PathEscape(task), region)
}

// ECSTaskDef takes "family:revision".
func ECSTaskDef(region, familyRev string) string {
	fam, rev, _ := strings.Cut(familyRev, ":")
	return fmt.Sprintf("%s/ecs/v2/task-definitions/%s/%s/containers?region=%s", host(region), url.PathEscape(fam), url.PathEscape(rev), region)
}

func RDSInstance(region, id string) string {
	return fmt.Sprintf("%s/rds/home?region=%s#database:id=%s", host(region), region, q(id))
}

func Lambda(region, name string) string {
	return fmt.Sprintf("%s/lambda/home?region=%s#/functions/%s", host(region), region, url.PathEscape(name))
}

func LogGroup(region, group string) string {
	return fmt.Sprintf("%s/cloudwatch/home?region=%s#logsV2:log-groups/log-group/%s", host(region), region, logEscape(group))
}

func Alarm(region, name string) string {
	return fmt.Sprintf("%s/cloudwatch/home?region=%s#alarmsV2:alarm/%s", host(region), region, url.PathEscape(name))
}

func LoadBalancer(region, arn string) string {
	return fmt.Sprintf("%s/ec2/home?region=%s#LoadBalancer:loadBalancerArn=%s", host(region), region, q(arn))
}

func TargetGroup(region, arn string) string {
	return fmt.Sprintf("%s/ec2/home?region=%s#TargetGroup:targetGroupArn=%s", host(region), region, q(arn))
}

func VPC(region, id string) string {
	return fmt.Sprintf("%s/vpcconsole/home?region=%s#VpcDetails:VpcId=%s", host(region), region, q(id))
}

func S3(region, bucket, prefix string) string {
	u := fmt.Sprintf("https://s3.console.aws.amazon.com/s3/buckets/%s?region=%s", url.PathEscape(bucket), region)
	if prefix != "" {
		u += "&prefix=" + q(prefix)
	}
	return u
}

func S3Object(region, bucket, key string) string {
	return fmt.Sprintf("https://s3.console.aws.amazon.com/s3/object/%s?region=%s&prefix=%s", url.PathEscape(bucket), region, q(key))
}

func Pipeline(region, name string) string {
	return fmt.Sprintf("%s/codesuite/codepipeline/pipelines/%s/view?region=%s", host(region), url.PathEscape(name), region)
}

func BuildProject(region, project string) string {
	return fmt.Sprintf("%s/codesuite/codebuild/projects/%s/history?region=%s", host(region), url.PathEscape(project), region)
}

// Build links to one build; buildID is "project:uuid".
func Build(region, account, buildID string) string {
	project, _, _ := strings.Cut(buildID, ":")
	if account == "" {
		return BuildProject(region, project)
	}
	return fmt.Sprintf("%s/codesuite/codebuild/%s/projects/%s/build/%s/?region=%s", host(region), account,
		url.PathEscape(project), url.PathEscape(buildID), region)
}

func Stack(region, stack string) string {
	return fmt.Sprintf("%s/cloudformation/home?region=%s#/stacks/stackinfo?stackId=%s", host(region), region, q(stack))
}

// Safe reports whether a URL is an AWS console link we generated.
func Safe(u string) bool {
	p, err := url.Parse(u)
	if err != nil || p.Scheme != "https" {
		return false
	}
	return p.Host == "s3.console.aws.amazon.com" || strings.HasSuffix(p.Host, ".console.aws.amazon.com")
}

func CloudTrailEvent(region, id string) string {
	return fmt.Sprintf("%s/cloudtrailv2/home?region=%s#/events/%s", host(region), region, url.PathEscape(id))
}
