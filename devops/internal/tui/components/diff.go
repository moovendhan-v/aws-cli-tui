package components

import (
	"strconv"

	"github.com/moovendhan-v/aws-cli-tui/devops/internal/tui/theme"
)

// Diff returns a unified-style line diff of a → b (LCS based), with
// unchanged runs longer than 6 lines collapsed.
func Diff(a, b []string) []string {
	n, m := len(a), len(b)
	if n*m > 4_000_000 {
		return []string{theme.Warn.Render("definitions too large to compare here")}
	}
	lcs := make([][]int, n+1)
	for i := range lcs {
		lcs[i] = make([]int, m+1)
	}
	for i := n - 1; i >= 0; i-- {
		for j := m - 1; j >= 0; j-- {
			if a[i] == b[j] {
				lcs[i][j] = lcs[i+1][j+1] + 1
			} else {
				lcs[i][j] = max(lcs[i+1][j], lcs[i][j+1])
			}
		}
	}
	type op struct {
		kind byte
		text string
	}
	var ops []op
	i, j := 0, 0
	for i < n && j < m {
		switch {
		case a[i] == b[j]:
			ops = append(ops, op{' ', a[i]})
			i++
			j++
		case lcs[i+1][j] >= lcs[i][j+1]:
			ops = append(ops, op{'-', a[i]})
			i++
		default:
			ops = append(ops, op{'+', b[j]})
			j++
		}
	}
	for ; i < n; i++ {
		ops = append(ops, op{'-', a[i]})
	}
	for ; j < m; j++ {
		ops = append(ops, op{'+', b[j]})
	}
	var out []string
	changes := 0
	for k := 0; k < len(ops); {
		if ops[k].kind != ' ' {
			changes++
			if ops[k].kind == '-' {
				out = append(out, theme.Bad.Render("- "+ops[k].text))
			} else {
				out = append(out, theme.OK.Render("+ "+ops[k].text))
			}
			k++
			continue
		}
		end := k
		for end < len(ops) && ops[end].kind == ' ' {
			end++
		}
		run := ops[k:end]
		if len(run) > 6 {
			for _, o := range run[:2] {
				out = append(out, theme.Dim.Render("  "+o.text))
			}
			out = append(out, theme.Dim.Render("  ⋯ "+strconv.Itoa(len(run)-4)+" unchanged lines"))
			for _, o := range run[len(run)-2:] {
				out = append(out, theme.Dim.Render("  "+o.text))
			}
		} else {
			for _, o := range run {
				out = append(out, theme.Dim.Render("  "+o.text))
			}
		}
		k = end
	}
	if changes == 0 {
		return []string{theme.OK.Render("The two revisions are identical (environment values are hidden).")}
	}
	return out
}
