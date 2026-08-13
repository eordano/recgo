package screencast

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	portalBus    = "org.freedesktop.portal.Desktop"
	portalPath   = "/org/freedesktop/portal/desktop"
	screenCast   = "org.freedesktop.portal.ScreenCast"
	requestIface = "org.freedesktop.portal.Request"
)

type SourceType uint32

const (
	SourceMonitor SourceType = 1 << iota
	SourceWindow
	SourceVirtual
)

type CursorMode uint32

const (
	CursorHidden   CursorMode = 1
	CursorEmbedded CursorMode = 2
	CursorMetadata CursorMode = 4
)

type PersistMode uint32

const (
	PersistNone PersistMode = iota
	PersistWhileRunning
	PersistUntilRevoked
)

type Stream struct {
	NodeID uint32
	W, H   int32
	Type   SourceType
}

type Session struct {
	conn    *dbus.Conn
	handle  dbus.ObjectPath
	Streams []Stream

	RestoreToken string

	mu     sync.Mutex
	closed bool
}

type Options struct {
	Types        SourceType
	Cursor       CursorMode
	Multiple     bool
	Persist      PersistMode
	RestoreToken string
	Timeout      time.Duration
}

func (o *Options) withDefaults() {
	if o.Types == 0 {
		o.Types = SourceMonitor | SourceWindow
	}
	if o.Cursor == 0 {
		o.Cursor = CursorEmbedded
	}
	if o.Persist == PersistNone {
		o.Persist = PersistUntilRevoked
	}
	if o.Timeout == 0 {
		o.Timeout = 2 * time.Minute
	}
}

func token() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("recgo%d", time.Now().UnixNano())
	}
	return "recgo_" + hex.EncodeToString(b[:])
}

func requestPath(uniqueName, tok string) dbus.ObjectPath {
	sender := strings.ReplaceAll(strings.TrimPrefix(uniqueName, ":"), ".", "_")
	return dbus.ObjectPath("/org/freedesktop/portal/desktop/request/" + sender + "/" + tok)
}

type response struct {
	code    uint32
	results map[string]dbus.Variant
}

func call(conn *dbus.Conn, method string, timeout time.Duration, args ...any) (response, error) {
	tok := token()
	path := requestPath(conn.Names()[0], tok)

	if err := conn.AddMatchSignal(
		dbus.WithMatchObjectPath(path),
		dbus.WithMatchInterface(requestIface),
		dbus.WithMatchMember("Response"),
	); err != nil {
		return response{}, fmt.Errorf("subscribe to %s: %w", path, err)
	}
	defer conn.RemoveMatchSignal(
		dbus.WithMatchObjectPath(path),
		dbus.WithMatchInterface(requestIface),
		dbus.WithMatchMember("Response"),
	)

	sigs := make(chan *dbus.Signal, 4)
	conn.Signal(sigs)
	defer conn.RemoveSignal(sigs)

	if len(args) == 0 {
		return response{}, fmt.Errorf("call %s: no options argument", method)
	}
	opts, ok := args[len(args)-1].(map[string]dbus.Variant)
	if !ok {
		return response{}, fmt.Errorf("call %s: last argument is not an options dict", method)
	}
	opts["handle_token"] = dbus.MakeVariant(tok)

	obj := conn.Object(portalBus, portalPath)
	var reqPath dbus.ObjectPath
	if err := obj.Call(method, 0, args...).Store(&reqPath); err != nil {
		return response{}, fmt.Errorf("%s: %w", method, err)
	}

	deadline := time.After(timeout)
	for {
		select {
		case sig := <-sigs:
			if sig.Path != reqPath && sig.Path != path {
				continue
			}
			if sig.Name != requestIface+".Response" || len(sig.Body) < 2 {
				continue
			}
			code, _ := sig.Body[0].(uint32)
			results, _ := sig.Body[1].(map[string]dbus.Variant)
			return response{code: code, results: results}, nil
		case <-deadline:
			return response{}, fmt.Errorf("%s timed out after %s", method, timeout)
		}
	}
}

func responseError(what string, code uint32) error {
	switch code {
	case 0:
		return nil
	case 1:
		return fmt.Errorf("%s: cancelled by the user", what)
	default:
		return fmt.Errorf("%s: portal returned code %d", what, code)
	}
}

func Open(opts Options) (*Session, error) {
	opts.withDefaults()

	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, fmt.Errorf("connect to the session bus: %w (is a desktop session running?)", err)
	}

	sessTok := token()
	res, err := call(conn, screenCast+".CreateSession", opts.Timeout, map[string]dbus.Variant{
		"session_handle_token": dbus.MakeVariant(sessTok),
	})
	if err != nil {
		conn.Close()
		return nil, err
	}
	if err := responseError("CreateSession", res.code); err != nil {
		conn.Close()
		return nil, err
	}

	var handle dbus.ObjectPath
	if v, ok := res.results["session_handle"]; ok {
		var s string
		if err := v.Store(&s); err == nil {
			handle = dbus.ObjectPath(s)
		}
	}
	if handle == "" {
		conn.Close()
		return nil, fmt.Errorf("CreateSession returned no session_handle")
	}

	sel := map[string]dbus.Variant{
		"types":        dbus.MakeVariant(uint32(opts.Types)),
		"multiple":     dbus.MakeVariant(opts.Multiple),
		"cursor_mode":  dbus.MakeVariant(uint32(opts.Cursor)),
		"persist_mode": dbus.MakeVariant(uint32(opts.Persist)),
	}
	if opts.RestoreToken != "" {
		sel["restore_token"] = dbus.MakeVariant(opts.RestoreToken)
	}

	res, err = call(conn, screenCast+".SelectSources", opts.Timeout, handle, sel)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if err := responseError("SelectSources", res.code); err != nil {
		conn.Close()
		return nil, err
	}

	res, err = call(conn, screenCast+".Start", opts.Timeout, handle, "", map[string]dbus.Variant{})
	if err != nil {
		conn.Close()
		return nil, err
	}
	if err := responseError("Start", res.code); err != nil {
		conn.Close()
		return nil, err
	}

	s := &Session{conn: conn, handle: handle}
	if v, ok := res.results["restore_token"]; ok {
		v.Store(&s.RestoreToken)
	}
	s.Streams = parseStreams(res.results)
	if len(s.Streams) == 0 {
		s.Close()
		return nil, fmt.Errorf("the portal granted no streams")
	}
	return s, nil
}

func parseStreams(results map[string]dbus.Variant) []Stream {
	v, ok := results["streams"]
	if !ok {
		return nil
	}
	var raw []struct {
		Node  uint32
		Props map[string]dbus.Variant
	}
	if err := v.Store(&raw); err != nil {
		return nil
	}

	out := make([]Stream, 0, len(raw))
	for _, r := range raw {
		st := Stream{NodeID: r.Node}
		if sv, ok := r.Props["size"]; ok {
			var size []int32
			if sv.Store(&size) == nil && len(size) == 2 {
				st.W, st.H = size[0], size[1]
			}
		}
		if tv, ok := r.Props["source_type"]; ok {
			var t uint32
			if tv.Store(&t) == nil {
				st.Type = SourceType(t)
			}
		}
		out = append(out, st)
	}
	return out
}

func (s *Session) OpenPipeWireRemote() (*os.File, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, fmt.Errorf("session closed")
	}

	obj := s.conn.Object(portalBus, portalPath)
	var fd dbus.UnixFD
	if err := obj.Call(screenCast+".OpenPipeWireRemote", 0,
		s.handle, map[string]dbus.Variant{}).Store(&fd); err != nil {
		return nil, fmt.Errorf("OpenPipeWireRemote: %w", err)
	}
	return os.NewFile(uintptr(fd), "pipewire-remote"), nil
}

func (s *Session) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	conn, handle := s.conn, s.handle
	s.mu.Unlock()

	if handle != "" {
		conn.Object(portalBus, handle).Call(
			"org.freedesktop.portal.Session.Close", 0)
	}
	return conn.Close()
}
