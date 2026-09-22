package alt

import (
	"context"
	"math"
	"strings"
	"sync"
	"time"

	"github.com/eordano/recgo/internal/screencast"
	"github.com/eordano/recgo/internal/tab"
)

// ToUnity converts a click in global points to Unity screen space (pixels,
// origin bottom-left) given the app window's client area and the app's own
// Screen.width x Screen.height. inside is false when the click is not in
// the window.
func ToUnity(x, y float64, r screencast.WindowRect, appW, appH float64) (ux, uy float64, inside bool) {
	if r.Width <= 0 || r.Height <= 0 || !r.Contains(x, y) {
		return 0, 0, false
	}
	sx, sy := appW/r.Width, appH/r.Height
	return (x - r.Left) * sx, (r.Height - (y - r.Top)) * sy, true
}

// Clicks names the Unity element under desktop clicks for one recording:
// it finds the window under the click, converts to Unity screen space and
// asks the Resolver. The app window is recognised by its appName (or the
// window match) in the title or class, or by a client size equal to the
// app's own Screen.width x Screen.height; the first window so corroborated
// is locked as the app, and a click on any other window is reported as
// outside it.
type Clicks struct {
	Driver   *Driver
	Resolver *Resolver
	WindowAt func(x, y float64) (screencast.WindowRect, bool)
	// Displays lists the attached displays (points) for the no-compositor
	// path; nil means screencast.DisplayList.
	Displays func() []screencast.DisplayInfo
	Timeout  time.Duration
	Log      func(format string, args ...any)

	mu       sync.Mutex
	appName  string
	match    string
	appW     float64
	appH     float64
	sizedFor [2]float64
	appIdent string
	display  screencast.WindowRect
	displayd bool
}

// SetAppName is the appName the app dialed with (or --alt-app), used to
// recognise its window by title or class. The SDK's default "__default__"
// names nothing.
func (c *Clicks) SetAppName(name string) {
	c.mu.Lock()
	c.appName = name
	c.mu.Unlock()
}

// SetWindowMatch is a substring of the app window's title or class
// (--alt-window), the way to name the window when the app dials with the
// default appName.
func (c *Clicks) SetWindowMatch(sub string) {
	c.mu.Lock()
	c.match = sub
	c.mu.Unlock()
}

func (c *Clicks) log(format string, args ...any) {
	if c.Log != nil {
		c.Log(format, args...)
	}
}

func windowIdent(r screencast.WindowRect) string {
	if r.Class != "" {
		return r.Class
	}
	return r.Title
}

func windowTitle(r screencast.WindowRect) string {
	switch {
	case r.Title != "":
		return r.Title
	case r.Class != "":
		return r.Class
	}
	return "(untitled)"
}

// unityWindowClasses are the window classes a Unity player registers on
// its own, whatever the product is called, so the app window is known
// without a title match: on Windows every player window is UnityWndClass
// (the Windows run's window was "Explorer" / UnityWndClass, which no
// --alt-window substring matched). Linux players carry no class this
// certain, so there the title rule does the work.
var unityWindowClasses = []string{"unitywndclass"}

// looksLikeApp reports whether the window's title or class names the app,
// or its class is one a Unity player always has.
func (c *Clicks) looksLikeApp(r screencast.WindowRect) bool {
	c.mu.Lock()
	names := []string{c.appName, c.match}
	c.mu.Unlock()
	title, class := strings.ToLower(r.Title), strings.ToLower(r.Class)
	for _, n := range names {
		n = strings.ToLower(strings.TrimSpace(n))
		if n == "" || n == "__default__" {
			continue
		}
		if strings.Contains(title, n) || strings.Contains(class, n) {
			return true
		}
	}
	for _, uc := range unityWindowClasses {
		if class == uc {
			return true
		}
	}
	return false
}

// sameSize is the other corroboration: a windowed Unity build reports
// Screen.width x Screen.height equal to its client area.
func sameSize(r screencast.WindowRect, w, h float64) bool {
	return w > 0 && h > 0 && math.Abs(r.Width-w) < 1 && math.Abs(r.Height-h) < 1
}

// screenSize is Screen.width x Screen.height, refetched when the window's
// client size changes (fullscreen toggles, resizes).
func (c *Clicks) screenSize(ctx context.Context, r screencast.WindowRect) (float64, float64, error) {
	c.mu.Lock()
	if c.appW > 0 && c.sizedFor == [2]float64{r.Width, r.Height} {
		w, h := c.appW, c.appH
		c.mu.Unlock()
		return w, h, nil
	}
	c.mu.Unlock()
	w, h, err := c.Driver.GetApplicationScreenSize(ctx)
	if err != nil {
		return 0, 0, err
	}
	c.mu.Lock()
	c.appW, c.appH, c.sizedFor = w, h, [2]float64{r.Width, r.Height}
	c.mu.Unlock()
	return w, h, nil
}

// fullScreenRect is the rect used when no compositor names the window: the
// app is assumed to fill the main display, whose size in points is what the
// click coordinates are in (the app's pixel size is off by the backing scale
// on HiDPI displays). Without a display list the app's own size is used.
func (c *Clicks) fullScreenRect(ctx context.Context) (screencast.WindowRect, error) {
	c.mu.Lock()
	if !c.displayd {
		c.displayd = true
		list := c.Displays
		if list == nil {
			list = screencast.DisplayList
		}
		c.mu.Unlock()
		displays := list()
		c.mu.Lock()
		for i, d := range displays {
			if d.W > 0 && d.H > 0 && (d.Main || i == 0) {
				c.display = screencast.WindowRect{Width: float64(d.W), Height: float64(d.H)}
				if d.Main {
					break
				}
			}
		}
	}
	rect := c.display
	c.mu.Unlock()
	if rect.Width > 0 {
		return rect, nil
	}
	w, h, err := c.Driver.GetApplicationScreenSize(ctx)
	if err != nil {
		return screencast.WindowRect{}, err
	}
	rect = screencast.WindowRect{Width: w, Height: h}
	c.mu.Lock()
	c.appW, c.appH, c.sizedFor = w, h, [2]float64{w, h}
	c.mu.Unlock()
	return rect, nil
}

// Resolve is the desktop recorder's click resolver: the element under the
// click, plus a note when the click landed outside the app window.
func (c *Clicks) Resolve(x, y float64) (*tab.Element, string) {
	if c.Driver == nil || !c.Driver.Connected() {
		return nil, ""
	}
	timeout := c.Timeout
	if timeout == 0 {
		timeout = 2 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	rect, ok := screencast.WindowRect{}, false
	if c.WindowAt != nil {
		rect, ok = c.WindowAt(x, y)
	}
	if !ok {
		// No compositor answer (sway, a bare X session, macOS): assume the
		// app fills the screen, the usual fullscreen case.
		var err error
		if rect, err = c.fullScreenRect(ctx); err != nil {
			c.log("alt: screen size: %v", err)
			return nil, ""
		}
	} else if outside, err := c.gate(ctx, rect); err != nil {
		c.log("alt: screen size: %v", err)
		return nil, ""
	} else if outside {
		return nil, "outside " + windowTitle(rect)
	}

	appW, appH, err := c.screenSize(ctx, rect)
	if err != nil {
		c.log("alt: screen size: %v", err)
		return nil, ""
	}
	ux, uy, inside := ToUnity(x, y, rect, appW, appH)
	if !inside {
		return nil, "outside " + windowTitle(rect)
	}
	el, err := c.Resolver.ElementAt(ctx, ux, uy)
	if err != nil {
		c.log("alt: element at %.0f,%.0f: %v", ux, uy, err)
		return nil, ""
	}
	return el, ""
}

// gate decides whether a named window is the app: the locked one, one whose
// title or class names the app (which then takes the lock, the app having
// come back under a new window), or, while nothing is locked, one whose
// client size equals the app's screen size. Anything else is outside, and
// is never probed: a click mapped into Unity space from a foreign window
// would name whatever HUD element sits under the mapped point.
func (c *Clicks) gate(ctx context.Context, rect screencast.WindowRect) (outside bool, err error) {
	ident := windowIdent(rect)
	c.mu.Lock()
	locked := c.appIdent
	c.mu.Unlock()
	if locked != "" && locked == ident {
		return false, nil
	}
	if !c.looksLikeApp(rect) {
		if locked != "" {
			return true, nil
		}
		w, h, err := c.screenSize(ctx, rect)
		if err != nil {
			return false, err
		}
		if !sameSize(rect, w, h) {
			return true, nil
		}
	}
	c.mu.Lock()
	c.appIdent = ident
	c.mu.Unlock()
	return false, nil
}
