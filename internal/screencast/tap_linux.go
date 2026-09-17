//go:build linux

package screencast

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/godbus/dbus/v5"

	"github.com/eordano/recgo/internal/logging"
)

// Linux click and window capture. Clicks come from evdev: every mouse-like
// device under /dev/input is read for BTN_LEFT/RIGHT/MIDDLE presses, which
// needs the user in the input group (the udev default for /dev/input/event*).
// The pointer position and the window under it are not on evdev -- the
// compositor owns them -- so a one-shot KWin script reads workspace.cursorPos
// and the active window and calls back into a D-Bus object this process
// exports. Focus changes and new windows use the same bridge from a script
// that stays loaded for the session.

const (
	evKey     = 1
	btnLeft   = 0x110
	btnRight  = 0x111
	btnMiddle = 0x112
	// struct input_event on 64-bit: timeval (16) + type u16 + code u16 + value s32.
	inputEventSize = 24
)

type inputEvent struct {
	Type, Code uint16
	Value      int32
}

func parseInputEvent(b []byte) (inputEvent, bool) {
	if len(b) < inputEventSize {
		return inputEvent{}, false
	}
	return inputEvent{
		Type:  binary.LittleEndian.Uint16(b[16:]),
		Code:  binary.LittleEndian.Uint16(b[18:]),
		Value: int32(binary.LittleEndian.Uint32(b[20:])),
	}, true
}

func clickButton(ev inputEvent) int {
	if ev.Type != evKey || ev.Value != 1 {
		return 0
	}
	switch ev.Code {
	case btnLeft:
		return 1
	case btnRight:
		return 2
	case btnMiddle:
		return 3
	}
	return 0
}

// mouseDevices resolves the *-event-mouse links udev keeps under by-id and
// by-path; without those (a VM, a tablet) every event node is a candidate and
// the button filter does the rest.
func mouseDevices() []string {
	seen := map[string]bool{}
	var out []string
	add := func(p string) {
		if r, err := filepath.EvalSymlinks(p); err == nil && !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	for _, dir := range []string{"/dev/input/by-id", "/dev/input/by-path"} {
		links, _ := filepath.Glob(filepath.Join(dir, "*-event-mouse"))
		for _, l := range links {
			add(l)
		}
	}
	if len(out) == 0 {
		nodes, _ := filepath.Glob("/dev/input/event*")
		for _, n := range nodes {
			add(n)
		}
	}
	sort.Strings(out)
	return out
}

func StartClickTap(onClick func(Click)) (func(), error) {
	devs := mouseDevices()
	if len(devs) == 0 {
		return nil, fmt.Errorf("click capture: no /dev/input devices found")
	}
	var files []*os.File
	var lastErr error
	for _, d := range devs {
		f, err := os.Open(d)
		if err != nil {
			lastErr = err
			continue
		}
		files = append(files, f)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("click capture needs read access to /dev/input (add your user to the input group and log in again): %v", lastErr)
	}
	bridge, err := openBridge()
	if err != nil {
		for _, f := range files {
			f.Close()
		}
		return nil, fmt.Errorf("click capture: %w", err)
	}
	logging.Log("clicktap: reading %d evdev device(s); cursor via KWin", len(files))

	var wg sync.WaitGroup
	for _, f := range files {
		wg.Add(1)
		go func(f *os.File) {
			defer wg.Done()
			buf := make([]byte, inputEventSize*16)
			for {
				n, err := f.Read(buf)
				if err != nil {
					if !errors.Is(err, os.ErrClosed) && !errors.Is(err, io.EOF) {
						logging.Log("clicktap: %s: %v", f.Name(), err)
					}
					return
				}
				for off := 0; off+inputEventSize <= n; off += inputEventSize {
					ev, _ := parseInputEvent(buf[off : off+inputEventSize])
					btn := clickButton(ev)
					if btn == 0 {
						continue
					}
					pos, _, ok := bridge.cursor(400 * time.Millisecond)
					if !ok {
						logging.Log("clicktap: button %d, cursor position unavailable", btn)
						continue
					}
					onClick(Click{X: pos[0], Y: pos[1], Button: btn})
				}
			}
		}(f)
	}
	stop := func() {
		for _, f := range files {
			f.Close()
		}
		wg.Wait()
		bridge.close()
	}
	return stop, nil
}

func StartWindowWatch(onFocus func(string), onWindow func(string)) (func(), error) {
	bridge, err := openBridge()
	if err != nil {
		return nil, fmt.Errorf("window watching: %w", err)
	}
	bridge.obj.mu.Lock()
	bridge.obj.onFocus = onFocus
	bridge.obj.onWindow = onWindow
	bridge.obj.mu.Unlock()
	unload, err := bridge.loadScript("recgo-watch", watchScript(bridge))
	if err != nil {
		bridge.close()
		return nil, fmt.Errorf("window watching: %w", err)
	}
	logging.Log("winwatch: KWin script loaded")
	return func() {
		unload()
		bridge.close()
	}, nil
}

func DisplayList() []DisplayInfo {
	bridge, err := openBridge()
	if err != nil {
		return nil
	}
	defer bridge.close()
	unload, err := bridge.loadScript("recgo-screens", screensScript(bridge))
	if err != nil {
		return nil
	}
	defer unload()
	select {
	case raw := <-bridge.obj.screens:
		var screens []struct {
			X, Y, W, H float64
			DPR        float64
			Name       string
		}
		if json.Unmarshal([]byte(raw), &screens) != nil {
			return nil
		}
		var out []DisplayInfo
		for i, s := range screens {
			dpr := s.DPR
			if dpr <= 0 {
				dpr = 1
			}
			out = append(out, DisplayInfo{
				W: int(s.W), H: int(s.H),
				PixelW: int(s.W*dpr + 0.5), PixelH: int(s.H*dpr + 0.5),
				Main: i == 0, Name: s.Name,
			})
		}
		return out
	case <-time.After(time.Second):
		return nil
	}
}

// --- KWin bridge ----------------------------------------------------------

const (
	bridgePath  = "/dev/eordano/recgo"
	bridgeIface = "dev.eordano.recgo.Bridge"
)

type bridgeObj struct {
	mu       sync.Mutex
	onFocus  func(string)
	onWindow func(string)
	cursor   chan cursorReport
	screens  chan string
}

type cursorReport struct {
	x, y    float64
	caption string
}

func (o *bridgeObj) Cursor(x, y, caption string) *dbus.Error {
	fx, _ := strconv.ParseFloat(x, 64)
	fy, _ := strconv.ParseFloat(y, 64)
	select {
	case o.cursor <- cursorReport{fx, fy, caption}:
	default:
	}
	return nil
}

func (o *bridgeObj) Focus(caption, class string) *dbus.Error {
	o.mu.Lock()
	cb := o.onFocus
	o.mu.Unlock()
	if cb != nil {
		cb(windowDesc(caption, class))
	}
	return nil
}

func (o *bridgeObj) Window(caption, class string) *dbus.Error {
	o.mu.Lock()
	cb := o.onWindow
	o.mu.Unlock()
	if cb != nil {
		cb(windowDesc(caption, class))
	}
	return nil
}

func (o *bridgeObj) Screens(raw string) *dbus.Error {
	select {
	case o.screens <- raw:
	default:
	}
	return nil
}

func windowDesc(caption, class string) string {
	caption = strings.TrimSpace(caption)
	class = strings.TrimSpace(class)
	switch {
	case caption == "" && class == "":
		return "(untitled)"
	case class == "" || strings.Contains(strings.ToLower(caption), strings.ToLower(class)):
		return caption
	case caption == "":
		return class
	}
	return class + ": " + caption
}

type kwinBridge struct {
	conn         *dbus.Conn
	name         string
	obj          *bridgeObj
	scripts      sync.Mutex
	cursorMu     sync.Mutex
	cursorScript string
}

var (
	bridgeOnce sync.Mutex
	bridgeRefs int
	bridgeInst *kwinBridge
)

// openBridge shares one exported object between the click tap, the window
// watch and DisplayList; the last close drops the bus name.
func openBridge() (*kwinBridge, error) {
	bridgeOnce.Lock()
	defer bridgeOnce.Unlock()
	if bridgeInst != nil {
		bridgeRefs++
		return bridgeInst, nil
	}
	conn, err := dbus.ConnectSessionBus()
	if err != nil {
		return nil, fmt.Errorf("session bus: %w", err)
	}
	if !KWinAvailable(conn) {
		conn.Close()
		return nil, fmt.Errorf("KWin is not on the session bus (only the KDE Plasma session is supported)")
	}
	obj := &bridgeObj{cursor: make(chan cursorReport, 1), screens: make(chan string, 1)}
	if err := conn.Export(obj, bridgePath, bridgeIface); err != nil {
		conn.Close()
		return nil, fmt.Errorf("export: %w", err)
	}
	name := fmt.Sprintf("dev.eordano.recgo.p%d", os.Getpid())
	reply, err := conn.RequestName(name, dbus.NameFlagDoNotQueue)
	if err != nil || reply != dbus.RequestNameReplyPrimaryOwner {
		conn.Close()
		return nil, fmt.Errorf("request bus name %s: %v", name, err)
	}
	bridgeInst = &kwinBridge{conn: conn, name: name, obj: obj}
	bridgeRefs = 1
	return bridgeInst, nil
}

func (b *kwinBridge) close() {
	bridgeOnce.Lock()
	defer bridgeOnce.Unlock()
	bridgeRefs--
	if bridgeRefs > 0 {
		return
	}
	_, _ = b.conn.ReleaseName(b.name)
	_ = b.conn.Export(nil, bridgePath, bridgeIface)
	_ = b.conn.Close()
	bridgeInst = nil
}

func (b *kwinBridge) call(kind, path string, args ...any) string {
	return fmt.Sprintf(`callDBus(%q, %q, %q, %q%s)`, b.name, bridgePath, bridgeIface, kind, joinArgs(path, args))
}

func joinArgs(first string, rest []any) string {
	parts := []string{first}
	for _, a := range rest {
		parts = append(parts, fmt.Sprint(a))
	}
	return ", " + strings.Join(parts, ", ")
}

func (b *kwinBridge) scriptFile(plugin string) string {
	base := os.Getenv("XDG_RUNTIME_DIR")
	if base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, plugin+".kwin.js")
}

// loadScript writes body to the runtime dir, loads it into KWin under plugin
// (replacing a stale copy from a crashed run) and starts it; the returned
// func unloads it.
func (b *kwinBridge) loadScript(plugin, body string) (func(), error) {
	b.scripts.Lock()
	defer b.scripts.Unlock()
	path := b.scriptFile(plugin)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		return nil, err
	}
	scripting := b.conn.Object("org.kde.KWin", "/Scripting")
	var loaded bool
	if scripting.Call("org.kde.kwin.Scripting.isScriptLoaded", 0, plugin).Store(&loaded) == nil && loaded {
		_ = scripting.Call("org.kde.kwin.Scripting.unloadScript", 0, plugin).Err
	}
	var id int32
	if err := scripting.Call("org.kde.kwin.Scripting.loadScript", 0, path, plugin).Store(&id); err != nil {
		return nil, fmt.Errorf("kwin loadScript: %w", err)
	}
	if id < 0 {
		return nil, fmt.Errorf("kwin refused script %s", plugin)
	}
	if err := scripting.Call("org.kde.kwin.Scripting.start", 0).Err; err != nil {
		return nil, fmt.Errorf("kwin start: %w", err)
	}
	return func() {
		b.scripts.Lock()
		defer b.scripts.Unlock()
		_ = scripting.Call("org.kde.kwin.Scripting.unloadScript", 0, plugin).Err
		_ = os.Remove(path)
	}, nil
}

// cursor loads the one-shot script and waits for its callback: the pointer
// position in workspace coordinates and the active window's caption.
func (b *kwinBridge) cursor(timeout time.Duration) ([2]float64, string, bool) {
	b.cursorMu.Lock()
	defer b.cursorMu.Unlock()
	for len(b.obj.cursor) > 0 {
		<-b.obj.cursor
	}
	unload, err := b.loadScript("recgo-cursor", cursorScript(b))
	if err != nil {
		logging.Log("clicktap: %v", err)
		return [2]float64{}, "", false
	}
	defer unload()
	select {
	case r := <-b.obj.cursor:
		return [2]float64{r.x, r.y}, r.caption, true
	case <-time.After(timeout):
		return [2]float64{}, "", false
	}
}

func cursorScript(b *kwinBridge) string {
	return `var p = workspace.cursorPos;
var w = workspace.activeWindow;
` + b.call("Cursor", "String(p.x)", "String(p.y)", "w && w.caption ? String(w.caption) : \"\"") + ";\n"
}

func watchScript(b *kwinBridge) string {
	return `function own(w) { return w && w.caption && (w.caption.indexOf("Recgo HUD") === 0 || w.caption.indexOf("Recgo Live") === 0); }
function report(kind, w) {
    if (!w || own(w)) return;
    if (!(w.normalWindow || w.dialog || w.utility)) return;
    var caption = w.caption ? String(w.caption) : "";
    var cls = w.resourceClass ? String(w.resourceClass) : "";
    if (kind === "Focus") { ` + b.call("Focus", "caption", "cls") + `; }
    else { ` + b.call("Window", "caption", "cls") + `; }
}
workspace.windowActivated.connect(function (w) { report("Focus", w); });
workspace.windowAdded.connect(function (w) { report("Window", w); });
`
}

func screensScript(b *kwinBridge) string {
	return `var out = [];
workspace.screens.forEach(function (s) {
    var g = s.geometry;
    out.push({ Name: String(s.name), X: g.x, Y: g.y, W: g.width, H: g.height, DPR: s.devicePixelRatio });
});
` + b.call("Screens", "JSON.stringify(out)") + ";\n"
}
