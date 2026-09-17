//go:build linux

package screencast

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"time"

	"github.com/godbus/dbus/v5"
)

type Desktop struct {
	sess       *Session
	pump       *Pump
	conn       *dbus.Conn
	kwin       bool
	oneSource  bool
	source     Stream
	screenName string
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
// (macOS) or the portal's own dialog does it at start (Linux).
func ScreenChoiceLocal() bool { return false }

func OpenDesktop(o DesktopOptions) (*Desktop, error) {
	if o.Now == nil || o.FrameDir == "" {
		return nil, fmt.Errorf("now and frameDir are required")
	}

	po := Options{
		Types: SourceMonitor, Cursor: CursorEmbedded,
		Persist: PersistUntilRevoked, RestoreToken: o.RestoreToken,
	}
	if o.OneSource {
		po = Options{Types: SourceMonitor | SourceWindow, Cursor: CursorEmbedded, Persist: PersistNone}
	}
	sess, err := Open(po)
	if err != nil {
		return nil, err
	}

	pump, err := StartPump(sess, sess.Streams[0], PumpOptions{
		Now: o.Now, Dir: o.FrameDir, WindowMs: o.WindowMs, GstLaunch: o.GstLaunch,
	})
	if err != nil {
		sess.Close()
		return nil, err
	}

	d := &Desktop{sess: sess, pump: pump, oneSource: o.OneSource, source: sess.Streams[0]}
	if conn, err := dbus.ConnectSessionBus(); err == nil {
		if KWinAvailable(conn) {
			d.conn, d.kwin = conn, true
		} else {
			conn.Close()
		}
	}
	if d.oneSource && d.kwin && d.source.Type == SourceMonitor {
		if hit := FindDisplay(DisplayList(), int(d.source.W), int(d.source.H)); hit != nil {
			d.screenName = hit.Name
		}
	}
	return d, nil
}

func (d *Desktop) RestoreToken() string { return d.sess.RestoreToken }

func (d *Desktop) Backend() string {
	base := "xdg-portal-screencast"
	if d.kwin {
		base += " + kwin-screenshot2"
	}
	if !d.oneSource {
		return base
	}
	return fmt.Sprintf("%s (%s)", base, d.Source())
}

// Source names what the portal granted: the one screen or window picked at
// start, or the whole-desktop stream.
func (d *Desktop) Source() string {
	kind := "one screen"
	if d.source.Type == SourceWindow {
		kind = "one window"
	} else if d.screenName != "" {
		kind = "screen " + d.screenName
	}
	if !d.oneSource {
		kind = "desktop"
	}
	if d.source.W > 0 && d.source.H > 0 {
		return fmt.Sprintf("%s, %dx%d", kind, d.source.W, d.source.H)
	}
	return kind
}

func (d *Desktop) FrameAt(t float64) (path string, found, definitive bool) {
	f, ok, later := d.pump.FrameAt(t)
	return f.Path, ok, later
}

func (d *Desktop) Snapshot() (image.Image, error) {
	if d.kwin {
		if !d.oneSource {
			if img, err := KWinShot(d.conn, "CaptureWorkspace", true, 10*time.Second); err == nil {
				return img, nil
			}
		} else if d.screenName != "" {
			img, err := KWinShotArgs(d.conn, "CaptureScreen", []any{d.screenName}, true, 10*time.Second)
			if err == nil {
				return img, nil
			}
		}
	}
	f, ok, _ := d.pump.FrameAt(1e18)
	if !ok {
		return nil, fmt.Errorf("no frame available")
	}
	fh, err := os.Open(f.Path)
	if err != nil {
		return nil, err
	}
	defer fh.Close()
	return png.Decode(fh)
}

func (d *Desktop) Frames() int { return d.pump.Count() }

func (d *Desktop) Close() error {
	err := d.pump.Stop()
	if d.conn != nil {
		d.conn.Close()
	}
	d.sess.Close()
	return err
}
