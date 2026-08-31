package tab

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

func packFixture(t *testing.T, opts PackOptions) string {
	t.Helper()
	dir := t.TempDir()

	clock := NewClock()
	events := []Event{
		{T: 0, Kind: "record-start", FullShot: InitialShot},
		{T: 3400, Kind: "click", Seq: 1, X: 34, Y: 23,
			Elem:     &Element{Selector: "a#header-login", Tag: "a", Text: "Log in"},
			Shots:    []Shot{{Label: "at", File: ShotName(1, "at"), FrameT: f(3400)}},
			FullShot: ShotName(1, "full")},
		{T: 6000, Kind: "click", Seq: 2, X: 830, Y: 200,
			Elem: &Element{Selector: "button.modal__close", Tag: "button"},
			Shots: []Shot{
				{Label: "before", File: ShotName(2, "before"), FrameT: f(5900)},
				{Label: "at", File: ShotName(2, "at"), FrameT: f(5900)},
				{Label: "after", File: ShotName(2, "after"), FrameT: f(5900)},
			},
			FullShot: ShotName(2, "full")},
		{T: 6200, Kind: "network-error", Status: 500, URL: "http://localhost:5173/api/session"},
		{T: 9000, Kind: "console", Level: "log", Text: "render pass"},
		{T: 9100, Kind: "console", Level: "log", Text: "render pass"},
		{T: 9200, Kind: "console", Level: "log", Text: "render pass"},
		{T: 9300, Kind: "console", Level: "warning", Text: "slow frame"},
	}
	tr := &Transcript{OK: true, Segments: []Segment{
		{T: 1000, EndT: 2800, Text: "Ok, so when we see this tab"},
		{T: 4100, EndT: 5200, Text: "we see the new login tab, but when i click"},
		{T: 6900, EndT: 8000, Text: "the pop up gets dismissed with no clear feedback"},
	}}

	if _, err := Pack(dir, events, clock, tr, Meta{
		Slug: "login-popup", Title: "Login popup dismisses itself with no feedback",
		StartedWall: "2026-04-30 15:23:23", DurationMs: 47000, Cwd: "/home/user/src/acme",
		TargetURL: "http://localhost:5173/", TargetTitle: "Acme", Tool: "recgo-tab",
	}, opts); err != nil {
		t.Fatalf("pack: %v", err)
	}

	md, err := os.ReadFile(filepath.Join(dir, "SESSION.md"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if t.Failed() {
			t.Logf("SESSION.md:\n%s", md)
		}
	})
	return string(md)
}

func f(v float64) *float64 { return &v }

func TestSessionDocIsOneChronologicalStream(t *testing.T) {
	md := packFixture(t, PackOptions{})

	for _, want := range []string{
		"# Session: Login popup dismisses itself with no feedback",
		"Start: 2026-04-30 15:23:23",
		"Folder: /home/user/src/acme",
		"Page: http://localhost:5173/ — Acme",
		"Initial screenshot: 0001.png",
		"00.00.01  **user narration**: Ok, so when we see this tab",
		"00.00.03  Click: 34,23 on a#header-login text: Log in → 0002.png",
		"00.00.04  **user narration**: we see the new login tab, but when i click",
		"00.00.06  Click: 830,200 on button.modal__close → 0003.png — screen did not repaint",
		"00.00.06  Error: network 500 http://localhost:5173/api/session",
		"00.00.06  **user narration**: the pop up gets dismissed with no clear feedback",
		"00.00.09  console.log: render pass (×3)",
		"00.00.09  console.warn: slow frame",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("SESSION.md missing line %q", want)
		}
	}

	if strings.Count(md, "console.log: render pass") != 1 {
		t.Error("repeated console lines were not collapsed to one")
	}

	lines := strings.Split(md, "\n")
	var order []int
	for i, l := range lines {
		switch {
		case strings.HasPrefix(l, "00.00.01 "), strings.HasPrefix(l, "00.00.03 "),
			strings.HasPrefix(l, "00.00.04 "), strings.HasPrefix(l, "00.00.06 "):
			order = append(order, i)
		}
	}
	if len(order) != 6 {
		t.Fatalf("expected 6 stream lines, saw %d", len(order))
	}
	for i := 1; i < len(order); i++ {
		if order[i] < order[i-1] {
			t.Fatal("stream lines are not in document order")
		}
	}
}

func TestSessionDocIsTheOnlyOutputByDefault(t *testing.T) {
	dir := t.TempDir()
	clock := NewClock()
	if _, err := Pack(dir, []Event{{T: 0, Kind: "record-start"}}, clock, nil,
		Meta{Slug: "empty", Tool: "recgo-tab"}, PackOptions{}); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Name() != "SESSION.md" {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("wrote %v, want only SESSION.md", names)
	}

	md, _ := os.ReadFile(filepath.Join(dir, "SESSION.md"))
	if !strings.Contains(string(md), "No narration:") {
		t.Error("a session without a transcript does not say so")
	}
}

func TestClosingBlockNamesEveryFileByRole(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"audio.wav", "system.wav", "0001.png", "0002.png", "notes.txt"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), sessionFileMode); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "logs"), sessionDirMode); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "logs", "websocket-frames.jsonl"),
		[]byte("{}\n"), sessionFileMode); err != nil {
		t.Fatal(err)
	}

	if _, err := Pack(dir, []Event{{T: 0, Kind: "record-start"}}, NewClock(), nil,
		Meta{Slug: "extras", Tool: "recgo-tab"}, PackOptions{JSON: true}); err != nil {
		t.Fatal(err)
	}

	md, _ := os.ReadFile(filepath.Join(dir, "SESSION.md"))
	for _, want := range []string{
		"- Raw dev-server websocket frames, including every HMR payload: `logs/websocket-frames.jsonl`.",
		"- Screenshots taken: 2, system audio: `system.wav`, " +
			"microphone/narration audio: `audio.wav`, " +
			"machine-readable timeline: `session.json`, `notes.txt`.",
	} {
		if !strings.Contains(string(md), want) {
			t.Errorf("SESSION.md missing %q\ngot:\n%s", want, md)
		}
	}
	if strings.Contains(string(md), "`logs/`") {
		t.Error("logs listed twice — the websocket line already names it")
	}
}

func TestDesktopDocDoesNotTalkAboutPageClicks(t *testing.T) {
	dir := t.TempDir()
	events := []Event{
		{T: 0, Kind: "record-start", FullShot: InitialShot},
		{T: 1000, Kind: "click", Seq: 1, X: 812, Y: 340, FullShot: ShotName(1, "full"),
			Shots: []Shot{{Label: "at", File: ShotName(1, "at"), FrameT: f(1000)}}},
		{T: 2000, Kind: "mark", Seq: 2, FullShot: ShotName(2, "full"),
			Shots: []Shot{{Label: "at", File: ShotName(2, "at"), FrameT: f(2000)}}},
		{T: 3000, Kind: "focus", Seq: 3, Title: "Ghostty — ~",
			Shots: []Shot{{Label: "at", File: ShotName(3, "at"), FrameT: f(3000)}}},
		{T: 4000, Kind: "window-appear", Seq: 4, Title: "Chrome — Save file?",
			Shots: []Shot{{Label: "at", File: ShotName(4, "at"), FrameT: f(4000)}}},
	}
	if _, err := Pack(dir, events, NewClock(), nil,
		Meta{Slug: "desktop-1-marks", Tool: "recgo-desktop"}, PackOptions{}); err != nil {
		t.Fatal(err)
	}

	md, _ := os.ReadFile(filepath.Join(dir, "SESSION.md"))
	for _, want := range []string{
		"Initial screenshot: 0001.png",
		// A desktop click has a position but no DOM element to name, so the
		// line must not degrade to "on <unknown>".
		"00.00.01  Click: 812,340 → 0002.png",
		"00.00.02  Mark: 2 → 0003.png",
		"00.00.03  Focus: Ghostty — ~ → 0004.png",
		"00.00.04  Window: Chrome — Save file? → 0005.png",
		"Marks are the moments",
		"Clicks come from a system-wide listener",
	} {
		if !strings.Contains(string(md), want) {
			t.Errorf("SESSION.md missing %q\ngot:\n%s", want, md)
		}
	}
	for _, unwanted := range []string{"injected into the page", "browser clock", "repaints"} {
		if strings.Contains(string(md), unwanted) {
			t.Errorf("desktop document uses browser wording %q", unwanted)
		}
	}
}

func TestClipCutsOnRuneBoundaries(t *testing.T) {
	s := "Data " + strings.Repeat("é", 40)
	got := clip(s, 10)
	if !utf8.ValidString(got) {
		t.Errorf("clip produced invalid UTF-8: %q", got)
	}
	if !strings.HasSuffix(got, "...") {
		t.Errorf("clip %q does not end in ASCII ellipsis", got)
	}
	if clip("short", 10) != "short" {
		t.Error("clip mangled a string under the limit")
	}
}

func TestFormatClock(t *testing.T) {
	for _, c := range []struct {
		ms   float64
		want string
	}{
		{0, "00.00.00"},
		{1499, "00.00.01"},
		{61000, "00.01.01"},
		{3723000, "01.02.03"},
		{-5, "00.00.00"},
	} {
		if got := FormatClock(c.ms); got != c.want {
			t.Errorf("FormatClock(%v) = %q, want %q", c.ms, got, c.want)
		}
	}
}

// Whisper hands back breath-sized fragments; the document should read the
// way the user spoke. Short gaps merge, but a click between two segments
// keeps them apart — the words before it and after it mean different things.
func TestNarrationMergesShortGapsButNotAcrossEvents(t *testing.T) {
	events := []Event{
		{T: 0, Kind: "record-start"},
		{T: 5000, Kind: "click", Seq: 1, X: 10, Y: 10},
	}
	utt := []Utterance{
		{T: 1000, EndT: 2000, Text: "Okay, let's review"},
		{T: 2500, EndT: 3000, Text: "this part of the design"},
		{T: 6000, EndT: 6500, Text: "and after the click"},
		{T: 9000, EndT: 9300, Text: "a long pause starts fresh"},
	}

	merged := mergeUtterances(utt, events)
	if len(merged) != 3 {
		t.Fatalf("got %d utterances, want 3: %+v", len(merged), merged)
	}
	if merged[0].Text != "Okay, let's review this part of the design" {
		t.Errorf("short gap did not merge: %q", merged[0].Text)
	}
	if merged[0].T != 1000 || merged[0].EndT != 3000 {
		t.Errorf("merged span %v-%v, want 1000-3000", merged[0].T, merged[0].EndT)
	}
	if merged[1].Text != "and after the click" {
		t.Errorf("merge crossed the click: %q", merged[1].Text)
	}
	if merged[2].Text != "a long pause starts fresh" {
		t.Errorf("merge crossed a %dms pause: %q", 2500, merged[2].Text)
	}
}

func TestSessionRecordsSystemInfo(t *testing.T) {
	dir := t.TempDir()
	sys := &SystemInfo{
		Hostname: "devbox", User: "alice", OS: "macOS 15.6 (24G84)", Arch: "arm64",
		Model: "Mac99,1", Kernel: "Darwin 25.6.0",
		Displays: []Display{{W: 3456, H: 2234, Scale: 2, Main: true}, {W: 2560, H: 1440}},
	}
	if _, err := Pack(dir, []Event{{T: 0, Kind: "record-start"}}, NewClock(), nil,
		Meta{Slug: "s", Tool: "recgo-desktop", System: sys}, PackOptions{JSON: true}); err != nil {
		t.Fatal(err)
	}

	md, _ := os.ReadFile(filepath.Join(dir, "SESSION.md"))
	for _, want := range []string{
		"Host: alice@devbox (macOS 15.6 (24G84), arm64, Mac99,1, Darwin 25.6.0)",
		"Displays: 3456x2234@2x (main), 2560x1440",
	} {
		if !strings.Contains(string(md), want) {
			t.Errorf("SESSION.md missing %q\ngot:\n%s", want, md)
		}
	}

	blob, _ := os.ReadFile(filepath.Join(dir, "session.json"))
	if !strings.Contains(string(blob), `"hostname": "devbox"`) {
		t.Errorf("session.json does not carry the system info:\n%s", blob)
	}

	// CollectSystemInfo itself must never fail and always knows the basics.
	got := CollectSystemInfo()
	if got.Hostname == "" || got.Arch == "" {
		t.Errorf("CollectSystemInfo missing basics: %+v", got)
	}
}
