package tab

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestClickSeqIsUniqueAcrossTabs(t *testing.T) {
	rec := NewRecording(NewClock(), t.TempDir())

	const perTab = 200
	var wg sync.WaitGroup
	seen := make(chan int, perTab*2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < perTab; j++ {
				seen <- rec.NextClickSeq()
			}
		}()
	}
	wg.Wait()
	close(seen)

	got := map[int]bool{}
	for s := range seen {
		if got[s] {
			t.Fatalf("duplicate click seq %d", s)
		}
		got[s] = true
	}
	if len(got) != perTab*2 {
		t.Errorf("allocated %d seqs, want %d", len(got), perTab*2)
	}
}

func TestSnapshotIsSortedAndCopied(t *testing.T) {
	rec := NewRecording(NewClock(), t.TempDir())
	rec.Push(Event{T: 300, Kind: "click"})
	rec.Push(Event{T: 100, Kind: "console"})
	rec.Push(Event{T: 200, Kind: "hmr"})

	snap := rec.Snapshot()
	if len(snap) != 3 {
		t.Fatalf("got %d events", len(snap))
	}
	for i := 1; i < len(snap); i++ {
		if snap[i-1].T > snap[i].T {
			t.Errorf("not sorted: %v", snap)
		}
	}

	snap[0].Kind = "tampered"
	if rec.Snapshot()[0].Kind == "tampered" {
		t.Error("Snapshot handed out a reference to internal state")
	}
}

func TestUpdateMutatesInPlace(t *testing.T) {
	rec := NewRecording(NewClock(), t.TempDir())
	ev := rec.Push(Event{T: 1, Kind: "click", Seq: 1})

	rec.Update(ev, func(e *Event) { e.FullShot = "0002.png" })

	if got := rec.Snapshot()[0].FullShot; got != "0002.png" {
		t.Errorf("FullShot = %q", got)
	}
	rec.Update(nil, func(e *Event) { t.Error("nil event should be ignored") })
}

func TestActiveTargetEmitsSwitchOnlyOnChange(t *testing.T) {
	rec := NewRecording(NewClock(), t.TempDir())

	rec.SetActiveTarget("A", "http://a", "A")
	rec.SetActiveTarget("A", "http://a", "A")
	rec.SetActiveTarget("B", "http://b", "B")

	var switches []string
	for _, e := range rec.Snapshot() {
		if e.Kind == "tab-switch" {
			switches = append(switches, e.TargetID)
		}
	}
	if len(switches) != 2 || switches[0] != "A" || switches[1] != "B" {
		t.Errorf("tab-switch events = %v, want [A B]", switches)
	}
	if rec.ActiveTarget() != "B" {
		t.Errorf("active = %q", rec.ActiveTarget())
	}
}

func TestWSFrameArchiveAppendsAcrossTabs(t *testing.T) {
	dir := t.TempDir()
	rec := NewRecording(NewClock(), dir)

	rec.WriteWSFrame(map[string]any{"targetId": "A", "payload": "one"})
	rec.WriteWSFrame(map[string]any{"targetId": "B", "payload": "two"})
	rec.closeWSLog()

	raw, err := os.ReadFile(filepath.Join(dir, "logs", "websocket-frames.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2:\n%s", len(lines), raw)
	}
	for _, l := range lines {
		var v map[string]any
		if err := json.Unmarshal([]byte(l), &v); err != nil {
			t.Errorf("line is not JSON: %q", l)
		}
	}
}

func TestOnChangeFiresWithoutHoldingTheLock(t *testing.T) {
	rec := NewRecording(NewClock(), t.TempDir())

	done := make(chan int, 4)
	rec.OnChange(func() { done <- len(rec.Snapshot()) })

	rec.Push(Event{T: 1, Kind: "click"})
	select {
	case n := <-done:
		if n != 1 {
			t.Errorf("callback saw %d events", n)
		}
	default:
		t.Error("OnChange did not fire")
	}
}

func TestCountsClassifiesEvents(t *testing.T) {
	rec := NewRecording(NewClock(), t.TempDir())
	rec.Push(Event{T: 1, Kind: "click"})
	rec.Push(Event{T: 2, Kind: "click"})
	rec.Push(Event{T: 3, Kind: "hmr", Type: "update"})
	rec.Push(Event{T: 4, Kind: "exception", Text: "boom"})
	rec.Push(Event{T: 5, Kind: "network-error", Status: 404, URL: "/favicon.ico"})

	clicks, hmr, errs := rec.Counts()
	if clicks != 2 || hmr != 1 {
		t.Errorf("clicks=%d hmr=%d", clicks, hmr)
	}
	if errs != 1 {
		t.Errorf("errors=%d, want 1 (favicon should not count)", errs)
	}
}

func TestEverythingWrittenIsOwnerOnly(t *testing.T) {
	dir := t.TempDir()
	rec := NewRecording(NewClock(), dir)

	rec.WriteWSFrame(map[string]any{"payload": "secret"})
	rec.closeWSLog()
	rec.Push(Event{T: 1, Kind: "click", Seq: 1})

	if _, err := Pack(dir, rec.Snapshot(), rec.Clock, nil, Meta{Slug: "perm-test"},
		PackOptions{JSON: true}); err != nil {
		t.Fatalf("Pack: %v", err)
	}

	if err := os.WriteFile(filepath.Join(dir, ShotName(1, "at")), []byte("png"), sessionFileMode); err != nil {
		t.Fatal(err)
	}

	var checked int
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if p == dir {
			return nil
		}
		checked++
		perm := info.Mode().Perm()
		if perm&0o077 != 0 {
			rel, _ := filepath.Rel(dir, p)
			t.Errorf("%s is %04o — accessible to other users", rel, perm)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if checked == 0 {
		t.Fatal("walked nothing; the test is not checking anything")
	}
	t.Logf("checked %d paths", checked)
}
