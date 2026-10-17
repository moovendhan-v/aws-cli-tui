package core

import (
	tea "github.com/charmbracelet/bubbletea"

	"devops/internal/commands"
	"devops/internal/executor"
	"devops/internal/safety"
	"devops/internal/tui/theme"
)

// OpenCommand is the single entry point for running a registered command,
// whether it came from search, the library or a resource screen:
// parameters → validation → confirmation → executor.
func (e *Env) OpenCommand(c *commands.Command, prefill map[string]string) tea.Cmd {
	if c == nil {
		return e.Toast("Command is not registered", theme.ToneBad)
	}
	if missing(c, prefill) {
		return e.Open(NewForm(c, prefill, func(vals map[string]string) (tea.Cmd, error) {
			p, err := e.Deps.Runner.Prepare(c, vals, e.Ctx())
			if err != nil {
				return nil, err
			}
			return e.confirm(p), nil
		}))
	}
	p, err := e.Deps.Runner.Prepare(c, prefill, e.Ctx())
	if err != nil {
		return e.Toast(err.Error(), theme.ToneBad)
	}
	return e.confirm(p)
}

// RunCommandID looks up a command by ID and opens it.
func (e *Env) RunCommandID(id string, prefill map[string]string) tea.Cmd {
	return e.OpenCommand(e.Deps.Registry.Get(id), prefill)
}

// missing reports whether the form should be shown: some required parameter
// has no value, or nothing was prefilled for a command that takes parameters.
func missing(c *commands.Command, prefill map[string]string) bool {
	if len(c.Parameters) == 0 {
		return false
	}
	if len(prefill) == 0 {
		return true
	}
	for _, p := range c.Parameters {
		if p.Required && prefill[p.Name] == "" && p.Default == "" {
			return true
		}
	}
	return false
}

func (e *Env) confirm(p *commands.Prepared) tea.Cmd {
	interactive := p.Cmd.Execution.Type == commands.TypeInteractive
	mutating := p.Cmd.Risk != safety.ReadOnly
	prod := e.Deps.Production.Is(p.Spec.Ctx.Profile, p.Spec.Ctx.AccountID)
	need := interactive || (mutating && (e.Deps.Config.Execution.ConfirmMutations || prod))
	if !need {
		return e.launch(p)
	}
	require := ""
	if prod && mutating && !interactive {
		require = p.Spec.Ctx.ProfileLabel()
	}
	return e.Open(NewConfirm(p, prod, require, func() tea.Cmd { return e.launch(p) }))
}

func (e *Env) launch(p *commands.Prepared) tea.Cmd {
	if p.Cmd.Execution.Type == commands.TypeInteractive {
		cmd, job, err := e.Deps.Runner.Interactive(p)
		if err != nil {
			return e.Toast(err.Error(), theme.ToneBad)
		}
		return tea.ExecProcess(cmd, func(err error) tea.Msg {
			code := 0
			if cmd.ProcessState != nil {
				code = cmd.ProcessState.ExitCode()
			}
			e.Deps.Exec.EndInteractive(job, err, code)
			return InteractiveDoneMsg{Job: job, Err: err}
		})
	}
	job, done := e.Deps.Runner.Start(p, e.Session)
	return tea.Batch(e.Open(NewOutput(job)), func() tea.Msg {
		d := <-done
		return ResultMsg{Screen: "", Key: "command", Gen: e.Gen, Job: d.Job, Value: d.Result, Err: d.Err}
	})
}

// StatusIcon renders a job status glyph.
func StatusIcon(e *Env, s executor.Status) string {
	switch s {
	case executor.Queued:
		return theme.Dim.Render("◌")
	case executor.Running:
		return theme.Warn.Render(e.Spinner())
	case executor.Completed:
		return theme.OK.Render("✓")
	case executor.Cancelled:
		return theme.Dim.Render("■")
	}
	return theme.Bad.Render("✗")
}
