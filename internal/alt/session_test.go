package alt

import (
	"strings"
	"sync"
	"testing"

	"github.com/eordano/recgo/internal/screencast"
)

type noteLog struct {
	mu    sync.Mutex
	lines []string
}

func (n *noteLog) add(line string) {
	n.mu.Lock()
	n.lines = append(n.lines, line)
	n.mu.Unlock()
}

func (n *noteLog) all() []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return append([]string(nil), n.lines...)
}

func (n *noteLog) has(prefix string) bool {
	for _, l := range n.all() {
		if strings.HasPrefix(l, prefix) {
			return true
		}
	}
	return false
}

func attach(t *testing.T, o SessionOptions) (*Session, *noteLog) {
	t.Helper()
	notes := &noteLog{}
	o.Host = "127.0.0.1"
	s, err := Attach(o, notes.add)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return s, notes
}

func TestAttachNotesConnectAndDisconnect(t *testing.T) {
	s, notes := attach(t, SessionOptions{})
	app := dialApp(t, s.Server(), "__default__", true)
	waitFor(t, "connected note", func() bool { return notes.has("AltTester app connected") })
	if got := notes.all(); got[0] != "AltTester app connected: __default__ (mock), server 2.3.0" {
		t.Fatalf("connected note = %q", got[0])
	}
	app.ws.Close()
	waitFor(t, "disconnected note", func() bool { return notes.has("AltTester app disconnected") })
	if got := notes.all(); got[1] != "AltTester app disconnected: __default__ (mock)" {
		t.Fatalf("disconnected note = %q", got[1])
	}
	s.Close()
	if notes.has("AltTester app never connected") {
		t.Fatalf("an app that came must not be reported as never connected: %v", notes.all())
	}
}

func TestAttachNotesAnAppThatNeverCame(t *testing.T) {
	s, notes := attach(t, SessionOptions{})
	addr := s.Addr()
	s.Close()
	if got := notes.all(); len(got) != 1 || got[0] != "AltTester app never connected (listened on "+addr+")" {
		t.Fatalf("notes = %v", got)
	}
}

func TestSessionNotesAMissingProbeOnce(t *testing.T) {
	game := screencast.WindowRect{Title: "Decentraland", Class: "decentraland", Width: 1280, Height: 720}
	s, notes := attach(t, SessionOptions{
		WindowAt: func(x, y float64) (screencast.WindowRect, bool) { return game, true },
	})
	dialApp(t, s.Server(), "Decentraland", false)
	waitFor(t, "app", func() bool { return s.Server().Driver().Connected() })
	for i := 0; i < 2; i++ {
		if el, note := s.Resolve(640, 360); el == nil || el.Selector != "/Canvas/Play" || note != "" {
			t.Fatalf("raycast fallback %d = %+v %q", i, el, note)
		}
	}
	var missing []string
	for _, l := range notes.all() {
		if strings.HasPrefix(l, "AltTester probe missing in this build (") {
			missing = append(missing, l)
		}
	}
	if len(missing) != 1 || !strings.HasSuffix(missing[0],
		"); clicks resolved by findObjectAtCoordinates: selectors are UGUI transform paths from the object tree "+
			"(bare object names when the tree does not know the hit), UI Toolkit elements are not seen") {
		t.Fatalf("probe-missing notes = %v", missing)
	}
}
