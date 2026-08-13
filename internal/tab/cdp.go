package tab

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	cdpReadBuffer  = 256 << 10
	cdpWriteBuffer = 32 << 10
	cdpCallTimeout = 30 * time.Second
)

type Target struct {
	ID                   string `json:"id"`
	Type                 string `json:"type"`
	Title                string `json:"title"`
	URL                  string `json:"url"`
	WebSocketDebuggerURL string `json:"webSocketDebuggerUrl"`
}

type cdpMessage struct {
	ID     int             `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *cdpError       `json:"error,omitempty"`
}

type cdpError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e *cdpError) Error() string { return fmt.Sprintf("%s (%d)", e.Message, e.Code) }

type pending struct {
	result chan json.RawMessage
	err    chan error
}

type EventHandler func(params json.RawMessage)

type CDP struct {
	conn *websocket.Conn

	writeMu sync.Mutex

	mu       sync.Mutex
	nextID   int
	pending  map[int]pending
	handlers map[string][]EventHandler
	closed   bool
}

func ListTargets(port int) ([]Target, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	res, err := client.Get(fmt.Sprintf("http://127.0.0.1:%d/json/list", port))
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("/json/list returned %d", res.StatusCode)
	}
	body, err := io.ReadAll(res.Body)
	if err != nil {
		return nil, err
	}
	var targets []Target
	if err := json.Unmarshal(body, &targets); err != nil {
		return nil, err
	}
	return targets, nil
}

func Attach(port int, match string) (*CDP, *Target, error) {
	targets, err := ListTargets(port)
	if err != nil {
		return nil, nil, err
	}

	var chosen *Target
	for i := range targets {
		t := &targets[i]
		if t.Type != "page" || t.WebSocketDebuggerURL == "" {
			continue
		}
		if match == "" || strings.Contains(t.URL, match) || strings.Contains(t.Title, match) {
			chosen = t
			break
		}
	}
	if chosen == nil {
		return nil, nil, fmt.Errorf("no page target matching %q", match)
	}

	c, err := Dial(chosen.WebSocketDebuggerURL)
	if err != nil {
		return nil, nil, err
	}
	return c, chosen, nil
}

func Dial(wsURL string) (*CDP, error) {
	dialer := &websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
		ReadBufferSize:   cdpReadBuffer,
		WriteBufferSize:  cdpWriteBuffer,
	}

	conn, res, err := dialer.Dial(wsURL, nil)
	if err != nil {
		if res != nil {
			return nil, fmt.Errorf("dial %s: %w (http %d)", wsURL, err, res.StatusCode)
		}
		return nil, fmt.Errorf("dial %s: %w", wsURL, err)
	}
	conn.SetReadLimit(0)

	c := &CDP{
		conn:     conn,
		nextID:   1,
		pending:  map[int]pending{},
		handlers: map[string][]EventHandler{},
	}
	go c.readLoop()
	return c, nil
}

func (c *CDP) readLoop() {
	for {
		_, data, err := c.conn.ReadMessage()
		if err != nil {
			c.failAll(err)
			return
		}

		var msg cdpMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}

		if msg.ID != 0 {
			c.mu.Lock()
			p, ok := c.pending[msg.ID]
			delete(c.pending, msg.ID)
			c.mu.Unlock()
			if !ok {
				continue
			}
			if msg.Error != nil {
				p.err <- msg.Error
			} else {
				p.result <- msg.Result
			}
			continue
		}

		c.mu.Lock()
		hs := append([]EventHandler(nil), c.handlers[msg.Method]...)
		c.mu.Unlock()
		for _, h := range hs {
			c.dispatch(msg.Method, h, msg.Params)
		}
	}
}

func (c *CDP) dispatch(method string, h EventHandler, params json.RawMessage) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "recgo-tab: handler for %s panicked: %v\n", method, r)
		}
	}()
	h(params)
}

func (c *CDP) failAll(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.closed = true
	for id, p := range c.pending {
		p.err <- err
		delete(c.pending, id)
	}
}

func (c *CDP) writeJSON(v any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.conn.WriteJSON(v)
}

func (c *CDP) Send(method string, params any, result any) error {
	raw, err := c.send(method, params)
	if err != nil {
		return err
	}
	if result != nil && len(raw) > 0 {
		return json.Unmarshal(raw, result)
	}
	return nil
}

func (c *CDP) send(method string, params any) (json.RawMessage, error) {
	body := map[string]any{"method": method}
	if params != nil {
		body["params"] = params
	}

	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, fmt.Errorf("cdp connection closed")
	}
	id := c.nextID
	c.nextID++
	body["id"] = id
	p := pending{result: make(chan json.RawMessage, 1), err: make(chan error, 1)}
	c.pending[id] = p
	c.mu.Unlock()

	if err := c.writeJSON(body); err != nil {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, err
	}

	select {
	case r := <-p.result:
		return r, nil
	case e := <-p.err:
		return nil, e
	case <-time.After(cdpCallTimeout):
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		return nil, fmt.Errorf("%s timed out", method)
	}
}

func (c *CDP) SendAsync(method string, params any) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	id := c.nextID
	c.nextID++
	c.mu.Unlock()

	body := map[string]any{"id": id, "method": method}
	if params != nil {
		body["params"] = params
	}
	c.writeJSON(body)
}

func (c *CDP) On(event string, h EventHandler) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.handlers[event] = append(c.handlers[event], h)
}

func (c *CDP) Close() error {
	c.mu.Lock()
	already := c.closed
	c.closed = true
	c.mu.Unlock()
	if already {
		return nil
	}

	c.writeMu.Lock()
	c.conn.WriteControl(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""),
		time.Now().Add(time.Second))
	c.writeMu.Unlock()

	return c.conn.Close()
}
