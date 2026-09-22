// Package alt speaks the AltTester SDK 2.3 wire protocol so recgo can ask an
// instrumented Unity build what sits under a click, without AltTester Desktop.
//
// Both AltTester ends are WebSocket clients: the app dials /altws/app and a
// driver dials /altws. This package is the listener both dial, the same role
// a stand-in relay plays. recgo's own driver rides the app socket
// directly; an external driver (a raw-protocol one) is paired with the app
// and its frames forwarded verbatim, so both can drive the same build.
package alt

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// Close codes the AltTester driver SDK understands.
	closeAppLost  = 4002 // altrelay.py: "App disconnected"
	closeReplaced = 4003
	closeRefused  = 4001

	// Frames an unpaired side sends are held until it is paired; a Unity
	// build streams notifications, so the queue is capped.
	pendingCap = 256

	driverRegistered = `{"commandName":"driverRegistered","isNotification":true,"messageId":"","driverId":"","data":null,"error":null}`
)

// AppInfo is what the instrumented app said in its /altws/app query string.
type AppInfo struct {
	Name, Platform, PlatformVersion, DeviceInstanceID, AppID string
}

func (a AppInfo) String() string {
	if a.Platform == "" {
		return a.Name
	}
	return a.Name + " (" + a.Platform + ")"
}

// Options configure the listener. OnApp fires once an app is connected and
// recgo's driver is attached; OnAppLost when that app's socket ends.
type Options struct {
	Host      string
	Port      int
	AppName   string // accept only this appName; "" accepts any
	OnApp     func(AppInfo)
	OnAppLost func(AppInfo)
	Log       func(format string, args ...any)
}

// Server is the listener both AltTester ends dial.
type Server struct {
	opts     Options
	ln       net.Listener
	srv      *http.Server
	upgrader websocket.Upgrader
	driver   *Driver

	mu      sync.Mutex
	app     *appConn
	drivers []*extDriver
	closed  bool
}

type appConn struct {
	ws      *websocket.Conn
	info    AppInfo
	wmu     sync.Mutex
	mate    *extDriver
	pending []string
	done    chan struct{}
	once    sync.Once
}

func (a *appConn) send(text string) error {
	a.wmu.Lock()
	defer a.wmu.Unlock()
	return a.ws.WriteMessage(websocket.TextMessage, []byte(text))
}

func (a *appConn) close(code int, reason string) {
	a.once.Do(func() {
		a.wmu.Lock()
		_ = a.ws.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(code, reason), time.Now().Add(time.Second))
		a.wmu.Unlock()
		_ = a.ws.Close()
	})
}

type extDriver struct {
	ws      *websocket.Conn
	id      string
	appName string
	wmu     sync.Mutex
	mate    *appConn
	pending []string
	once    sync.Once
}

func (d *extDriver) send(text string) error {
	d.wmu.Lock()
	defer d.wmu.Unlock()
	return d.ws.WriteMessage(websocket.TextMessage, []byte(text))
}

func (d *extDriver) close(code int, reason string) {
	d.once.Do(func() {
		d.wmu.Lock()
		_ = d.ws.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(code, reason), time.Now().Add(time.Second))
		d.wmu.Unlock()
		_ = d.ws.Close()
	})
}

// Listen binds host:port and starts serving. A taken port is refused with
// the two ways out, since AltTester Desktop defaults to the same 13000.
func Listen(opts Options) (*Server, error) {
	if opts.Host == "" {
		opts.Host = "127.0.0.1"
	}
	if opts.Log == nil {
		opts.Log = func(string, ...any) {}
	}
	addr := fmt.Sprintf("%s:%d", opts.Host, opts.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("cannot listen on %s: %v\n"+
			"  pick another port with --alt-port (and launch the client with --alttester host:port to match),\n"+
			"  or stop whatever else owns it (AltTester Desktop, or a relay of your own)", addr, err)
	}
	s := &Server{
		opts:     opts,
		ln:       ln,
		upgrader: websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }},
		driver:   newDriver(newID()),
	}
	s.srv = &http.Server{Handler: http.HandlerFunc(s.serveHTTP)}
	go func() { _ = s.srv.Serve(ln) }()
	return s, nil
}

// Addr is the bound host:port.
func (s *Server) Addr() string { return s.ln.Addr().String() }

// Driver is recgo's own driver, attached to whichever app is connected.
func (s *Server) Driver() *Driver { return s.driver }

// App is the connected app, or nil.
func (s *Server) App() (AppInfo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.app == nil {
		return AppInfo{}, false
	}
	return s.app.info, true
}

// Close stops listening and drops every connection.
func (s *Server) Close() error {
	s.mu.Lock()
	s.closed = true
	app := s.app
	drivers := append([]*extDriver(nil), s.drivers...)
	s.mu.Unlock()
	if app != nil {
		app.close(websocket.CloseGoingAway, "recgo stopped")
	}
	for _, d := range drivers {
		d.close(websocket.CloseGoingAway, "recgo stopped")
	}
	return s.srv.Close()
}

func (s *Server) serveHTTP(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimRight(r.URL.Path, "/")
	switch {
	case strings.HasPrefix(path, "/altws/app"):
		s.serveApp(w, r)
	case strings.HasPrefix(path, "/altws"):
		s.serveDriver(w, r)
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) serveApp(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	info := AppInfo{
		Name: q.Get("appName"), Platform: q.Get("platform"), PlatformVersion: q.Get("platformVersion"),
		DeviceInstanceID: q.Get("deviceInstanceId"), AppID: q.Get("appId"),
	}
	ws, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	a := &appConn{ws: ws, info: info, done: make(chan struct{})}
	if s.opts.AppName != "" && info.Name != s.opts.AppName {
		s.opts.Log("alt: refusing app %q (--alt-app %s)", info.Name, s.opts.AppName)
		a.close(closeRefused, "recgo is waiting for appName "+s.opts.AppName)
		return
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		a.close(websocket.CloseGoingAway, "recgo stopped")
		return
	}
	old := s.app
	s.app = a
	// Attached under the same lock, so nobody sees the new app published
	// while recgo's driver still points at the old socket.
	s.driver.attach(a)
	s.mu.Unlock()
	if old != nil {
		s.opts.Log("alt: app %s replaces the previous connection", info)
		old.close(closeReplaced, "replaced by a new app connection")
	}
	s.pair()
	if s.opts.OnApp != nil {
		// Off this goroutine: the callback usually sends the app a command,
		// whose answer only the read loop below can receive.
		go s.opts.OnApp(info)
	}
	for {
		_, data, err := ws.ReadMessage()
		if err != nil {
			break
		}
		s.fromApp(a, string(data))
	}
	s.appGone(a)
}

func (s *Server) fromApp(a *appConn, text string) {
	var env envelope
	_ = json.Unmarshal([]byte(text), &env)
	if s.driver.owns(env) {
		s.driver.deliver(env, text)
		return
	}
	s.mu.Lock()
	mate := a.mate
	if mate == nil && len(a.pending) < pendingCap {
		a.pending = append(a.pending, text)
	}
	s.mu.Unlock()
	if mate != nil {
		_ = mate.send(text)
	}
}

func (s *Server) appGone(a *appConn) {
	s.mu.Lock()
	if s.app == a {
		s.app = nil
	}
	mate := a.mate
	a.mate = nil
	if mate != nil {
		mate.mate = nil
	}
	s.driver.detach(a)
	s.mu.Unlock()
	a.close(websocket.CloseNormalClosure, "")
	close(a.done)
	if mate != nil {
		mate.close(closeAppLost, "App disconnected")
	}
	if s.opts.OnAppLost != nil {
		s.opts.OnAppLost(a.info)
	}
}

func (s *Server) serveDriver(w http.ResponseWriter, r *http.Request) {
	ws, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	d := &extDriver{ws: ws, id: newID(), appName: r.URL.Query().Get("appName")}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		d.close(websocket.CloseGoingAway, "recgo stopped")
		return
	}
	s.drivers = append(s.drivers, d)
	s.mu.Unlock()
	s.opts.Log("alt: external driver connected (appName=%q)", d.appName)
	s.pair()
	for {
		_, data, err := ws.ReadMessage()
		if err != nil {
			break
		}
		s.fromDriver(d, string(data))
	}
	s.driverGone(d)
}

func (s *Server) fromDriver(d *extDriver, text string) {
	s.mu.Lock()
	app := d.mate
	if app == nil && len(d.pending) < pendingCap {
		d.pending = append(d.pending, text)
	}
	s.mu.Unlock()
	if app != nil {
		_ = app.send(text)
	}
}

func (s *Server) driverGone(d *extDriver) {
	s.mu.Lock()
	for i, x := range s.drivers {
		if x == d {
			s.drivers = append(s.drivers[:i], s.drivers[i+1:]...)
			break
		}
	}
	app := d.mate
	d.mate = nil
	if app != nil && app.mate == d {
		app.mate = nil
	}
	s.mu.Unlock()
	d.close(websocket.CloseNormalClosure, "")
	if app != nil {
		_ = app.send(notification("DriverDisconnectedNotification", d.id))
	}
	s.opts.Log("alt: external driver disconnected")
	s.pair()
}

// pair binds the app to the longest-waiting unpaired driver, the way
// altrelay.py does: the app hears DriverConnectedNotification, the driver
// hears driverRegistered, and whatever either side sent while alone is
// flushed. Like the relay, an appName mismatch does not block pairing.
func (s *Server) pair() {
	s.mu.Lock()
	defer s.mu.Unlock()
	app := s.app
	if app == nil || app.mate != nil {
		return
	}
	for _, d := range s.drivers {
		if d.mate != nil {
			continue
		}
		d.mate = app
		app.mate = d
		s.opts.Log("alt: paired external driver (appName=%q) with app %s", d.appName, app.info)
		_ = app.send(notification("DriverConnectedNotification", d.id))
		for _, m := range app.pending {
			_ = d.send(m)
		}
		app.pending = nil
		_ = d.send(driverRegistered)
		for _, m := range d.pending {
			_ = app.send(m)
		}
		d.pending = nil
		return
	}
}

func notification(name, driverID string) string {
	b, _ := json.Marshal(map[string]any{
		"commandName": name, "isNotification": true, "messageId": "", "driverId": driverID,
	})
	return string(b)
}

func newID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
