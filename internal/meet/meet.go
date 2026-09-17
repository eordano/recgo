// Package meet observes joined Google Meet calls through a local Chromium CDP
// endpoint. It never records or prompts; the desktop shell owns those actions.
package meet

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

//go:embed detect.js
var Expression string

var codePath = regexp.MustCompile(`^/[a-z]{3}-[a-z]{4}-[a-z]{3}/?$`)

func IsMeet(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Host == "meet.google.com" && codePath.MatchString(u.Path)
}

type Call struct {
	ID      string `json:"id"`
	URL     string `json:"url"`
	Present bool   `json:"present"`
	// Title is a recording name to offer for this call: the meeting's name
	// from the tab title when Meet shows one, else meet-<room code>.
	Title string `json:"title,omitempty"`
}

type Snapshot struct {
	Available bool   `json:"available"`
	Calls     []Call `json:"calls"`
}

type target struct {
	ID    string `json:"id"`
	Type  string `json:"type"`
	URL   string `json:"url"`
	Title string `json:"title"`
	WS    string `json:"webSocketDebuggerUrl"`
}

var meetTitleNoise = regexp.MustCompile(`(?i)^\s*(meet|google meet)\s*[-–—:|]\s*|\s*[-–—:|]\s*(google )?meet\s*$`)

// SuggestTitle turns a Meet tab title ("Meet – Weekly sync", "Weekly sync -
// Google Meet", or just "Meet – abc-defg-hij") into a recording name the
// way recgo <name> wants it, falling back to meet-<room code>.
func SuggestTitle(tabTitle, canonical string) string {
	code := strings.TrimPrefix(strings.TrimPrefix(canonical, "https://meet.google.com"), "/")
	name := tabTitle
	for i := 0; i < 2; i++ {
		name = meetTitleNoise.ReplaceAllString(name, "")
	}
	name = strings.TrimSpace(name)
	if name == "" || strings.EqualFold(name, code) || strings.EqualFold(name, "google meet") {
		return "meet-" + code
	}
	return name
}

// Probe keeps unknown separate from absent: a debugger outage must not end a
// recording. Only page targets on the exact Meet origin are inspected.
func Probe(ctx context.Context, port int) ([]Call, error) {
	endpoint := fmt.Sprintf("127.0.0.1:%d", port)
	req, err := http.NewRequestWithContext(ctx, "GET", "http://"+endpoint+"/json/list", nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("CDP returned %d", res.StatusCode)
	}
	var targets []target
	if err := json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&targets); err != nil {
		return nil, err
	}
	calls := []Call{}
	for _, t := range targets {
		if t.Type != "page" || !IsMeet(t.URL) {
			continue
		}
		// Do not follow a debugger URL off this loopback endpoint.
		u, err := url.Parse(t.WS)
		if err != nil || u.Scheme != "ws" || u.User != nil || u.Host != endpoint {
			return nil, fmt.Errorf("unexpected debugger endpoint")
		}
		joined, err := evaluate(ctx, t.WS)
		if err != nil {
			return nil, err
		}
		if joined {
			// Include the room in the identity: navigating one tab to a different
			// meeting needs fresh consent. Query string changes do not.
			room, _ := url.Parse(t.URL)
			canonical := "https://meet.google.com" + strings.TrimSuffix(room.Path, "/")
			calls = append(calls, Call{ID: t.ID + ":" + canonical, URL: canonical, Present: true,
				Title: SuggestTitle(t.Title, canonical)})
		}
	}
	return calls, nil
}

func evaluate(ctx context.Context, endpoint string) (bool, error) {
	d := websocket.Dialer{HandshakeTimeout: 2 * time.Second, NetDialContext: (&net.Dialer{Timeout: 2 * time.Second}).DialContext}
	c, _, err := d.DialContext(ctx, endpoint, nil)
	if err != nil {
		return false, err
	}
	defer c.Close()
	c.SetReadLimit(64 << 10)
	deadline := time.Now().Add(2 * time.Second)
	if end, ok := ctx.Deadline(); ok && end.Before(deadline) {
		deadline = end
	}
	c.SetReadDeadline(deadline)
	c.SetWriteDeadline(deadline)
	err = c.WriteJSON(map[string]any{"id": 1, "method": "Runtime.evaluate", "params": map[string]any{"expression": Expression, "returnByValue": true, "silent": true, "timeout": 1000}})
	if err != nil {
		return false, err
	}
	for {
		var msg struct {
			ID     int             `json:"id"`
			Error  json.RawMessage `json:"error"`
			Result struct {
				Result struct {
					Value *bool `json:"value"`
				} `json:"result"`
				Exception json.RawMessage `json:"exceptionDetails"`
			} `json:"result"`
		}
		if err := c.ReadJSON(&msg); err != nil {
			return false, err
		}
		if msg.ID != 1 {
			continue
		}
		if len(msg.Error) > 0 || len(msg.Result.Exception) > 0 || msg.Result.Result.Value == nil {
			return false, fmt.Errorf("Meet probe did not return a boolean")
		}
		return *msg.Result.Result.Value, nil
	}
}

type observation struct {
	call   Call
	since  time.Time
	absent time.Time
	active bool
}

// Tracker requires two seconds of presence and ten seconds of confirmed
// absence. Unknown observations break the debounce, retaining active calls.
type Tracker struct{ calls map[string]*observation }

func (t *Tracker) Update(now time.Time, calls []Call, available bool) Snapshot {
	if t.calls == nil {
		t.calls = map[string]*observation{}
	}
	seen := map[string]bool{}
	if available {
		for _, c := range calls {
			seen[c.ID] = true
			o := t.calls[c.ID]
			if o == nil {
				o = &observation{since: now}
				t.calls[c.ID] = o
			}
			o.call = c
			o.call.Present = true
			o.absent = time.Time{}
			if now.Sub(o.since) >= 2*time.Second {
				o.active = true
			}
		}
	}
	for id, o := range t.calls {
		if !available || !seen[id] {
			o.call.Present = false
			if !o.active {
				delete(t.calls, id)
				continue
			}
			if !available {
				o.absent = time.Time{}
				continue
			}
			if o.absent.IsZero() {
				o.absent = now
			}
			if now.Sub(o.absent) >= 10*time.Second {
				delete(t.calls, id)
			}
		}
	}
	s := Snapshot{Available: available, Calls: []Call{}}
	for _, o := range t.calls {
		if o.active {
			s.Calls = append(s.Calls, o.call)
		}
	}
	sort.Slice(s.Calls, func(i, j int) bool { return s.Calls[i].ID < s.Calls[j].ID })
	return s
}
