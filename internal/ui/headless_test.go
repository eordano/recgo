package ui

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/eordano/recgo/internal/audio"
	"github.com/eordano/recgo/internal/config"
	"github.com/eordano/recgo/internal/transcribe"
)

func headlessModel(t *testing.T, o Options) (Model, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	o.Headless = true
	o.Out = &buf
	m := NewModelWith(config.DefaultConfig(), "standup", o)
	m.mics = []audio.Device{{Name: "mic-a", IsDefault: true, Type: audio.DeviceTypeMic}, {Name: "mic-b", Type: audio.DeviceTypeMic}}
	m.monitors = []audio.Device{{Name: "out-x.monitor", Type: audio.DeviceTypeMonitor}, {Name: "out-y.monitor", Type: audio.DeviceTypeMonitor}}
	return m, &buf
}

func TestHeadlessPinsTheWantedDevicesAndKeepsDefaultsOtherwise(t *testing.T) {
	m, buf := headlessModel(t, Options{Mic: "mic-b", Monitor: "out-y.monitor"})
	m = m.applyWantedDevices()
	if m.selectedMic != 1 || m.selectedMon != 1 {
		t.Fatalf("selected mic=%d mon=%d, want 1/1", m.selectedMic, m.selectedMon)
	}
	m, buf = headlessModel(t, Options{Mic: "gone", Monitor: "default"})
	m = m.applyWantedDevices()
	if m.selectedMic != 0 || m.selectedMon != 0 {
		t.Fatalf("unknown names must keep the defaults, got mic=%d mon=%d", m.selectedMic, m.selectedMon)
	}
	if !strings.Contains(buf.String(), `mic "gone" not found, using mic-a`) {
		t.Fatalf("unknown mic not announced: %q", buf.String())
	}
}

func TestHeadlessPrintsTranscriptLinesLikeTheTUIShowsThem(t *testing.T) {
	m, buf := headlessModel(t, Options{})
	m.startTime = time.Now().Add(-65 * time.Second)
	m.sayTranscript("narration", transcribe.State{PassText: "hello there", Speaker: "Ana"})
	m.sayTranscript("system", transcribe.State{PassText: "and you"})
	m.sayTranscript("narration", transcribe.State{Fast: "half a sent"})
	m.sayTranscript("narration", transcribe.State{Locked: "nothing new"})
	got := buf.String()
	for _, want := range []string{"00:01:05  narration: [Ana] hello there\n", "00:01:05  system: and you\n", "00:01:05  hearing: half a sent\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
	if strings.Count(got, "\n") != 3 {
		t.Errorf("a state with no new text must print nothing: %q", got)
	}
}

func TestHeadlessErrorsAreReportedOnceAndAFatalStartQuits(t *testing.T) {
	m, buf := headlessModel(t, Options{})
	next, _ := m.Update(errMsg(errors.New("boom")))
	next, _ = next.(Model).Update(tickMsg(time.Now()))
	if strings.Count(buf.String(), "error: boom") != 1 {
		t.Fatalf("error must be printed exactly once: %q", buf.String())
	}
	next, cmd := next.(Model).Update(devicesLoadedMsg{err: errors.New("no pulse")})
	if cmd == nil {
		t.Fatal("a device-loading failure must quit the headless recorder")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("want tea.Quit, got %T", cmd())
	}
	if next.(Model).Err() == nil || next.(Model).FinalRecording() != "" {
		t.Fatal("the fatal error must be exposed with no recording")
	}
}

func TestHeadlessStdinAndSignalsStopLikeTheQKey(t *testing.T) {
	m, buf := headlessModel(t, Options{})
	next, _ := m.Update(CommandMsg("transcribe on"))
	if !next.(Model).transcribing {
		t.Fatal("transcribe on before recording must arm transcription for the start")
	}
	next, _ = next.(Model).Update(CommandMsg("transcribe off"))
	if next.(Model).transcribing {
		t.Fatal("transcribe off must disarm it")
	}
	m = next.(Model)
	m.sessionFinalPath = "/tmp/2026.09.14-10.00-standup.mkv"
	next, cmd := m.Update(StopMsg{})
	if !next.(Model).quitting || cmd == nil {
		t.Fatal("StopMsg must run the quit path")
	}
	if _, ok := cmd().(tea.QuitMsg); !ok {
		t.Fatalf("want tea.Quit, got %T", cmd())
	}
	if !strings.Contains(buf.String(), "wrote /tmp/2026.09.14-10.00-standup.mkv\n") {
		t.Fatalf("the final file must be announced: %q", buf.String())
	}
	m, buf = headlessModel(t, Options{})
	m.sessionFinalPath = "/tmp/x.mkv"
	next, _ = m.Update(CommandMsg("q"))
	if !next.(Model).quitting || !strings.Contains(buf.String(), "wrote /tmp/x.mkv") {
		t.Fatal("q on stdin must quit like the key")
	}
}
