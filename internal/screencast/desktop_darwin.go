//go:build darwin

package screencast

import (
	"fmt"
	"image"
	"time"
)

type Desktop struct {
	now func() float64
}

type DesktopOptions struct {
	Now          func() float64
	FrameDir     string
	WindowMs     float64
	RestoreToken string
	GstLaunch    string
}

func OpenDesktop(o DesktopOptions) (*Desktop, error) {
	if o.Now == nil {
		return nil, fmt.Errorf("now is required")
	}
	if ok, why := Available(); !ok {
		return nil, fmt.Errorf("%s", why)
	}
	if err := CheckPermission(); err != nil {
		return nil, err
	}
	return &Desktop{now: o.Now}, nil
}

func (d *Desktop) RestoreToken() string { return "" }
func (d *Desktop) Backend() string      { return "macos-screencapture" }
func (d *Desktop) Frames() int          { return 0 }
func (d *Desktop) Close() error         { return nil }

func (d *Desktop) FrameAt(t float64) (string, bool, bool) { return "", false, false }

func (d *Desktop) Snapshot() (image.Image, error) {
	return Shot(false, true, 10*time.Second)
}
