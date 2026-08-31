package tab

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// fakePortal is the server side of the bridge lane: it upgrades whatever
// connects and hands each connection to the test over a channel, so a test
// plays portal by receiving the hello and sending tool_call frames itself.
type fakePortal struct {
	srv   *httptest.Server
	conns chan *websocket.Conn
}

func newFakePortal(t *testing.T) *fakePortal {
	t.Helper()
	f := &fakePortal{conns: make(chan *websocket.Conn, 4)}
	up := websocket.Upgrader{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("intent") != "bridge" {
			t.Errorf("dialed intent %q, want bridge", r.URL.Query().Get("intent"))
		}
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		f.conns <- conn
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakePortal) accept(t *testing.T) *websocket.Conn {
	t.Helper()
	select {
	case conn := <-f.conns:
		return conn
	case <-time.After(5 * time.Second):
		t.Fatal("no bridge connection arrived")
		return nil
	}
}

func recvJSON(t *testing.T, conn *websocket.Conn) map[string]any {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var msg map[string]any
	if err := conn.ReadJSON(&msg); err != nil {
		t.Fatalf("read: %v", err)
	}
	return msg
}

func callTool(t *testing.T, conn *websocket.Conn, id, name string, input map[string]any) map[string]any {
	t.Helper()
	if err := conn.WriteJSON(map[string]any{
		"t": "tool_call", "id": id, "name": name, "input": input,
	}); err != nil {
		t.Fatalf("write: %v", err)
	}
	res := recvJSON(t, conn)
	if res["t"] != "tool_result" || res["id"] != id {
		t.Fatalf("unexpected reply %v", res)
	}
	return res
}

func portalRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	session := filepath.Join(root, "2026-08-17-12-00-demo")
	if err := os.MkdirAll(session, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(session, "SESSION.live.md"),
		[]byte("# Session\n00.00.01: hello\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(session, "0002.png"),
		[]byte("\x89PNG\r\n\x1a\nfake"), 0o600); err != nil {
		t.Fatal(err)
	}
	return root
}

func startBridge(t *testing.T, server, root string) *PortalBridge {
	t.Helper()
	b, err := StartPortalBridge(server, "test-room", root, t.Logf)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)
	return b
}

func TestPortalURL(t *testing.T) {
	for in, want := range map[string]string{
		"https://p.example":     "wss://p.example/?intent=bridge&room=a+b",
		"http://p.example:8080": "ws://p.example:8080/?intent=bridge&room=a+b",
		"wss://p.example/":      "wss://p.example/?intent=bridge&room=a+b",
		"p.example:8080":        "ws://p.example:8080/?intent=bridge&room=a+b",
	} {
		if got := PortalURL(in, "a b"); got != want {
			t.Errorf("PortalURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPortalBridgeHelloAndRoundTrips(t *testing.T) {
	f := newFakePortal(t)
	root := portalRoot(t)
	startBridge(t, f.srv.URL, root)
	conn := f.accept(t)
	defer conn.Close()

	hello := recvJSON(t, conn)
	if hello["t"] != "hello" || hello["write"] != false {
		t.Fatalf("bad hello %v", hello)
	}
	realRoot, _ := filepath.EvalSymlinks(root)
	if hello["root"] != realRoot {
		t.Errorf("hello root %v, want %s", hello["root"], realRoot)
	}
	tools := hello["tools"].([]any)
	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		doc := tool.(map[string]any)
		names = append(names, doc["name"].(string))
		if doc["input_schema"] == nil {
			t.Errorf("tool %v advertises no schema", doc["name"])
		}
	}
	if got := strings.Join(names, ","); got != "list_dir,read_file,read_bytes" {
		t.Errorf("advertised %s", got)
	}

	res := callTool(t, conn, "c1", "list_dir", nil)
	if res["content"] != "2026-08-17-12-00-demo/" {
		t.Errorf("list_dir: %v", res["content"])
	}
	res = callTool(t, conn, "c2", "read_file",
		map[string]any{"path": "2026-08-17-12-00-demo/SESSION.live.md"})
	if !strings.Contains(res["content"].(string), "00.00.01: hello") {
		t.Errorf("read_file: %v", res["content"])
	}
	res = callTool(t, conn, "c3", "read_bytes",
		map[string]any{"path": "2026-08-17-12-00-demo/0002.png"})
	var doc struct {
		B64  string `json:"b64"`
		Mime string `json:"mime"`
		Size int    `json:"size"`
	}
	if err := json.Unmarshal([]byte(res["content"].(string)), &doc); err != nil {
		t.Fatalf("read_bytes content: %v", err)
	}
	raw, _ := base64.StdEncoding.DecodeString(doc.B64)
	if doc.Mime != "image/png" || doc.Size != 12 || string(raw[:4]) != "\x89PNG" {
		t.Errorf("read_bytes: %+v", doc)
	}
}

func TestPortalBridgeConfinesPaths(t *testing.T) {
	f := newFakePortal(t)
	root := portalRoot(t)
	outside := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "leak")); err != nil {
		t.Fatal(err)
	}
	startBridge(t, f.srv.URL, root)
	conn := f.accept(t)
	defer conn.Close()
	recvJSON(t, conn)

	// A textual escape is re-rooted, not followed: ../../secret.txt reads
	// as /secret.txt under the root and simply does not exist.
	res := callTool(t, conn, "c1", "read_file", map[string]any{"path": "../../" + outside})
	if res["is_error"] != true {
		t.Errorf("textual escape served: %v", res)
	}
	res = callTool(t, conn, "c2", "read_file", map[string]any{"path": "leak"})
	if res["is_error"] != true || !strings.Contains(res["content"].(string), "escapes") {
		t.Errorf("symlink escape served: %v", res)
	}
}

func TestPortalBridgeCapsAndUnknownTools(t *testing.T) {
	f := newFakePortal(t)
	root := portalRoot(t)
	big := filepath.Join(root, "big.bin")
	if err := os.WriteFile(big, make([]byte, portalMaxBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	startBridge(t, f.srv.URL, root)
	conn := f.accept(t)
	defer conn.Close()
	recvJSON(t, conn)

	res := callTool(t, conn, "c1", "read_bytes", map[string]any{"path": "big.bin"})
	if res["is_error"] != true || !strings.Contains(res["content"].(string), "binary read cap") {
		t.Errorf("oversize read served: %v", res)
	}
	res = callTool(t, conn, "c2", "write_file",
		map[string]any{"path": "x", "content": "y"})
	if res["is_error"] != true || !strings.Contains(res["content"].(string), "unknown tool") {
		t.Errorf("write_file answered: %v", res)
	}
}

func TestPortalBridgeReconnects(t *testing.T) {
	f := newFakePortal(t)
	startBridge(t, f.srv.URL, portalRoot(t))
	first := f.accept(t)
	recvJSON(t, first)
	first.Close()

	second := f.accept(t)
	defer second.Close()
	hello := recvJSON(t, second)
	if hello["t"] != "hello" {
		t.Fatalf("no hello after reconnect: %v", hello)
	}
}
