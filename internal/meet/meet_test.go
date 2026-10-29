package meet

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

func TestMeetOrigin(t *testing.T) {
	for _, raw := range []string{"https://meet.google.com/abc-defg-hij", "https://meet.google.com/abc-defg-hij/?authuser=1"} {
		if !IsMeet(raw) {
			t.Fatal(raw)
		}
	}
	for _, raw := range []string{"https://meet.google.com/", "https://meet.google.com/landing", "https://meet.google.com.evil/abc-defg-hij", "http://meet.google.com/abc-defg-hij", "https://example.com/?next=https://meet.google.com/abc-defg-hij"} {
		if IsMeet(raw) {
			t.Fatal(raw)
		}
	}
}

func TestDebounceReconnectAndUnknown(t *testing.T) {
	var tracker Tracker
	base := time.Now()
	call := []Call{{ID: "one", URL: "https://meet.google.com/abc-defg-hij", Present: true}}
	update := func(sec int, c []Call, ok bool) Snapshot {
		return tracker.Update(base.Add(time.Duration(sec)*time.Second), c, ok)
	}
	if len(update(0, call, true).Calls) != 0 {
		t.Fatal("prompted before stable join")
	}
	if len(update(2, call, true).Calls) != 1 {
		t.Fatal("missed join")
	}
	if s := update(4, nil, true); len(s.Calls) != 1 || s.Calls[0].Present {
		t.Fatal("grace must not permit acceptance", s)
	}
	if len(update(10, call, true).Calls) != 1 {
		t.Fatal("lost reconnect")
	}
	update(12, nil, true)
	if s := update(40, nil, false); s.Available || len(s.Calls) != 1 || s.Calls[0].Present {
		t.Fatal("outage lost ownership", s)
	}
	if len(update(42, nil, true).Calls) != 1 {
		t.Fatal("outage counted as absence")
	}
	if len(update(52, nil, true).Calls) != 0 {
		t.Fatal("did not leave")
	}
	if len(update(54, call, true).Calls) != 0 || len(update(56, call, true).Calls) != 1 {
		t.Fatal("same-room rejoin missing")
	}
}

func TestObserveQuitBrowserVersusOutage(t *testing.T) {
	base := time.Now()
	call := []Call{{ID: "one", URL: "https://meet.google.com/abc-defg-hij", Present: true}}
	joined := func() *Tracker {
		var tracker Tracker
		tracker.Observe(base, call, nil)
		if len(tracker.Observe(base.Add(2*time.Second), call, nil).Calls) != 1 {
			t.Fatal("missed join")
		}
		return &tracker
	}
	quit := joined()
	for _, sec := range []int{4, 13} {
		if s := quit.Observe(base.Add(time.Duration(sec)*time.Second), nil, ErrNoBrowser); !s.Available || s.Browser || len(s.Calls) != 1 {
			t.Fatal("a quit browser must debounce like leaving", sec, s)
		}
	}
	s := quit.Observe(base.Add(14*time.Second), nil, ErrNoBrowser)
	if b, _ := json.Marshal(s); string(b) != `{"available":true,"browser":false,"calls":[]}` {
		t.Fatal("a quit browser must end the call", string(b))
	}
	outage := joined()
	for _, sec := range []int{4, 40} {
		if s := outage.Observe(base.Add(time.Duration(sec)*time.Second), nil, errors.New("CDP returned 503")); s.Available || !s.Browser || len(s.Calls) != 1 {
			t.Fatal("an outage is unknown, not a quit browser", sec, s)
		}
	}
	for _, tc := range []struct {
		err  error
		want string
	}{
		{nil, `{"available":true,"browser":true,"calls":[]}`},
		{ErrNoBrowser, `{"available":true,"browser":false,"calls":[]}`},
		{errors.New("CDP returned 503"), `{"available":false,"browser":true,"calls":[]}`},
	} {
		if b, _ := json.Marshal(Classify(nil, tc.err)); string(b) != tc.want {
			t.Errorf("Classify(%v) = %s, want %s", tc.err, b, tc.want)
		}
	}
}

func TestInterruptedJoinAndMultipleCalls(t *testing.T) {
	var tracker Tracker
	base := time.Now()
	one := Call{ID: "one", Present: true}
	two := Call{ID: "two", Present: true}
	tracker.Update(base, []Call{one}, true)
	tracker.Update(base.Add(time.Second), nil, false)
	if len(tracker.Update(base.Add(10*time.Second), []Call{one, two}, true).Calls) != 0 {
		t.Fatal("unknown completed join")
	}
	if len(tracker.Update(base.Add(12*time.Second), []Call{one, two}, true).Calls) != 2 {
		t.Fatal("second call missing")
	}
	tracker.Update(base.Add(14*time.Second), []Call{two}, true)
	s := tracker.Update(base.Add(24*time.Second), []Call{two}, true)
	if len(s.Calls) != 1 || s.Calls[0].ID != "two" {
		t.Fatal(s)
	}
}

func TestProbeCDP(t *testing.T) {
	for _, tc := range []struct {
		name, response string
		want, fail     bool
	}{
		{"joined", `{"id":1,"result":{"result":{"type":"boolean","value":true}}}`, true, false},
		{"lobby", `{"id":1,"result":{"result":{"type":"boolean","value":false}}}`, false, false},
		{"navigation", `{"id":1,"error":{"message":"context destroyed"}}`, false, true},
		{"exception", `{"id":1,"result":{"result":{},"exceptionDetails":{}}}`, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/json/list" {
					json.NewEncoder(w).Encode([]target{{ID: "t", Type: "page", URL: "https://meet.google.com/abc-defg-hij?authuser=2", Title: "Meet – Weekly sync", WS: strings.Replace(server.URL, "http:", "ws:", 1) + "/devtools/page/t"}, {ID: "skip", Type: "page", URL: "https://example.com", WS: "ws://off-host/"}})
					return
				}
				c, err := websocket.Accept(w, r, nil)
				if err != nil {
					t.Error(err)
					return
				}
				defer c.CloseNow()
				c.SetReadLimit(-1)
				var request struct {
					Method string
					Params struct{ Expression string }
				}
				if err := wsjson.Read(r.Context(), c, &request); err != nil {
					t.Error(err)
					return
				}
				if request.Method != "Runtime.evaluate" || request.Params.Expression != Expression {
					t.Error("wrong evaluation")
				}
				wsjson.Write(r.Context(), c, map[string]any{"method": "Runtime.executionContextCreated"})
				c.Write(r.Context(), websocket.MessageText, []byte(tc.response))
			}))
			defer server.Close()
			u, _ := url.Parse(server.URL)
			port, _ := strconv.Atoi(u.Port())
			calls, err := Probe(context.Background(), port)
			if (err != nil) != tc.fail {
				t.Fatalf("err=%v", err)
			}
			if (len(calls) == 1) != tc.want {
				t.Fatal(calls)
			}
			if tc.want && (calls[0].URL != "https://meet.google.com/abc-defg-hij" || calls[0].ID != "t:https://meet.google.com/abc-defg-hij" || calls[0].Title != "Weekly sync") {
				t.Fatal(calls)
			}
		})
	}
}

func TestProbeQuitBrowserIsNotAnOutage(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows refuses with WSAECONNREFUSED; recgo-meet-watch ships only for Linux and macOS")
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := l.Addr().(*net.TCPAddr).Port
	l.Close()
	if _, err := Probe(context.Background(), port); !errors.Is(err, ErrNoBrowser) {
		t.Fatalf("err=%v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "busy", http.StatusServiceUnavailable)
	}))
	defer server.Close()
	u, _ := url.Parse(server.URL)
	port, _ = strconv.Atoi(u.Port())
	if _, err := Probe(context.Background(), port); err == nil || errors.Is(err, ErrNoBrowser) {
		t.Fatalf("an answering debugger is an outage, not a quit browser: %v", err)
	}
}

func TestSuggestTitle(t *testing.T) {
	const room = "https://meet.google.com/abc-defg-hij"
	for title, want := range map[string]string{
		"Meet – Weekly sync":        "Weekly sync",
		"Meet - Weekly sync":        "Weekly sync",
		"Weekly sync - Google Meet": "Weekly sync",
		"Meet – abc-defg-hij":       "meet-abc-defg-hij",
		"Google Meet":               "meet-abc-defg-hij",
		"":                          "meet-abc-defg-hij",
		"Design review":             "Design review",
	} {
		if got := SuggestTitle(title, room); got != want {
			t.Errorf("SuggestTitle(%q) = %q, want %q", title, got, want)
		}
	}
}
