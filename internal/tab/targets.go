package tab

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
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
		cut := width - 1
		for cut > 0 && !utf8.RuneStart(label[cut]) {
			cut--
		}
		label = label[:cut] + "…"
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

// MatchTab picks the recordable tab whose URL or title contains match, using
// the same substring rule Attach does so --match means one thing across the
// CLIs. An empty match takes the first recordable tab.
func MatchTab(port int, match string) (TabInfo, error) {
	tabs, err := ListRecordableTabs(port)
	if err != nil {
		return TabInfo{}, err
	}
	if len(tabs) == 0 {
		return TabInfo{}, fmt.Errorf(
			"no recordable tabs on port %d — is the browser running with --remote-debugging-port=%d?",
			port, port)
	}
	for _, t := range tabs {
		if match == "" || strings.Contains(t.URL, match) || strings.Contains(t.Title, match) {
			return t, nil
		}
	}
	return TabInfo{}, fmt.Errorf("no tab matching %q among the %d recordable tab(s) on port %d",
		match, len(tabs), port)
}
