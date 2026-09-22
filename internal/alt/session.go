package alt

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/eordano/recgo/internal/screencast"
	"github.com/eordano/recgo/internal/tab"
)

// SessionOptions configure Attach.
type SessionOptions struct {
	Host    string
	Port    int
	AppName string // accept only this appName; "" accepts any
	// Window is a substring of the app window's title or class, the way to
	// recognise the window when the app dials with the SDK's default
	// appName (see Clicks.SetWindowMatch).
	Window   string
	Probe    Probe
	WindowAt func(x, y float64) (screencast.WindowRect, bool)
	Log      func(format string, args ...any)
}

// Session is one recgo-alttester run: the listener the app dials, the
// click resolver, and the Note lines that tell the SESSION.md reader what
// the AltTester side did (app connected, disconnected, never came; probe
// missing from the build).
type Session struct {
	srv    *Server
	clicks *Clicks
	note   func(string)
	log    func(format string, args ...any)
	ready  chan struct{} // closed once srv and clicks are set

	connected  atomic.Bool
	probeNoted sync.Once
}

// Attach listens for the app and wires its connection state to note, the
// recorder's Note line. It returns once the listener is up; the app may
// connect any time after.
func Attach(o SessionOptions, note func(string)) (*Session, error) {
	logf := o.Log
	if logf == nil {
		logf = func(string, ...any) {}
	}
	if note == nil {
		note = func(string) {}
	}
	s := &Session{note: note, log: logf, ready: make(chan struct{})}
	srv, err := Listen(Options{
		Host: o.Host, Port: o.Port, AppName: o.AppName, Log: logf,
		OnApp: func(info AppInfo) {
			<-s.ready
			s.connected.Store(true)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			version, err := s.srv.Driver().GetServerVersion(ctx)
			if err != nil {
				version = "unknown (" + err.Error() + ")"
			}
			line := fmt.Sprintf("AltTester app connected: %s, server %s", info, version)
			logf("%s", line)
			note(line)
			s.clicks.SetAppName(info.Name)
			// The object tree gives fallback hits their transform path; a
			// big scene takes a while, so it gets its own bound.
			tctx, tcancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer tcancel()
			if err := s.clicks.Resolver.LoadTree(tctx); err != nil {
				logf("alt: object tree: %v -- fallback selectors stay bare names until a refetch", err)
			}
		},
		OnAppLost: func(info AppInfo) {
			<-s.ready
			line := fmt.Sprintf("AltTester app disconnected: %s", info)
			logf("%s -- clicks are position-only until it reconnects", line)
			note(line)
		},
	})
	if err != nil {
		return nil, err
	}
	s.srv = srv
	s.clicks = &Clicks{
		Driver:   srv.Driver(),
		Resolver: NewResolver(srv.Driver(), o.Probe),
		WindowAt: o.WindowAt,
		Log:      logf,
	}
	s.clicks.SetAppName(o.AppName)
	s.clicks.SetWindowMatch(o.Window)
	close(s.ready)
	return s, nil
}

// Addr is the listener's host:port.
func (s *Session) Addr() string { return s.srv.Addr() }

// Server is the listener, for tests and tools that drive the app directly.
func (s *Session) Server() *Server { return s.srv }

// Clicks is the click resolver.
func (s *Session) Clicks() *Clicks { return s.clicks }

// Resolve names the element under a desktop click. The first time the
// build turns out to lack the probe, a Note says what that changes for the
// document: selectors come from the SDK's object tree and UI Toolkit is
// invisible.
func (s *Session) Resolve(x, y float64) (*tab.Element, string) {
	el, note := s.clicks.Resolve(x, y)
	if reason, missing := s.clicks.Resolver.ProbeMissing(); missing {
		s.probeNoted.Do(func() {
			line := fmt.Sprintf("AltTester probe missing in this build (%s); clicks resolved by "+
				"findObjectAtCoordinates: selectors are UGUI transform paths from the object tree "+
				"(bare object names when the tree does not know the hit), UI Toolkit elements are not seen", reason)
			s.log("%s", line)
			s.note(line)
		})
	}
	return el, note
}

// Close ends the run: a recording the app never joined says so, since it
// otherwise reads like one with no UI under any click.
func (s *Session) Close() {
	if !s.connected.Load() {
		line := fmt.Sprintf("AltTester app never connected (listened on %s)", s.srv.Addr())
		s.log("%s", line)
		s.note(line)
	}
	_ = s.srv.Close()
}
