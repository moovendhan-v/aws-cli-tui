package core

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/moovendhan-v/aws-cli-tui/devops/internal/commands"
	"github.com/moovendhan-v/aws-cli-tui/devops/internal/executor"
	"github.com/moovendhan-v/aws-cli-tui/devops/internal/safety"
	"github.com/moovendhan-v/aws-cli-tui/devops/internal/tui/theme"
)

// OpenCommand is the single entry point for running a registered command,
// whether it came from search, the library or a resource screen:
// parameters → validation → confirmation → executor.
func (e *Env) OpenCommand(c *commands.Command, prefill map[string]string) tea.Cmd {
	if c == nil {
		return e.Toast("Command is not registered", theme.ToneBad)
	}
	// "name#label" entries carry display names (e.g. instance name for an ID).
	display := map[string]string{}
	values := map[string]string{}
	for k, v := range prefill {
		if name, ok := strings.CutSuffix(k, "#label"); ok {
			display[name] = v
		} else {
			values[k] = v
		}
	}
	forceForm := values["#form"] != ""
	delete(values, "#form")
	prefill = values
	if forceForm || missing(c, prefill) {
		form := NewForm(e, c, prefill, func(vals, display map[string]string) (tea.Cmd, error) {
			p, err := e.Deps.Runner.Prepare(c, vals, e.Ctx())
			if err != nil {
				return nil, err
			}
			p.Display = display
			return e.confirm(p), nil
		})
		form.setLabels(display)
		return tea.Sequence(e.Open(form), form.ensure(e))
	}
	p, err := e.Deps.Runner.Prepare(c, prefill, e.Ctx())
	if err != nil {
		return e.Toast(err.Error(), theme.ToneBad)
	}
	p.Display = display
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
	billed := e.Deps.Registry.BilledNote(p.Cmd)
	prod := e.Deps.Production.Is(p.Spec.Ctx.Profile, p.Spec.Ctx.AccountID)
	need := interactive || (mutating && (e.Deps.Config.Execution.ConfirmMutations || prod)) ||
		(billed != "" && e.Deps.Config.Limits.ConfirmBilled)
	if !need {
		return e.launch(p)
	}
	require := ""
	if prod && mutating && !interactive {
		require = p.Spec.Ctx.ProfileLabel()
	}
	c := NewConfirm(p, prod, require, func() tea.Cmd { return e.launch(p) })
	c.billed = billed
	if interactive {
		c.fullScreen = func() tea.Cmd { return e.launchFullScreen(p) }
	}
	return e.Open(c)
}

func (e *Env) launch(p *commands.Prepared) tea.Cmd {
	if p.Cmd.Execution.Type == commands.TypeInteractive {
		if e.Deps.Config.UI.EmbeddedSessions {
			return Msg(OpenTerminalMsg{Prepared: p})
		}
		return e.launchFullScreen(p)
	}
	return e.launchJob(p)
}

// launchFullScreen hands the whole terminal to an interactive command.
func (e *Env) launchFullScreen(p *commands.Prepared) tea.Cmd {
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

func (e *Env) launchJob(p *commands.Prepared) tea.Cmd {
	job, done := e.Deps.Runner.Start(p, e.Session)
	gen := e.Gen
	return tea.Batch(e.Open(NewOutput(job)), func() tea.Msg {
		d := <-done
		if p.Cmd.Risk != safety.ReadOnly {
			// The change may affect anything cached for this context.
			e.Deps.Cache.InvalidateContext(p.Spec.Ctx.Profile, p.Spec.Ctx.Region)
		}
		return ResultMsg{Screen: "", Key: "command", Gen: gen, Job: d.Job, Value: d.Result, Err: d.Err}
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
