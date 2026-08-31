package tab

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

const (
	sessionDirMode  = 0o700
	sessionFileMode = 0o600
)

const InitialShot = "0001.png"

func ShotName(seq int, label string) string {
	if label == "" || label == "at" {
		return fmt.Sprintf("%04d.png", seq+1)
	}
	return fmt.Sprintf("%04d-%s.png", seq+1, label)
}

type Recording struct {
	Clock  *Clock
	OutDir string

	mu          sync.Mutex
	events      []*Event
	clickSeq    int
	initialShot bool

	activeTargetID string
	onChange       func()

	wsLog *os.File
}

func NewRecording(clock *Clock, outDir string) *Recording {
	return &Recording{Clock: clock, OutDir: outDir}
}

func (s *Recording) WriteWSFrame(v any) {
	line, err := json.Marshal(v)
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.wsLog == nil {
		if err := os.MkdirAll(filepath.Join(s.OutDir, "logs"), sessionDirMode); err != nil {
			return
		}
		f, err := os.OpenFile(filepath.Join(s.OutDir, "logs", "websocket-frames.jsonl"),
			os.O_CREATE|os.O_WRONLY|os.O_APPEND, sessionFileMode)
		if err != nil {
			return
		}
		s.wsLog = f
	}
	s.wsLog.Write(append(line, '\n'))
}

func (s *Recording) closeWSLog() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.wsLog != nil {
		s.wsLog.Close()
		s.wsLog = nil
	}
}

func (s *Recording) OnChange(fn func()) {
	s.mu.Lock()
	s.onChange = fn
	s.mu.Unlock()
}

func (s *Recording) Push(e Event) *Event {
	s.mu.Lock()
	ev := &e
	s.events = append(s.events, ev)
	fn := s.onChange
	s.mu.Unlock()
	if fn != nil {
		fn()
	}
	return ev
}

func (s *Recording) Update(ev *Event, fn func(*Event)) {
	if ev == nil {
		return
	}
	s.mu.Lock()
	fn(ev)
	cb := s.onChange
	s.mu.Unlock()
	if cb != nil {
		cb()
	}
}

func (s *Recording) ClaimInitialShot() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.initialShot {
		return false
	}
	s.initialShot = true
	return true
}

func (s *Recording) NextClickSeq() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clickSeq++
	return s.clickSeq
}

func (s *Recording) EventsSince(n int) ([]Event, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n > len(s.events) {
		n = len(s.events)
	}
	out := make([]Event, 0, len(s.events)-n)
	for _, e := range s.events[n:] {
		out = append(out, *e)
	}
	return out, len(s.events)
}

func (s *Recording) Snapshot() []Event {
	s.mu.Lock()
	out := make([]Event, 0, len(s.events))
	for _, e := range s.events {
		out = append(out, *e)
	}
	s.mu.Unlock()

	sortEventsByTime(out)
	return out
}

func (s *Recording) Counts() (clicks, hmr, errs int) {
	for _, e := range s.Snapshot() {
		switch {
		case e.Kind == "click":
			clicks++
		case e.Kind == "hmr":
			hmr++
		}
		if isError(e) {
			errs++
		}
	}
	return
}

func (s *Recording) SetActiveTarget(id, url, title string) {
	s.mu.Lock()
	if s.activeTargetID == id {
		s.mu.Unlock()
		return
	}
	s.activeTargetID = id
	s.mu.Unlock()

	s.Push(Event{
		T: s.Clock.Now(), Kind: "tab-switch",
		URL: url, Title: title, TargetID: id,
	})
}

func (s *Recording) ActiveTarget() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.activeTargetID
}
