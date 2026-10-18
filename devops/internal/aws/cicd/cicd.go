// Package cicd reads CodePipeline and CodeBuild state.
package cicd

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	cb "github.com/aws/aws-sdk-go-v2/service/codebuild"
	cbtypes "github.com/aws/aws-sdk-go-v2/service/codebuild/types"
	cp "github.com/aws/aws-sdk-go-v2/service/codepipeline"

	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/aws/auth"
)

type Action struct {
	Name, Status, Summary, Error string
	ExternalID, ExternalURL      string // e.g. the CodeBuild build ID
	Token                        string // manual approval token (pending approvals only)
	Changed                      time.Time
	Percent                      int32
}

type Stage struct {
	Name, Status, ExecutionID string
	TransitionEnabled         bool
	Actions                   []Action
}

type Pipeline struct {
	Name    string
	Updated time.Time
	Stages  []Stage
	Err     error // state could not be read
}

// Overall summarises stage statuses: InProgress beats Failed beats Succeeded.
func (p Pipeline) Overall() string {
	st := ""
	for _, s := range p.Stages {
		switch s.Status {
		case "InProgress":
			return "InProgress"
		case "Failed", "Stopped":
			st = s.Status
		case "Succeeded":
			if st == "" {
				st = "Succeeded"
			}
		}
	}
	return st
}

func State(ctx context.Context, s *auth.Session, name string) ([]Stage, error) {
	out, err := s.CodePipeline().GetPipelineState(ctx, &cp.GetPipelineStateInput{Name: aws.String(name)})
	if err != nil {
		return nil, err
	}
	var stages []Stage
	for _, st := range out.StageStates {
		stage := Stage{Name: aws.ToString(st.StageName), TransitionEnabled: true}
		if st.LatestExecution != nil {
			stage.Status = string(st.LatestExecution.Status)
			stage.ExecutionID = aws.ToString(st.LatestExecution.PipelineExecutionId)
		}
		if st.InboundTransitionState != nil {
			stage.TransitionEnabled = st.InboundTransitionState.Enabled
		}
		for _, a := range st.ActionStates {
			act := Action{Name: aws.ToString(a.ActionName)}
			if ex := a.LatestExecution; ex != nil {
				act.Status, act.Summary = string(ex.Status), aws.ToString(ex.Summary)
				act.ExternalID, act.ExternalURL = aws.ToString(ex.ExternalExecutionId), aws.ToString(ex.ExternalExecutionUrl)
				act.Token, act.Changed, act.Percent = aws.ToString(ex.Token), aws.ToTime(ex.LastStatusChange), aws.ToInt32(ex.PercentComplete)
				if ex.ErrorDetails != nil {
					act.Error = aws.ToString(ex.ErrorDetails.Message)
				}
			}
			stage.Actions = append(stage.Actions, act)
		}
		stages = append(stages, stage)
	}
	return stages, nil
}

// PipelinesPage lists up to 25 pipelines with their current stage states
// (fetched concurrently, at most 5 at a time).
func PipelinesPage(ctx context.Context, s *auth.Session, token string) ([]Pipeline, string, error) {
	in := &cp.ListPipelinesInput{MaxResults: aws.Int32(25)}
	if token != "" {
		in.NextToken = aws.String(token)
	}
	out, err := s.CodePipeline().ListPipelines(ctx, in)
	if err != nil {
		return nil, "", err
	}
	ps := make([]Pipeline, len(out.Pipelines))
	var wg sync.WaitGroup
	sem := make(chan struct{}, 5)
	for i, p := range out.Pipelines {
		ps[i] = Pipeline{Name: aws.ToString(p.Name), Updated: aws.ToTime(p.Updated)}
		wg.Add(1)
		go func() {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			ps[i].Stages, ps[i].Err = State(ctx, s, ps[i].Name)
		}()
	}
	wg.Wait()
	return ps, aws.ToString(out.NextToken), nil
}

type Execution struct {
	ID, Status, Trigger, Summary string
	Revision, RevisionURL        string
	Started, Updated             time.Time
}

func Executions(ctx context.Context, s *auth.Session, name string) ([]Execution, error) {
	out, err := s.CodePipeline().ListPipelineExecutions(ctx, &cp.ListPipelineExecutionsInput{PipelineName: aws.String(name), MaxResults: aws.Int32(10)})
	if err != nil {
		return nil, err
	}
	var ex []Execution
	for _, e := range out.PipelineExecutionSummaries {
		x := Execution{ID: aws.ToString(e.PipelineExecutionId), Status: string(e.Status),
			Started: aws.ToTime(e.StartTime), Updated: aws.ToTime(e.LastUpdateTime), Summary: aws.ToString(e.StatusSummary)}
		if e.Trigger != nil {
			x.Trigger = string(e.Trigger.TriggerType)
		}
		for _, r := range e.SourceRevisions {
			x.Revision, x.RevisionURL = firstLine(aws.ToString(r.RevisionSummary)), aws.ToString(r.RevisionUrl)
			if x.Revision == "" {
				x.Revision = short(aws.ToString(r.RevisionId))
			}
			break
		}
		ex = append(ex, x)
	}
	return ex, nil
}

type Phase struct {
	Type, Status, Message string
	Start, End            time.Time
	Duration              time.Duration
}

type Build struct {
	ID, Project, Status, Phase string
	Number                     int64
	SourceVersion, Initiator   string
	Start, End                 time.Time
	Phases                     []Phase
	LogGroup, LogStream        string
	Complete                   bool
}

func (b Build) Elapsed() time.Duration {
	if b.End.IsZero() {
		return time.Since(b.Start)
	}
	return b.End.Sub(b.Start)
}

func convertBuild(x cbtypes.Build) Build {
	b := Build{ID: aws.ToString(x.Id), Project: aws.ToString(x.ProjectName), Status: string(x.BuildStatus),
		Phase: aws.ToString(x.CurrentPhase), Number: aws.ToInt64(x.BuildNumber), Initiator: aws.ToString(x.Initiator),
		SourceVersion: short(aws.ToString(x.ResolvedSourceVersion)), Start: aws.ToTime(x.StartTime), End: aws.ToTime(x.EndTime),
		Complete: x.BuildComplete}
	if b.SourceVersion == "" {
		b.SourceVersion = aws.ToString(x.SourceVersion)
	}
	for _, p := range x.Phases {
		ph := Phase{Type: string(p.PhaseType), Status: string(p.PhaseStatus), Start: aws.ToTime(p.StartTime), End: aws.ToTime(p.EndTime),
			Duration: time.Duration(aws.ToInt64(p.DurationInSeconds)) * time.Second}
		for _, c := range p.Contexts {
			if m := aws.ToString(c.Message); m != "" {
				ph.Message = m
			}
		}
		b.Phases = append(b.Phases, ph)
	}
	if x.Logs != nil {
		b.LogGroup, b.LogStream = aws.ToString(x.Logs.GroupName), aws.ToString(x.Logs.StreamName)
	}
	return b
}

func Builds(ctx context.Context, s *auth.Session, ids []string) ([]Build, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	out, err := s.CodeBuild().BatchGetBuilds(ctx, &cb.BatchGetBuildsInput{Ids: ids})
	if err != nil {
		return nil, err
	}
	var bs []Build
	for _, x := range out.Builds {
		bs = append(bs, convertBuild(x))
	}
	sort.Slice(bs, func(i, j int) bool { return bs[i].Start.After(bs[j].Start) })
	return bs, nil
}

// ProjectsPage lists CodeBuild project names.
func ProjectsPage(ctx context.Context, s *auth.Session, token string) ([]string, string, error) {
	in := &cb.ListProjectsInput{SortBy: cbtypes.ProjectSortByTypeLastModifiedTime, SortOrder: cbtypes.SortOrderTypeDescending}
	if token != "" {
		in.NextToken = aws.String(token)
	}
	out, err := s.CodeBuild().ListProjects(ctx, in)
	if err != nil {
		return nil, "", err
	}
	return out.Projects, aws.ToString(out.NextToken), nil
}

// BuildsPage lists one page (≤100) of builds for a project, newest first.
func BuildsPage(ctx context.Context, s *auth.Session, project, token string) ([]Build, string, error) {
	in := &cb.ListBuildsForProjectInput{ProjectName: aws.String(project), SortOrder: cbtypes.SortOrderTypeDescending}
	if token != "" {
		in.NextToken = aws.String(token)
	}
	out, err := s.CodeBuild().ListBuildsForProject(ctx, in)
	if err != nil {
		return nil, "", err
	}
	bs, err := Builds(ctx, s, out.Ids)
	return bs, aws.ToString(out.NextToken), err
}

func short(id string) string {
	if len(id) > 10 {
		return id[:10]
	}
	return id
}

func firstLine(s string) string {
	for i, r := range s {
		if r == '\n' {
			return s[:i]
		}
	}
	return s
}
