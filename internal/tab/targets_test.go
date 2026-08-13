package tab

import (
	"strings"
	"testing"
)

func TestIsRecordableFiltersNonAppTargets(t *testing.T) {
	ws := "ws://127.0.0.1:9222/devtools/page/X"
	cases := []struct {
		name string
		tgt  Target
		want bool
	}{
		{"app page", Target{Type: "page", URL: "http://localhost:5173/", WebSocketDebuggerURL: ws}, true},
		{"https page", Target{Type: "page", URL: "https://example.com", WebSocketDebuggerURL: ws}, true},
		{"devtools window", Target{Type: "page", URL: "devtools://devtools/bundled/x.html", WebSocketDebuggerURL: ws}, false},
		{"chrome internal", Target{Type: "page", URL: "chrome://newtab/", WebSocketDebuggerURL: ws}, false},
		{"extension", Target{Type: "page", URL: "chrome-extension://abc/popup.html", WebSocketDebuggerURL: ws}, false},
		{"service worker", Target{Type: "service_worker", URL: "http://x/sw.js", WebSocketDebuggerURL: ws}, false},
		{"iframe target", Target{Type: "iframe", URL: "http://x/", WebSocketDebuggerURL: ws}, false},
		{"no debugger url", Target{Type: "page", URL: "http://x/"}, false},
		{"blank url", Target{Type: "page", URL: "", WebSocketDebuggerURL: ws}, false},
	}

	for _, c := range cases {
		if got := IsRecordable(c.tgt); got != c.want {
			t.Errorf("%s: IsRecordable = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestTabInfoShort(t *testing.T) {
	ti := TabInfo{Title: "Document editor", URL: "http://localhost:5173/app"}
	if got := ti.Short(40); got != "Document editor" {
		t.Errorf("Short = %q", got)
	}

	ti = TabInfo{URL: "https://example.com/deep/path"}
	if got := ti.Short(40); got != "example.com/deep/path" {
		t.Errorf("Short = %q", got)
	}

	ti = TabInfo{Title: strings.Repeat("x", 100)}
	got := ti.Short(10)
	if len([]rune(got)) != 10 || !strings.HasSuffix(got, "…") {
		t.Errorf("Short(10) = %q (%d runes)", got, len([]rune(got)))
	}

	tiny := TabInfo{Title: "abc"}
	if got := tiny.Short(0); got != "abc" {
		t.Errorf("Short(0) = %q", got)
	}
}

func TestListRecordableTabsFailsClosedOnDeadPort(t *testing.T) {
	if _, err := ListRecordableTabs(1); err == nil {
		t.Error("expected an error against a port with no browser")
	}
}

func TestTabWatcherStopIsIdempotent(t *testing.T) {
	w := NewTabWatcher(1)
	ch := w.Watch()

	w.Stop()
	w.Stop()

	for range ch {
	}
}

func TestTabWatcherStopBeforeWatchIsSafe(t *testing.T) {
	NewTabWatcher(1).Stop()
}
