package tab

import (
	"strings"
	"testing"

	"github.com/eordano/recgo/internal/audio"
)

func TestReadCommandsMarksAndNotes(t *testing.T) {
	var marks int
	var notes []string
	ReadCommands(strings.NewReader("m\nnoise\nmark\nsysaudio off\n"), nil,
		func() float64 { marks++; return 1000 },
		func(s string) float64 { notes = append(notes, s); return 0 })
	if marks != 2 {
		t.Fatalf("marks = %d, want 2", marks)
	}
	if len(notes) != 0 {
		t.Fatalf("a toggle with no mic must not land in the document, got %v", notes)
	}
}

func TestResolveMonitorKeepsExplicitSource(t *testing.T) {
	if m, err := ResolveMonitor(""); m != "" || err != nil {
		t.Fatalf("empty flag: %q %v", m, err)
	}
	if m, err := ResolveMonitor("alsa_output.pci.analog-stereo.monitor"); err != nil ||
		m != "alsa_output.pci.analog-stereo.monitor" {
		t.Fatalf("explicit source: %q %v", m, err)
	}
}

func TestMicFFmpegArgsMixesMonitor(t *testing.T) {
	solo := strings.Join(micFFmpegArgs("mic", ""), " ")
	if strings.Contains(solo, "amix") {
		t.Fatalf("mic alone must not mix: %s", solo)
	}
	mixed := strings.Join(micFFmpegArgs("mic", "out.monitor"), " ")
	micIn := strings.Join(audio.FFmpegInputArgs("mic"), " ")
	monIn := strings.Join(audio.FFmpegMonitorInputArgs("out.monitor"), " ")
	for _, want := range []string{micIn, monIn, "amix=inputs=2", "normalize=0",
		"-map [a]", "-ac 1 -ar 16000 -f s16le pipe:1"} {
		if !strings.Contains(mixed, want) {
			t.Fatalf("mixed args missing %q: %s", want, mixed)
		}
	}
	if strings.Index(mixed, monIn) > strings.Index(mixed, "-filter_complex") {
		t.Fatalf("inputs must precede the filter graph: %s", mixed)
	}
}
