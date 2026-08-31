package tab

// The portal bridge: recgo-tab dialing OUT to a portal server's
// ?intent=bridge lane and serving read-only file tools over the output
// root, so an agent anywhere portal is reachable reads the live session --
// no inbound port on this machine, no sync, no copies. This is deliberate
// egress and is treated like the remote STT backend: off by default, the
// destination announced on stderr before anything is served, and pinned by
// noupstream_test. What it exposes is bounded in three ways: the tool set
// is read-only by construction (write:false in the hello, and the server
// filters the advertisement against the same rule), every path is confined
// to the output root with symlinks resolved, and read_bytes refuses files
// over its cap rather than truncating them.

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

const (
	portalMaxRead    = 256 << 10
	portalMaxBytes   = 4 << 20
	portalMaxList    = 500
	portalBackoffMax = 15 * time.Second
)

var portalMime = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
	".gif": "image/gif", ".webp": "image/webp", ".wav": "audio/wav",
}

type portalTool struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"input_schema"`
}

func pathSchema(required bool) map[string]any {
	req := []string{}
	if required {
		req = []string{"path"}
	}
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{"type": "string"},
		},
		"required": req,
	}
}

func portalTools() []portalTool {
	return []portalTool{
		{"list_dir", "List one directory of the recording output root, not recursively. " +
			"Entries are sorted, directories marked with a trailing slash. " +
			"Paths are relative to the root; omit for the root itself.", pathSchema(false)},
		{"read_file", "Read a text file under the recording output root (up to 256KB, " +
			"longer files come back truncated with their real size). " +
			"SESSION.live.md in the newest folder is the live session document.", pathSchema(true)},
		{"read_bytes", "Read a file under the recording output root as raw bytes, for " +
			"screenshots and audio. Returns JSON {\"b64\", \"mime\", \"size\"} with the whole " +
			"file base64-encoded; files over 4MB are refused with their size rather than " +
			"truncated.", pathSchema(true)},
	}
}

// PortalURL turns a server base URL (ws://, wss://, http://, https://, or
// bare host) and a room name into the bridge lane URL.
func PortalURL(server, room string) string {
	server = strings.TrimSuffix(server, "/")
	for _, s := range [][2]string{{"https://", "wss://"}, {"http://", "ws://"}} {
		if strings.HasPrefix(server, s[0]) {
			server = s[1] + server[len(s[0]):]
		}
	}
	if !strings.HasPrefix(server, "ws://") && !strings.HasPrefix(server, "wss://") {
		server = "ws://" + server
	}
	return server + "/?intent=bridge&room=" + url.QueryEscape(room)
}

type PortalBridge struct {
	url    string
	root   string
	logf   func(string, ...any)
	cancel context.CancelFunc
	done   chan struct{}
}

// StartPortalBridge resolves root, announces what is about to be exposed,
// and keeps one bridge connection alive with backoff until Close. The
// caller has already opted in; the announcement is so the terminal states
// the destination before the first byte can leave.
func StartPortalBridge(server, room, root string, logf func(string, ...any)) (*PortalBridge, error) {
	real, err := filepath.EvalSymlinks(root)
	if err != nil {
		return nil, fmt.Errorf("portal: resolve root: %w", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	b := &PortalBridge{
		url:    PortalURL(server, room),
		root:   real,
		logf:   logf,
		cancel: cancel,
		done:   make(chan struct{}),
	}
	b.logf("portal: exposing %s READ-ONLY to room %q at %s -- anyone in that room can read the session as it records", real, room, server)
	go b.run(ctx)
	return b, nil
}

func (b *PortalBridge) Close() {
	b.cancel()
	<-b.done
}

func (b *PortalBridge) run(ctx context.Context) {
	defer close(b.done)
	delay := time.Second
	for ctx.Err() == nil {
		if err := b.serve(ctx); err != nil && ctx.Err() == nil {
			b.logf("portal: %v (retrying in %s)", err, delay)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		if delay *= 2; delay > portalBackoffMax {
			delay = portalBackoffMax
		}
	}
}

func (b *PortalBridge) serve(ctx context.Context) error {
	conn, _, err := websocket.DefaultDialer.DialContext(ctx, b.url, nil)
	if err != nil {
		return err
	}
	defer conn.Close()
	// The connection dies with the recording: without this, a read blocked
	// on an idle server would outlive Close and the deferred cleanup order.
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()

	if err := conn.WriteJSON(map[string]any{
		"t": "hello", "root": b.root, "write": false,
		"caps": []string{"fs"}, "tools": portalTools(),
	}); err != nil {
		return err
	}
	b.logf("portal: bridge connected")

	for {
		var msg struct {
			T     string          `json:"t"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		}
		if err := conn.ReadJSON(&msg); err != nil {
			return fmt.Errorf("bridge read: %w", err)
		}
		if msg.T != "tool_call" {
			continue
		}
		content, err := b.call(msg.Name, msg.Input)
		reply := map[string]any{"t": "tool_result", "id": msg.ID, "content": content}
		if err != nil {
			reply["content"] = err.Error()
			reply["is_error"] = true
		}
		if err := conn.WriteJSON(reply); err != nil {
			return err
		}
	}
}

func (b *PortalBridge) call(name string, input json.RawMessage) (string, error) {
	var in struct {
		Path string `json:"path"`
	}
	if len(input) > 0 {
		if err := json.Unmarshal(input, &in); err != nil {
			return "", fmt.Errorf("bad input: %w", err)
		}
	}
	switch name {
	case "list_dir":
		return b.listDir(in.Path)
	case "read_file":
		return b.readFile(in.Path)
	case "read_bytes":
		return b.readBytes(in.Path)
	}
	return "", fmt.Errorf("unknown tool %q", name)
}

// confine resolves a tool path under the root: textual escapes are removed
// by rooting the cleaned path, then symlinks on the deepest existing
// ancestor are resolved so a link inside the tree cannot point out of it.
func (b *PortalBridge) confine(p string) (string, error) {
	abs := filepath.Join(b.root, filepath.Clean("/"+p))
	probe := abs
	for {
		if _, err := os.Lstat(probe); err == nil || probe == b.root {
			break
		}
		probe = filepath.Dir(probe)
	}
	real, err := filepath.EvalSymlinks(probe)
	if err != nil {
		return "", err
	}
	real += abs[len(probe):]
	if real != b.root && !strings.HasPrefix(real, b.root+string(filepath.Separator)) {
		return "", fmt.Errorf("path escapes the output root: %s", p)
	}
	return real, nil
}

func (b *PortalBridge) listDir(p string) (string, error) {
	dir, err := b.confine(p)
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() {
			name += "/"
		}
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) > portalMaxList {
		names = names[:portalMaxList]
	}
	if len(names) == 0 {
		return "(empty)", nil
	}
	return strings.Join(names, "\n"), nil
}

func (b *PortalBridge) readFile(p string) (string, error) {
	file, err := b.confine(p)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(file)
	if err != nil {
		return "", err
	}
	f, err := os.Open(file)
	if err != nil {
		return "", err
	}
	defer f.Close()
	buf := make([]byte, portalMaxRead)
	n, _ := f.Read(buf)
	text := string(buf[:n])
	if info.Size() > portalMaxRead {
		text += fmt.Sprintf("\n...(truncated, %d bytes total)", info.Size())
	}
	return text, nil
}

func (b *PortalBridge) readBytes(p string) (string, error) {
	file, err := b.confine(p)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(file)
	if err != nil {
		return "", err
	}
	if info.Size() > portalMaxBytes {
		return "", fmt.Errorf("%s is %d bytes; the binary read cap is %d", p, info.Size(), portalMaxBytes)
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return "", err
	}
	mime := portalMime[strings.ToLower(filepath.Ext(file))]
	if mime == "" {
		mime = "application/octet-stream"
	}
	doc, err := json.Marshal(map[string]any{
		"b64": base64.StdEncoding.EncodeToString(raw), "mime": mime, "size": len(raw),
	})
	return string(doc), err
}
