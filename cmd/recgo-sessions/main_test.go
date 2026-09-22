package main

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

const fixture = `# Session: fixture

Start: 2026-08-13 13:33:21
Folder: /tmp/app
Page: http://localhost:5173/ — app
Recorded 86s by recgo-tab · 2 clicks · 1 errors · 3 utterances
No narration: whatever

Initial screenshot: 0001.png
00.00.01  **user narration**: Hello,
00.00.01  **user narration**: world.
00.00.03  Click: 100,50 on button.save text: Save → 0002.png
00.00.03  Error: network 500 http://localhost:5173/api
00.00.05: legacy narration line
00.00.06  Click: a → 0003.png — screen did not repaint
00.00.07  Click: 4,3 on button.save text: Save → 0004.png (×3, through 0006.png) — screen did not repaint
00.00.08  **user approves**: looks good
00.00.09  Click: 20,20 → 0005.png — outside ~ — Konsole
00.00.10  Click: 20,20 → 0006.png — right-click
00.00.11  Click: 20,20 → 0007.png — outside X — screen did not repaint
00.00.12  Click: 20,20 → 0008.png — right-click (×2, through 0009.png) — screen did not repaint

## How this was captured

- boilerplate that must not parse as events
`

func writePNG(t *testing.T, path string, w, h int) {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewGray(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
}

func setupSession(t *testing.T, fullW, fullH int) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SESSION.md"), []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"0001.png", "0002.png", "0003.png", "0004.png"} {
		writePNG(t, filepath.Join(dir, name), 10, 10)
	}
	// The noted desktop clicks: frames big enough that their 20,20 does
	// not read as a 1x display to annotate's DPR guess.
	for _, name := range []string{"0005.png", "0006.png", "0007.png", "0008.png"} {
		writePNG(t, filepath.Join(dir, name), 400, 200)
	}
	writePNG(t, filepath.Join(dir, "0002-full.png"), fullW, fullH)
	return dir
}

func TestParseSession(t *testing.T) {
	s, err := parseSession(setupSession(t, 400, 200))
	if err != nil {
		t.Fatal(err)
	}
	if s.Title != "fixture" || s.Duration != 86 {
		t.Errorf("header: title=%q duration=%d", s.Title, s.Duration)
	}
	if len(s.Notes) != 1 || s.Notes[0] != "No narration: whatever" {
		t.Errorf("notes: %v", s.Notes)
	}

	kinds := []string{}
	for _, ev := range s.Events {
		kinds = append(kinds, ev.Kind)
	}
	want := []string{"narration", "click", "error", "narration", "click", "click", "narration", "click", "click", "click", "click"}
	if len(kinds) != len(want) {
		t.Fatalf("events: got %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("events: got %v, want %v", kinds, want)
		}
	}
	if s.Events[0].Text != "Hello, world." {
		t.Errorf("narration not merged: %q", s.Events[0].Text)
	}

	first := s.Events[1]
	if first.X == nil || *first.X != 100 || first.Sel != "button.save" || first.Text != "Save" {
		t.Errorf("click 1: %+v", first)
	}
	if first.Img != "0002.png" || first.NoRepaint {
		t.Errorf("click 1 img/repaint: %+v", first)
	}
	last := s.Events[4]
	if last.X != nil || last.Sel != "a" || !last.NoRepaint {
		t.Errorf("click 2: %+v", last)
	}
	collapsed := s.Events[5]
	if collapsed.X == nil || *collapsed.X != 4 || collapsed.Sel != "button.save" || collapsed.Text != "Save" ||
		collapsed.Img != "0004.png" || collapsed.Count != 3 || collapsed.Through != "0006.png" || !collapsed.NoRepaint {
		t.Errorf("collapsed click: %+v", collapsed)
	}
	approves := s.Events[6]
	if !approves.Approves || approves.Text != "looks good" {
		t.Errorf("approval: %+v", approves)
	}

	// Desktop clicks with a note after the image (recgo-alttester's
	// "outside <window>", any tool's right-click) are still clicks.
	for i, want := range []struct {
		img, note string
		repaint   bool
		count     int
	}{
		{"0005.png", "outside ~ — Konsole", false, 0},
		{"0006.png", "right-click", false, 0},
		{"0007.png", "outside X", true, 0},
		{"0008.png", "right-click", true, 2},
	} {
		ev := s.Events[7+i]
		if ev.X == nil || *ev.X != 20 || *ev.Y != 20 || ev.Img != want.img || ev.Note != want.note ||
			ev.NoRepaint != want.repaint || ev.Count != want.count || ev.Sel != "" {
			t.Errorf("noted click %d: %+v, want %+v", i, ev, want)
		}
	}

	if len(s.Frames) != 8 || !s.Frames[0].Start || s.Frames[0].Img != "0001.png" || s.Frames[3].Img != "0004.png" || s.Frames[7].Img != "0008.png" {
		t.Errorf("frames: %+v", s.Frames)
	}
}

func TestAnnotateRetina(t *testing.T) {
	s, err := parseSession(setupSession(t, 400, 200))
	if err != nil {
		t.Fatal(err)
	}
	click := s.Frames[1]
	if click.Fx == nil || *click.Fx != 0.5 || *click.Fy != 0.5 {
		t.Errorf("expected 2x fractions 0.5,0.5: %+v", click)
	}
	if s.Frames[2].Fx != nil {
		t.Errorf("coordinate-free click must carry no marker: %+v", s.Frames[2])
	}
}

func TestAnnotate1x(t *testing.T) {
	s, err := parseSession(setupSession(t, 160, 80))
	if err != nil {
		t.Fatal(err)
	}
	click := s.Frames[1]
	if click.Fx == nil || *click.Fx != 0.625 || *click.Fy != 0.625 {
		t.Errorf("expected 1x fractions 0.625,0.625: %+v", click)
	}
}

func TestInvalidUTF8Survives(t *testing.T) {
	dir := t.TempDir()
	md := append([]byte("# Session: bad\n\n00.00.01  Click: 1,1 on a\xe2 → 0002.png\n"), []byte("\n## end\n")...)
	if err := os.WriteFile(filepath.Join(dir, "SESSION.md"), md, 0o600); err != nil {
		t.Fatal(err)
	}
	writePNG(t, filepath.Join(dir, "0001.png"), 10, 10)
	s, err := parseSession(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Events) != 1 || s.Events[0].Kind != "click" {
		t.Fatalf("events: %+v", s.Events)
	}
}

const desktopFixture = `# Session: desktop walk
Start: 2026-08-28 17:09:55
Host: alice@devbox (macOS 26.6.2, arm64)
Displays: 1512x982@2x
Capture: macos-screencapture
Recorded 12s by recgo-desktop · 2 clicks · 1 marks · 0 errors · 0 utterances

Initial screenshot: 0001.png
00.00.01  Click: 812,340 → 0002.png
00.00.02  Focus: Ghostty — ~ → 0003.png
00.00.04  Window: Google Chrome — Save file? → 0004.png
00.00.06  Mark: 4 → 0005.png
00.00.08  Click: 100,200

## How this was captured
`

func TestParseDesktopSession(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SESSION.md"), []byte(desktopFixture), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"0001.png", "0002.png", "0003.png", "0004.png", "0005.png"} {
		writePNG(t, filepath.Join(dir, name), 10, 10)
	}

	s, err := parseSession(dir)
	if err != nil {
		t.Fatal(err)
	}
	if s.Page != "macos-screencapture" {
		t.Errorf("Capture header not picked up as page: %q", s.Page)
	}
	joined := ""
	for _, n := range s.Notes {
		joined += n + "\n"
	}
	if !bytes.Contains([]byte(joined), []byte("alice@devbox")) ||
		!bytes.Contains([]byte(joined), []byte("1512x982@2x")) {
		t.Errorf("host/display info missing from notes: %v", s.Notes)
	}

	kinds := []string{}
	for _, ev := range s.Events {
		kinds = append(kinds, ev.Kind)
	}
	want := []string{"click", "focus", "window", "mark", "click"}
	if len(kinds) != len(want) {
		t.Fatalf("events: got %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("events: got %v, want %v", kinds, want)
		}
	}

	click := s.Events[0]
	if click.X == nil || *click.X != 812 || *click.Y != 340 || click.Sel != "" {
		t.Errorf("desktop click coords: %+v", click)
	}
	if s.Events[1].Text != "Ghostty — ~" || s.Events[1].Img != "0003.png" {
		t.Errorf("focus event: %+v", s.Events[1])
	}
	if s.Events[2].Text != "Google Chrome — Save file?" || s.Events[2].Img != "0004.png" {
		t.Errorf("window event: %+v", s.Events[2])
	}

	imgs := []string{}
	for _, f := range s.Frames {
		imgs = append(imgs, f.Img)
	}
	wantImgs := []string{"0001.png", "0002.png", "0003.png", "0004.png", "0005.png"}
	if len(imgs) != len(wantImgs) {
		t.Fatalf("frames: got %v, want %v", imgs, wantImgs)
	}
	for i := range wantImgs {
		if imgs[i] != wantImgs[i] {
			t.Fatalf("frames: got %v, want %v", imgs, wantImgs)
		}
	}
}
