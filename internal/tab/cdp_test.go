package tab

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

type fakeCDPServer struct {
	server  *httptest.Server
	handler func(conn *websocket.Conn, msg map[string]any)

	mu       sync.Mutex
	received []map[string]any
	conns    []*websocket.Conn
	writeMu  sync.Mutex
}

func newFakeCDP(t *testing.T) *fakeCDPServer {
	t.Helper()
	f := &fakeCDPServer{}
	up := websocket.Upgrader{
		ReadBufferSize:  1 << 20,
		WriteBufferSize: 1 << 20,
		CheckOrigin:     func(*http.Request) bool { return true },
	}

	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		conn.SetReadLimit(0)
		f.mu.Lock()
		f.conns = append(f.conns, conn)
		f.mu.Unlock()

		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			var msg map[string]any
			if json.Unmarshal(data, &msg) != nil {
				continue
			}
			f.mu.Lock()
			f.received = append(f.received, msg)
			h := f.handler
			f.mu.Unlock()
			if h != nil {
				h(conn, msg)
			}
		}
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeCDPServer) wsURL() string {
	return "ws" + strings.TrimPrefix(f.server.URL, "http")
}

func (f *fakeCDPServer) send(conn *websocket.Conn, v any) {
	f.writeMu.Lock()
	defer f.writeMu.Unlock()
	conn.WriteJSON(v)
}

func (f *fakeCDPServer) broadcast(v any) {
	f.mu.Lock()
	conns := append([]*websocket.Conn(nil), f.conns...)
	f.mu.Unlock()
	for _, c := range conns {
		f.send(c, v)
	}
}

func (f *fakeCDPServer) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.received)
}

func echoResults(f *fakeCDPServer) {
	f.handler = func(conn *websocket.Conn, msg map[string]any) {
		id, ok := msg["id"]
		if !ok {
			return
		}
		f.send(conn, map[string]any{
			"id":     id,
			"result": map[string]any{"echo": msg["method"]},
		})
	}
}

func dialFake(t *testing.T, f *fakeCDPServer) *CDP {
	t.Helper()
	c, err := Dial(f.wsURL())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func TestSendReceivesResult(t *testing.T) {
	f := newFakeCDP(t)
	echoResults(f)
	c := dialFake(t, f)

	var res struct {
		Echo string `json:"echo"`
	}
	if err := c.Send("Page.enable", map[string]any{"x": 1}, &res); err != nil {
		t.Fatal(err)
	}
	if res.Echo != "Page.enable" {
		t.Errorf("echo = %q", res.Echo)
	}
}

func TestSendPropagatesProtocolError(t *testing.T) {
	f := newFakeCDP(t)
	f.handler = func(conn *websocket.Conn, msg map[string]any) {
		f.send(conn, map[string]any{
			"id":    msg["id"],
			"error": map[string]any{"code": -32000, "message": "Target closed"},
		})
	}
	c := dialFake(t, f)

	err := c.Send("Page.enable", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "Target closed") ||
		!strings.Contains(err.Error(), "-32000") {
		t.Errorf("err = %v", err)
	}
}

func TestConcurrentSendsCorrelateIndependently(t *testing.T) {
	f := newFakeCDP(t)
	f.handler = func(conn *websocket.Conn, msg map[string]any) {
		method, _ := msg["method"].(string)
		delay := time.Duration(0)
		if method == "Slow" {
			delay = 150 * time.Millisecond
		}
		go func() {
			time.Sleep(delay)
			f.send(conn, map[string]any{"id": msg["id"], "result": map[string]any{"echo": method}})
		}()
	}
	c := dialFake(t, f)

	var wg sync.WaitGroup
	errs := make(chan error, 32)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		method := "Fast"
		if i%4 == 0 {
			method = "Slow"
		}
		go func(m string) {
			defer wg.Done()
			var res struct {
				Echo string `json:"echo"`
			}
			if err := c.Send(m, nil, &res); err != nil {
				errs <- err
				return
			}
			if res.Echo != m {
				errs <- fmt.Errorf("got reply for %q while calling %q", res.Echo, m)
			}
		}(method)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestEventsDispatchToHandlers(t *testing.T) {
	f := newFakeCDP(t)
	echoResults(f)
	c := dialFake(t, f)

	got := make(chan string, 4)
	c.On("Page.screencastFrame", func(p json.RawMessage) {
		var v struct {
			Data string `json:"data"`
		}
		json.Unmarshal(p, &v)
		got <- v.Data
	})
	c.On("Page.screencastFrame", func(p json.RawMessage) { got <- "second" })
	c.On("Other.event", func(p json.RawMessage) { got <- "WRONG" })

	c.Send("ping", nil, nil)
	f.broadcast(map[string]any{
		"method": "Page.screencastFrame",
		"params": map[string]any{"data": "abc"},
	})

	seen := map[string]bool{}
	for i := 0; i < 2; i++ {
		select {
		case v := <-got:
			seen[v] = true
		case <-time.After(3 * time.Second):
			t.Fatal("timed out waiting for event handlers")
		}
	}
	if !seen["abc"] || !seen["second"] {
		t.Errorf("handlers fired: %v", seen)
	}
}

func TestLargeMessagesSurviveIntact(t *testing.T) {
	f := newFakeCDP(t)
	big := strings.Repeat("a", 900_000)
	f.handler = func(conn *websocket.Conn, msg map[string]any) {
		f.send(conn, map[string]any{
			"id":     msg["id"],
			"result": map[string]any{"data": big},
		})
	}
	c := dialFake(t, f)

	var res struct {
		Data string `json:"data"`
	}
	if err := c.Send("Page.captureScreenshot", nil, &res); err != nil {
		t.Fatal(err)
	}
	if len(res.Data) != len(big) {
		t.Errorf("received %d bytes, sent %d", len(res.Data), len(big))
	}
}

func TestLargeOutboundMessageIsAccepted(t *testing.T) {
	f := newFakeCDP(t)
	echoResults(f)
	c := dialFake(t, f)

	if err := c.Send("Page.addScriptToEvaluateOnNewDocument",
		map[string]any{"source": strings.Repeat("x", 500_000)}, nil); err != nil {
		t.Fatal(err)
	}
}

func TestSendAsyncDoesNotWaitForAReply(t *testing.T) {
	f := newFakeCDP(t)
	f.handler = func(conn *websocket.Conn, msg map[string]any) {}
	c := dialFake(t, f)

	done := make(chan struct{})
	go func() {
		for i := 0; i < 50; i++ {
			c.SendAsync("Page.screencastFrameAck", map[string]any{"sessionId": i})
		}
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("SendAsync blocked")
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && f.count() < 50 {
		time.Sleep(20 * time.Millisecond)
	}
	if f.count() < 50 {
		t.Errorf("server saw %d of 50 acks", f.count())
	}
}

func TestPendingCallsFailWhenConnectionDrops(t *testing.T) {
	f := newFakeCDP(t)
	f.handler = func(conn *websocket.Conn, msg map[string]any) {
		conn.Close()
	}
	c := dialFake(t, f)

	done := make(chan error, 1)
	go func() { done <- c.Send("Page.enable", nil, nil) }()

	select {
	case err := <-done:
		if err == nil {
			t.Error("expected an error when the connection drops")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Send hung after the connection dropped")
	}
}

func TestSendAfterCloseIsRefused(t *testing.T) {
	f := newFakeCDP(t)
	echoResults(f)
	c := dialFake(t, f)

	if err := c.Send("Page.enable", nil, nil); err != nil {
		t.Fatal(err)
	}
	c.Close()

	if err := c.Send("Page.enable", nil, nil); err == nil {
		t.Error("expected a refusal after Close")
	}
	c.Close()
	c.SendAsync("Page.screencastFrameAck", nil)
}

func TestHandlerPanicDoesNotKillTheEventPump(t *testing.T) {
	f := newFakeCDP(t)
	echoResults(f)
	c := dialFake(t, f)

	survived := make(chan struct{}, 1)
	c.On("Boom", func(p json.RawMessage) { panic("handler exploded") })
	c.On("After", func(p json.RawMessage) { survived <- struct{}{} })

	c.Send("ping", nil, nil)
	f.broadcast(map[string]any{"method": "Boom"})
	time.Sleep(100 * time.Millisecond)
	f.broadcast(map[string]any{"method": "After"})

	select {
	case <-survived:
	case <-time.After(3 * time.Second):
		t.Fatal("event pump died after a handler panicked")
	}
}

func TestDialRejectsBadEndpoint(t *testing.T) {
	if _, err := Dial("ws://127.0.0.1:1/devtools/page/x"); err == nil {
		t.Error("expected a dial failure")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	if _, err := Dial("ws" + strings.TrimPrefix(srv.URL, "http")); err == nil {
		t.Error("expected a handshake failure against a non-websocket server")
	}
}

func TestListTargetsRejectsBadPort(t *testing.T) {
	if _, err := ListTargets(1); err == nil {
		t.Error("expected an error listing targets on a dead port")
	}
}
