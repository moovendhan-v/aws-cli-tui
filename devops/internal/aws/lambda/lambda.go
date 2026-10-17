// Package lambda lists Lambda functions.
package lambda

import (
	"context"
	"sort"

	"github.com/aws/aws-sdk-go-v2/aws"
	sdk "github.com/aws/aws-sdk-go-v2/service/lambda"

	"devops/internal/aws/auth"
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
			out = append(out, Function{
				Name: aws.ToString(f.FunctionName), Runtime: string(f.Runtime), State: string(f.State),
				LastModified: aws.ToString(f.LastModified), Handler: aws.ToString(f.Handler),
				MemoryMB: aws.ToInt32(f.MemorySize), TimeoutSec: aws.ToInt32(f.Timeout), CodeSize: f.CodeSize,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
