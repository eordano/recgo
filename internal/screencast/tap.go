package screencast

// Click is one system-wide mouse press, in global display points.
type Click struct {
	X, Y   float64
	Button int // 1 left, 2 right
}

// DisplayInfo is one attached display: bounds in points, active mode in
// pixels, and whether it is the main display.
type DisplayInfo struct {
	W, H           int
	PixelW, PixelH int
	Main           bool
}
