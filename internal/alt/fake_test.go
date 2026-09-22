package alt

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// fakeApp is a spec-shaped mock app: it dials /altws/app and answers
// commands with the same envelope shapes, plus the recgo probe when
// enabled. It records every command and notification it saw.
type fakeApp struct {
	t     *testing.T
	ws    *websocket.Conn
	probe bool
	hang  bool

	mu       sync.Mutex
	commands []map[string]any
	notes    []map[string]any
	tree     []map[string]any // getAllLoadedScenesAndObjects answer; nil means fakeObjects
	done     chan struct{}
}

// setTree is what the app reports as its object tree from now on: a stale
// or partial tree is how a hit the resolver does not know comes about.
func (f *fakeApp) setTree(objs []map[string]any) {
	f.mu.Lock()
	f.tree = objs
	f.mu.Unlock()
}

var fakeObjects = []map[string]any{
	{"name": "Canvas", "id": 1, "transformId": 101, "transformParentId": 0, "enabled": true, "x": 0, "y": 0},
	{"name": "Play", "id": 2, "transformId": 102, "transformParentId": 101, "enabled": true, "x": 640, "y": 360},
	{"name": "Title", "id": 3, "transformId": 103, "transformParentId": 101, "enabled": true, "x": 640, "y": 100},
}

var fakeComponents = map[int][]map[string]string{
	2: {{"componentName": "UnityEngine.RectTransform", "assemblyName": "UnityEngine.CoreModule"},
		{"componentName": "UnityEngine.UI.Image", "assemblyName": "UnityEngine.UI"},
		{"componentName": "UnityEngine.UI.Button", "assemblyName": "UnityEngine.UI"}},
	3: {{"componentName": "TMPro.TextMeshProUGUI", "assemblyName": "Unity.TextMeshPro"}},
}

var fakeTexts = map[int]string{3: "Decentraland"}

// A 1x1 PNG, enough to check the screenshot bytes survive the double answer.
var fakePNG = []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR\x00\x00\x00\x01\x00\x00\x00\x01\x08\x06\x00\x00\x00\x1f\x15\xc4\x89\x00\x00\x00\nIDATx\x9cc\x00\x01\x00\x00\x05\x00\x01\r\n-\xb4\x00\x00\x00\x00IEND\xaeB`\x82")

const probeUITK = `{"kind":"uitk","path":"/root/backpack/card-3","name":"card-3","type":"Button","text":"Backpack","id":4242,"classes":"card muted","testId":""}`
const probeUGUI = `{"kind":"ugui","path":"/Canvas/Play","name":"Play","type":"Button","text":"Play","id":2,"classes":"","testId":""}`

func dialApp(t *testing.T, s *Server, name string, probe bool) *fakeApp {
	t.Helper()
	q := url.Values{"appName": {name}, "platform": {"mock"}, "platformVersion": {"0"},
		"deviceInstanceId": {"mock"}, "appId": {"mock"}}
	ws, _, err := websocket.DefaultDialer.Dial("ws://"+s.Addr()+"/altws/app?"+q.Encode(), nil)
	if err != nil {
		t.Fatalf("app dial: %v", err)
	}
	f := &fakeApp{t: t, ws: ws, probe: probe, done: make(chan struct{})}
	go f.loop()
	t.Cleanup(func() { ws.Close() })
	return f
}

func (f *fakeApp) loop() {
	defer close(f.done)
	for {
		_, data, err := f.ws.ReadMessage()
		if err != nil {
			return
		}
		var cmd map[string]any
		if json.Unmarshal(data, &cmd) != nil {
			continue
		}
		if b, _ := cmd["isNotification"].(bool); b {
			f.mu.Lock()
			f.notes = append(f.notes, cmd)
			f.mu.Unlock()
			continue
		}
		f.mu.Lock()
		f.commands = append(f.commands, cmd)
		f.mu.Unlock()
		if f.hang {
			continue
		}
		for _, item := range f.answer(cmd) {
			if err := f.ws.WriteMessage(websocket.TextMessage, []byte(fakeEnvelope(cmd, item))); err != nil {
				return
			}
		}
	}
}

func (f *fakeApp) close() {
	f.ws.Close()
	<-f.done
}

func (f *fakeApp) seen(name string) []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []map[string]any
	for _, c := range f.commands {
		if c["commandName"] == name {
			out = append(out, c)
		}
	}
	return out
}

func (f *fakeApp) notifications() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, n := range f.notes {
		out = append(out, fmt.Sprint(n["commandName"]))
	}
	return out
}

func (f *fakeApp) waitNotifications(n int) []string {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if got := f.notifications(); len(got) >= n {
			return got
		}
		time.Sleep(10 * time.Millisecond)
	}
	return f.notifications()
}

func js(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func num(v any) float64 {
	f, _ := v.(float64)
	return f
}

// answer mirrors mockapp.answer: a string item is data, a map is an error.
func (f *fakeApp) answer(cmd map[string]any) []any {
	obj, _ := cmd["altObject"].(map[string]any)
	switch cmd["commandName"] {
	case "getServerVersion":
		return []any{js("2.3.0")}
	case "getApplicationScreenSize":
		return []any{js(map[string]any{"x": 1280, "y": 720})}
	case "getCurrentScene":
		return []any{js(map[string]any{"name": "MockScene"})}
	case "getAllLoadedScenesAndObjects":
		f.mu.Lock()
		objs := f.tree
		f.mu.Unlock()
		if objs == nil {
			objs = fakeObjects
		}
		// The server lists each scene as an AltObjectLight with only a
		// name before that scene's objects.
		scene := map[string]any{"name": "MockScene", "id": 0, "transformId": 0, "transformParentId": 0, "enabled": true}
		return []any{js(append([]map[string]any{scene}, objs...))}
	case "findObject":
		path, _ := cmd["path"].(string)
		for _, o := range fakeObjects {
			if path == "//"+o["name"].(string) {
				return []any{js(o)}
			}
		}
		return []any{map[string]any{"type": "objectNotFound", "message": "Object " + path + " not found"}}
	case "findObjectAtCoordinates":
		c, _ := cmd["coordinates"].(map[string]any)
		if num(c["x"]) >= 600 {
			return []any{js(fakeObjects[1])}
		}
		if num(c["x"]) >= 300 {
			return []any{js(fakeObjects[2])}
		}
		return []any{js(nil)}
	case "getAllComponents":
		return []any{js(fakeComponents[int(num(cmd["altObjectId"]))])}
	case "getText":
		return []any{js(fakeTexts[int(num(obj["id"]))])}
	case "callComponentMethodForObject":
		if !f.probe {
			return []any{map[string]any{"type": "componentNotFound", "message": "Component not found"}}
		}
		params, _ := cmd["parameters"].([]any)
		if len(params) != 2 {
			return []any{map[string]any{"type": "invalidParameterType", "message": "want 2 parameters"}}
		}
		x, _ := params[0].(string)
		switch {
		case x == "0":
			return []any{js("null")}
		case mustFloat(x) >= 600:
			return []any{js(probeUGUI)}
		}
		return []any{js(probeUITK)}
	case "getScreenshot":
		return []any{js("Ok"), js(map[string]any{
			"compressedImage": base64.StdEncoding.EncodeToString(fakePNG),
			// An AltVector3 the way Newtonsoft writes it: floats, not ints.
			"textureSize": map[string]any{"x": json.Number("1.0"), "y": json.Number("1.0"), "z": json.Number("0.0")},
		})}
	}
	return []any{map[string]any{"type": "unknownCommand", "message": fmt.Sprintf("fake app does not implement %v", cmd["commandName"])}}
}

func mustFloat(s string) float64 {
	var f float64
	fmt.Sscan(s, &f)
	return f
}

func fakeEnvelope(cmd map[string]any, item any) string {
	out := map[string]any{
		"messageId": cmd["messageId"], "commandName": cmd["commandName"], "driverId": cmd["driverId"],
		"isNotification": false, "data": nil, "error": nil,
	}
	if m, ok := item.(map[string]any); ok {
		out["error"] = m
	} else {
		out["data"] = item
	}
	return js(out)
}

func newServer(t *testing.T, opts Options) *Server {
	t.Helper()
	opts.Host = "127.0.0.1"
	s, err := Listen(opts)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// extDriverConn is altdrive.py's side: a client on /altws.
type extDriverConn struct {
	t  *testing.T
	ws *websocket.Conn
}

func dialDriver(t *testing.T, s *Server, appName string) *extDriverConn {
	t.Helper()
	q := url.Values{"appName": {appName}, "driverType": {"go-test"}}
	ws, _, err := websocket.DefaultDialer.Dial("ws://"+s.Addr()+"/altws?"+q.Encode(), nil)
	if err != nil {
		t.Fatalf("driver dial: %v", err)
	}
	t.Cleanup(func() { ws.Close() })
	return &extDriverConn{t: t, ws: ws}
}

func (d *extDriverConn) read() (map[string]any, error) {
	d.ws.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, data, err := d.ws.ReadMessage()
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func (d *extDriverConn) mustRead() map[string]any {
	d.t.Helper()
	m, err := d.read()
	if err != nil {
		d.t.Fatalf("driver read: %v", err)
	}
	return m
}

func (d *extDriverConn) send(m map[string]any) {
	d.t.Helper()
	if err := d.ws.WriteMessage(websocket.TextMessage, []byte(js(m))); err != nil {
		d.t.Fatalf("driver send: %v", err)
	}
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}
