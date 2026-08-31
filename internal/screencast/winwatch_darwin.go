//go:build darwin && cgo

package screencast

/*
#cgo LDFLAGS: -framework CoreGraphics -framework CoreFoundation
#include <CoreGraphics/CoreGraphics.h>

typedef struct {
	uint32_t number;
	int layer;
	int w, h;
	char owner[128];
	char title[256];
} recgoWin;

static void recgoCopyStr(CFDictionaryRef d, CFStringRef key, char *dst, size_t n) {
	dst[0] = 0;
	CFStringRef s = CFDictionaryGetValue(d, key);
	if (s) CFStringGetCString(s, dst, n, kCFStringEncodingUTF8);
}

static int recgoWindowList(recgoWin *out, int max) {
	CFArrayRef list = CGWindowListCopyWindowInfo(
		kCGWindowListOptionOnScreenOnly | kCGWindowListExcludeDesktopElements, kCGNullWindowID);
	if (!list) return 0;
	int n = 0;
	for (CFIndex i = 0; i < CFArrayGetCount(list) && n < max; i++) {
		CFDictionaryRef d = CFArrayGetValueAtIndex(list, i);
		recgoWin *w = &out[n];
		w->layer = 0;
		w->number = 0;
		CFNumberRef num = CFDictionaryGetValue(d, kCGWindowLayer);
		if (num) CFNumberGetValue(num, kCFNumberIntType, &w->layer);
		num = CFDictionaryGetValue(d, kCGWindowNumber);
		if (num) CFNumberGetValue(num, kCFNumberSInt32Type, &w->number);
		CGRect bounds = CGRectZero;
		CFDictionaryRef b = CFDictionaryGetValue(d, kCGWindowBounds);
		if (b) CGRectMakeWithDictionaryRepresentation(b, &bounds);
		w->w = (int)bounds.size.width;
		w->h = (int)bounds.size.height;
		recgoCopyStr(d, kCGWindowOwnerName, w->owner, sizeof(w->owner));
		recgoCopyStr(d, kCGWindowName, w->title, sizeof(w->title));
		n++;
	}
	CFRelease(list);
	return n;
}
*/
import "C"

import (
	"fmt"
	"time"
)

const (
	winPollInterval = 400 * time.Millisecond
	// Sheets and alerts sit at NSModalPanelWindowLevel (8); anything higher
	// is menus, tooltips and status items -- noise, not modals.
	winMaxLayer = 8
	// Popovers and 1px overlay windows churn constantly; a dialog a person
	// can read is bigger than this.
	winMinSize = 50
)

type winSnap struct {
	frontKey  string
	frontDesc string
	known     map[uint32]bool
	newDescs  map[uint32]string
}

func snapshotWindows() winSnap {
	var raw [256]C.recgoWin
	n := int(C.recgoWindowList(&raw[0], 256))
	s := winSnap{known: map[uint32]bool{}, newDescs: map[uint32]string{}}
	for i := 0; i < n; i++ {
		w := raw[i]
		if int(w.layer) < 0 || int(w.layer) > winMaxLayer ||
			int(w.w) < winMinSize || int(w.h) < winMinSize {
			continue
		}
		owner := C.GoString(&w.owner[0])
		title := C.GoString(&w.title[0])
		desc := owner
		if title != "" && title != owner {
			if desc != "" {
				desc += " — " + title
			} else {
				desc = title
			}
		}
		num := uint32(w.number)
		s.known[num] = true
		s.newDescs[num] = desc
		if s.frontKey == "" {
			s.frontKey = fmt.Sprintf("%d|%s", num, desc)
			s.frontDesc = desc
		}
	}
	return s
}

// StartWindowWatch polls the window list and reports two things: the focused
// window changing (a different window in front, or the front window's title
// changing -- which is what a browser tab switch looks like from outside),
// and a new window or dialog appearing. A change must survive two polls
// before it is reported, so a title that flickers mid-update is not an
// event. Window titles come through the Screen Recording grant the recorder
// already holds; no extra permission is needed.
func StartWindowWatch(onFocus func(desc string), onWindow func(desc string)) (func(), error) {
	base := snapshotWindows()
	if len(base.known) == 0 {
		return nil, fmt.Errorf("no windows visible — is Screen Recording granted?")
	}

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		lastFront := base.frontKey
		known := base.known
		var pendFront string
		pendNew := map[uint32]string{}
		ticker := time.NewTicker(winPollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
			}
			s := snapshotWindows()
			if len(s.known) == 0 {
				continue
			}

			for num, desc := range s.newDescs {
				if known[num] {
					continue
				}
				if _, seen := pendNew[num]; seen {
					known[num] = true
					delete(pendNew, num)
					onWindow(desc)
					if s.frontKey == fmt.Sprintf("%d|%s", num, desc) {
						lastFront = s.frontKey
						pendFront = ""
					}
				} else {
					pendNew[num] = desc
				}
			}
			for num := range pendNew {
				if !s.known[num] {
					delete(pendNew, num)
				}
			}
			for num := range known {
				if !s.known[num] {
					delete(known, num)
				}
			}

			switch {
			case s.frontKey == lastFront:
				pendFront = ""
			case s.frontKey == pendFront:
				lastFront = s.frontKey
				pendFront = ""
				onFocus(s.frontDesc)
			default:
				pendFront = s.frontKey
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
