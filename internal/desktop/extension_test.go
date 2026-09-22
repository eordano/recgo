package desktop

import (
	"errors"
	"strings"
	"testing"

	"github.com/eordano/recgo/internal/tab"
)

func TestApplyResolvedMergesElementAndNote(t *testing.T) {
	rec := tab.NewRecording(tab.NewClock(), t.TempDir())
	ev := rec.Push(tab.Event{T: 10, Kind: "click", Seq: 1, X: 812, Y: 340, Note: "right-click"})
	el := &tab.Element{Selector: "/Canvas/Play", Tag: "Button", Text: "Play"}
	rec.Update(ev, func(e *tab.Event) { applyResolved(e, el, "") })
	if ev.Elem != el || ev.Note != "right-click" {
		t.Fatalf("element only: %+v", ev)
	}
	rec.Update(ev, func(e *tab.Event) { applyResolved(e, nil, "outside Konsole") })
	if ev.Elem != el || ev.Note != "right-click; outside Konsole" {
		t.Fatalf("note appended, element kept: %+v", ev)
	}
	plain := &tab.Event{Kind: "click"}
	applyResolved(plain, nil, "outside Konsole")
	if plain.Note != "outside Konsole" || plain.Elem != nil {
		t.Fatalf("nothing found: %+v", plain)
	}
	snap := rec.Snapshot()
	if snap[0].Elem == nil || snap[0].Elem.Selector != "/Canvas/Play" {
		t.Fatal("the snapshot Pack reads must carry the resolved element")
	}
}

// A tool whose Start fails (its AltTester port is taken) aborts the run
// before any capture begins, and the deferred Stop never runs for it.
func TestExtensionStartFailureAbortsTheRun(t *testing.T) {
	prev := extension
	t.Cleanup(func() { extension = prev })
	stopped := false
	SetExtension(&Extension{
		Start: func(note func(string)) error { return errors.New("listen tcp 127.0.0.1:13000: address already in use") },
		Stop:  func() { stopped = true },
	})
	err := run(opts{out: t.TempDir(), sttBackend: "none"})
	if err == nil || !strings.Contains(err.Error(), "address already in use") {
		t.Fatalf("run = %v, want the Start error", err)
	}
	if stopped {
		t.Fatal("Stop ran for a tool that never started")
	}
}
