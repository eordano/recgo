package tab

import (
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

//go:embed inject.js
var injectSource string

const ringWindowMs = 20_000

const frameGraceMs = 300

var shotOffsets = []struct {
	Label  string
	Offset float64
}{
	{"before", -100},
	{"at", 0},
	{"after", 100},
}

type Shot struct {
	Label      string   `json:"label"`
	OffsetMs   float64  `json:"offsetMs"`
	TargetT    float64  `json:"targetT"`
	Captured   bool     `json:"captured"`
	FrameT     *float64 `json:"frameT"`
	StaleMs    *float64 `json:"staleMs"`
	Definitive bool     `json:"definitive"`
	File       string   `json:"file,omitempty"`
	Note       string   `json:"note,omitempty"`
}

type Element struct {
	Selector  string `json:"selector"`
	Tag       string `json:"tag"`
	ID        string `json:"id,omitempty"`
	Classes   string `json:"classes,omitempty"`
	TestID    string `json:"testid,omitempty"`
	Role      string `json:"role,omitempty"`
	AriaLabel string `json:"ariaLabel,omitempty"`
	Text      string `json:"text,omitempty"`
	Href      string `json:"href,omitempty"`
}

type Event struct {
	T    float64 `json:"t"`
	Kind string  `json:"kind"`

	Seq       int      `json:"seq,omitempty"`
	URL       string   `json:"url,omitempty"`
	Title     string   `json:"title,omitempty"`
	X         float64  `json:"x,omitempty"`
	Y         float64  `json:"y,omitempty"`
	Elem      *Element `json:"element,omitempty"`
	Shots     []Shot   `json:"shots,omitempty"`
	FullShot  string   `json:"fullShot,omitempty"`
	FullLagMs float64  `json:"fullShotLatencyMs,omitempty"`

	Level string `json:"level,omitempty"`
	Text  string `json:"text,omitempty"`
	Stack string `json:"stack,omitempty"`

	Status    int    `json:"status,omitempty"`
	ErrorText string `json:"errorText,omitempty"`

	Flavor    string   `json:"flavor,omitempty"`
	Type      string   `json:"type,omitempty"`
	Files     []string `json:"files,omitempty"`
	Direction string   `json:"direction,omitempty"`

	Note string `json:"note,omitempty"`

	TargetID string `json:"targetId,omitempty"`
}

type frame struct {
	t    float64
	data []byte
}

type Recorder struct {
	cdp     *CDP
	session *Recording
	clock   *Clock
	outDir  string

	targetID  string
	targetURL string

	mu      sync.Mutex
	visible bool
	ring    []frame
	wsURLs  map[string]string
	stopped bool

	frameSubs map[int]chan struct{}
	nextSub   int

	pending sync.WaitGroup
}

func NewRecorder(cdp *CDP, clock *Clock, outDir string) *Recorder {
	return NewSessionRecorder(cdp, NewRecording(clock, outDir), "", "")
}

func NewSessionRecorder(cdp *CDP, s *Recording, targetID, targetURL string) *Recorder {
	return &Recorder{
		cdp:       cdp,
		session:   s,
		clock:     s.Clock,
		outDir:    s.OutDir,
		targetID:  targetID,
		targetURL: targetURL,
		wsURLs:    map[string]string{},
		frameSubs: map[int]chan struct{}{},
	}
}

func (r *Recorder) Recording() *Recording { return r.session }

func (r *Recorder) push(e Event) *Event {
	if e.TargetID == "" {
		e.TargetID = r.targetID
	}
	return r.session.Push(e)
}

func (r *Recorder) Events() []Event { return r.session.Snapshot() }

func sortEventsByTime(out []Event) {
	sort.SliceStable(out, func(i, j int) bool { return out[i].T < out[j].T })
}

func (r *Recorder) Start() (offsetMs, errorMs float64, err error) {
	for _, d := range []string{"Runtime.enable", "Page.enable", "Network.enable", "Log.enable"} {
		if err := r.cdp.Send(d, nil, nil); err != nil {
			return 0, 0, fmt.Errorf("%s: %w", d, err)
		}
	}

	if err := r.cdp.Send("Runtime.addBinding", map[string]any{"name": "__rtEmit"}, nil); err != nil {
		return 0, 0, err
	}
	if err := r.cdp.Send("Page.addScriptToEvaluateOnNewDocument",
		map[string]any{"source": injectSource}, nil); err != nil {
		return 0, 0, err
	}
	if err := r.cdp.Send("Runtime.evaluate", map[string]any{"expression": injectSource}, nil); err != nil {
		return 0, 0, err
	}

	r.registerHandlers()

	if r.clock.Calibrated() {
		offsetMs, errorMs = r.clock.Offsets()
	} else {
		offsetMs, errorMs, err = r.clock.CalibrateToBrowser(r.cdp, 12)
		if err != nil {
			return 0, 0, err
		}
	}

	r.cdp.On("Page.screencastFrame", r.onScreencastFrame)
	if err := r.cdp.Send("Page.startScreencast", map[string]any{
		"format": "png", "maxWidth": 2560, "maxHeight": 1600, "everyNthFrame": 1,
	}, nil); err != nil {
		return 0, 0, err
	}

	start := r.push(Event{T: 0, Kind: "record-start"})
	if r.session.ClaimInitialShot() {
		if data, err := r.captureFullShot(); err == nil {
			if os.WriteFile(filepath.Join(r.outDir, InitialShot), data, sessionFileMode) == nil {
				r.session.Update(start, func(e *Event) { e.FullShot = InitialShot })
			}
		}
	}
	return offsetMs, errorMs, nil
}

func (r *Recorder) captureFullShot() ([]byte, error) {
	var res struct {
		Data string `json:"data"`
	}
	if err := r.cdp.Send("Page.captureScreenshot",
		map[string]any{"format": "png", "captureBeyondViewport": false}, &res); err != nil {
		return nil, err
	}
	return base64.StdEncoding.DecodeString(res.Data)
}

func (r *Recorder) registerHandlers() {
	r.cdp.On("Runtime.bindingCalled", func(p json.RawMessage) {
		var v struct {
			Name    string `json:"name"`
			Payload string `json:"payload"`
		}
		if json.Unmarshal(p, &v) != nil || v.Name != "__rtEmit" {
			return
		}
		var msg struct {
			Kind        string   `json:"kind"`
			TimeOrigin  float64  `json:"timeOrigin"`
			PageTime    float64  `json:"pageTime"`
			URL         string   `json:"url"`
			Title       string   `json:"title"`
			X           float64  `json:"x"`
			Y           float64  `json:"y"`
			Visible     bool     `json:"visible"`
			Target      *Element `json:"target"`
			Interactive *Element `json:"interactive"`
		}
		if json.Unmarshal([]byte(v.Payload), &msg) != nil {
			return
		}
		t, ok := r.clock.FromPageTime(msg.TimeOrigin, msg.PageTime)
		if !ok {
			return
		}
		switch msg.Kind {
		case "click":
			el := msg.Interactive
			if el == nil {
				el = msg.Target
			}
			r.onClick(t, msg.URL, msg.Title, msg.X, msg.Y, el)
		case "navigation":
			r.push(Event{T: t, Kind: "navigation", URL: msg.URL})
		case "visibility":
			r.onVisibility(msg.Visible, msg.URL, msg.Title)
		}
	})

	r.cdp.On("Runtime.consoleAPICalled", func(p json.RawMessage) {
		var v struct {
			Type       string         `json:"type"`
			Timestamp  float64        `json:"timestamp"`
			Args       []remoteObject `json:"args"`
			StackTrace *cdpStackTrace `json:"stackTrace"`
		}
		if json.Unmarshal(p, &v) != nil {
			return
		}
		if t, ok := r.clock.FromBrowserWall(v.Timestamp); ok {
			r.push(Event{T: t, Kind: "console", Level: v.Type,
				Text: renderArgs(v.Args), Stack: topFrame(v.StackTrace)})
		}
	})

	r.cdp.On("Runtime.exceptionThrown", func(p json.RawMessage) {
		var v struct {
			Timestamp        float64 `json:"timestamp"`
			ExceptionDetails struct {
				Text       string         `json:"text"`
				Exception  *remoteObject  `json:"exception"`
				StackTrace *cdpStackTrace `json:"stackTrace"`
			} `json:"exceptionDetails"`
		}
		if json.Unmarshal(p, &v) != nil {
			return
		}
		text := v.ExceptionDetails.Text
		if v.ExceptionDetails.Exception != nil && v.ExceptionDetails.Exception.Description != "" {
			text = v.ExceptionDetails.Exception.Description
		}
		if t, ok := r.clock.FromBrowserWall(v.Timestamp); ok {
			r.push(Event{T: t, Kind: "exception", Text: text,
				Stack: topFrame(v.ExceptionDetails.StackTrace)})
		}
	})

	r.cdp.On("Log.entryAdded", func(p json.RawMessage) {
		var v struct {
			Entry struct {
				Level     string  `json:"level"`
				Source    string  `json:"source"`
				Text      string  `json:"text"`
				URL       string  `json:"url"`
				Timestamp float64 `json:"timestamp"`
			} `json:"entry"`
		}
		if json.Unmarshal(p, &v) != nil {
			return
		}
		if v.Entry.Level != "error" && v.Entry.Level != "warning" {
			return
		}
		if t, ok := r.clock.FromBrowserWall(v.Entry.Timestamp); ok {
			r.push(Event{T: t, Kind: "browser-log", Level: v.Entry.Level,
				Text: v.Entry.Text, URL: v.Entry.URL})
		}
	})

	r.cdp.On("Network.requestWillBeSent", func(p json.RawMessage) {
		var v struct {
			Timestamp float64 `json:"timestamp"`
			WallTime  float64 `json:"wallTime"`
		}
		if json.Unmarshal(p, &v) == nil {
			r.clock.LearnMonotonicEpoch(v.Timestamp, v.WallTime)
		}
	})

	r.cdp.On("Network.responseReceived", func(p json.RawMessage) {
		var v struct {
			Timestamp float64 `json:"timestamp"`
			Response  struct {
				Status int    `json:"status"`
				URL    string `json:"url"`
			} `json:"response"`
		}
		if json.Unmarshal(p, &v) != nil || v.Response.Status < 400 {
			return
		}
		if t, ok := r.clock.FromCDPMonotonic(v.Timestamp); ok {
			r.push(Event{T: t, Kind: "network-error", Status: v.Response.Status, URL: v.Response.URL})
		}
	})

	r.cdp.On("Network.loadingFailed", func(p json.RawMessage) {
		var v struct {
			Timestamp float64 `json:"timestamp"`
			ErrorText string  `json:"errorText"`
			Canceled  bool    `json:"canceled"`
			RequestID string  `json:"requestId"`
		}
		if json.Unmarshal(p, &v) != nil || v.Canceled {
			return
		}
		if t, ok := r.clock.FromCDPMonotonic(v.Timestamp); ok {
			r.mu.Lock()
			u := r.wsURLs[v.RequestID]
			r.mu.Unlock()
			r.push(Event{T: t, Kind: "network-error", ErrorText: v.ErrorText, URL: u})
		}
	})

	r.cdp.On("Network.webSocketCreated", func(p json.RawMessage) {
		var v struct {
			RequestID string `json:"requestId"`
			URL       string `json:"url"`
		}
		if json.Unmarshal(p, &v) != nil {
			return
		}
		r.mu.Lock()
		r.wsURLs[v.RequestID] = v.URL
		r.mu.Unlock()
		r.push(Event{T: r.clock.Now(), Kind: "ws-open", URL: v.URL})
	})

	r.cdp.On("Network.webSocketFrameReceived", r.wsFrameHandler("received"))
	r.cdp.On("Network.webSocketFrameSent", r.wsFrameHandler("sent"))
}

func (r *Recorder) wsFrameHandler(direction string) EventHandler {
	return func(p json.RawMessage) {
		var v struct {
			RequestID string  `json:"requestId"`
			Timestamp float64 `json:"timestamp"`
			Response  struct {
				PayloadData string `json:"payloadData"`
			} `json:"response"`
		}
		if json.Unmarshal(p, &v) != nil {
			return
		}
		t, ok := r.clock.FromCDPMonotonic(v.Timestamp)
		if !ok {
			t = r.clock.Now()
		}
		r.mu.Lock()
		url := r.wsURLs[v.RequestID]
		r.mu.Unlock()

		r.session.WriteWSFrame(map[string]any{
			"t": t, "direction": direction, "url": url,
			"targetId": r.targetID, "payload": v.Response.PayloadData,
		})

		if h := ClassifyHMR(v.Response.PayloadData); h != nil {
			r.push(Event{T: t, Kind: "hmr", Direction: direction, URL: url,
				Flavor: h.Flavor, Type: h.Type, Files: h.Files})
		}
	}
}

func (r *Recorder) onScreencastFrame(p json.RawMessage) {
	var v struct {
		Data      string `json:"data"`
		SessionID int    `json:"sessionId"`
		Metadata  struct {
			Timestamp float64 `json:"timestamp"`
		} `json:"metadata"`
	}
	if json.Unmarshal(p, &v) != nil {
		return
	}
	r.cdp.SendAsync("Page.screencastFrameAck", map[string]any{"sessionId": v.SessionID})

	t, ok := r.clock.FromCDPEpochSeconds(v.Metadata.Timestamp)
	if !ok || v.Metadata.Timestamp == 0 {
		t = r.clock.Now()
	}
	data, err := base64.StdEncoding.DecodeString(v.Data)
	if err != nil {
		return
	}

	r.mu.Lock()
	r.ring = append(r.ring, frame{t: t, data: data})
	cutoff := r.clock.Now() - ringWindowMs
	i := 0
	for i < len(r.ring) && r.ring[i].t < cutoff {
		i++
	}
	r.ring = r.ring[i:]
	subs := make([]chan struct{}, 0, len(r.frameSubs))
	for _, ch := range r.frameSubs {
		subs = append(subs, ch)
	}
	r.mu.Unlock()

	for _, ch := range subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (r *Recorder) frameAt(targetT float64) (f *frame, definitive bool, staleMs *float64) {
	r.mu.Lock()
	id := r.nextSub
	r.nextSub++
	ch := make(chan struct{}, 1)
	r.frameSubs[id] = ch
	haveLater := r.hasLaterLocked(targetT)
	r.mu.Unlock()

	defer func() {
		r.mu.Lock()
		delete(r.frameSubs, id)
		r.mu.Unlock()
	}()

	if !haveLater {
		wait := targetT - r.clock.Now() + frameGraceMs
		if wait < 0 {
			wait = frameGraceMs
		}
		deadline := time.After(time.Duration(wait) * time.Millisecond)
	loop:
		for {
			select {
			case <-ch:
				r.mu.Lock()
				ok := r.hasLaterLocked(targetT)
				r.mu.Unlock()
				if ok {
					haveLater = true
					break loop
				}
			case <-deadline:
				break loop
			}
		}
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	var chosen *frame
	for i := range r.ring {
		if r.ring[i].t <= targetT && (chosen == nil || r.ring[i].t > chosen.t) {
			chosen = &r.ring[i]
		}
	}
	if chosen == nil {
		return nil, false, nil
	}
	stale := targetT - chosen.t
	cp := *chosen
	return &cp, haveLater, &stale
}

func (r *Recorder) hasLaterLocked(targetT float64) bool {
	for i := range r.ring {
		if r.ring[i].t > targetT {
			return true
		}
	}
	return false
}

func (r *Recorder) onClick(t float64, url, title string, x, y float64, el *Element) {
	seq := r.session.NextClickSeq()
	ev := r.push(Event{
		T: t, Kind: "click", Seq: seq, URL: url, Title: title, X: x, Y: y, Elem: el,
		Shots: make([]Shot, 0, 3),
	})

	r.pending.Add(1)
	go func() {
		defer r.pending.Done()
		req := r.clock.Now()
		data, err := r.captureFullShot()
		if err != nil {
			return
		}
		name := ShotName(seq, "full")
		if os.WriteFile(filepath.Join(r.outDir, name), data, sessionFileMode) == nil {
			lag := r.clock.Now() - req
			r.session.Update(ev, func(e *Event) {
				e.FullShot = name
				e.FullLagMs = lag
			})
		}
	}()

	for _, so := range shotOffsets {
		so := so
		r.pending.Add(1)
		go func() {
			defer r.pending.Done()
			target := t + so.Offset
			f, definitive, stale := r.frameAt(target)

			shot := Shot{
				Label: so.Label, OffsetMs: so.Offset, TargetT: target,
				Captured: f != nil, Definitive: definitive, StaleMs: stale,
			}
			if f != nil {
				ft := f.t
				shot.FrameT = &ft
				name := ShotName(seq, so.Label)
				if os.WriteFile(filepath.Join(r.outDir, name), f.data, sessionFileMode) == nil {
					shot.File = name
				}
			} else {
				shot.Note = "no screencast frame at or before this instant"
			}

			r.session.Update(ev, func(e *Event) {
				e.Shots = append(e.Shots, shot)
			})
		}()
	}
}

func (r *Recorder) onVisibility(visible bool, url, title string) {
	r.mu.Lock()
	r.visible = visible
	r.targetURL = url
	r.mu.Unlock()

	if visible {
		r.session.SetActiveTarget(r.targetID, url, title)
	}
}

func (r *Recorder) Visible() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.visible
}

func (r *Recorder) TargetID() string { return r.targetID }

func (r *Recorder) URL() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.targetURL
}

func (r *Recorder) Mark(note string) float64 {
	t := r.clock.Now()
	r.push(Event{T: t, Kind: "mark", Note: note})
	return t
}

func (r *Recorder) Stop() []Event {
	r.mu.Lock()
	if r.stopped {
		r.mu.Unlock()
		return r.Events()
	}
	r.stopped = true
	r.mu.Unlock()

	r.push(Event{T: r.clock.Now(), Kind: "record-stop"})
	r.cdp.Send("Page.stopScreencast", nil, nil)

	r.pending.Wait()

	return r.Events()
}
