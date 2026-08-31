package tab

import (
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"
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

	ti = TabInfo{Title: strings.Repeat("é", 100)}
	got = ti.Short(10)
	if !utf8.ValidString(got) {
		t.Errorf("Short on a multibyte title produced invalid UTF-8: %q", got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("Short on a multibyte title did not truncate: %q", got)
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

// fakeCDPListing serves a /json/list on a loopback port, the way Chrome's
// remote-debugging endpoint does, and returns that port.
func fakeCDPListing(t *testing.T, body string) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/json/list", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, body)
	})
	srv := &http.Server{Handler: mux}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return ln.Addr().(*net.TCPAddr).Port
}

func TestMatchTabPicksTheRequestedTab(t *testing.T) {
	port := fakeCDPListing(t, `[
	  {"id":"A","type":"page","url":"http://localhost:5173/dashboard","title":"Dashboard","webSocketDebuggerUrl":"ws://x/A"},
	  {"id":"B","type":"page","url":"https://docs.example/guide","title":"Guide","webSocketDebuggerUrl":"ws://x/B"},
	  {"id":"C","type":"page","url":"chrome://settings","title":"Settings","webSocketDebuggerUrl":"ws://x/C"}
	]`)

	for _, c := range []struct{ match, want string }{
		{"", "A"},
		{"docs.example", "B"},
		{"Guide", "B"},
		{"dashboard", "A"},
	} {
		got, err := MatchTab(port, c.match)
		if err != nil {
			t.Fatalf("MatchTab(%q): %v", c.match, err)
		}
		if got.ID != c.want {
			t.Errorf("MatchTab(%q) = %q, want %q", c.match, got.ID, c.want)
		}
	}

	// A chrome:// page is not recordable, so matching it must fail rather
	// than pin the recording to a tab that can never produce events.
	if _, err := MatchTab(port, "settings"); err == nil {
		t.Error("MatchTab matched a chrome:// target")
	}
	if _, err := MatchTab(port, "nothing-here"); err == nil {
		t.Error("MatchTab silently accepted a match nothing satisfies")
	}
}
