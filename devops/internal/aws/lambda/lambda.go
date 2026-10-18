// Package lambda lists Lambda functions.
package lambda

import (
	"context"
	"encoding/base64"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	sdk "github.com/aws/aws-sdk-go-v2/service/lambda"
	"github.com/aws/aws-sdk-go-v2/service/lambda/types"

	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/aws/auth"
)

type Function struct {
	Name, Runtime, State, LastModified, Handler string
	MemoryMB, TimeoutSec                        int32
	CodeSize                                    int64
}

func List(ctx context.Context, s *auth.Session) ([]Function, error) {
	var out []Function
	p := sdk.NewListFunctionsPaginator(s.Lambda(), &sdk.ListFunctionsInput{})
	for p.HasMorePages() {
		page, err := p.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, f := range page.Functions {
			out = append(out, convert(f))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// ListPage returns up to 50 functions per page.
func ListPage(ctx context.Context, s *auth.Session, marker string) ([]Function, string, error) {
	in := &sdk.ListFunctionsInput{MaxItems: aws.Int32(50)}
	if marker != "" {
		in.Marker = aws.String(marker)
	}
	page, err := s.Lambda().ListFunctions(ctx, in)
	if err != nil {
		return nil, "", err
	}
	out := make([]Function, 0, len(page.Functions))
	for _, f := range page.Functions {
		out = append(out, convert(f))
	}
	return out, aws.ToString(page.NextMarker), nil
}

func convert(f types.FunctionConfiguration) Function {
	return Function{
		Name: aws.ToString(f.FunctionName), Runtime: string(f.Runtime), State: string(f.State),
		LastModified: aws.ToString(f.LastModified), Handler: aws.ToString(f.Handler),
		MemoryMB: aws.ToInt32(f.MemorySize), TimeoutSec: aws.ToInt32(f.Timeout), CodeSize: f.CodeSize,
	}
}

// Detail is a function's configuration with environment values hidden.
type Detail struct {
	Function
	ARN, Role, Description, Arch, Package string
	EnvNames                              []string
	Layers                                []string
	LogGroup                              string
	VPC                                   string
	Subnets, SecurityGroups               []string
	DLQ, Tracing, StateReason             string
	EphemeralMB                           int32
}

func Describe(ctx context.Context, s *auth.Session, name string) (Detail, error) {
	out, err := s.Lambda().GetFunctionConfiguration(ctx, &sdk.GetFunctionConfigurationInput{FunctionName: aws.String(name)})
	if err != nil {
		return Detail{}, err
	}
	d := Detail{Function: convert(*cfgOf(out)), ARN: aws.ToString(out.FunctionArn), Role: shortARN(aws.ToString(out.Role)),
		Description: aws.ToString(out.Description), Package: string(out.PackageType), StateReason: aws.ToString(out.StateReason),
		LogGroup: "/aws/lambda/" + name}
	for _, a := range out.Architectures {
		d.Arch = string(a)
	}
	if out.Environment != nil {
		for k := range out.Environment.Variables {
			d.EnvNames = append(d.EnvNames, k)
		}
		sort.Strings(d.EnvNames)
	}
	for _, l := range out.Layers {
		d.Layers = append(d.Layers, shortARN(aws.ToString(l.Arn)))
	}
	if lc := out.LoggingConfig; lc != nil && aws.ToString(lc.LogGroup) != "" {
		d.LogGroup = aws.ToString(lc.LogGroup)
	}
	if v := out.VpcConfig; v != nil {
		d.VPC, d.Subnets, d.SecurityGroups = aws.ToString(v.VpcId), v.SubnetIds, v.SecurityGroupIds
	}
	if out.DeadLetterConfig != nil {
		d.DLQ = shortARN(aws.ToString(out.DeadLetterConfig.TargetArn))
	}
	if out.TracingConfig != nil {
		d.Tracing = string(out.TracingConfig.Mode)
	}
	if out.EphemeralStorage != nil {
		d.EphemeralMB = aws.ToInt32(out.EphemeralStorage.Size)
	}
	return d, nil
}

// cfgOf converts the GetFunctionConfiguration output to the shared type.
func cfgOf(out *sdk.GetFunctionConfigurationOutput) *types.FunctionConfiguration {
	return &types.FunctionConfiguration{FunctionName: out.FunctionName, Runtime: out.Runtime, State: out.State,
		LastModified: out.LastModified, Handler: out.Handler, MemorySize: out.MemorySize, Timeout: out.Timeout, CodeSize: out.CodeSize}
}

// shortARN keeps the readable tail of an ARN (role name, layer:version…).
func shortARN(a string) string {
	if i := strings.LastIndex(a, "/"); i >= 0 {
		return a[i+1:]
	}
	if parts := strings.Split(a, ":"); len(parts) > 6 {
		return strings.Join(parts[6:], ":")
	}
	return a
}

type InvokeResult struct {
	StatusCode    int32
	FunctionError string
	Payload       string
	LogTail       string
	Duration      time.Duration
}

// Invoke runs a function synchronously with a JSON payload and returns the
// response and the last 4 KB of its log.
func Invoke(ctx context.Context, s *auth.Session, name, payload string) (InvokeResult, error) {
	start := time.Now()
	out, err := s.Lambda().Invoke(ctx, &sdk.InvokeInput{FunctionName: aws.String(name), Payload: []byte(payload),
		InvocationType: types.InvocationTypeRequestResponse, LogType: types.LogTypeTail})
	if err != nil {
		return InvokeResult{}, err
	}
	r := InvokeResult{StatusCode: out.StatusCode, FunctionError: aws.ToString(out.FunctionError), Payload: string(out.Payload),
		Duration: time.Since(start)}
	if lr := aws.ToString(out.LogResult); lr != "" {
		if b, err := base64.StdEncoding.DecodeString(lr); err == nil {
			r.LogTail = string(b)
		}
	}
	return r, nil
}
