//go:build darwin && cgo

package screencast

/*
#cgo LDFLAGS: -framework CoreGraphics -framework CoreFoundation
#include <CoreGraphics/CoreGraphics.h>

extern void recgoClickGo(double x, double y, int button);

static CFMachPortRef      recgoTapPort = NULL;
static CFRunLoopSourceRef recgoTapSrc  = NULL;
static CFRunLoopRef       recgoTapLoop = NULL;

static CGEventRef recgoTapCB(CGEventTapProxy proxy, CGEventType type, CGEventRef event, void *info) {
	if (type == kCGEventTapDisabledByTimeout || type == kCGEventTapDisabledByUserInput) {
		if (recgoTapPort) CGEventTapEnable(recgoTapPort, true);
		return event;
	}
	CGPoint p = CGEventGetLocation(event);
	recgoClickGo(p.x, p.y, type == kCGEventRightMouseDown ? 2 : 1);
	return event;
}

// recgoTapCreate must run on the thread that will call recgoTapRun. Returns
// non-zero when the tap cannot be created, which on a healthy system means
// the process lacks the Accessibility / Input Monitoring TCC grant.
static int recgoTapCreate(void) {
	CGEventMask mask = CGEventMaskBit(kCGEventLeftMouseDown) | CGEventMaskBit(kCGEventRightMouseDown);
	recgoTapPort = CGEventTapCreate(kCGSessionEventTap, kCGHeadInsertEventTap,
		kCGEventTapOptionListenOnly, mask, recgoTapCB, NULL);
	if (!recgoTapPort) return 1;
	recgoTapSrc  = CFMachPortCreateRunLoopSource(kCFAllocatorDefault, recgoTapPort, 0);
	recgoTapLoop = CFRunLoopGetCurrent();
	CFRunLoopAddSource(recgoTapLoop, recgoTapSrc, kCFRunLoopCommonModes);
	CGEventTapEnable(recgoTapPort, true);
	return 0;
}

static void recgoTapRun(void) {
	CFRunLoopRun();
	CFRunLoopRemoveSource(recgoTapLoop, recgoTapSrc, kCFRunLoopCommonModes);
	CFRelease(recgoTapSrc);
	CFRelease(recgoTapPort);
	recgoTapSrc  = NULL;
	recgoTapPort = NULL;
	recgoTapLoop = NULL;
}

static void recgoTapStop(void) {
	if (recgoTapLoop) CFRunLoopStop(recgoTapLoop);
}

typedef struct { int w, h, pw, ph, is_main; } recgoDisplay;

static int recgoDisplays(recgoDisplay *out, int max) {
	CGDirectDisplayID ids[16];
	uint32_t n = 0;
	if (CGGetActiveDisplayList(16, ids, &n) != kCGErrorSuccess) return 0;
	if ((int)n > max) n = (uint32_t)max;
	for (uint32_t i = 0; i < n; i++) {
		CGRect b = CGDisplayBounds(ids[i]);
		out[i].w  = (int)b.size.width;
		out[i].h  = (int)b.size.height;
		out[i].pw = out[i].w;
		out[i].ph = out[i].h;
		CGDisplayModeRef m = CGDisplayCopyDisplayMode(ids[i]);
		if (m) {
			out[i].pw = (int)CGDisplayModeGetPixelWidth(m);
			out[i].ph = (int)CGDisplayModeGetPixelHeight(m);
			CGDisplayModeRelease(m);
		}
		out[i].is_main = CGDisplayIsMain(ids[i]) ? 1 : 0;
	}
	return (int)n;
}
*/
import "C"

import (
	"fmt"
	"runtime"
	"sync"
)

var (
	clickMu sync.Mutex
	clickFn func(Click)
)

// StartClickTap installs a listen-only, system-wide mouse-down tap and calls
// onClick for every press. One tap per process; the returned stop function
// tears it down. Coordinates are global display points (not pixels).
func StartClickTap(onClick func(Click)) (func(), error) {
	clickMu.Lock()
	if clickFn != nil {
		clickMu.Unlock()
		return nil, fmt.Errorf("a click tap is already running")
	}
	clickFn = onClick
	clickMu.Unlock()

	created := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		defer close(done)
		if C.recgoTapCreate() != 0 {
			created <- fmt.Errorf("event tap refused — grant this terminal Accessibility " +
				"(or Input Monitoring) in System Settings > Privacy & Security and relaunch")
			return
		}
		created <- nil
		C.recgoTapRun()
	}()

	if err := <-created; err != nil {
		clickMu.Lock()
		clickFn = nil
		clickMu.Unlock()
		return nil, err
	}

	var once sync.Once
	stop := func() {
		once.Do(func() {
			C.recgoTapStop()
			<-done
			clickMu.Lock()
			clickFn = nil
			clickMu.Unlock()
		})
	}
	return stop, nil
}

// DisplayList reports the attached displays: bounds in points, the active
// mode in pixels, and which one is main.
func DisplayList() []DisplayInfo {
	var raw [16]C.recgoDisplay
	n := int(C.recgoDisplays(&raw[0], 16))
	out := make([]DisplayInfo, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, DisplayInfo{
			W: int(raw[i].w), H: int(raw[i].h),
			PixelW: int(raw[i].pw), PixelH: int(raw[i].ph),
			Main: raw[i].is_main != 0,
		})
	}
	return out
}
