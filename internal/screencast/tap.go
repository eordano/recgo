package screencast

import "fmt"

// Click is one system-wide mouse press, in global display points.
type Click struct {
	X, Y   float64
	Button int // 1 left, 2 right
}

// DisplayInfo is one attached display: bounds in points, active mode in
// pixels, whether it is the main display, and the compositor's name for it
// (empty where the platform has none).
type DisplayInfo struct {
	W, H           int
	PixelW, PixelH int
	Main           bool
	Name           string
}

func (d DisplayInfo) Size() string {
	if d.PixelW > 0 && d.PixelH > 0 {
		return fmt.Sprintf("%dx%d", d.PixelW, d.PixelH)
	}
	return fmt.Sprintf("%dx%d", d.W, d.H)
}

// FindDisplay is the one display whose size (pixels or points) matches, or
// nil when none or several do.
func FindDisplay(displays []DisplayInfo, w, h int) *DisplayInfo {
	var hit *DisplayInfo
	for i := range displays {
		d := &displays[i]
		if (d.PixelW == w && d.PixelH == h) || (d.W == w && d.H == h) {
			if hit != nil {
				return nil
			}
			hit = d
		}
	}
	return hit
}
