package tab

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writePCM(t *testing.T, path string, totalMs, toneAtMs, toneDurMs float64, amp float64, noise float64) {
	t.Helper()
	n := int(totalMs / 1000 * micRate)
	buf := make([]byte, n*2)

	toneStart := int(toneAtMs / 1000 * micRate)
	toneEnd := toneStart + int(toneDurMs/1000*micRate)

	seed := uint32(12345)
	for i := 0; i < n; i++ {
		seed = seed*1664525 + 1013904223
		v := (float64(seed>>16)/65535.0 - 0.5) * 2 * noise
		if i >= toneStart && i < toneEnd {
			v += amp * math.Sin(2*math.Pi*toneFreqHz*float64(i)/micRate)
		}
		s := int16(math.Max(-1, math.Min(1, v)) * 32767)
		binary.LittleEndian.PutUint16(buf[i*2:], uint16(s))
	}
	if err := os.WriteFile(path, buf, 0o644); err != nil {
		t.Fatal(err)
	}
}

func buildWav(t *testing.T, totalMs, toneAtMs, toneDurMs, amp, noise float64) string {
	t.Helper()
	dir := t.TempDir()
	raw := filepath.Join(dir, "audio.pcm")
	wav := filepath.Join(dir, "audio.wav")
	writePCM(t, raw, totalMs, toneAtMs, toneDurMs, amp, noise)

	info, err := os.Stat(raw)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeWAV(raw, wav, info.Size()); err != nil {
		t.Fatal(err)
	}
	return wav
}

func TestWriteWAVHeader(t *testing.T) {
	wav := buildWav(t, 1000, 500, 100, 0.5, 0.001)
	data, err := os.ReadFile(wav)
	if err != nil {
		t.Fatal(err)
	}

	if string(data[0:4]) != "RIFF" || string(data[8:12]) != "WAVE" || string(data[36:40]) != "data" {
		t.Fatal("not a canonical WAV")
	}
	if got := binary.LittleEndian.Uint16(data[22:]); got != micChannels {
		t.Errorf("channels = %d", got)
	}
	if got := binary.LittleEndian.Uint32(data[24:]); got != micRate {
		t.Errorf("sample rate = %d, want %d", got, micRate)
	}
	if got := binary.LittleEndian.Uint16(data[34:]); got != 16 {
		t.Errorf("bits per sample = %d", got)
	}
	payload := uint32(len(data) - 44)
	if got := binary.LittleEndian.Uint32(data[40:]); got != payload {
		t.Errorf("data size = %d, want %d", got, payload)
	}
	if got := binary.LittleEndian.Uint32(data[4:]); got != 36+payload {
		t.Errorf("RIFF size = %d, want %d", got, 36+payload)
	}
}

func TestFindToneOnset(t *testing.T) {
	for _, at := range []float64{200, 1500, 3200} {
		wav := buildWav(t, 5000, at, 120, 0.35, 0.002)
		got, ok := findToneOnset(wav, toneFreqHz)
		if !ok {
			t.Fatalf("tone at %vms not found", at)
		}
		if math.Abs(got-at) > 20 {
			t.Errorf("tone at %vms detected at %vms", at, got)
		}
	}
}

func TestFindToneOnsetSurvivesNoise(t *testing.T) {
	wav := buildWav(t, 4000, 1200, 150, 0.15, 0.02)
	got, ok := findToneOnset(wav, toneFreqHz)
	if !ok {
		t.Fatal("tone not found under noise")
	}
	if math.Abs(got-1200) > 30 {
		t.Errorf("detected at %vms, want ~1200ms", got)
	}
}

func TestFindToneOnsetIgnoresLateEnergy(t *testing.T) {
	late := toneSearchMs + 15_000.0
	wav := buildWav(t, late+10_000, late, 2000, 0.5, 0.002)
	if got, ok := findToneOnset(wav, toneFreqHz); ok {
		t.Errorf("1kHz energy at %vms (outside the search window) reported as the tone at %vms — "+
			"this is how a whole session's narration ends up before 00.00.00", late, got)
	}
}

func TestToneAnchorPlausibility(t *testing.T) {
	cases := []struct {
		name      string
		firstByte float64
		corrected float64
		want      bool
	}{
		{"typical capture latency", 50, -80, true},
		{"large but sane latency", 100, -10_000, true},
		{"slight clock error forward", 50, 1000, true},
		{"stale page timestamp shifts anchor minutes early", 50, -170_000, false},
		{"anchor after first byte", 50, 3000, false},
	}
	for _, c := range cases {
		if got := toneAnchorPlausible(c.firstByte, c.corrected); got != c.want {
			t.Errorf("%s: toneAnchorPlausible(%v, %v) = %v, want %v",
				c.name, c.firstByte, c.corrected, got, c.want)
		}
	}
}

func TestToneAnchorDecision(t *testing.T) {
	base := "First-byte estimate."

	anchor, note := toneAnchor(50, 3400, 3700, true, base)
	if anchor != -300 {
		t.Errorf("plausible onset: anchor %v, want -300", anchor)
	}
	if !strings.Contains(note, "Anchored by calibration tone") {
		t.Errorf("plausible onset note %q does not say it anchored", note)
	}

	anchor, note = toneAnchor(50, -170_000, 3700, true, base)
	if anchor != 50 {
		t.Errorf("implausible onset: anchor %v, want first-byte 50", anchor)
	}
	if !strings.HasPrefix(note, base) || !strings.Contains(note, "implausible anchor") {
		t.Errorf("implausible onset note %q must keep the base note and explain the rejection", note)
	}

	anchor, note = toneAnchor(50, 3400, 0, false, base)
	if anchor != 50 {
		t.Errorf("tone not found: anchor %v, want first-byte 50", anchor)
	}
	if !strings.Contains(note, "not found") {
		t.Errorf("tone-not-found note %q does not say so", note)
	}
}

func TestFindToneOnsetReportsAbsence(t *testing.T) {
	wav := buildWav(t, 3000, 0, 0, 0, 0.02)
	if got, ok := findToneOnset(wav, toneFreqHz); ok {
		t.Errorf("reported a tone at %vms in tone-free audio", got)
	}
}

func TestFindToneOnsetIgnoresWrongFrequency(t *testing.T) {
	wav := buildWav(t, 3000, 1000, 200, 0.4, 0.002)
	if _, ok := findToneOnset(wav, 400); ok {
		t.Error("400Hz search matched a 1000Hz tone")
	}
}

func TestFindToneOnsetHandlesTinyAndMissingFiles(t *testing.T) {
	if _, ok := findToneOnset(filepath.Join(t.TempDir(), "nope.wav"), toneFreqHz); ok {
		t.Error("missing file should not report a tone")
	}

	short := filepath.Join(t.TempDir(), "short.wav")
	if err := os.WriteFile(short, make([]byte, 60), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := findToneOnset(short, toneFreqHz); ok {
		t.Error("a file too short to analyse should not report a tone")
	}
}
