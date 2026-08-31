package tab

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

const LiveDocName = "SESSION.live.md"

const (
	liveTickMs = 300
	// A click line is not printed until its three screencast shots have
	// landed (they carry the image name and the did-not-repaint evidence);
	// after this long it is printed with whatever it has.
	liveClickSettleMs = 2000
	liveDocEveryTicks = 4
)

// LiveView renders the session document while it is being recorded: one
// SESSION.md-shaped line to out as each thing happens, and a full
// SESSION.live.md in the session folder, rewritten as the stream grows and
// replaced by the real SESSION.md at stop.
type LiveView struct {
	rec    *Recording
	out    io.Writer
	header string

	printMu sync.Mutex

	mu         sync.Mutex
	utterances []Utterance
	dirty      bool

	seen    int
	pending map[int]float64

	stop chan struct{}
	done chan struct{}
}

func StartLiveView(rec *Recording, out io.Writer, header string) *LiveView {
	v := &LiveView{
		rec:     rec,
		out:     out,
		header:  header,
		pending: map[int]float64{},
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
	go v.loop()
	return v
}

// Utterance adds a live narration line. t is session time in ms; live
// narration is a preview — the final document re-transcribes the full wav.
func (v *LiveView) Utterance(t float64, text string) {
	text = oneLine(text)
	if text == "" {
		return
	}
	v.mu.Lock()
	v.utterances = append(v.utterances, Utterance{T: t, EndT: t, Text: text})
	v.dirty = true
	v.mu.Unlock()
	v.printf("%s  **user narration**: %s", FormatClock(t), text)
}

func (v *LiveView) Stop() {
	select {
	case <-v.stop:
	default:
		close(v.stop)
	}
	<-v.done
	os.Remove(filepath.Join(v.rec.OutDir, LiveDocName))
}

func (v *LiveView) printf(format string, args ...any) {
	v.printMu.Lock()
	fmt.Fprintf(v.out, format+"\n", args...)
	v.printMu.Unlock()
}

func (v *LiveView) loop() {
	defer close(v.done)
	ticker := time.NewTicker(liveTickMs * time.Millisecond)
	defer ticker.Stop()
	ticks := 0
	for {
		select {
		case <-v.stop:
			v.consume()
			v.flushClicks(true)
			return
		case <-ticker.C:
			v.consume()
			v.flushClicks(false)
			ticks++
			if ticks%liveDocEveryTicks == 0 {
				v.writeDoc()
			}
		}
	}
}

func (v *LiveView) consume() {
	evs, n := v.rec.EventsSince(v.seen)
	if len(evs) == 0 {
		return
	}
	v.seen = n
	v.mu.Lock()
	v.dirty = true
	v.mu.Unlock()

	for _, e := range evs {
		stamp := FormatClock(e.T)
		switch {
		case e.Kind == "click":
			v.pending[e.Seq] = v.rec.Clock.Now()
		case e.Kind == "record-start", e.Kind == "record-stop":
		case isNoise(e):
		case e.Kind == "mark":
			v.printf("%s  Mark: %d", stamp, e.Seq)
		case e.Kind == "navigation":
			v.printf("%s  Navigate: %s", stamp, e.URL)
		case e.Kind == "tab-switch":
			v.printf("%s  Tab: %s", stamp, oneLine(firstNonEmpty(e.Title, e.URL)))
		case e.Kind == "focus":
			v.printf("%s  Focus: %s", stamp, oneLine(e.Title))
		case e.Kind == "window-appear":
			v.printf("%s  Window: %s", stamp, oneLine(e.Title))
		case e.Kind == "hmr":
			v.printf("%s", oneLine(fmt.Sprintf("%s  HMR: %s %s %s",
				stamp, e.Flavor, e.Type, strings.Join(e.Files, ", "))))
		case isError(e):
			if echoesANetworkError(e, v.rec.Snapshot()) {
				continue
			}
			v.printf("%s  %s", stamp, errorLine(e))
		case e.Kind == "console":
			v.printf("%s  console.%s: %s", stamp, consoleLevel(e), clip(e.Text, 200))
		}
	}
}

func (v *LiveView) flushClicks(force bool) {
	if len(v.pending) == 0 {
		return
	}
	snap := v.rec.Snapshot()
	byShotSeq := map[int]Event{}
	for _, e := range snap {
		if e.Kind == "click" {
			byShotSeq[e.Seq] = e
		}
	}

	var ready []int
	now := v.rec.Clock.Now()
	for seq, first := range v.pending {
		e, ok := byShotSeq[seq]
		if !ok {
			continue
		}
		if force || (len(e.Shots) == 3 && e.FullShot != "") || now-first > liveClickSettleMs {
			ready = append(ready, seq)
		}
	}
	sort.Ints(ready)
	for _, seq := range ready {
		e := byShotSeq[seq]
		v.printf("%s  %s", FormatClock(e.T), clickLine(e))
		delete(v.pending, seq)
	}
}

func (v *LiveView) writeDoc() {
	v.mu.Lock()
	if !v.dirty {
		v.mu.Unlock()
		return
	}
	v.dirty = false
	utt := append([]Utterance(nil), v.utterances...)
	v.mu.Unlock()

	events := v.rec.Snapshot()
	var b strings.Builder
	b.WriteString(v.header)
	for _, l := range buildStream(events, utt) {
		b.WriteString(l.render())
		b.WriteString("\n")
	}
	os.WriteFile(filepath.Join(v.rec.OutDir, LiveDocName), []byte(b.String()), sessionFileMode)
}
