package screencast

import (
	"image/png"
	"os"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

func TestKWinShotAgainstLiveCompositor(t *testing.T) {
	if os.Getenv("RECGO_KWIN_E2E") == "" {
		t.Skip("set RECGO_KWIN_E2E=1 inside a KDE session")
	}

	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		t.Fatalf("session bus: %v", err)
	}
	defer conn.Close()

	if !KWinAvailable(conn) {
		t.Fatal("org.kde.KWin.ScreenShot2 is not on the bus")
	}

	img, err := KWinShot(conn, "CaptureWorkspace", false, 15*time.Second)
	if err != nil {
		t.Fatalf("CaptureWorkspace: %v", err)
	}
	b := img.Bounds()
	t.Logf("captured %dx%d", b.Dx(), b.Dy())
	if b.Dx() < 100 || b.Dy() < 100 {
		t.Errorf("implausible size %dx%d", b.Dx(), b.Dy())
	}

	f, err := os.CreateTemp(t.TempDir(), "shot-*.png")
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		t.Fatalf("png encode: %v", err)
	}
	f.Close()
	st, _ := os.Stat(f.Name())
	t.Logf("png: %d bytes", st.Size())
	if st.Size() < 1000 {
		t.Errorf("png is only %d bytes", st.Size())
	}
}
