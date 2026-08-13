package transcribe

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func fakeFFmpeg(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "ffmpeg")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return dir
}

func TestStartEmitsErrWhenFFmpegDies(t *testing.T) {
	fakeFFmpeg(t, "#!/bin/sh\necho 'boom: no such device' >&2\nexit 1\n")

	s := New(context.Background(), "0", Config{Endpoint: "http://127.0.0.1:0/v1"})
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Stop()

	deadline := time.After(2 * time.Second)
	for {
		select {
		case st, ok := <-s.Updates():
			if !ok {
				t.Fatal("updates channel closed before error state")
			}
			if st.Err == nil {
				continue
			}
			msg := st.Err.Error()
			if !strings.Contains(msg, "ffmpeg exited") {
				t.Errorf("error %q does not mention ffmpeg exit", msg)
			}
			if !strings.Contains(msg, "boom: no such device") {
				t.Errorf("error %q does not carry stderr tail", msg)
			}
			return
		case <-deadline:
			t.Fatal("no State with Err on Updates() within 2s of ffmpeg death")
		}
	}
}

func TestStopAfterFFmpegDeathDoesNotHang(t *testing.T) {
	fakeFFmpeg(t, "#!/bin/sh\nexit 1\n")

	s := New(context.Background(), "0", Config{Endpoint: "http://127.0.0.1:0/v1"})
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	select {
	case <-s.procDone:
	case <-time.After(2 * time.Second):
		t.Fatal("ffmpeg monitor did not finish within 2s")
	}

	done := make(chan struct{})
	go func() {
		s.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Stop hung after ffmpeg already exited")
	}
}

func TestStartArgv(t *testing.T) {
	dir := fakeFFmpeg(t, "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$FAKE_FFMPEG_ARGV\"\nexit 1\n")
	argvFile := filepath.Join(dir, "argv")
	t.Setenv("FAKE_FFMPEG_ARGV", argvFile)

	s := New(context.Background(), "3", Config{Endpoint: "http://127.0.0.1:0/v1"})
	if err := s.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer s.Stop()

	select {
	case <-s.procDone:
	case <-time.After(2 * time.Second):
		t.Fatal("fake ffmpeg did not exit within 2s")
	}

	data, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("fake ffmpeg did not record argv: %v", err)
	}
	argv := strings.Join(strings.Split(strings.TrimSpace(string(data)), "\n"), " ")

	if runtime.GOOS == "darwin" {
		if !strings.Contains(argv, "-f avfoundation -i :3") {
			t.Errorf("darwin argv %q missing avfoundation input", argv)
		}
		if strings.Contains(argv, "pulse") {
			t.Errorf("darwin argv %q must not use pulse", argv)
		}
	} else {
		if !strings.Contains(argv, "-f pulse -i 3") {
			t.Errorf("linux argv %q missing pulse input", argv)
		}
	}
	if !strings.Contains(argv, "-ac 1 -ar 16000 -f s16le -loglevel error -") {
		t.Errorf("argv %q missing output args", argv)
	}
}
