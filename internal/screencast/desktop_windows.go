//go:build windows

package screencast

import (
	"fmt"
	"image"
)

// Windows mirrors the macOS design: no frame pump, a GDI screenshot per
// shot. Frames() is 0 and FrameAt never finds anything, so captureAround
// takes the mark-instant shot itself and notes the missing -before/-after.

type Desktop struct {
	now     func() float64
	display int
	screen  DisplayInfo
	bounds  rect
}

type DesktopOptions struct {
	Now          func() float64
	FrameDir     string
	WindowMs     float64
	RestoreToken string
	GstLaunch    string
	OneSource    bool
	Screen       *DisplayInfo
}

// ScreenChoiceLocal says whether the caller must pick the screen itself
// (Windows and macOS: a display number) or the platform's own dialog does
// it at start (Linux).
func ScreenChoiceLocal() bool { return true }

func OpenDesktop(o DesktopOptions) (*Desktop, error) {
	if o.Now == nil {
		return nil, fmt.Errorf("now is required")
	}
	ensureDPIAware()
	d := &Desktop{now: o.Now}
	if o.OneSource {
		if o.Screen == nil {
			return nil, fmt.Errorf("one-screen capture needs a display (pass -screen)")
		}
		d.screen = *o.Screen
		for i, m := range monitors() {
			if m.info() == *o.Screen {
				d.display, d.bounds = i+1, m.bounds
			}
		}
		if d.display == 0 {
			return nil, fmt.Errorf("display %s is not attached any more", o.Screen.Size())
		}
	}
	// A process started over ssh or as a service lands in session 0, where
	// GetDC(0) is a desktop nobody sees; fail here rather than record black.
	img, err := d.Snapshot()
	if err != nil {
		return nil, fmt.Errorf("screen capture: %w (a service or ssh session has no interactive desktop)", err)
	}
	if b := img.Bounds(); b.Dx() == 0 || b.Dy() == 0 {
		return nil, fmt.Errorf("screen capture returned an empty image")
	}
	return d, nil
}

func (d *Desktop) RestoreToken() string { return "" }

func (d *Desktop) Backend() string {
	if d.display == 0 {
		return "windows-gdi"
	}
	return fmt.Sprintf("windows-gdi (%s)", d.Source())
}

// Source names what is captured: the display picked at start, or the whole
// virtual screen.
func (d *Desktop) Source() string {
	if d.display == 0 {
		return "desktop"
	}
	main := ""
	if d.screen.Main {
		main = ", main"
	}
	return fmt.Sprintf("display %d, %s%s", d.display, d.screen.Size(), main)
}

func (d *Desktop) Frames() int  { return 0 }
func (d *Desktop) Close() error { return nil }

// FrameStream is false: GDI takes one screenshot per event, so there is
// never evidence that the screen did or did not repaint between two.
func (d *Desktop) FrameStream() bool { return false }

func (d *Desktop) FrameAt(t float64) (string, bool, bool) { return "", false, false }

func (d *Desktop) Snapshot() (image.Image, error) {
	if d.display == 0 {
		return captureRect(virtualScreen())
	}
	return captureRect(d.bounds)
}

// DisplayList reports the attached displays: physical pixels, the size in
// DPI-scaled points, the primary one, and the GDI device name.
func DisplayList() []DisplayInfo {
	var out []DisplayInfo
	for _, m := range monitors() {
		out = append(out, m.info())
	}
	return out
}
