package desktop

import (
	"bytes"
	"strings"
	"testing"

	"github.com/eordano/recgo/internal/screencast"
)

var twoDisplays = []screencast.DisplayInfo{
	{W: 1440, H: 900, PixelW: 2880, PixelH: 1800, Main: true},
	{W: 2560, H: 1440, PixelW: 2560, PixelH: 1440},
}

// The Capture line describes the capture source, never the window that
// happened to be focused last.
func TestCaptureLabelNamesTheSource(t *testing.T) {
	for _, c := range []struct{ backend, source, want string }{
		{"windows-gdi", "desktop", "windows-gdi (desktop)"},
		{"windows-gdi (display 1, 1536x960, main)", "display 1, 1536x960, main", "windows-gdi (display 1, 1536x960, main)"},
		{"macos-screencapture", "desktop", "macos-screencapture (desktop)"},
		{"xdg-portal-screencast + kwin-screenshot2", "desktop, 2560x1440", "xdg-portal-screencast + kwin-screenshot2 (desktop, 2560x1440)"},
		{"xdg-portal-screencast (screen DP-1, 2560x1440)", "screen DP-1, 2560x1440", "xdg-portal-screencast (screen DP-1, 2560x1440)"},
		{"audio-only", "", "audio-only"},
	} {
		if got := captureLabel(c.backend, c.source); got != c.want {
			t.Errorf("captureLabel(%q, %q) = %q, want %q", c.backend, c.source, got, c.want)
		}
	}
}

func TestChooseScreenBySpec(t *testing.T) {
	var out bytes.Buffer
	d, err := chooseScreen("2", twoDisplays, strings.NewReader(""), &out)
	if err != nil || d == nil || d.W != 2560 {
		t.Fatalf("-screen 2 = %+v, %v", d, err)
	}
	d, err = chooseScreen("main", twoDisplays, strings.NewReader(""), &out)
	if err != nil || d == nil || !d.Main {
		t.Fatalf("-screen main = %+v, %v", d, err)
	}
	if _, err := chooseScreen("3", twoDisplays, strings.NewReader(""), &out); err == nil {
		t.Error("-screen 3 of 2 displays: want an error")
	}
	if _, err := chooseScreen("dp-1", twoDisplays, strings.NewReader(""), &out); err == nil {
		t.Error("-screen dp-1: want an error, only numbers and main are accepted")
	}
	if _, err := chooseScreen("1", nil, strings.NewReader(""), &out); err == nil {
		t.Error("no displays: want an error")
	}
}

func TestChooseScreenLoneDisplayNeedsNoPrompt(t *testing.T) {
	var out bytes.Buffer
	d, err := chooseScreen("", twoDisplays[:1], strings.NewReader(""), &out)
	if err != nil || d == nil || !d.Main {
		t.Fatalf("lone display = %+v, %v", d, err)
	}
	if out.Len() != 0 {
		t.Errorf("a lone display was still asked about:\n%s", out.String())
	}
}

func TestChooseScreenAsksOnTheTerminal(t *testing.T) {
	var out bytes.Buffer
	d, err := chooseScreen("", twoDisplays, strings.NewReader("2\n"), &out)
	if err != nil || d == nil || d.W != 2560 {
		t.Fatalf("typed 2 = %+v, %v", d, err)
	}
	for _, want := range []string{"Which screen?", "1  2880x1800  (main)", "2  2560x1440", "screen [1-2]:"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("prompt lacks %q:\n%s", want, out.String())
		}
	}
	if _, err := chooseScreen("", twoDisplays, strings.NewReader("x\n"), &out); err == nil {
		t.Error("typed x: want an error")
	}
	if _, err := chooseScreen("", twoDisplays, strings.NewReader(""), &out); err == nil {
		t.Error("empty stdin: want an error")
	}
}

func TestPrintScreensIsOneLinePerDisplay(t *testing.T) {
	var out bytes.Buffer
	printScreens(&out, twoDisplays)
	if got, want := out.String(), "1\t2880x1800\tmain\n2\t2560x1440\t\n"; got != want {
		t.Errorf("printScreens = %q, want %q", got, want)
	}
}

func TestRequiredSystemAudioRejectsInvalidSetupBeforeCapture(t *testing.T) {
	for _, o := range []opts{{requireSystemAudio: true}, {requireSystemAudio: true, systemAudio: "default", noAudio: true}} {
		if err := run(o); err == nil || !strings.Contains(err.Error(), "system audio required") {
			t.Fatalf("got %v", err)
		}
	}
}
