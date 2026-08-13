package screencast

import (
	"image/png"
	"os"
	"testing"
	"time"
)

func TestPumpCapturesFrames(t *testing.T) {
	if os.Getenv("RECGO_PORTAL_E2E") == "" {
		t.Skip("set RECGO_PORTAL_E2E=1 inside a session with a ScreenCast portal")
	}

	sess, err := Open(Options{Types: SourceMonitor, Cursor: CursorHidden,
		Persist: PersistUntilRevoked, Timeout: 30 * time.Second})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer sess.Close()

	start := time.Now()
	now := func() float64 { return float64(time.Since(start).Milliseconds()) }

	dir := t.TempDir()
	p, err := StartPump(sess, sess.Streams[0], PumpOptions{
		Now: now, Dir: dir, WindowMs: 30_000,
		GstLaunch: os.Getenv("GST_LAUNCH"),
	})
	if err != nil {
		t.Fatalf("StartPump: %v", err)
	}

	time.Sleep(3 * time.Second)
	n := p.Count()
	if err := p.Stop(); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	t.Logf("captured %d frames in 3s", n)
	if n == 0 {
		t.Fatal("no frames captured")
	}

	f, _, _ := p.FrameAt(now())
	fh, err := os.Open(f.Path)
	if err != nil {
		t.Fatalf("open frame: %v", err)
	}
	defer fh.Close()
	img, err := png.Decode(fh)
	if err != nil {
		t.Fatalf("frame is not a valid PNG: %v", err)
	}
	b := img.Bounds()
	t.Logf("frame %dx%d", b.Dx(), b.Dy())
	if b.Dx() < 100 || b.Dy() < 100 {
		t.Errorf("implausible frame size %dx%d", b.Dx(), b.Dy())
	}
}

func TestPumpFrameAtLooksBackwards(t *testing.T) {
	p := &Pump{seen: map[string]bool{}, done: make(chan struct{}), window: 10_000}
	p.frames = []Frame{{T: 100, Path: "a"}, {T: 200, Path: "b"}, {T: 300, Path: "c"}}

	f, found, later := p.FrameAt(250)
	if !found || f.Path != "b" || !later {
		t.Errorf("FrameAt(250) = %+v found=%v later=%v", f, found, later)
	}
	if f, found, later := p.FrameAt(400); !found || f.Path != "c" || later {
		t.Errorf("FrameAt(400) = %+v found=%v later=%v", f, found, later)
	}
	if _, found, _ := p.FrameAt(50); found {
		t.Error("FrameAt(50) should find nothing")
	}
}
