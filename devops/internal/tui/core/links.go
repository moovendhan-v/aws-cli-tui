package core

import (
	"os/exec"
	"runtime"

	"github.com/atotto/clipboard"
	tea "github.com/charmbracelet/bubbletea"

	"devops/internal/aws/console"
	"devops/internal/tui/theme"
)

// OpenURL opens an AWS console link in the default browser. Only links built
// by the console package (or S3 presigned URLs we create) are allowed.
func (e *Env) OpenURL(u string, trusted bool) tea.Cmd {
	if u == "" {
		return e.Toast("No console link for this item", theme.ToneDim)
	}
	if !trusted && !console.Safe(u) {
		return e.Toast("Refusing to open a non-AWS link", theme.ToneBad)
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", u)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", u)
	default:
		cmd = exec.Command("xdg-open", u)
	}
	if err := cmd.Start(); err != nil {
		return tea.Batch(e.Copy(u, "link"), e.Toast("Could not open a browser — link copied instead", theme.ToneWarn))
	}
	go cmd.Wait()
	return e.Toast("Opened in the AWS console (sign in to the same account if asked)", theme.ToneOK)
}

// Copy puts text on the system clipboard.
func (e *Env) Copy(text, what string) tea.Cmd {
	if text == "" {
		return nil
	}
	if err := clipboard.WriteAll(text); err != nil {
		return e.Toast("Clipboard unavailable: "+text, theme.ToneWarn)
	}
	return e.Toast("Copied "+what+": "+text, theme.ToneOK)
}
