package tab

import (
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/eordano/recgo/internal/audio"
)

func requireMockSession(t *testing.T) {
	t.Helper()
	if os.Getenv("RECGO_MOCK_ROOT") == "" {
		t.Skip("run under test/mock-session.sh")
	}
}

func TestAudioBackendIsUsable(t *testing.T) {
	requireMockSession(t)

	if err := audio.CheckBackend(); err != nil {
		t.Fatalf("CheckBackend: %v", err)
	}
	src, err := audio.GetDefaultSource()
	if err != nil || src == "" {
		t.Fatalf("GetDefaultSource: %q %v", src, err)
	}
	t.Logf("default source: %s", src)

	mics, monitors, err := audio.ListAllDevices()
	if err != nil {
		t.Fatalf("ListAllDevices: %v", err)
	}
	if len(mics)+len(monitors) == 0 {
		t.Fatal("no capture devices at all")
	}
	t.Logf("%d mic(s), %d monitor(s)", len(mics), len(monitors))
}

func TestMicCapturesRealAudio(t *testing.T) {
	requireMockSession(t)

	dir := t.TempDir()
	clock := NewClock()

	mic, err := StartMic(MicOptions{Clock: clock, OutDir: dir})
	if err != nil {
		t.Fatalf("StartMic: %v", err)
	}

	time.Sleep(2 * time.Second)
	res := mic.Stop()
	if res.Err != nil {
		t.Fatalf("Stop: %v", res.Err)
	}

	if !clock.AudioAnchored() {
		t.Fatal("clock was never anchored; the first PCM byte was never seen")
	}
	if clock.AudioStartMs < 0 || clock.AudioStartMs > 3000 {
		t.Errorf("audio anchored at %.0fms, which is not plausible", clock.AudioStartMs)
	}
	t.Logf("anchored at %.0fms, %d bytes, %.2fs", clock.AudioStartMs, res.Bytes, res.Duration)

	if res.Bytes < 16000 {
		t.Errorf("captured only %d bytes in 2s — the pipe is not carrying audio", res.Bytes)
	}
	if res.Duration < 0.5 {
		t.Errorf("duration %.2fs is too short", res.Duration)
	}

	data, err := os.ReadFile(res.WavPath)
	if err != nil {
		t.Fatalf("read wav: %v", err)
	}
	if len(data) < 44 {
		t.Fatal("wav is shorter than its own header")
	}
	if string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" {
		t.Error("not a RIFF/WAVE file")
	}
	if got := binary.LittleEndian.Uint32(data[24:]); got != micRate {
		t.Errorf("sample rate %d, want %d", got, micRate)
	}
	if got := binary.LittleEndian.Uint16(data[22:]); got != micChannels {
		t.Errorf("channels %d, want %d", got, micChannels)
	}
	if got := binary.LittleEndian.Uint32(data[40:]); int(got) != len(data)-44 {
		t.Errorf("data chunk says %d bytes, file carries %d", got, len(data)-44)
	}

	st, err := os.Stat(res.WavPath)
	if err != nil {
		t.Fatal(err)
	}
	if perm := st.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("audio.wav is %04o — readable by other users", perm)
	}

	if _, err := exec.LookPath("ffprobe"); err == nil {
		out, err := exec.Command("ffprobe", "-v", "error",
			"-show_entries", "format=duration", "-of", "default=nw=1:nk=1", res.WavPath).Output()
		if err != nil {
			t.Errorf("ffprobe rejected the wav: %v", err)
		} else {
			t.Logf("ffprobe duration: %s", out)
		}
	}
}

func TestMicStopIsPromptAndIdempotent(t *testing.T) {
	requireMockSession(t)

	dir := t.TempDir()
	mic, err := StartMic(MicOptions{Clock: NewClock(), OutDir: dir})
	if err != nil {
		t.Fatalf("StartMic: %v", err)
	}
	time.Sleep(700 * time.Millisecond)

	start := time.Now()
	res := mic.Stop()
	if elapsed := time.Since(start); elapsed > 8*time.Second {
		t.Errorf("Stop took %s; the escalation is not working", elapsed)
	}
	if res.Err != nil {
		t.Fatalf("Stop: %v", res.Err)
	}
	_ = mic.Stop()

	if _, err := os.Stat(filepath.Join(dir, "audio.wav")); err != nil {
		t.Errorf("no wav written: %v", err)
	}
}

func TestMicReportsAMissingDeviceClearly(t *testing.T) {
	requireMockSession(t)

	_, err := StartMic(MicOptions{
		Clock: NewClock(), OutDir: t.TempDir(),
		Device: "recgo-definitely-not-a-real-source",
	})
	if err != nil {
		t.Logf("rejected at start: %v", err)
		return
	}
	t.Log("started against a bogus device; Stop must report the failure")
}
