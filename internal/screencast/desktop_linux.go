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
	sess *Session
	pump *Pump
	conn *dbus.Conn
	kwin bool
}

type DesktopOptions struct {
	Now          func() float64
	FrameDir     string
	WindowMs     float64
	RestoreToken string
	GstLaunch    string
}

func OpenDesktop(o DesktopOptions) (*Desktop, error) {
	if o.Now == nil || o.FrameDir == "" {
		return nil, fmt.Errorf("now and frameDir are required")
	}

	sess, err := Open(Options{
		Types: SourceMonitor, Cursor: CursorEmbedded,
		Persist: PersistUntilRevoked, RestoreToken: o.RestoreToken,
	})
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

	d := &Desktop{sess: sess, pump: pump}
	if conn, err := dbus.ConnectSessionBus(); err == nil {
		if KWinAvailable(conn) {
			d.conn, d.kwin = conn, true
		} else {
			conn.Close()
		}
	}
	return d, nil
}

func (d *Desktop) RestoreToken() string { return d.sess.RestoreToken }

func (d *Desktop) Backend() string {
	if d.kwin {
		return "xdg-portal-screencast + kwin-screenshot2"
	}
	return "xdg-portal-screencast"
}

func (d *Desktop) FrameAt(t float64) (path string, found, definitive bool) {
	f, ok, later := d.pump.FrameAt(t)
	return f.Path, ok, later
}

func (d *Desktop) Snapshot() (image.Image, error) {
	if d.kwin {
		img, err := KWinShot(d.conn, "CaptureWorkspace", true, 10*time.Second)
		if err == nil {
			return img, nil
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
