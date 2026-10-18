package screens

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/executor"
	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/storage"
	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/tui/components"
	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/tui/core"
	"github.com/moovendhan-v/aws-dev-ops-utils/devops/internal/tui/theme"
)

type Activity struct {
	table     *components.Table
	history   bool
	hideQuiet bool
	past      []storage.JobRecord
	pastErr   error
}

func NewActivity() *Activity {
	return &Activity{table: components.NewTable(
		components.Column{Title: "", Width: 1},
		components.Column{Title: "#", Width: 4},
		components.Column{Title: "Command", Width: 24, Flex: true},
		components.Column{Title: "Context", Width: 22},
		components.Column{Title: "Started", Width: 8},
		components.Column{Title: "Took", Width: 7},
		components.Column{Title: "Result", Width: 14},
	)}
}

func (a *Activity) ID() string      { return "activity" }
func (a *Activity) Capturing() bool { return false }

func (a *Activity) Hints() []core.Hint {
	if a.history {
		return []core.Hint{{Key: "enter", Label: "Details"}, {Key: "h", Label: "This session"}}
	}
	quiet := "Hide discovery"
	if a.hideQuiet {
		quiet = "Show discovery"
	}
	return []core.Hint{{Key: "enter", Label: "Output"}, {Key: "x", Label: "Cancel"}, {Key: "a", Label: quiet}, {Key: "h", Label: "History"}}
}

func (a *Activity) Init(e *core.Env) tea.Cmd {
	a.refresh(e)
	return nil
}

func (a *Activity) refresh(e *core.Env) {
	if a.history {
		a.historyRows()
		return
	}
	views := e.Deps.Exec.Views()
	rows := make([]components.Row, 0, len(views))
	for _, v := range views {
		if a.hideQuiet && v.Quiet {
			continue
		}
		result := string(v.Status)
		switch {
		case v.ExitCode != nil:
			result = fmt.Sprintf("exit %d", *v.ExitCode)
			if v.Err != nil && v.ErrKind != "" {
				result += " · " + string(v.ErrKind)
			}
		case v.Err != nil && v.Status == executor.Failed:
			result = string(v.ErrKind)
		case v.Status == executor.Running && v.Kind == executor.KindStream:
			result = fmt.Sprintf("%d lines", v.Lines)
		case v.Status == executor.Running && v.Kind == executor.KindInteractive:
			result = "session open"
		}
		tone := theme.StatusTone(string(v.Status))
		if v.Quiet && tone == theme.ToneOK {
			tone = theme.ToneDim
		}
		name := v.Name
		if len(v.Resources) > 0 {
			name += " (" + strings.Join(v.Resources, ", ") + ")"
		}
		rows = append(rows, components.Row{
			Key: fmt.Sprint(v.ID), Data: v, Tone: tone,
			Cells: []string{core.StatusIcon(e, v.Status), fmt.Sprint(v.ID), name,
				v.Ctx.ProfileLabel() + "/" + v.Ctx.Region, v.Started.Format("15:04:05"),
				components.Dur(v.Elapsed()), result},
		})
	}
	a.table.SetRows(rows)
}

func (a *Activity) loadHistory(e *core.Env) {
	if e.Deps.Store == nil {
		a.pastErr = fmt.Errorf("history storage is unavailable")
		return
	}
	a.past, a.pastErr = e.Deps.Store.RecentJobs(e.Deps.SessionID, 500)
}

func (a *Activity) historyRows() {
	rows := make([]components.Row, 0, len(a.past))
	for i, r := range a.past {
		result := r.Status
		if r.ExitCode != nil {
			result = fmt.Sprintf("exit %d", *r.ExitCode)
		}
		ctx := r.Profile + "/" + r.Region
		rows = append(rows, components.Row{Key: fmt.Sprint(i), Data: r, Tone: theme.StatusTone(r.Status),
			Cells: []string{"·", fmt.Sprint(r.JobID), r.Name, ctx, r.Started.Format("01-02 15:04"),
				components.Dur(r.Ended.Sub(r.Started)), result}})
	}
	a.table.SetRows(rows)
}

func (a *Activity) Update(e *core.Env, msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case core.TickMsg:
		if !a.history {
			a.refresh(e)
		}
	case core.MouseMsg:
		if tableMouse(a.table, m, 1) {
			return a.Update(e, tea.KeyMsg{Type: tea.KeyEnter})
		}
	case tea.KeyMsg:
		row, ok := a.table.Selected()
		switch m.String() {
		case "enter":
			if !ok {
				return nil
			}
			if a.history {
				return e.Open(core.NewText("History", historyDetail(row.Data.(storage.JobRecord))))
			}
			v := row.Data.(executor.View)
			if j := e.Deps.Exec.Get(v.ID); j != nil {
				return e.Open(core.NewOutput(j))
			}
		case "x":
			if ok && !a.history {
				v := row.Data.(executor.View)
				if v.Kind == executor.KindInteractive {
					return e.Toast("Interactive sessions end when you exit them", theme.ToneNormal)
				}
				if e.Deps.Exec.Cancel(v.ID) {
					return e.Toast(fmt.Sprintf("Cancelling #%d — AWS-side work that already started may still complete", v.ID), theme.ToneWarn)
				}
			}
		case "a":
			a.hideQuiet = !a.hideQuiet
			a.refresh(e)
		case "h":
			a.history = !a.history
			if a.history {
				a.loadHistory(e)
			}
			a.refresh(e)
		default:
			a.table.Update(m)
		}
	}
	return nil
}

func historyDetail(r storage.JobRecord) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", r.Name)
	fmt.Fprintf(&b, "Command    %s\nStatus     %s\nProfile    %s\nAccount    %s\nRegion     %s\n", r.CommandID, r.Status, r.Profile, r.Account, r.Region)
	fmt.Fprintf(&b, "Started    %s\nDuration   %s\nKind       %s · %s\n", r.Started.Format("2006-01-02 15:04:05"), components.Dur(r.Ended.Sub(r.Started)), r.Kind, r.Risk)
	if len(r.Args) > 0 {
		keys := make([]string, 0, len(r.Args))
		for k := range r.Args {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		b.WriteString("\nArguments\n")
		for _, k := range keys {
			fmt.Fprintf(&b, "  %s = %s\n", k, r.Args[k])
		}
	}
	if r.Error != "" {
		fmt.Fprintf(&b, "\nError\n  %s\n", r.Error)
	}
	if r.Output != "" {
		b.WriteString("\nOutput\n" + r.Output)
	}
	return b.String()
}

func (a *Activity) View(e *core.Env, w, h int) string {
	title, right := "Running Commands", fmt.Sprintf("%d running · %d total", e.Deps.Exec.RunningCount(), len(e.Deps.Exec.Views()))
	if a.history {
		title, right = "Command History", fmt.Sprintf("%d from earlier sessions · retention %dd", len(a.past), e.Deps.Config.History.RetentionDays)
	}
	out := heading(title, right, w) + "\n"
	if a.history && a.pastErr != nil {
		return out + "\n" + errorView(a.pastErr, w)
	}
	if a.table.Total() == 0 {
		msg := "Nothing has run yet. Operations launched from any screen appear here."
		if a.history {
			msg = "No history from earlier sessions."
		}
		return out + "\n" + theme.Dim.Render(msg)
	}
	return out + a.table.View(w, h-1)
}
