//go:build darwin

package screencast

import (
	"fmt"
	"image"
	"time"
)

type Desktop struct {
	now     func() float64
	display int
	screen  DisplayInfo
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
// (macOS: screencapture -D takes a display number) or the platform's own
// dialog does it at start (Linux).
func ScreenChoiceLocal() bool { return true }

func OpenDesktop(o DesktopOptions) (*Desktop, error) {
	if o.Now == nil {
		return nil, fmt.Errorf("now is required")
	}
	if ok, why := Available(); !ok {
		return nil, fmt.Errorf("%s", why)
	}
	d := &Desktop{now: o.Now}
	if o.OneSource {
		if o.Screen == nil {
			return nil, fmt.Errorf("one-screen capture needs a display (pass -screen)")
		}
		d.screen = *o.Screen
		d.display = displayIndex(*o.Screen)
		if d.display == 0 {
			return nil, fmt.Errorf("display %s is not attached any more", o.Screen.Size())
		}
	}
	if err := CheckPermission(); err != nil {
		return nil, err
	}
	return d, nil
}

// displayIndex is the 1-based screencapture -D number of a display from
// DisplayList, which enumerates in the same order CoreGraphics does.
func displayIndex(want DisplayInfo) int {
	for i, d := range DisplayList() {
		if d == want {
			return i + 1
		}
	}
	return 0
}

func (d *Desktop) RestoreToken() string { return "" }

func (d *Desktop) Backend() string {
	if d.display == 0 {
		return "macos-screencapture"
	}
	return fmt.Sprintf("macos-screencapture (%s)", d.Source())
}

// Source names what is captured: the display picked at start, or every
// display.
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

func (d *Desktop) FrameAt(t float64) (string, bool, bool) { return "", false, false }

func (d *Desktop) Snapshot() (image.Image, error) {
	return ShotDisplay(d.display, true, 10*time.Second)
}
