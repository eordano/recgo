package screencast

import (
	"fmt"
	"image"
)

// bgraToRGBA converts a top-down 32-bit GDI bitmap (BGRA, alpha undefined)
// into an opaque image.RGBA of the same size.
func bgraToRGBA(w, h int, bgra []byte) (*image.RGBA, error) {
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("empty %dx%d bitmap", w, h)
	}
	if len(bgra) < w*h*4 {
		return nil, fmt.Errorf("bitmap has %d bytes, %dx%d needs %d", len(bgra), w, h, w*h*4)
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < w*h*4; i += 4 {
		img.Pix[i+0] = bgra[i+2]
		img.Pix[i+1] = bgra[i+1]
		img.Pix[i+2] = bgra[i+0]
		img.Pix[i+3] = 0xff
	}
	return img, nil
}

// logicalSize is a display's size in DPI-independent points, the way macOS
// reports bounds: pixels scaled back by the monitor's effective DPI, so the
// session's Scale reads 1.25 on a 125% display.
func logicalSize(pixels, dpi int) int {
	if dpi <= 0 {
		return pixels
	}
	return (pixels*96 + dpi/2) / dpi
}
