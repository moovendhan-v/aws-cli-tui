package commands

import (
	"context"
	"fmt"
	"os/exec"
	"time"

	"github.com/moovendhan-v/aws-cli-tui/devops/internal/aws/auth"
	"github.com/moovendhan-v/aws-cli-tui/devops/internal/aws/ops"
	"github.com/moovendhan-v/aws-cli-tui/devops/internal/executor"
	"github.com/moovendhan-v/aws-cli-tui/devops/internal/safety"
)

// Prepared is a validated, context-bound command ready to run.
type Prepared struct {
	Cmd    *Command
	Values map[string]string
	// Display holds human labels for values (e.g. instance name for an ID).
	Display map[string]string
	Spec    executor.Spec
	Argv    []string    // process / interactive
	Steps   []*Prepared // workflow
	Then    *Prepared   // interactive follow-up, started once Ready appears
	cont    []bool
}

// Target lists the parameter values for confirmation dialogs.
func (p *Prepared) Target() [][2]string {
	var out [][2]string
	for _, prm := range p.Cmd.Parameters {
		if v, ok := p.Values[prm.Name]; ok {
			if l := p.Display[prm.Name]; l != "" && l != v {
				v = l + " (" + v + ")"
			}
			out = append(out, [2]string{prm.Name, v})
		}
	}
	return out
}

type Runner struct {
	Registry       *Registry
	Exec           *executor.Manager
	DefaultTimeout time.Duration
}

// Prepare validates values and binds the command to the caller context.
func (r *Runner) Prepare(c *Command, values map[string]string, actx auth.Context) (*Prepared, error) {
	vals, err := c.Bind(values)
	if err != nil {
		return nil, err
	}
	p := &Prepared{Cmd: c, Values: vals}
	timeout := c.Timeout
	if timeout == 0 {
		timeout = r.DefaultTimeout
	}
	var resources []string
	for _, prm := range c.Parameters {
		if prm.Type == "resource" && vals[prm.Name] != "" {
			resources = append(resources, vals[prm.Name])
		}
	}
	p.Spec = executor.Spec{
		Name: c.Name, CommandID: c.ID, Args: safety.RedactMap(vals), Ctx: actx,
		Risk: c.Risk, Timeout: timeout, Resources: resources,
	}

	all := map[string]string{"profile": actx.Profile, "region": actx.Region, "account": actx.AccountID}
	for k, v := range vals {
		all[k] = v
	}

	switch c.Execution.Type {
	case TypeSDK:
		p.Spec.Kind = executor.KindAPI
	case TypeProcess, TypeInteractive:
		p.Spec.Kind = executor.KindProcess
		if c.Execution.Type == TypeInteractive {
			p.Spec.Kind = executor.KindInteractive
			p.Spec.Timeout = 0
		}
		p.Argv = []string{c.Execution.Program}
		for _, a := range c.Execution.Args {
			p.Argv = append(p.Argv, expand(a, all))
		}
		if c.Execution.Program == "aws" {
			if actx.Profile != "" {
				p.Argv = append(p.Argv, "--profile", actx.Profile)
			}
			p.Argv = append(p.Argv, "--region", actx.Region)
		}
		if t := c.Execution.Then; t != nil {
			sv := map[string]string{}
			for k, v := range t.With {
				sv[k] = expand(v, all)
			}
			tp, err := r.Prepare(r.Registry.Get(t.Command), sv, actx)
			if err != nil {
				return nil, fmt.Errorf("then (%s): %w", t.Command, err)
			}
			p.Then = tp
		}
	case TypeWorkflow:
		p.Spec.Kind = executor.KindWorkflow
		if c.Timeout == 0 {
			p.Spec.Timeout = 5 * time.Minute
		}
		for i, st := range c.Execution.Steps {
			sub := r.Registry.Get(st.Command)
			sv := map[string]string{}
			for k, v := range st.With {
				sv[k] = expand(v, all)
			}
			sp, err := r.Prepare(sub, sv, actx)
			if err != nil {
				return nil, fmt.Errorf("step %d (%s): %w", i+1, sub.Name, err)
			}
			p.Steps = append(p.Steps, sp)
			p.cont = append(p.cont, st.ContinueOnError)
		}
	}
	return p, nil
}

// Start runs a non-interactive prepared command through the executor.
func (r *Runner) Start(p *Prepared, s *auth.Session) (*executor.Job, <-chan executor.Done) {
	r.Registry.NoteUsage(p.Cmd.ID)
	return r.Exec.Start(p.Spec, func(ctx context.Context, j *executor.Job) (any, error) {
		return r.run(ctx, j, p, s)
	})
}

func (r *Runner) run(ctx context.Context, j *executor.Job, p *Prepared, s *auth.Session) (any, error) {
	switch p.Cmd.Execution.Type {
	case TypeSDK:
		if err := s.Ready(); err != nil {
			return nil, err
		}
		op, _ := r.Registry.ops.Get(p.Cmd.Execution.Service, p.Cmd.Execution.Operation)
		res, err := op.Run(ctx, s, ops.Params(p.Values))
		if err == nil {
			j.WriteValue(res)
		}
		j.AddResult(p.Cmd.Name, res, err)
		return res, err
	case TypeProcess:
		return nil, executor.RunProcess(ctx, j, p.Argv)
	case TypeWorkflow:
		var failed int
		for i, st := range p.Steps {
			j.Printf("━━ Step %d/%d · %s", i+1, len(p.Steps), st.Cmd.Name)
			start := time.Now()
			_, err := r.run(ctx, j, st, s)
			if err != nil {
				failed++
				kind := safety.Classify(err)
				j.Printf("✗ %s failed (%s): %s", st.Cmd.Name, kind, safety.Describe(err))
				if h := safety.Hint(kind); h != "" {
					j.Printf("  hint: %s", h)
				}
				if !p.cont[i] || ctx.Err() != nil {
					return nil, fmt.Errorf("step %d (%s) failed: %w", i+1, st.Cmd.Name, err)
				}
			} else {
				j.Printf("✓ %s (%s)", st.Cmd.Name, time.Since(start).Round(time.Millisecond))
			}
			j.Write("")
		}
		if failed > 0 {
			j.Printf("Workflow finished with %d failed step(s).", failed)
		}
		return nil, nil
	}
	return nil, fmt.Errorf("%s commands cannot run in the background", p.Cmd.Execution.Type)
}

// Interactive returns the exec.Cmd for an interactive command plus its job.
// The caller hands the terminal to the process and calls Exec.EndInteractive.
func (r *Runner) Interactive(p *Prepared) (*exec.Cmd, *executor.Job, error) {
	cmd, err := executor.Command(context.Background(), p.Argv)
	if err != nil {
		return nil, nil, err
	}
	r.Registry.NoteUsage(p.Cmd.ID)
	return cmd, r.Exec.BeginInteractive(p.Spec), nil
}
