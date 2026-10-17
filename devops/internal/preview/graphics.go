// Package preview shows images at full resolution in terminals that support
// an inline image protocol (Kitty graphics or iTerm2 inline images).
package preview

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"os"
	"strings"

	_ "golang.org/x/image/webp"
)

type Protocol string

const (
	None   Protocol = ""
	Kitty  Protocol = "kitty"
	ITerm2 Protocol = "iterm2"
)

// Detect guesses the terminal's image protocol from its environment.
// Terminals not listed (including Warp and VS Code, where support depends on
// version and settings) report None, and the block preview is used instead.
func Detect() Protocol {
	switch {
	case os.Getenv("KITTY_WINDOW_ID") != "", os.Getenv("TERM") == "xterm-kitty", os.Getenv("TERM_PROGRAM") == "ghostty":
		return Kitty
	case os.Getenv("TERM_PROGRAM") == "iTerm.app", os.Getenv("TERM_PROGRAM") == "WezTerm", os.Getenv("LC_TERMINAL") == "iTerm2":
		return ITerm2
	}
	if p := os.Getenv("DEVOPS_IMAGE_PROTOCOL"); p == "kitty" || p == "iterm2" {
		return Protocol(p) // manual override
	}
	return None
}

// Show prints the image file with the detected protocol and waits for Enter.
// It runs as `devops --show-image <file>` while the TUI is suspended.
func Show(path string, out io.Writer, in io.Reader) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	fmt.Fprint(out, "\x1b[2J\x1b[H")
	switch Detect() {
	case Kitty:
		if !bytes.HasPrefix(data, []byte("\x89PNG")) {
			img, _, err := image.Decode(bytes.NewReader(data))
			if err != nil {
				return err
			}
			var buf bytes.Buffer
			if err := png.Encode(&buf, img); err != nil {
				return err
			}
			data = buf.Bytes()
		}
		writeKitty(out, data)
	case ITerm2:
		fmt.Fprintf(out, "\x1b]1337;File=inline=1;size=%d;preserveAspectRatio=1:%s\a", len(data), base64.StdEncoding.EncodeToString(data))
	default:
		return fmt.Errorf("this terminal does not support inline images")
	}
	fmt.Fprint(out, "\r\n\r\nPress Enter to return… ")
	_, _ = bufio.NewReader(in).ReadString('\n')
	if Detect() == Kitty {
		fmt.Fprint(out, "\x1b_Ga=d\x1b\\") // delete images
	}
	return nil
}

// writeKitty sends a PNG/JPEG file in 4 KB chunks (f=100 means "file bytes").
func writeKitty(out io.Writer, data []byte) {
	enc := base64.StdEncoding.EncodeToString(data)
	for i := 0; i < len(enc); i += 4096 {
		end := min(i+4096, len(enc))
		more := 1
		if end == len(enc) {
			more = 0
		}
		ctrl := fmt.Sprintf("m=%d", more)
		if i == 0 {
			ctrl = "a=T,f=100," + ctrl
		}
		fmt.Fprintf(out, "\x1b_G%s;%s\x1b\\", ctrl, enc[i:end])
	}
}

// Name describes the terminal support for messages.
func Name() string {
	switch Detect() {
	case Kitty:
		return "Kitty graphics"
	case ITerm2:
		return "iTerm2 inline images"
	}
	tp := os.Getenv("TERM_PROGRAM")
	if strings.EqualFold(tp, "WarpTerminal") {
		return "Warp (no inline image protocol detected)"
	}
	if tp == "vscode" {
		return "VS Code (inline images not detected)"
	}
	return "this terminal (no inline image protocol detected)"
}
