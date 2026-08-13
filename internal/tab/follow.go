package tab

import (
	"fmt"
	"sort"
	"sync"
)

type Follower struct {
	Port      int
	Recording *Recording

	OnUpdate func()

	mu        sync.Mutex
	recorders map[string]*Recorder
	titles    map[string]string
	watcher   *TabWatcher
	stopped   bool
}

func NewFollower(port int, rec *Recording) *Follower {
	return &Follower{
		Port:      port,
		Recording: rec,
		recorders: map[string]*Recorder{},
		titles:    map[string]string{},
	}
}

func (f *Follower) Start() error {
	tabs, err := ListRecordableTabs(f.Port)
	if err != nil {
		return err
	}
	if len(tabs) == 0 {
		return fmt.Errorf("no recordable tabs on port %d — is the browser running with --remote-debugging-port=%d?", f.Port, f.Port)
	}

	var attached int
	var firstErr error
	for _, t := range tabs {
		if err := f.Attach(t); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		attached++
	}
	if attached == 0 {
		return fmt.Errorf("could not attach to any of %d tab(s): %w", len(tabs), firstErr)
	}

	f.watcher = NewTabWatcher(f.Port)
	go func() {
		for ev := range f.watcher.Watch() {
			if ev.Err != nil {
				continue
			}
			for _, t := range ev.Appeared {
				f.Attach(t)
			}
			for _, id := range ev.Vanished {
				f.detach(id)
			}
			f.notify()
		}
	}()
	return nil
}

func (f *Follower) Attach(t TabInfo) error {
	f.mu.Lock()
	if f.stopped {
		f.mu.Unlock()
		return fmt.Errorf("follower stopped")
	}
	if _, ok := f.recorders[t.ID]; ok {
		f.mu.Unlock()
		return nil
	}
	f.mu.Unlock()

	targets, err := ListTargets(f.Port)
	if err != nil {
		return err
	}
	var wsURL string
	for _, tg := range targets {
		if tg.ID == t.ID {
			wsURL = tg.WebSocketDebuggerURL
			break
		}
	}
	if wsURL == "" {
		return fmt.Errorf("tab %s has no debugger url", t.ID)
	}

	cdp, err := Dial(wsURL)
	if err != nil {
		return err
	}

	rec := NewSessionRecorder(cdp, f.Recording, t.ID, t.URL)
	if _, _, err := rec.Start(); err != nil {
		cdp.Close()
		return err
	}

	f.mu.Lock()
	f.recorders[t.ID] = rec
	f.titles[t.ID] = t.Title
	f.mu.Unlock()

	f.Recording.Push(Event{
		T: f.Recording.Clock.Now(), Kind: "tab-attach",
		URL: t.URL, Title: t.Title, TargetID: t.ID,
	})
	f.notify()
	return nil
}

func (f *Follower) detach(id string) {
	f.mu.Lock()
	rec, ok := f.recorders[id]
	title := f.titles[id]
	delete(f.recorders, id)
	delete(f.titles, id)
	f.mu.Unlock()
	if !ok {
		return
	}

	url := rec.URL()
	rec.Stop()
	f.Recording.Push(Event{
		T: f.Recording.Clock.Now(), Kind: "tab-close",
		URL: url, Title: title, TargetID: id,
	})
}

func (f *Follower) notify() {
	if f.OnUpdate != nil {
		f.OnUpdate()
	}
}

func (f *Follower) Tabs() []TabInfo {
	active := f.Recording.ActiveTarget()
	clicks := map[string]int{}
	for _, e := range f.Recording.Snapshot() {
		if e.Kind == "click" && e.TargetID != "" {
			clicks[e.TargetID]++
		}
	}

	f.mu.Lock()
	out := make([]TabInfo, 0, len(f.recorders))
	for id, rec := range f.recorders {
		out = append(out, TabInfo{
			ID: id, URL: rec.URL(), Title: f.titles[id],
			Attached: true, Visible: id == active, Clicks: clicks[id],
		})
	}
	f.mu.Unlock()

	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (f *Follower) Count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.recorders)
}

func (f *Follower) Stop() []Event {
	f.mu.Lock()
	if f.stopped {
		f.mu.Unlock()
		return f.Recording.Snapshot()
	}
	f.stopped = true
	recs := make([]*Recorder, 0, len(f.recorders))
	for _, r := range f.recorders {
		recs = append(recs, r)
	}
	f.recorders = map[string]*Recorder{}
	watcher := f.watcher
	f.mu.Unlock()

	if watcher != nil {
		watcher.Stop()
	}
	var wg sync.WaitGroup
	for _, r := range recs {
		wg.Add(1)
		go func(rec *Recorder) {
			defer wg.Done()
			rec.Stop()
			rec.cdp.Close()
		}(r)
	}
	wg.Wait()

	f.Recording.closeWSLog()
	return f.Recording.Snapshot()
}
