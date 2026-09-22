package alt

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// A Python mock app and driver, when this machine has them: recgo
// must be a drop-in for altrelay.py from both ends.
func rigDir(t *testing.T) string {
	t.Helper()
	dir := os.Getenv("RECGO_ALT_RIG")
	if dir == "" {
		t.Skip("set RECGO_ALT_RIG to a directory holding mockapp.py and altdrive.py")
	}
	if _, err := os.Stat(filepath.Join(dir, "mockapp.py")); err != nil {
		t.Skip("RECGO_ALT_RIG has no mockapp.py")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not on PATH")
	}
	return dir
}

func TestRigMockAppAndAltdriveThroughRecgo(t *testing.T) {
	dir := rigDir(t)
	s := newServer(t, Options{})
	_, port, _ := splitHostPort(s.Addr())

	mock := exec.Command("python3", filepath.Join(dir, "mockapp.py"),
		"--host", "127.0.0.1", "--port", strconv.Itoa(port), "--app", "__default__")
	var mockOut strings.Builder
	mock.Stdout, mock.Stderr = &mockOut, &mockOut
	if err := mock.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { mock.Process.Kill(); mock.Wait() })
	waitFor(t, "mock app", func() bool { return s.Driver().Connected() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if v, err := s.Driver().GetServerVersion(ctx); err != nil || v != "2.3.0" {
		t.Fatalf("recgo's driver on the mock: %q %v", v, err)
	}

	drive := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("python3", append([]string{filepath.Join(dir, "altdrive.py"), "--connect-timeout", "5"}, args...)...)
		cmd.Env = append(os.Environ(), "DCL_ALT_HOST=127.0.0.1", "DCL_ALT_PORT="+strconv.Itoa(port), "DCL_ALT_APPNAME=__default__")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("altdrive %v: %v\n%s\nmock:\n%s", args, err, out, mockOut.String())
		}
		return string(out)
	}
	if out := drive("version"); !strings.Contains(out, "AltTester server version: 2.3.0") {
		t.Fatalf("altdrive version through recgo: %s", out)
	}
	if out := drive("click", "Play"); !strings.Contains(out, "Clicked Play (id=2)") {
		t.Fatalf("altdrive click through recgo: %s", out)
	}
	shot := filepath.Join(t.TempDir(), "shot.png")
	drive("shot", shot)
	if b, err := os.ReadFile(shot); err != nil || !strings.HasPrefix(string(b), "\x89PNG") {
		t.Fatalf("screenshot through recgo: %d bytes, %v", len(b), err)
	}
	// The external driver came and went; recgo's own driver is unaffected.
	if v, err := s.Driver().GetServerVersion(ctx); err != nil || v != "2.3.0" {
		t.Fatalf("recgo's driver after the external one left: %q %v", v, err)
	}
}
