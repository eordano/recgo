package alt

import (
	"testing"

	"github.com/eordano/recgo/internal/screencast"
)

func TestToUnityFlipsYAndScales(t *testing.T) {
	// A 640x360 client area at (100, 50) showing a 1280x720 app.
	r := screencast.WindowRect{Left: 100, Top: 50, Width: 640, Height: 360}
	ux, uy, in := ToUnity(100, 50, r, 1280, 720)
	if !in || ux != 0 || uy != 720 {
		t.Errorf("top-left corner -> %v,%v %v (want 0,720)", ux, uy, in)
	}
	ux, uy, in = ToUnity(420, 230, r, 1280, 720)
	if !in || ux != 640 || uy != 360 {
		t.Errorf("centre -> %v,%v %v (want 640,360)", ux, uy, in)
	}
	if _, _, in := ToUnity(99, 50, r, 1280, 720); in {
		t.Error("left of the window is outside")
	}
	if _, _, in := ToUnity(740, 410, r, 1280, 720); in {
		t.Error("the far edge is exclusive")
	}
	if _, _, in := ToUnity(1, 1, screencast.WindowRect{}, 1280, 720); in {
		t.Error("an empty rect contains nothing")
	}
}

func TestClicksResolveInsideOutsideAndFullscreen(t *testing.T) {
	s := newServer(t, Options{})
	app := dialApp(t, s, "Decentraland", true)
	waitFor(t, "app", func() bool { return s.Driver().Connected() })

	game := screencast.WindowRect{Title: "Decentraland", Class: "decentraland", Left: 100, Top: 50, Width: 640, Height: 360}
	konsole := screencast.WindowRect{Title: "~ — Konsole", Class: "konsole", Left: 0, Top: 0, Width: 100, Height: 800}
	var under screencast.WindowRect
	var found bool
	c := &Clicks{
		Driver: s.Driver(), Resolver: NewResolver(s.Driver(), DefaultProbe),
		WindowAt: func(x, y float64) (screencast.WindowRect, bool) { return under, found },
		Displays: func() []screencast.DisplayInfo { return nil },
	}
	c.SetAppName("Decentraland")

	under, found = game, true
	el, note := c.Resolve(420, 230) // centre -> Unity 640,360 -> probe: ugui Play
	if el == nil || el.Selector != "/Canvas/Play" || note != "" {
		t.Fatalf("click inside = %+v %q", el, note)
	}
	probe := app.seen("callComponentMethodForObject")
	if len(probe) != 1 || js(probe[0]["parameters"]) != `["640","360"]` {
		t.Fatalf("Unity coordinates sent = %v", probe)
	}

	under, found = konsole, true
	if el, note := c.Resolve(50, 50); el != nil || note != "outside ~ — Konsole" {
		t.Fatalf("click on another window = %+v %q", el, note)
	}
	if n := len(app.seen("callComponentMethodForObject")); n != 1 {
		t.Fatalf("another window must not be probed (%d probes)", n)
	}

	// Screen size is asked once per window size.
	if n := len(app.seen("getApplicationScreenSize")); n != 1 {
		t.Fatalf("getApplicationScreenSize calls = %d, want 1", n)
	}
	under, found = game, true
	under.Width, under.Height = 1280, 720
	if el, _ := c.Resolve(740, 410); el == nil || el.Selector != "/Canvas/Play" {
		t.Fatalf("after resize = %+v", el)
	}
	if n := len(app.seen("getApplicationScreenSize")); n != 2 {
		t.Fatalf("getApplicationScreenSize calls after a resize = %d, want 2", n)
	}

	// No compositor answer and no display list: the app is assumed to fill
	// the screen at its own size.
	found = false
	el, note = c.Resolve(640, 360)
	if el == nil || note != "" {
		t.Fatalf("fullscreen assumption = %+v %q", el, note)
	}
	probe = app.seen("callComponentMethodForObject")
	if last := probe[len(probe)-1]; js(last["parameters"]) != `["640","360"]` {
		t.Fatalf("fullscreen coordinates = %s", js(last["parameters"]))
	}
	if n := len(app.seen("getApplicationScreenSize")); n != 3 {
		t.Fatalf("getApplicationScreenSize calls on the fullscreen path = %d, want 3 (one, not two)", n)
	}
}

func TestClicksWithoutACompositorUseDisplayPoints(t *testing.T) {
	s := newServer(t, Options{})
	app := dialApp(t, s, "__default__", true)
	waitFor(t, "app", func() bool { return s.Driver().Connected() })

	// A Retina display: 1440x900 points, 2880x1800 pixels; the app reports
	// 1280x720. Clicks arrive in points, so the centre of the display is the
	// centre of the app.
	c := &Clicks{
		Driver: s.Driver(), Resolver: NewResolver(s.Driver(), DefaultProbe),
		Displays: func() []screencast.DisplayInfo {
			return []screencast.DisplayInfo{
				{W: 1024, H: 768, PixelW: 1024, PixelH: 768},
				{W: 1440, H: 900, PixelW: 2880, PixelH: 1800, Main: true},
			}
		},
	}
	el, note := c.Resolve(720, 450)
	if el == nil || el.Selector != "/Canvas/Play" || note != "" {
		t.Fatalf("centre of the display = %+v %q", el, note)
	}
	probe := app.seen("callComponentMethodForObject")
	if len(probe) != 1 || js(probe[0]["parameters"]) != `["640","360"]` {
		t.Fatalf("Unity coordinates sent = %v", probe)
	}
	if _, note := c.Resolve(1500, 100); note != "outside (untitled)" {
		t.Fatalf("beyond the display = %q", note)
	}
	if n := len(app.seen("getApplicationScreenSize")); n != 1 {
		t.Fatalf("getApplicationScreenSize calls = %d, want 1", n)
	}
}

func TestClicksLockOnlyOnACorroboratedWindow(t *testing.T) {
	s := newServer(t, Options{})
	app := dialApp(t, s, "__default__", true)
	waitFor(t, "app", func() bool { return s.Driver().Connected() })

	// The explorer dials as __default__, which names no window. The
	// editor's client rect maps (800,400) to Unity (1024,360), where the
	// fake answers a hit: that must never be probed, let alone lock the
	// editor as the app.
	unity := screencast.WindowRect{Title: "Explorer", Class: "unity-explorer", Left: 0, Top: 0, Width: 1280, Height: 720}
	editor := screencast.WindowRect{Title: "main.go", Class: "code", Left: 0, Top: 0, Width: 1000, Height: 800}
	under := editor
	c := &Clicks{
		Driver: s.Driver(), Resolver: NewResolver(s.Driver(), DefaultProbe),
		WindowAt: func(x, y float64) (screencast.WindowRect, bool) { return under, true },
	}
	c.SetAppName("__default__")
	if el, note := c.Resolve(800, 400); el != nil || note != "outside main.go" {
		t.Fatalf("uncorroborated window = %+v %q", el, note)
	}
	if n := len(app.seen("callComponentMethodForObject")); n != 0 {
		t.Fatalf("the editor was probed %d times", n)
	}
	// The Unity window's client size equals Screen.width x Screen.height:
	// that corroborates it and takes the lock.
	under = unity
	if el, note := c.Resolve(640, 360); el == nil || note != "" {
		t.Fatalf("size-corroborated window = %+v %q", el, note)
	}
	under = editor
	if el, note := c.Resolve(640, 360); el != nil || note != "outside main.go" {
		t.Fatalf("after the lock, the editor is outside: %+v %q", el, note)
	}
	// A same-sized foreign window is outside once the app is locked.
	under = screencast.WindowRect{Title: "~ — Konsole", Class: "konsole", Width: 1280, Height: 720}
	if el, note := c.Resolve(640, 360); el != nil || note != "outside ~ — Konsole" {
		t.Fatalf("same-sized window after the lock: %+v %q", el, note)
	}
	if n := len(app.seen("callComponentMethodForObject")); n != 1 {
		t.Fatalf("probes = %d, want 1 (the Unity window only)", n)
	}
}

func TestClicksWindowMatchNamesTheAppWindow(t *testing.T) {
	s := newServer(t, Options{})
	app := dialApp(t, s, "__default__", true)
	waitFor(t, "app", func() bool { return s.Driver().Connected() })

	// --alt-window decentraland: the title names the window even when its
	// client size (a scaled-down window) differs from the app's screen size.
	game := screencast.WindowRect{Title: "Decentraland", Class: "decentraland", Left: 100, Top: 50, Width: 640, Height: 360}
	code := screencast.WindowRect{Title: "clicks.go", Class: "code", Width: 640, Height: 360}
	under := code
	c := &Clicks{
		Driver: s.Driver(), Resolver: NewResolver(s.Driver(), DefaultProbe),
		WindowAt: func(x, y float64) (screencast.WindowRect, bool) { return under, true },
	}
	c.SetAppName("__default__")
	c.SetWindowMatch("decentraland")
	if el, note := c.Resolve(420, 230); el != nil || note != "outside clicks.go" {
		t.Fatalf("editor = %+v %q", el, note)
	}
	under = game
	if el, note := c.Resolve(420, 230); el == nil || el.Selector != "/Canvas/Play" || note != "" {
		t.Fatalf("named window = %+v %q", el, note)
	}
	// The app came back under a new window (a restart): the name moves the
	// lock instead of reporting the app as outside itself.
	under = screencast.WindowRect{Title: "Decentraland", Class: "decentraland-2", Left: 100, Top: 50, Width: 640, Height: 360}
	if el, note := c.Resolve(420, 230); el == nil || note != "" {
		t.Fatalf("relocked window = %+v %q", el, note)
	}
	under = game
	if el, note := c.Resolve(420, 230); el == nil || note != "" {
		t.Fatalf("named window again = %+v %q", el, note)
	}
	if n := len(app.seen("callComponentMethodForObject")); n != 3 {
		t.Fatalf("probes = %d, want 3", n)
	}
}

// A Windows player's window is UnityWndClass whatever its title: on the
// Windows run the title was "Explorer", so the default --alt-window
// "decentraland" named nothing and the window was only corroborated by its
// client size. The class alone must name it, at any window size.
func TestClicksUnityWindowClassNamesTheAppWindow(t *testing.T) {
	s := newServer(t, Options{})
	app := dialApp(t, s, "__default__", true)
	waitFor(t, "app", func() bool { return s.Driver().Connected() })

	unity := screencast.WindowRect{Title: "Explorer", Class: "UnityWndClass", Left: 100, Top: 50, Width: 640, Height: 360}
	edge := screencast.WindowRect{Title: "127.0.0.1:18765/noquery - Microsoft Edge", Class: "Chrome_WidgetWin_1", Width: 640, Height: 360}
	under := edge
	c := &Clicks{
		Driver: s.Driver(), Resolver: NewResolver(s.Driver(), DefaultProbe),
		WindowAt: func(x, y float64) (screencast.WindowRect, bool) { return under, true },
	}
	c.SetAppName("__default__")
	c.SetWindowMatch("decentraland")
	if el, note := c.Resolve(420, 230); el != nil || note != "outside 127.0.0.1:18765/noquery - Microsoft Edge" {
		t.Fatalf("browser = %+v %q", el, note)
	}
	under = unity
	if el, note := c.Resolve(420, 230); el == nil || el.Selector != "/Canvas/Play" || note != "" {
		t.Fatalf("UnityWndClass window = %+v %q", el, note)
	}
	under = edge
	if el, note := c.Resolve(420, 230); el != nil || note != "outside 127.0.0.1:18765/noquery - Microsoft Edge" {
		t.Fatalf("browser after the lock = %+v %q", el, note)
	}
	if n := len(app.seen("callComponentMethodForObject")); n != 1 {
		t.Fatalf("probes = %d, want 1 (the Unity window only)", n)
	}
	// --alt-window still overrides: an explicit match that names a
	// different window is honoured alongside the class rule, not instead.
	c.SetWindowMatch("chrome_widgetwin")
	if el, note := c.Resolve(420, 230); el == nil || note != "" {
		t.Fatalf("explicit --alt-window match = %+v %q", el, note)
	}
}

func TestClicksWithoutAnApp(t *testing.T) {
	s := newServer(t, Options{})
	c := &Clicks{Driver: s.Driver(), Resolver: NewResolver(s.Driver(), DefaultProbe)}
	if el, note := c.Resolve(10, 10); el != nil || note != "" {
		t.Fatalf("no app: %+v %q", el, note)
	}
}
