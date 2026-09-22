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
		{T: 47000, Kind: "record-stop"},
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
		// The login click fell in the 1.3s breath between two segments, so
		// the sentence stays whole and the click is anchored where it fell.
		"00.00.01  **user narration**: Ok, so when we see this tab [→0002.png] we see the new login tab, but when i click",
		"00.00.03  Click: 34,23 on a#header-login text: Log in → 0002.png",
		"00.00.06  Click: 830,200 on button.modal__close → 0003.png — screen did not repaint",
		"00.00.06  Error: network 500 http://localhost:5173/api/session",
		"00.00.06  **user narration**: the pop up gets dismissed with no clear feedback",
		"00.00.09  console.log: render pass (×3)",
		"00.00.09  console.warn: slow frame",
		"00.00.47  Stop: recording ended",
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
			strings.HasPrefix(l, "00.00.06 "), strings.HasPrefix(l, "00.00.47 "):
			order = append(order, i)
		}
	}
	if len(order) != 6 {
		t.Fatalf("expected 6 stream lines, saw %d", len(order))
	}
	if strings.Contains(md, "Start: recording") {
		t.Error("record-start rendered as a line; the initial screenshot already covers it")
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
// way the user spoke. Short gaps merge, but an event between two segments
// that is not a click (a navigation, an error) keeps them apart — the words
// before it and after it are about different screens.
func TestNarrationMergesShortGapsButNotAcrossEvents(t *testing.T) {
	events := []Event{
		{T: 0, Kind: "record-start"},
		{T: 3500, Kind: "navigation", URL: "http://localhost:5173/settings"},
		{T: 20000, Kind: "record-stop"},
	}
	utt := []Utterance{
		{T: 1000, EndT: 2000, Text: "Okay, let's review"},
		{T: 2500, EndT: 3000, Text: "this part of the design"},
		{T: 4000, EndT: 4500, Text: "and after the navigation"},
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
	if merged[1].Text != "and after the navigation" {
		t.Errorf("merge crossed the navigation: %q", merged[1].Text)
	}
	if merged[2].Text != "a long pause starts fresh" {
		t.Errorf("merge crossed a %dms pause: %q", 4500, merged[2].Text)
	}
}

// words builds a word-granular transcript the way the STT backends do: one
// segment per word, wordMs apart, starting at t.
func words(t float64, wordMs float64, text string) []Segment {
	var segs []Segment
	for i, w := range strings.Fields(text) {
		start := t + float64(i)*wordMs
		segs = append(segs, Segment{T: start, EndT: start + wordMs*0.8, Text: w})
	}
	return segs
}

func shotClick(t float64, seq int, elem *Element, repaint bool) Event {
	frame := t
	if !repaint {
		frame = t - 100
	}
	return Event{T: t, Kind: "click", Seq: seq, X: 100, Y: 200, Elem: elem,
		Shots: []Shot{
			{Label: "before", File: ShotName(seq, "before"), FrameT: f(t - 100)},
			{Label: "at", File: ShotName(seq, "at"), FrameT: f(frame)},
			{Label: "after", File: ShotName(seq, "after"), FrameT: f(frame)},
		},
		FullShot: ShotName(seq, "full")}
}

func renderStream(events []Event, segs []Segment) []string {
	var out []string
	for _, l := range buildStream(events, ToUtterances(segs, 0, 0)) {
		out = append(out, l.render())
	}
	return out
}

func requireLines(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d lines, want %d:\n%s", len(got), len(want), strings.Join(got, "\n"))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d:\n got %q\nwant %q", i, got[i], want[i])
		}
	}
}

// Modeled on a real session: "Let's de-emphasize" / click / click / "count
// and does not count" came out as four lines the reader had to stitch. The
// clicks fell in the breath between two segments; the sentence should read
// whole with each click anchored at the word it fell before.
func TestClickInsideSentenceAnchorsAtTheWord(t *testing.T) {
	price := &Element{Selector: "section:nth-of-type(3) > div > p", Tag: "p", Text: "$50/week"}
	segs := append(words(25000, 300, "Let's de-emphasize"),
		words(26900, 300, "count and does not count and let's make this text more important")...)
	events := []Event{
		{T: 0, Kind: "record-start"},
		shotClick(26100, 7, price, false),
	}

	requireLines(t, renderStream(events, segs),
		"00.00.25  **user narration**: Let's de-emphasize [→0008.png] count and does not count and let's make this text more important",
		"00.00.26  Click: 100,200 on section:nth-of-type(3) > div > p text: $50/week → 0008.png — screen did not repaint",
	)
}

func TestTwoClicksInOneSentenceAnchorAtTheirOwnWords(t *testing.T) {
	segs := words(38000, 250, "and maybe one card per each counterexample or per each example")
	events := []Event{
		{T: 0, Kind: "record-start"},
		// "and maybe" then the first click, "one card per each" then the second.
		shotClick(38450, 10, &Element{Selector: "div", Tag: "div"}, true),
		shotClick(39400, 11, &Element{Selector: "span", Tag: "span", Text: "Does not count"}, true),
	}

	requireLines(t, renderStream(events, segs),
		"00.00.38  **user narration**: and maybe [→0011.png] one card per each [→0012.png] counterexample or per each example",
		"00.00.38  Click: 100,200 on div → 0011.png",
		"00.00.39  Click: 100,200 on span text: Does not count → 0012.png",
	)
}

func TestClickWithoutScreenshotAnchorsBySeq(t *testing.T) {
	segs := words(1000, 300, "drop this subtitle here")
	events := []Event{
		{T: 0, Kind: "record-start"},
		{T: 1500, Kind: "click", Seq: 4, X: 10, Y: 10, Elem: &Element{Selector: "p", Tag: "p"}},
	}

	requireLines(t, renderStream(events, segs),
		"00.00.01  **user narration**: drop this [click 4] subtitle here",
		"00.00.01  Click: 10,10 on p",
	)
}

func TestConsoleErrorStillBreaksTheSentence(t *testing.T) {
	segs := append(words(1000, 300, "when i click"), words(2200, 300, "nothing happens")...)
	events := []Event{
		{T: 0, Kind: "record-start"},
		shotClick(1700, 1, &Element{Selector: "button", Tag: "button"}, true),
		{T: 1900, Kind: "console", Level: "error", Text: "TypeError: x is undefined"},
	}

	requireLines(t, renderStream(events, segs),
		"00.00.01  **user narration**: when i click [→0002.png]",
		"00.00.01  Click: 100,200 on button → 0002.png",
		"00.00.01  Error: TypeError: x is undefined",
		"00.00.02  **user narration**: nothing happens",
	)
}

// A segment-level transcript has no word times: the anchor goes to the
// segment boundary, which is where the line used to break.
func TestSegmentGranularityAnchorsAtTheBoundary(t *testing.T) {
	segs := []Segment{
		{T: 1000, EndT: 2000, Text: "let's highlight this"},
		{T: 2800, EndT: 3900, Text: "and unhighlight that"},
	}
	events := []Event{
		{T: 0, Kind: "record-start"},
		shotClick(1400, 1, &Element{Selector: "span", Tag: "span", Text: "idea"}, false),
		shotClick(3200, 2, &Element{Selector: "b", Tag: "b", Text: "play"}, false),
	}

	requireLines(t, renderStream(events, segs),
		"00.00.01  **user narration**: let's highlight this [→0002.png] and unhighlight that [→0003.png]",
		"00.00.01  Click: 100,200 on span text: idea → 0002.png — screen did not repaint",
		"00.00.03  Click: 100,200 on b text: play → 0003.png — screen did not repaint",
	)
}

func TestMarkInsideSentenceAnchorsLikeAClick(t *testing.T) {
	segs := words(1000, 300, "this bit right here is wrong")
	events := []Event{
		{T: 0, Kind: "record-start"},
		{T: 1750, Kind: "mark", Seq: 2},
	}

	requireLines(t, renderStream(events, segs),
		"00.00.01  **user narration**: this bit right [mark 2] here is wrong",
		"00.00.01  Mark: 2",
	)
}

// Three no-repaint clicks on the same control with nothing said between
// them are one fact: the user kept clicking and nothing changed.
func TestRepeatedNoRepaintClicksCollapse(t *testing.T) {
	price := &Element{Selector: "section:nth-of-type(3) > div > p", Tag: "p", Text: "$50/week"}
	events := []Event{
		{T: 0, Kind: "record-start"},
		shotClick(24000, 5, price, false),
		shotClick(26000, 6, price, false),
		shotClick(26400, 7, price, false),
		shotClick(28000, 8, price, true),
		shotClick(28500, 9, price, false),
		shotClick(29000, 10, &Element{Selector: "h1", Tag: "h1", Text: "Worth coming back"}, false),
	}

	requireLines(t, renderStream(events, nil),
		"00.00.24  Click: 100,200 on section:nth-of-type(3) > div > p text: $50/week → 0006.png (×3, through 0008.png) — screen did not repaint",
		"00.00.28  Click: 100,200 on section:nth-of-type(3) > div > p text: $50/week → 0009.png",
		"00.00.28  Click: 100,200 on section:nth-of-type(3) > div > p text: $50/week → 0010.png — screen did not repaint",
		"00.00.29  Click: 100,200 on h1 text: Worth coming back → 0011.png — screen did not repaint",
	)
}

func TestNarrationBetweenClicksPreventsTheCollapse(t *testing.T) {
	price := &Element{Selector: "p", Tag: "p", Text: "$50/week"}
	events := []Event{
		{T: 0, Kind: "record-start"},
		shotClick(24000, 5, price, false),
		shotClick(28000, 6, price, false),
	}
	segs := words(26000, 300, "this one")

	requireLines(t, renderStream(events, segs),
		"00.00.24  Click: 100,200 on p text: $50/week → 0006.png — screen did not repaint",
		"00.00.26  **user narration**: this one",
		"00.00.28  Click: 100,200 on p text: $50/week → 0007.png — screen did not repaint",
	)
}

func TestIsApproval(t *testing.T) {
	for _, c := range []struct {
		text string
		want bool
	}{
		{"this is great", true},
		{"I love it", true},
		{"this slide is amazing", true},
		{"Presentations, great.", true},
		{"looks good to me", true},
		{"perfect", true},
		{"this is great make sure that creators paid from the players is the only text", false},
		{"up build play earn I love it and drop this subtext", false},
		{"this slide is great let's just say games", false},
		{"looks great but more contrast", false},
		{"great, now highlight the title", false},
		{"", false},
		{"amazing amazing amazing amazing amazing amazing amazing amazing amazing amazing amazing amazing amazing", false},
	} {
		if got := isApproval(c.text); got != c.want {
			t.Errorf("isApproval(%q) = %v, want %v", c.text, got, c.want)
		}
	}
}

func TestApprovalLineIsTagged(t *testing.T) {
	events := []Event{{T: 0, Kind: "record-start"}, {T: 6000, Kind: "record-stop"}}
	segs := append(words(1000, 300, "I love it"), words(4000, 300, "now drop this subtext")...)

	requireLines(t, renderStream(events, segs),
		"00.00.01  **user approves**: I love it",
		"00.00.04  **user narration**: now drop this subtext",
		"00.00.06  Stop: recording ended",
	)
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

// Desktop sessions have no clicks to speak of in a terminal review: the
// events mid-sentence are the window coming to the front and a dialog
// appearing, and they anchor by their number like a mark does.
func TestFocusAndWindowInsideSentenceAnchorBySeq(t *testing.T) {
	segs := words(1000, 300, "switch to the terminal and the dialog pops")
	events := []Event{
		{T: 0, Kind: "record-start"},
		{T: 1500, Kind: "focus", Seq: 3, Title: "Ghostty — ~"},
		{T: 2700, Kind: "window-appear", Seq: 4, Title: "Google Chrome — Save file?"},
	}

	requireLines(t, renderStream(events, segs),
		"00.00.01  **user narration**: switch to [focus 3] the terminal and the [window 4] dialog pops",
		"00.00.01  Focus: Ghostty — ~",
		"00.00.02  Window: Google Chrome — Save file?",
	)
}

// The documented boundary: an event at the first word stays on its own line
// just before the sentence, one at the last word's end is still inlined, and
// one a millisecond later is not.
func TestAnchorBoundaries(t *testing.T) {
	segs := words(1000, 300, "drop this one") // the last word ends at 1840
	events := []Event{
		{T: 0, Kind: "record-start"},
		shotClick(1000, 1, &Element{Selector: "h1", Tag: "h1"}, true),
		shotClick(1840, 2, &Element{Selector: "h2", Tag: "h2"}, true),
		shotClick(1841, 3, &Element{Selector: "h3", Tag: "h3"}, true),
	}

	requireLines(t, renderStream(events, segs),
		"00.00.01  Click: 100,200 on h1 → 0002.png",
		"00.00.01  **user narration**: drop this one [→0003.png]",
		"00.00.01  Click: 100,200 on h2 → 0003.png",
		"00.00.01  Click: 100,200 on h3 → 0004.png",
	)
}

// With clicks no longer splitting sentences, the length cap is what keeps a
// review spoken without pauses from becoming one line.
func TestNarrationMergeStopsAtFourHundredCharacters(t *testing.T) {
	events := []Event{{T: 0, Kind: "record-start"}, {T: 60000, Kind: "record-stop"}}
	pair := func(chars int) []Utterance {
		text := strings.TrimSpace(strings.Repeat("word ", chars/5))
		return []Utterance{
			{T: 1000, EndT: 5000, Text: text},
			{T: 5500, EndT: 9000, Text: text},
		}
	}
	if got := mergeUtterances(pair(250), events); len(got) != 2 {
		t.Errorf("two 250-char utterances merged into %d line(s): %q", len(got), got[0].Text)
	}
	if got := mergeUtterances(pair(150), events); len(got) != 1 {
		t.Errorf("two 150-char utterances stayed %d lines", len(got))
	}
}

func desktopClick(t float64, seq int, x, y float64) Event {
	return Event{T: t, Kind: "click", Seq: seq, X: x, Y: y,
		Shots: []Shot{
			{Label: "before", File: ShotName(seq, "before"), FrameT: f(t - 100)},
			{Label: "at", File: ShotName(seq, "at"), FrameT: f(t - 100)},
			{Label: "after", File: ShotName(seq, "after"), FrameT: f(t - 100)},
		},
		FullShot: ShotName(seq, "full")}
}

// A desktop click has no element, so the run is keyed on the position.
func TestDesktopClicksCollapseByPosition(t *testing.T) {
	events := []Event{
		{T: 0, Kind: "record-start"},
		desktopClick(1000, 1, 812, 340),
		desktopClick(1500, 2, 812, 340),
		desktopClick(2000, 3, 813, 340),
	}

	requireLines(t, renderStream(events, nil),
		"00.00.01  Click: 812,340 → 0002.png (×2, through 0003.png) — screen did not repaint",
		"00.00.02  Click: 813,340 → 0004.png — screen did not repaint",
	)
}

// The closing block is what a reader of the document has to go on; each
// tool's version must name the behaviours its stream shows.
func TestCaptureNotesNameTheNewBehaviours(t *testing.T) {
	anchor := "fell mid-sentence is anchored"
	collapse := "collapse to"
	approves := "tagged **user approves**"
	for _, c := range []struct {
		tool     string
		want     []string
		unwanted []string
	}{
		{"recgo-tab", []string{anchor, "`[→0002.png]` (or `[click 3]`", collapse, "through 0008.png", approves}, nil},
		{"recgo-desktop", []string{anchor, "`[mark 3]`, `[focus 3]`,", "`[window 3]`",
			"Repeated clicks at one position", collapse, "through 0008.png", approves}, []string{"on one control"}},
		{"recgo-audio", []string{"anchored inline as `[mark 3]`", approves}, []string{collapse, "through 0008.png"}},
		{"recgo-alttester", []string{anchor, "AltTester SDK", "UGUI transform path", "`uitk:` path",
			"outside <window>", "`[mark 3]`, `[focus 3]`,", collapse, "through", "0008.png", approves},
			[]string{"position only"}},
	} {
		dir := t.TempDir()
		events := []Event{{T: 0, Kind: "record-start"}, {T: 5000, Kind: "record-stop"}}
		if _, err := Pack(dir, events, NewClock(), nil, Meta{Slug: "notes", Tool: c.tool, FrameStream: true}, PackOptions{}); err != nil {
			t.Fatal(err)
		}
		md, _ := os.ReadFile(filepath.Join(dir, "SESSION.md"))
		_, tail, _ := strings.Cut(string(md), "## How this was captured")
		for _, want := range c.want {
			if !strings.Contains(tail, want) {
				t.Errorf("%s: closing block lacks %q:\n%s", c.tool, want, tail)
			}
		}
		for _, unwanted := range c.unwanted {
			if strings.Contains(tail, unwanted) {
				t.Errorf("%s: closing block should not say %q:\n%s", c.tool, unwanted, tail)
			}
		}
	}
}

// The frame bullet must match how the frames were made: the Linux portal
// streams them (an unchanged frame is repaint evidence), windows-gdi and
// macOS screencapture shoot once per event (the Windows run's document
// claimed a screencast it never had). Without a stream nothing ever
// collapses either, so that bullet goes too.
func TestCaptureNotesSayWhetherFramesStream(t *testing.T) {
	stream := "Frames come from the desktop screencast, which emits one only when the"
	shots := "a screenshot is taken at each\n  click, mark and focus event"
	noEvidence := "no line carries a `screen did not repaint` verdict"
	for _, tool := range []string{"recgo-desktop", "recgo-window", "recgo-alttester"} {
		for _, streams := range []bool{true, false} {
			dir := t.TempDir()
			events := []Event{{T: 0, Kind: "record-start"}, {T: 5000, Kind: "record-stop"}}
			meta := Meta{Slug: "frames", Tool: tool, FrameStream: streams}
			if _, err := Pack(dir, events, NewClock(), nil, meta, PackOptions{}); err != nil {
				t.Fatal(err)
			}
			md, _ := os.ReadFile(filepath.Join(dir, "SESSION.md"))
			_, tail, _ := strings.Cut(string(md), "## How this was captured")
			want, unwanted := []string{stream, "collapse to"}, []string{shots, noEvidence}
			if !streams {
				want, unwanted = unwanted, want
			}
			for _, s := range want {
				if !strings.Contains(tail, s) {
					t.Errorf("%s stream=%v: closing block lacks %q:\n%s", tool, streams, s, tail)
				}
			}
			for _, s := range unwanted {
				if strings.Contains(tail, s) {
					t.Errorf("%s stream=%v: closing block should not say %q:\n%s", tool, streams, s, tail)
				}
			}
		}
	}
}

// A desktop tool's Capture line is the capture source; the focus events'
// titles never reach it (on the Windows run the Unity window was the subject
// and Edge, focused last, took over the line). A browser's Page line still
// carries the page title.
func TestCaptureLineNamesTheSourceNotTheFocusedWindow(t *testing.T) {
	edge := "Chrome_WidgetWin_1: 127.0.0.1:18765/noquery and 1 more page - Profile 1 - Microsoft Edge"
	events := []Event{
		{T: 0, Kind: "record-start"},
		{T: 20000, Kind: "focus", Seq: 1, Title: "UnityWndClass: Explorer"},
		{T: 62000, Kind: "focus", Seq: 2, Title: edge},
		{T: 85000, Kind: "record-stop"},
	}
	for _, c := range []struct {
		meta Meta
		want string
	}{
		{Meta{Slug: "win", Tool: "recgo-alttester", TargetURL: "windows-gdi (desktop)"}, "Capture: windows-gdi (desktop)\n"},
		{Meta{Slug: "win", Tool: "recgo-desktop", TargetURL: "windows-gdi (display 1, 1536x960, main)"}, "Capture: windows-gdi (display 1, 1536x960, main)\n"},
		{Meta{Slug: "win", Tool: "recgo-window", TargetURL: "xdg-portal-screencast (screen DP-1, 2560x1440)"}, "Capture: xdg-portal-screencast (screen DP-1, 2560x1440)\n"},
		{Meta{Slug: "tab", Tool: "recgo-tab", TargetURL: "http://localhost:5173/", TargetTitle: "Acme"}, "Page: http://localhost:5173/ — " + edge + "\n"},
	} {
		dir := t.TempDir()
		if _, err := Pack(dir, events, NewClock(), nil, c.meta, PackOptions{}); err != nil {
			t.Fatal(err)
		}
		md, _ := os.ReadFile(filepath.Join(dir, "SESSION.md"))
		if !strings.Contains(string(md), c.want) {
			t.Errorf("%s: header lacks %q:\n%s", c.meta.Tool, c.want, md)
		}
		if c.meta.Tool != "recgo-tab" && strings.Contains(string(md), "Capture: "+c.meta.TargetURL+" —") {
			t.Errorf("%s: Capture line carries a window title:\n%s", c.meta.Tool, md)
		}
	}
}

// recgo-alttester names the Unity element under a desktop click and notes
// clicks that fell outside the game window.
func TestClickLineNamesUnityElementAndNote(t *testing.T) {
	hit := desktopClick(1000, 1, 812, 340)
	hit.Elem = &Element{Selector: "/Root/Panel/Button", Tag: "Button", ID: "12345", Text: "Backpack"}
	uitk := desktopClick(2000, 2, 400, 300)
	uitk.Elem = &Element{Selector: "uitk:/root/backpack/card-3", Tag: "Button", Classes: "card muted"}
	outside := desktopClick(3000, 3, 20, 20)
	outside.Note = "outside ~ — Konsole"
	requireLines(t, renderStream([]Event{{T: 0, Kind: "record-start"}, hit, uitk, outside}, nil),
		"00.00.01  Click: 812,340 on /Root/Panel/Button text: Backpack → 0002.png — screen did not repaint",
		"00.00.02  Click: 400,300 on uitk:/root/backpack/card-3 → 0003.png — screen did not repaint",
		"00.00.03  Click: 20,20 → 0004.png — outside ~ — Konsole — screen did not repaint",
	)
}

// The note renders for every desktop tool: a plain recgo-desktop
// right-click, and one the alttester resolver also placed outside the app.
// recgo-sessions parses both shapes (cmd/recgo-sessions/main_test.go).
func TestClickLineRendersDesktopRightClick(t *testing.T) {
	right := desktopClick(1000, 1, 20, 20)
	right.Note = "right-click"
	both := desktopClick(2000, 2, 30, 30)
	both.Note = "right-click; outside ~ — Konsole"
	requireLines(t, renderStream([]Event{{T: 0, Kind: "record-start"}, right, both}, nil),
		"00.00.01  Click: 20,20 → 0002.png — right-click — screen did not repaint",
		"00.00.02  Click: 30,30 → 0003.png — right-click; outside ~ — Konsole — screen did not repaint",
	)
}
