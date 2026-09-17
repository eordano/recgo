//go:build linux

package screencast

import (
	"encoding/binary"
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

func TestWindowDesc(t *testing.T) {
	for _, c := range []struct{ caption, class, want string }{
		{"~ — Konsole", "konsole", "~ — Konsole"},
		{"Settings", "org.kde.systemsettings", "org.kde.systemsettings: Settings"},
		{"", "firefox", "firefox"},
		{"", "", "(untitled)"},
	} {
		if got := windowDesc(c.caption, c.class); got != c.want {
			t.Errorf("windowDesc(%q,%q) = %q want %q", c.caption, c.class, got, c.want)
		}
	}
}
