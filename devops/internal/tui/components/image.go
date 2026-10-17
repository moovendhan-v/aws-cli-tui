package components

import (
	"fmt"
	"image"
	"image/color"
	"strings"

	_ "image/gif"  // decoders
	_ "image/jpeg" //
	_ "image/png"  //

	_ "golang.org/x/image/bmp"
	_ "golang.org/x/image/webp"
)

// canvas is the colour transparent pixels are blended onto (#0d1117).
var canvas = color.RGBA{0x0d, 0x11, 0x17, 0xff}

// RenderImage draws img into at most w columns × h rows using "▀" half
// blocks (two pixels per cell) with 24-bit colour. It works in any terminal
// with truecolor support and keeps the aspect ratio.
func RenderImage(img image.Image, w, h int) string {
	b := img.Bounds()
	iw, ih := b.Dx(), b.Dy()
	if iw == 0 || ih == 0 || w <= 0 || h <= 0 {
		return ""
	}
	scale := min(float64(w)/float64(iw), float64(2*h)/float64(ih))
	tw, th := max(1, int(float64(iw)*scale)), max(2, int(float64(ih)*scale))
	th += th % 2
	sample := func(x, y int) color.RGBA {
		// Box-average the source area covered by this target pixel.
		x0, x1 := b.Min.X+x*iw/tw, b.Min.X+max((x+1)*iw/tw, x*iw/tw+1)
		y0, y1 := b.Min.Y+y*ih/th, b.Min.Y+max((y+1)*ih/th, y*ih/th+1)
		var r, g, bl, a, n uint32
		step := max(1, (x1-x0)*(y1-y0)/64) // cap work on huge images
		i := 0
		for yy := y0; yy < y1; yy++ {
			for xx := x0; xx < x1; xx++ {
				if i%step == 0 {
					cr, cg, cb, ca := img.At(xx, yy).RGBA()
					r, g, bl, a, n = r+cr, g+cg, bl+cb, a+ca, n+1
				}
				i++
			}
		}
		if n == 0 {
			return canvas
		}
		r, g, bl, a = r/n>>8, g/n>>8, bl/n>>8, a/n>>8
		// Alpha blend onto the canvas (colours are premultiplied).
		inv := 255 - a
		return color.RGBA{uint8(r + uint32(canvas.R)*inv/255), uint8(g + uint32(canvas.G)*inv/255), uint8(bl + uint32(canvas.B)*inv/255), 255}
	}
	pad := strings.Repeat(" ", max(0, (w-tw)/2))
	var sb strings.Builder
	for y := 0; y < th; y += 2 {
		if y > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(pad)
		for x := 0; x < tw; x++ {
			t, u := sample(x, y), sample(x, y+1)
			fmt.Fprintf(&sb, "\x1b[38;2;%d;%d;%dm\x1b[48;2;%d;%d;%dm▀", t.R, t.G, t.B, u.R, u.G, u.B)
		}
		sb.WriteString("\x1b[0m")
	}
	return sb.String()
}
