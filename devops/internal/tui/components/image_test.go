package components

import (
	"image"
	"image/color"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestRenderImageFitsBox(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 400, 100))
	for x := 0; x < 400; x++ {
		for y := 0; y < 100; y++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 200, 255})
		}
	}
	out := RenderImage(img, 40, 10)
	lines := strings.Split(out, "\n")
	if len(lines) > 10 {
		t.Fatalf("%d rows, want ≤ 10", len(lines))
	}
	for _, l := range lines {
		if w := ansi.StringWidth(l); w > 40 {
			t.Fatalf("row width %d > 40", w)
		}
	}
	if !strings.Contains(out, "\x1b[38;2;") || !strings.Contains(out, "▀") {
		t.Fatal("expected truecolor half blocks")
	}
}

func TestLineChart(t *testing.T) {
	vals := []float64{1, 5, 3, 9, 4, 8, 2}
	thr := 6.0
	lines := LineChart([]Series{{Values: vals}}, 50, 6, "%", &thr)
	if len(lines) != 6 {
		t.Fatalf("rows = %d", len(lines))
	}
	found := false
	for _, r := range ansi.Strip(strings.Join(lines, "\n")) {
		if r > 0x2800 && r <= 0x28FF {
			found = true
		}
	}
	if !found {
		t.Fatal("no braille dots drawn")
	}
	for _, l := range lines {
		if ansi.StringWidth(l) > 50 {
			t.Fatalf("row too wide: %d", ansi.StringWidth(l))
		}
	}
}
