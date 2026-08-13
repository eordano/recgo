package tab

import (
	"sort"
	"strings"
	"sync"
	"time"
)

type TabInfo struct {
	ID       string
	URL      string
	Title    string
	Attached bool
	Visible  bool
	Clicks   int
}

func (t TabInfo) Short(width int) string {
	label := strings.TrimSpace(t.Title)
	if label == "" {
		label = strings.TrimPrefix(strings.TrimPrefix(t.URL, "https://"), "http://")
	}
	if width > 3 && len(label) > width {
		label = label[:width-1] + "…"
	}
	return label
}

func IsRecordable(t Target) bool {
	if t.Type != "page" || t.WebSocketDebuggerURL == "" {
		return false
	}
	switch {
	case strings.HasPrefix(t.URL, "devtools://"),
		strings.HasPrefix(t.URL, "chrome://"),
		strings.HasPrefix(t.URL, "chrome-extension://"),
		strings.HasPrefix(t.URL, "edge://"),
		t.URL == "":
		return false
	}
	return true
}

func ListRecordableTabs(port int) ([]TabInfo, error) {
	targets, err := ListTargets(port)
	if err != nil {
		return nil, err
	}
	out := make([]TabInfo, 0, len(targets))
	for _, t := range targets {
		if !IsRecordable(t) {
			continue
		}
		out = append(out, TabInfo{ID: t.ID, URL: t.URL, Title: t.Title})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

type TabWatcher struct {
	Port     int
	Interval time.Duration

	mu      sync.Mutex
	known   map[string]TabInfo
	stop    chan struct{}
	stopped bool
}

type TabEvent struct {
	Appeared []TabInfo
	Vanished []string
	Err      error
	Snapshot []TabInfo
}

func NewTabWatcher(port int) *TabWatcher {
	return &TabWatcher{Port: port, Interval: time.Second, known: map[string]TabInfo{}}
}

func (w *TabWatcher) Watch() <-chan TabEvent {
	if w.Interval <= 0 {
		w.Interval = time.Second
	}
	w.mu.Lock()
	w.stop = make(chan struct{})
	stop := w.stop
	w.mu.Unlock()

	ch := make(chan TabEvent, 4)
	go func() {
		defer close(ch)
		tick := time.NewTicker(w.Interval)
		defer tick.Stop()

		emit := func() {
			tabs, err := ListRecordableTabs(w.Port)
			if err != nil {
				select {
				case ch <- TabEvent{Err: err}:
				default:
				}
				return
			}

			w.mu.Lock()
			var appeared []TabInfo
			var vanished []string
			seen := map[string]bool{}
			for _, t := range tabs {
				seen[t.ID] = true
				if _, ok := w.known[t.ID]; !ok {
					appeared = append(appeared, t)
				}
				w.known[t.ID] = t
			}
			for id := range w.known {
				if !seen[id] {
					vanished = append(vanished, id)
					delete(w.known, id)
				}
			}
			w.mu.Unlock()

			if len(appeared) == 0 && len(vanished) == 0 {
				return
			}
			select {
			case ch <- TabEvent{Appeared: appeared, Vanished: vanished, Snapshot: tabs}:
			default:
			}
		}

		emit()
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
				emit()
			}
		}
	}()
	return ch
}

func (w *TabWatcher) Stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopped || w.stop == nil {
		return
	}
	w.stopped = true
	close(w.stop)
}
