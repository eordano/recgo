package tab

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func waitForOutput(t *testing.T, out *syncBuffer, wants ...string) string {
	t.Helper()
	deadline := time.Now().Add(4 * time.Second)
	for {
		got := out.String()
		missing := ""
		for _, w := range wants {
			if !strings.Contains(got, w) {
				missing = w
				break
			}
		}
		if missing == "" {
			return got
		}
		if time.Now().After(deadline) {
			t.Fatalf("live output never contained %q; got:\n%s", missing, got)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func completedClick(seq int, t float64) Event {
	f1, f2, f3 := t-100, t, t+100
	name := ShotName(seq, "at")
	return Event{
		T: t, Kind: "click", Seq: seq, X: 34, Y: 23,
		Elem:     &Element{Selector: "a#header-login", Tag: "a", Text: "Log in"},
		FullShot: ShotName(seq, "full"),
		Shots: []Shot{
			{Label: "before", Captured: true, FrameT: &f1, File: ShotName(seq, "before")},
			{Label: "at", Captured: true, FrameT: &f2, File: name},
			{Label: "after", Captured: true, FrameT: &f3, File: ShotName(seq, "after")},
		},
	}
}

func TestLiveViewPrintsTheStreamAsItHappens(t *testing.T) {
	rec := NewRecording(NewClock(), t.TempDir())
	out := &syncBuffer{}
	lv := StartLiveView(rec, out, "# live header\n\n")
	defer lv.Stop()

	rec.Push(completedClick(1, 3000))
	rec.Push(Event{T: 4000, Kind: "console", Level: "log", Text: "phx mount"})
	rec.Push(Event{T: 4100, Kind: "network-error", Status: 500, URL: "http://x/api"})
	rec.Push(Event{T: 4200, Kind: "network-error", URL: "http://x/favicon.ico"})
	rec.Push(Event{T: 5000, Kind: "hmr", Flavor: "phoenix", Type: "assets_change", Files: []string{"css"}})
	lv.Utterance(3500, "  when i  click here ")

	got := waitForOutput(t, out,
		"00.00.03  Click: 34,23 on a#header-login text: Log in → 0002.png",
		"00.00.04  console.log: phx mount",
		"00.00.04  Error: network 500 http://x/api",
		"00.00.05 HMR: phoenix assets_change css",
		"00.00.03  **user narration**: when i click here",
	)
	if strings.Contains(got, "favicon") {
		t.Errorf("favicon noise reached the live view:\n%s", got)
	}
}

func TestLiveViewKeepsTheLiveDocumentCurrentAndRemovesItAtStop(t *testing.T) {
	dir := t.TempDir()
	rec := NewRecording(NewClock(), dir)
	out := &syncBuffer{}
	lv := StartLiveView(rec, out, "# live header\n\n")

	rec.Push(completedClick(1, 2000))
	lv.Utterance(1000, "first thought")

	doc := filepath.Join(dir, LiveDocName)
	deadline := time.Now().Add(4 * time.Second)
	for {
		if data, err := os.ReadFile(doc); err == nil {
			text := string(data)
			if strings.Contains(text, "# live header") &&
				strings.Contains(text, "**user narration**: first thought") &&
				strings.Contains(text, "Click: 34,23 on a#header-login") {
				if !strings.Contains(text, "narration**: first thought\n00.00.02  Click") {
					t.Errorf("live document not in stream order:\n%s", text)
				}
				break
			}
		}
		if time.Now().After(deadline) {
			data, _ := os.ReadFile(doc)
			t.Fatalf("live document never caught up; got:\n%s", data)
		}
		time.Sleep(50 * time.Millisecond)
	}

	lv.Stop()
	if _, err := os.Stat(doc); !os.IsNotExist(err) {
		t.Errorf("%s still present after Stop", LiveDocName)
	}
}

func TestLiveViewFlushesUnsettledClicksAtStop(t *testing.T) {
	rec := NewRecording(NewClock(), t.TempDir())
	out := &syncBuffer{}
	lv := StartLiveView(rec, out, "")

	rec.Push(Event{T: 700, Kind: "click", Seq: 1, X: 1, Y: 2,
		Elem: &Element{Selector: "button.save", Tag: "button"}})
	lv.Stop()

	if got := out.String(); !strings.Contains(got, "Click: 1,2 on button.save") {
		t.Errorf("shotless click was dropped at stop; got:\n%s", got)
	}
}
