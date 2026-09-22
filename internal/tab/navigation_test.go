package tab

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const deckURL = "file:///home/user/slides/q4-pillars.html"

// handlerRecorder is a Recorder whose event handlers are live on a fake CDP
// connection, with a clock calibrated so page time maps 1:1 onto session time.
func handlerRecorder(t *testing.T) (*Recorder, *fakeCDPServer, *CDP) {
	t.Helper()
	f := newFakeCDP(t)
	echoResults(f)
	c := dialFake(t, f)
	clock := NewClock()
	clock.browserCalibrated = true
	rec := NewRecorder(c, clock, t.TempDir())
	rec.registerHandlers()
	if err := c.Send("ping", nil, nil); err != nil {
		t.Fatal(err)
	}
	return rec, f, c
}

// emitBinding delivers a payload the way inject.js does from the top frame
// unless the test says otherwise with an explicit "top".
func emitBinding(t *testing.T, f *fakeCDPServer, c *CDP, payload map[string]any) {
	t.Helper()
	if _, ok := payload["top"]; !ok {
		payload["top"] = true
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	f.broadcast(map[string]any{
		"method": "Runtime.bindingCalled",
		"params": map[string]any{"name": "__rtEmit", "payload": string(raw)},
	})
	// A round trip after the broadcast proves the event pump has run the
	// handler: replies are delivered in wire order.
	if err := c.Send("sync", nil, nil); err != nil {
		t.Fatal(err)
	}
}

func navigations(rec *Recorder) []Event {
	var out []Event
	for _, e := range rec.Events() {
		if e.Kind == "navigation" {
			out = append(out, e)
		}
	}
	return out
}

func TestSameDocumentNavigationsBecomeOneLineEach(t *testing.T) {
	rec, f, c := handlerRecorder(t)

	emitBinding(t, f, c, map[string]any{"kind": "visibility", "pageTime": 100.0,
		"visible": true, "url": deckURL + "#/9", "title": deckTitle})
	if n := navigations(rec); len(n) != 0 {
		t.Fatalf("the install-time visibility report seeded a navigation: %+v", n)
	}

	// reveal.js: replaceState fires the wrapper, then hashchange fires it
	// again for the same URL.
	for _, pt := range []float64{2000, 2001} {
		emitBinding(t, f, c, map[string]any{"kind": "navigation", "pageTime": pt,
			"url": deckURL + "#/10", "title": deckTitle})
	}
	emitBinding(t, f, c, map[string]any{"kind": "navigation", "pageTime": 2500,
		"url": deckURL + "#/10", "title": deckTitle})
	emitBinding(t, f, c, map[string]any{"kind": "navigation", "pageTime": 4000,
		"url": deckURL + "#/11", "title": deckTitle})

	got := navigations(rec)
	if len(got) != 2 {
		t.Fatalf("navigations = %+v, want one per URL change", got)
	}
	if got[0].T != 2000 || got[0].URL != deckURL+"#/10" || got[0].Title != "" {
		t.Errorf("first = %+v, want t=2000 #/10 with no title (unchanged)", got[0])
	}
	if got[1].URL != deckURL+"#/11" {
		t.Errorf("second = %+v", got[1])
	}
	if line := navigationLine(got[0]); line != "Navigate: "+deckURL+"#/10" {
		t.Errorf("line = %q", line)
	}
}

func TestNavigationCarriesTheTitleWhenItChanges(t *testing.T) {
	rec, f, c := handlerRecorder(t)

	emitBinding(t, f, c, map[string]any{"kind": "visibility", "pageTime": 100.0,
		"visible": true, "url": "http://localhost:5173/", "title": "Acme — Home"})
	emitBinding(t, f, c, map[string]any{"kind": "navigation", "pageTime": 3000,
		"url": "http://localhost:5173/settings", "title": "Acme — Settings"})
	emitBinding(t, f, c, map[string]any{"kind": "navigation", "pageTime": 5000,
		"url": "http://localhost:5173/settings?tab=billing", "title": "Acme — Settings"})
	// A click reports where it happened; that URL is already known.
	emitBinding(t, f, c, map[string]any{"kind": "click", "pageTime": 1,
		"url": "http://localhost:5173/settings?tab=billing", "title": "Acme — Settings",
		"x": 10, "y": 20, "target": map[string]any{"tag": "button", "text": "Save"}})

	got := navigations(rec)
	if len(got) != 2 {
		t.Fatalf("navigations = %+v", got)
	}
	if got[0].Title != "Acme — Settings" {
		t.Errorf("first navigation lost its new title: %+v", got[0])
	}
	if got[1].Title != "" {
		t.Errorf("unchanged title repeated: %+v", got[1])
	}
	if line := navigationLine(got[0]); line != "Navigate: http://localhost:5173/settings — Acme — Settings" {
		t.Errorf("line = %q", line)
	}
	rec.Stop()
}

func TestAFreshDocumentAnnouncesItselfAsANavigation(t *testing.T) {
	rec, f, c := handlerRecorder(t)
	rec.seedLocation("about:blank", "")

	// The injected script's install-time visibility report is how a new
	// document (Page.navigate, a link click) reaches the timeline.
	emitBinding(t, f, c, map[string]any{"kind": "visibility", "pageTime": 50.0,
		"timeOrigin": 1000.0, "visible": true,
		"url": "http://localhost:5173/", "title": "Acme dashboard"})
	emitBinding(t, f, c, map[string]any{"kind": "visibility", "pageTime": 900.0,
		"timeOrigin": 1000.0, "visible": false,
		"url": "http://localhost:5173/", "title": "Acme dashboard"})

	got := navigations(rec)
	if len(got) != 1 || got[0].URL != "http://localhost:5173/" || got[0].Title != "Acme dashboard" || got[0].T != 1050 {
		t.Errorf("navigations = %+v", got)
	}
}

func TestTheFirstTitleSeenAfterANavigationBelongsToIt(t *testing.T) {
	rec, f, c := handlerRecorder(t)
	rec.seedLocation("about:blank", "")

	// Document start: <title> not parsed yet. DOMContentLoaded: it is.
	emitBinding(t, f, c, map[string]any{"kind": "visibility", "pageTime": 5.0,
		"timeOrigin": 1000.0, "visible": true, "url": "http://localhost:5173/", "title": ""})
	emitBinding(t, f, c, map[string]any{"kind": "navigation", "pageTime": 80.0,
		"timeOrigin": 1000.0, "url": "http://localhost:5173/", "title": "recgo-tab fixture"})
	// An HMR payload renames the document minutes later; that is not the
	// title the page had when it was navigated to.
	emitBinding(t, f, c, map[string]any{"kind": "navigation", "pageTime": 90000.0,
		"timeOrigin": 1000.0, "url": "http://localhost:5173/", "title": "updated /src/App.tsx"})

	got := navigations(rec)
	if len(got) != 1 || got[0].T != 1005 || got[0].Title != "recgo-tab fixture" {
		t.Errorf("navigations = %+v", got)
	}

	// On a deck the title never changes, so a re-report must not pin it.
	emitBinding(t, f, c, map[string]any{"kind": "navigation", "pageTime": 100000.0,
		"timeOrigin": 1000.0, "url": deckURL + "#/1", "title": "updated /src/App.tsx"})
	emitBinding(t, f, c, map[string]any{"kind": "navigation", "pageTime": 100100.0,
		"timeOrigin": 1000.0, "url": deckURL + "#/1", "title": "updated /src/App.tsx"})
	got = navigations(rec)
	if len(got) != 2 || got[1].Title != "" {
		t.Errorf("navigations = %+v, want the second without a title", got)
	}
}

func TestUnknownStartIsSeededNotReported(t *testing.T) {
	rec, f, c := handlerRecorder(t)

	emitBinding(t, f, c, map[string]any{"kind": "navigation", "pageTime": 500.0,
		"url": deckURL + "#/9", "title": deckTitle})
	if n := navigations(rec); len(n) != 0 {
		t.Errorf("first URL ever seen was reported as a change: %+v", n)
	}
	emitBinding(t, f, c, map[string]any{"kind": "navigation", "pageTime": 900.0,
		"url": deckURL + "#/10", "title": deckTitle})
	if n := navigations(rec); len(n) != 1 {
		t.Errorf("navigations = %+v", n)
	}
}

func TestNavigationLinesReachTheDocument(t *testing.T) {
	rec, f, c := handlerRecorder(t)
	emitBinding(t, f, c, map[string]any{"kind": "visibility", "pageTime": 100.0,
		"visible": true, "url": deckURL, "title": deckTitle})
	emitBinding(t, f, c, map[string]any{"kind": "navigation", "pageTime": 61000,
		"url": deckURL + "#/1", "title": deckTitle})
	emitBinding(t, f, c, map[string]any{"kind": "navigation", "pageTime": 62000,
		"url": deckURL + "#/2", "title": "Q4 pillars — Build"})

	dir := t.TempDir()
	if _, err := Pack(dir, rec.Events(), rec.clock, nil, Meta{Slug: "deck", Tool: "recgo-tab",
		TargetURL: deckURL}, PackOptions{}); err != nil {
		t.Fatal(err)
	}
	md := readFile(t, dir, "SESSION.md")
	for _, want := range []string{
		"00.01.01  Navigate: " + deckURL + "#/1\n",
		"00.01.02  Navigate: " + deckURL + "#/2 — Q4 pillars — Build\n",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("SESSION.md missing %q:\n%s", want, md)
		}
	}
}

func readFile(t *testing.T, dir, name string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// inject.js is installed in every frame. A same-site iframe announces itself
// on install and reports its own location with each click; neither is the
// user going anywhere, and its title is not the page's.
func TestFramesDoNotNavigateOrRetitleThePage(t *testing.T) {
	rec, f, c := handlerRecorder(t)

	emitBinding(t, f, c, map[string]any{"kind": "visibility", "pageTime": 100.0,
		"visible": true, "url": "http://localhost:5173/", "title": "Top page"})
	emitBinding(t, f, c, map[string]any{"kind": "visibility", "top": false, "pageTime": 150.0,
		"visible": true, "url": "http://localhost:5173/frame", "title": "Frame page"})
	emitBinding(t, f, c, map[string]any{"kind": "navigation", "top": false, "pageTime": 200.0,
		"url": "http://localhost:5173/frame", "title": "Frame page"})
	emitBinding(t, f, c, map[string]any{"kind": "click", "top": false, "pageTime": 1000.0,
		"x": 10.0, "y": 20.0, "url": "http://localhost:5173/frame", "title": "Frame page",
		"target": map[string]any{"tag": "p", "selector": "p"}})
	emitBinding(t, f, c, map[string]any{"kind": "click", "pageTime": 2000.0,
		"x": 30.0, "y": 40.0, "url": "http://localhost:5173/", "title": "Top page",
		"target": map[string]any{"tag": "h1", "selector": "h1"}})

	if n := navigations(rec); len(n) != 0 {
		t.Errorf("the frame's reports became navigations: %+v", n)
	}
	if got := rec.URL(); got != "http://localhost:5173/" {
		t.Errorf("tracked URL = %q, want the top frame's", got)
	}
	var clicks []Event
	for _, e := range rec.Stop() {
		if e.Kind == "click" {
			clicks = append(clicks, e)
		}
	}
	if len(clicks) != 2 || clicks[0].URL != "http://localhost:5173/frame" {
		t.Errorf("clicks inside the frame are still clicks: %+v", clicks)
	}
}
