// recgo-meet-watch writes one JSON presence snapshot per poll for the desktop
// apps. Stdout is a protocol stream; it does not trigger recording itself.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/eordano/recgo/internal/meet"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	port := flag.Int("port", 9222, "local Chromium debugging port")
	once := flag.Bool("once", false, "probe once without debounce (diagnostics; never records)")
	flag.Parse()
	if *port < 1 || *port > 65535 {
		fmt.Fprintln(os.Stderr, "invalid port")
		os.Exit(2)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	started := time.Now()
	var tracker meet.Tracker
	enc := json.NewEncoder(os.Stdout)
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		probeCtx, done := context.WithTimeout(ctx, 5*time.Second)
		calls, err := meet.Probe(probeCtx, *port)
		done()
		s := tracker.Update(time.Now(), calls, err == nil)
		if time.Since(started) < 10*time.Second {
			s.Available = false
		}
		if *once {
			s = meet.Snapshot{Available: err == nil, Calls: calls}
		}
		if enc.Encode(s) != nil {
			return
		}
		if *once {
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
