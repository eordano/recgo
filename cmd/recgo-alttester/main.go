// recgo-alttester is recgo-desktop for an instrumented Unity build: every
// click is resolved to the UI element under it through the AltTester SDK,
// with recgo itself playing the server the game dials (no AltTester
// Desktop). See RECGO-ALTTESTER.md.
package main

import (
	"flag"
	"fmt"
	"os"
	"sync/atomic"

	"github.com/eordano/recgo/internal/alt"
	"github.com/eordano/recgo/internal/desktop"
	"github.com/eordano/recgo/internal/screencast"
	"github.com/eordano/recgo/internal/tab"
)

func main() {
	host := flag.String("alt-host", "127.0.0.1", "address the instrumented app dials (its --alttester host:port)")
	port := flag.Int("alt-port", 13000, "port the instrumented app dials (the client's --alttester default)")
	appName := flag.String("alt-app", "", "accept only this appName from the app (default: any)")
	window := flag.String("alt-window", "decentraland",
		"substring of the app window's title or class, the way its window is told from the others when the app dials with the SDK's default appName (a window of class UnityWndClass, any Windows player, is always taken as the app)")
	probeType := flag.String("probe-type", alt.DefaultProbe.Type,
		"static class in the build that answers ElementAtScreenPoint(float, float)")
	probeAssembly := flag.String("probe-assembly", alt.DefaultProbe.Assembly, "assembly that holds --probe-type")

	logf := func(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...) }
	var sess atomic.Pointer[alt.Session]
	desktop.SetExtension(&desktop.Extension{
		Start: func(note func(string)) error {
			s, err := alt.Attach(alt.SessionOptions{
				Host: *host, Port: *port, AppName: *appName, Window: *window,
				Probe:    alt.Probe{Type: *probeType, Assembly: *probeAssembly},
				WindowAt: screencast.WindowAt, Log: logf,
			}, note)
			if err != nil {
				return err
			}
			sess.Store(s)
			logf("alttester: listening on %s -- launch the client with  --alttester %s:%d", s.Addr(), *host, *port)
			logf("alttester: waiting for the app; recording continues without it (clicks stay position-only)")
			return nil
		},
		Stop: func() {
			if s := sess.Load(); s != nil {
				s.Close()
			}
		},
		ResolveClick: func(x, y float64) (*tab.Element, string) {
			if s := sess.Load(); s != nil {
				return s.Resolve(x, y)
			}
			return nil, ""
		},
	})
	desktop.Main("recgo-alttester")
}
