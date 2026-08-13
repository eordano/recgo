package tab

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWhisperArgsIncludeVADWhenAvailable(t *testing.T) {
	args := whisperArgs("/m/model.bin", "/tmp/a.wav", "/tmp/transcript", "/m/silero.bin")
	joined := strings.Join(args, " ")

	for _, want := range []string{
		"-m /m/model.bin", "-f /tmp/a.wav", "-oj", "-of /tmp/transcript",
		"-ml 1", "-sow", "--vad --vad-model /m/silero.bin",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("args missing %q: %s", want, joined)
		}
	}
}

func TestWhisperArgsOmitVADWhenAbsent(t *testing.T) {
	joined := strings.Join(whisperArgs("/m/model.bin", "/tmp/a.wav", "/tmp/t", ""), " ")
	if strings.Contains(joined, "--vad") {
		t.Errorf("VAD flags present without a model: %s", joined)
	}
	if !strings.Contains(joined, "-ml 1") || !strings.Contains(joined, "-sow") {
		t.Errorf("word-level flags dropped: %s", joined)
	}
}

func TestTranscribeLocalRefusesWithoutModel(t *testing.T) {
	c := NewClock()
	c.SetAudioStart(0, "test")
	got := TranscribeLocal(c, "/tmp/a.wav", "whisper-cli", "", "")
	if got.OK || !strings.Contains(got.Reason, "no local whisper model") {
		t.Errorf("reason = %q", got.Reason)
	}
}

func TestTranscribeLocalRefusesWithoutAudioAnchor(t *testing.T) {
	got := TranscribeLocal(NewClock(), "/tmp/a.wav", "whisper-cli", "/m/model.bin", "")
	if got.OK || !strings.Contains(got.Reason, "no audio") {
		t.Errorf("reason = %q", got.Reason)
	}
}

func TestTranscribeLocalReportsMissingBinary(t *testing.T) {
	c := NewClock()
	c.SetAudioStart(0, "test")
	got := TranscribeLocal(c, "/tmp/a.wav", "/nonexistent/whisper-cli", "/m/model.bin", "")
	if got.OK || !strings.Contains(got.Reason, "whisper.cpp failed") {
		t.Errorf("reason = %q", got.Reason)
	}
}

func TestModelDiscoveryPrefersLargerModels(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_DATA_HOME", dir)
	models := filepath.Join(dir, "recgo", "models")
	if err := os.MkdirAll(models, 0o755); err != nil {
		t.Fatal(err)
	}

	if err := os.WriteFile(filepath.Join(models, "ggml-tiny.en.bin"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if m, _ := DiscoverWhisperModel(); m != filepath.Join(models, "ggml-tiny.en.bin") {
		t.Errorf("model = %q", m)
	}

	if err := os.WriteFile(filepath.Join(models, "ggml-large-v3-turbo-q8_0.bin"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if m, _ := DiscoverWhisperModel(); m != filepath.Join(models, "ggml-large-v3-turbo-q8_0.bin") {
		t.Errorf("model = %q, expected the larger one to win", m)
	}

	if err := os.WriteFile(filepath.Join(models, "ggml-silero-v5.1.2.bin"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, v := DiscoverWhisperModel(); !strings.Contains(v, "silero") {
		t.Errorf("vad = %q", v)
	}
}

func TestModelSearchPathsAreDeduped(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))

	dirs := ModelSearchPaths()
	seen := map[string]bool{}
	for _, d := range dirs {
		if seen[d] {
			t.Errorf("duplicate search path %q in %v", d, dirs)
		}
		seen[d] = true
	}
	if len(dirs) == 0 {
		t.Error("expected at least one search path")
	}
}

func TestModelDiscoveryReturnsEmptyWhenNothingInstalled(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	if m, v := DiscoverWhisperModel(); m != "" || v != "" {
		t.Errorf("discovered %q / %q in an empty tree", m, v)
	}
}
