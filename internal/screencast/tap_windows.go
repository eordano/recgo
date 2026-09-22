//go:build windows

package screencast

import (
	"fmt"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/eordano/recgo/internal/logging"
)

// Windows click and window capture. Clicks come from a low-level mouse
// hook (WH_MOUSE_LL), which any process may install without a grant; it
// runs on a thread of ours that pumps messages, and the point it carries is
// already in screen pixels. Focus and new windows have no hook of that
// kind, so the foreground window and the top-level window list are polled.

const (
	whMouseLL     = 14
	wmLButtonDown = 0x0201
	wmRButtonDown = 0x0204
	wmMButtonDown = 0x0207
	wmQuit        = 0x0012
	pmNoRemove    = 0

	focusPollInterval  = 200 * time.Millisecond
	windowPollInterval = 500 * time.Millisecond
	// A dialog a person can read is bigger than this; 1px overlay windows
	// and tooltips churn constantly.
	winMinSize = 50
)

type msllHookStruct struct {
	Pt        point
	MouseData uint32
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}

type msg struct {
	Hwnd     uintptr
	Message  uint32
	WParam   uintptr
	LParam   uintptr
	Time     uint32
	Pt       point
	LPrivate uint32
}

var (
	clickMu   sync.Mutex
	clickChan chan Click
	hookCB    uintptr
	hookOnce  sync.Once
)

// hookButton maps the hook's message to a Click button; 0 is not a press.
func hookButton(wParam uintptr) int {
	switch wParam {
	case wmLButtonDown:
		return 1
	case wmRButtonDown:
		return 2
	case wmMButtonDown:
		return 3
	}
	return 0
}

// mouseHook must return within the low-level hook timeout or Windows
// silently removes the hook, so it only queues the click.
func mouseHook(nCode, wParam uintptr, ev *msllHookStruct) uintptr {
	if int32(nCode) >= 0 && ev != nil {
		if btn := hookButton(wParam); btn != 0 {
			c := Click{X: float64(ev.Pt.X), Y: float64(ev.Pt.Y), Button: btn}
			clickMu.Lock()
			ch := clickChan
			clickMu.Unlock()
			if ch != nil {
				select {
				case ch <- c:
				default:
				}
			}
		}
	}
	r, _, _ := pCallNextHookEx.Call(0, nCode, wParam, uintptr(unsafe.Pointer(ev)))
	return r
}

// StartClickTap installs a system-wide mouse-down hook and calls onClick
// for every press, in order, off the hook thread. One tap per process; the
// returned stop function tears it down. Coordinates are physical pixels.
func StartClickTap(onClick func(Click)) (func(), error) {
	ensureDPIAware()
	hookOnce.Do(func() { hookCB = syscall.NewCallback(mouseHook) })

	clickMu.Lock()
	if clickChan != nil {
		clickMu.Unlock()
		return nil, fmt.Errorf("a click tap is already running")
	}
	ch := make(chan Click, 64)
	clickChan = ch
	clickMu.Unlock()

	created := make(chan error, 1)
	var tid uint32
	pumpDone := make(chan struct{})
	go func() {
		// The hook is delivered on the thread that installed it, while
		// that thread sits in GetMessageW; it must be one OS thread.
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer close(pumpDone)
		hook, _, err := pSetWindowsHookExW.Call(whMouseLL, hookCB, 0, 0)
		if hook == 0 {
			created <- fmt.Errorf("SetWindowsHookEx(WH_MOUSE_LL): %v", err)
			return
		}
		t, _, _ := pGetCurrentThreadId.Call()
		tid = uint32(t)
		// PostThreadMessage needs the queue to exist; PeekMessage creates it.
		var m msg
		pPeekMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0, pmNoRemove)
		created <- nil
		for {
			r, _, _ := pGetMessageW.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
			if r == 0 || int32(r) == -1 {
				break
			}
			pDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
		}
		pUnhookWindowsHookEx.Call(hook)
	}()

	if err := <-created; err != nil {
		clickMu.Lock()
		clickChan = nil
		clickMu.Unlock()
		return nil, err
	}
	logging.Log("clicktap: WH_MOUSE_LL hook installed on thread %d", tid)

	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for c := range ch {
			onClick(c)
		}
	}()

	var once sync.Once
	stop := func() {
		once.Do(func() {
			pPostThreadMessageW.Call(uintptr(tid), wmQuit, 0, 0)
			<-pumpDone
			clickMu.Lock()
			clickChan = nil
			clickMu.Unlock()
			close(ch)
			<-drained
		})
	}
	return stop, nil
}

// --- window watching -------------------------------------------------------

var (
	enumWindowsCB   uintptr
	enumWindowsOnce sync.Once
	enumWinMu       sync.Mutex
	enumWinOut      []uintptr
)

func windowsCallback(hwnd, lparam uintptr) uintptr {
	enumWinOut = append(enumWinOut, hwnd)
	return 1
}

func topLevelWindows() []uintptr {
	enumWindowsOnce.Do(func() { enumWindowsCB = syscall.NewCallback(windowsCallback) })
	enumWinMu.Lock()
	defer enumWinMu.Unlock()
	enumWinOut = nil
	pEnumWindows.Call(enumWindowsCB, 0)
	out := make([]uintptr, len(enumWinOut))
	copy(out, enumWinOut)
	return out
}

func ownWindow(title string) bool {
	return strings.HasPrefix(title, "Recgo HUD") || strings.HasPrefix(title, "Recgo Live")
}

// reportable says whether a window is one a person would notice appear or
// come to the front: visible, titled, not a tool window, not ours, and big
// enough to be read.
func reportable(hwnd uintptr, self uint32) (string, bool) {
	if v, _, _ := pIsWindowVisible.Call(hwnd); v == 0 {
		return "", false
	}
	if ex, _, _ := pGetWindowLongPtrW.Call(hwnd, i32(gwlExStyle)); ex&wsExToolWindow != 0 {
		return "", false
	}
	if windowPID(hwnd) == self {
		return "", false
	}
	title := windowText(hwnd)
	if title == "" || ownWindow(title) {
		return "", false
	}
	if r := windowRect(hwnd); r.w() < winMinSize || r.h() < winMinSize {
		return "", false
	}
	return windowDesc(title, windowClass(hwnd)), true
}

// StartWindowWatch polls two things: the foreground window every 200ms
// (a different window, or the front window's title changing -- which is
// what a browser tab switch looks like from outside), and the top-level
// window list every 500ms for windows that were not there before. A change
// must survive two polls before it is reported, so a title that flickers
// mid-update is not an event.
func StartWindowWatch(onFocus func(desc string), onWindow func(desc string)) (func(), error) {
	self32, _, _ := pGetCurrentProcessId.Call()
	self := uint32(self32)
	known := map[uintptr]bool{}
	for _, h := range topLevelWindows() {
		known[h] = true
	}
	if len(known) == 0 {
		return nil, fmt.Errorf("no windows visible — is this an interactive session?")
	}
	logging.Log("winwatch: polling the foreground window and %d top-level windows", len(known))

	frontKey := func() (string, string) {
		h, _, _ := pGetForegroundWindow.Call()
		if h == 0 {
			return "", ""
		}
		desc, ok := reportable(h, self)
		if !ok {
			return "", ""
		}
		return fmt.Sprintf("%x|%s", h, desc), desc
	}

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		lastFront, _ := frontKey()
		var pendFront string
		pendNew := map[uintptr]string{}
		focusTick := time.NewTicker(focusPollInterval)
		windowTick := time.NewTicker(windowPollInterval)
		defer focusTick.Stop()
		defer windowTick.Stop()
		for {
			select {
			case <-stop:
				return
			case <-focusTick.C:
				key, desc := frontKey()
				switch {
				case key == "" || key == lastFront:
					pendFront = ""
				case key == pendFront:
					lastFront, pendFront = key, ""
					onFocus(desc)
				default:
					pendFront = key
				}
			case <-windowTick.C:
				present := map[uintptr]bool{}
				for _, h := range topLevelWindows() {
					present[h] = true
					if known[h] {
						continue
					}
					desc, ok := reportable(h, self)
					if !ok {
						continue
					}
					if _, seen := pendNew[h]; seen {
						known[h] = true
						delete(pendNew, h)
						onWindow(desc)
						if k, _ := frontKey(); k == fmt.Sprintf("%x|%s", h, desc) {
							lastFront, pendFront = k, ""
						}
					} else {
						pendNew[h] = desc
					}
				}
				for h := range pendNew {
					if !present[h] {
						delete(pendNew, h)
					}
				}
				for h := range known {
					if !present[h] {
						delete(known, h)
					}
				}
			}
		}
	}()

	var stopped bool
	return func() {
		if !stopped {
			stopped = true
			close(stop)
			<-done
		}
	}, nil
}
