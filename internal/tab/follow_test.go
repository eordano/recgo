package tab

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestShotNameLayout(t *testing.T) {
	cases := []struct {
		seq   int
		label string
		want  string
	}{
		{1, "at", "0002.png"},
		{1, "", "0002.png"},
		{1, "before", "0002-before.png"},
		{1, "after", "0002-after.png"},
		{1, "full", "0002-full.png"},
		{2, "at", "0003.png"},
	}
	for _, c := range cases {
		got := ShotName(c.seq, c.label)
		if got != c.want {
			t.Errorf("ShotName(%d, %q) = %q, want %q", c.seq, c.label, got, c.want)
		}
		if strings.Contains(got, string(filepath.Separator)) {
			t.Errorf("ShotName(%d, %q) = %q is not flat in the session dir", c.seq, c.label, got)
		}
	}
	if ShotName(1, "at") == InitialShot {
		t.Error("first click's timed shot collides with the initial shot")
	}
}

func TestFollowerMergesClicksFromTwoTabs(t *testing.T) {
	chromium := os.Getenv("CHROMIUM")
	if chromium == "" {
		t.Skip("set CHROMIUM to run the follower end-to-end test")
	}

	fx := newFixture(t)
	const port = 9424
	launchTestChromium(t, chromium, port)

	first, _, err := Attach(port, "")
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	if err := first.Send("Page.navigate", map[string]any{"url": fx.server.URL + "/?tab=one"}, nil); err != nil {
		t.Fatal(err)
	}
	first.Close()

	newURL := fmt.Sprintf("http://127.0.0.1:%d/json/new?%s",
		port, url.QueryEscape(fx.server.URL+"/?tab=two"))
	req, err := http.NewRequest(http.MethodPut, newURL, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("open second tab: %v", err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("open second tab: http %d: %s", res.StatusCode, body)
	}
	time.Sleep(1500 * time.Millisecond)

	outDir := t.TempDir()
	clock := NewClock()
	rec := NewRecording(clock, outDir)
	follower := NewFollower(port, rec)

	if err := follower.Start(); err != nil {
		t.Fatalf("follower start: %v", err)
	}
	if n := follower.Count(); n != 2 {
		t.Fatalf("attached to %d tabs, want 2", n)
	}
	time.Sleep(800 * time.Millisecond)

	tabs, err := ListRecordableTabs(port)
	if err != nil {
		t.Fatal(err)
	}
	targets, _ := ListTargets(port)
	wsFor := map[string]string{}
	for _, tg := range targets {
		wsFor[tg.ID] = tg.WebSocketDebuggerURL
	}

	for i, ti := range tabs {
		drv, err := Dial(wsFor[ti.ID])
		if err != nil {
			t.Fatalf("dial tab %d: %v", i, err)
		}
		clickSelector(t, drv, "[data-testid=load]")
		time.Sleep(600 * time.Millisecond)
		clickSelector(t, drv, "[data-testid=inert]")
		time.Sleep(600 * time.Millisecond)
		drv.Close()
	}
	time.Sleep(1200 * time.Millisecond)

	events := follower.Stop()

	byTarget := map[string]int{}
	seqs := map[int]bool{}
	var clicks int
	for _, e := range events {
		if e.Kind != "click" {
			continue
		}
		clicks++
		byTarget[e.TargetID]++
		if seqs[e.Seq] {
			t.Errorf("click seq %d issued twice — screenshots would collide", e.Seq)
		}
		seqs[e.Seq] = true
		if e.TargetID == "" {
			t.Error("click has no TargetID; cannot tell which tab it came from")
		}
	}
	if clicks != 4 {
		t.Errorf("captured %d clicks, want 4 (2 per tab)", clicks)
	}
	if len(byTarget) != 2 {
		t.Errorf("clicks came from %d tab(s), want 2: %v", len(byTarget), byTarget)
	}

	files := map[string]bool{}
	var withImage, withoutImage int
	for _, e := range events {
		if e.Kind != "click" {
			continue
		}
		if len(e.Shots) != 3 {
			t.Errorf("click %d has %d shot entries, want 3", e.Seq, len(e.Shots))
		}
		for _, s := range e.Shots {
			if s.File == "" {
				withoutImage++
				if s.Captured {
					t.Errorf("click %d %s: no file but Captured=true", e.Seq, s.Label)
				}
				if s.Note == "" {
					t.Errorf("click %d %s: missing image with no explanation", e.Seq, s.Label)
				}
				continue
			}
			withImage++
			if files[s.File] {
				t.Errorf("two clicks wrote the same shot file %q", s.File)
			}
			files[s.File] = true
			if st, err := os.Stat(filepath.Join(outDir, s.File)); err != nil || st.Size() < 500 {
				t.Errorf("shot %q missing or trivial", s.File)
			}
		}
	}
	if withImage == 0 {
		t.Error("no screenshots at all — the foreground tab should have produced some")
	}
	t.Logf("shots: %d with an image, %d without (background tabs do not repaint)",
		withImage, withoutImage)

	all, err := filepath.Glob(filepath.Join(outDir, "*.png"))
	if err != nil {
		t.Fatal(err)
	}
	var timed int
	for _, f := range all {
		if filepath.Base(f) == InitialShot || strings.HasSuffix(f, "-full.png") {
			continue
		}
		timed++
	}
	if timed != withImage {
		t.Errorf("%d timed files on disk but %d recorded in the timeline", timed, withImage)
	}

	attaches := 0
	for _, e := range events {
		if e.Kind == "tab-attach" {
			attaches++
		}
	}
	if attaches != 2 {
		t.Errorf("recorded %d tab-attach events, want 2", attaches)
	}

	if !clock.Calibrated() {
		t.Error("clock was never calibrated")
	}
	_, errMs := clock.Offsets()
	if errMs > 50 {
		t.Errorf("calibration error %.1fms is implausibly high", errMs)
	}
}
