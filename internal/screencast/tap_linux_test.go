//go:build linux

package screencast

import (
	"encoding/binary"
	"strings"
	"testing"
)

func TestParseInputEvent(t *testing.T) {
	b := make([]byte, inputEventSize)
	binary.LittleEndian.PutUint16(b[16:], evKey)
	binary.LittleEndian.PutUint16(b[18:], btnRight)
	binary.LittleEndian.PutUint32(b[20:], 1)
	ev, ok := parseInputEvent(b)
	if !ok || clickButton(ev) != 2 {
		t.Fatalf("right press: %+v %v", ev, ok)
	}
	binary.LittleEndian.PutUint32(b[20:], 0)
	ev, _ = parseInputEvent(b)
	if clickButton(ev) != 0 {
		t.Fatal("release must not count as a click")
	}
	if _, ok := parseInputEvent(b[:10]); ok {
		t.Fatal("short read accepted")
	}
}

func TestParseWindowRect(t *testing.T) {
	r, ok := parseWindowRect(`{"Title":"Decentraland","Class":"unity-explorer","Left":100,"Top":50.5,"Width":1280,"Height":720}`)
	if !ok || r.Title != "Decentraland" || r.Class != "unity-explorer" || r.Left != 100 || r.Top != 50.5 || r.Width != 1280 || r.Height != 720 {
		t.Fatalf("parsed %+v %v", r, ok)
	}
	if !r.Contains(100, 50.5) || r.Contains(1380, 100) {
		t.Error("Contains: left/top inclusive, right/bottom exclusive")
	}
	for _, raw := range []string{"null", "", "  null ", "{}", `{"Width":0,"Height":10}`, "not json"} {
		if _, ok := parseWindowRect(raw); ok {
			t.Errorf("parseWindowRect(%q) accepted", raw)
		}
	}
}

func TestWindowAtScriptCarriesThePoint(t *testing.T) {
	b := &kwinBridge{name: "dev.eordano.recgo.p1"}
	s := windowAtScript(b, 812.5, 340)
	for _, want := range []string{"windowAt(812.5, 340)", `"WindowAt"`, "stackingOrder", "clientGeometry"} {
		if !strings.Contains(s, want) {
			t.Errorf("script lacks %q:\n%s", want, s)
		}
	}
	if !strings.Contains(cursorScript(b), "windowAt(p.x, p.y)") {
		t.Error("the per-click cursor script should also report the window under the pointer")
	}
}
