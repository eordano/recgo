//go:build linux

package screencast

import (
	"os"
	"testing"
	"time"
)

// RECGO_KWIN_E2E=1 go test ./internal/screencast -run KWinBridge -v
// Needs a live Plasma session; move the pointer / switch windows / click.
func TestKWinBridgeEndToEnd(t *testing.T) {
	if os.Getenv("RECGO_KWIN_E2E") == "" {
		t.Skip("set RECGO_KWIN_E2E=1 inside a Plasma session")
	}
	t.Logf("displays: %+v", DisplayList())
	b, err := openBridge()
	if err != nil {
		t.Fatal(err)
	}
	pos, caption, ok := b.cursor(2 * time.Second)
	b.close()
	if !ok {
		t.Fatal("no cursor report from KWin")
	}
	t.Logf("cursor at %v over %q", pos, caption)

	events := make(chan string, 16)
	stopWatch, err := StartWindowWatch(
		func(d string) { events <- "focus " + d },
		func(d string) { events <- "window " + d })
	if err != nil {
		t.Fatal(err)
	}
	defer stopWatch()
	clicks := make(chan Click, 16)
	stopTap, err := StartClickTap(func(c Click) { clicks <- c })
	if err != nil {
		t.Fatal(err)
	}
	defer stopTap()
	deadline := time.After(20 * time.Second)
	gotClick := false
	for !gotClick {
		select {
		case e := <-events:
			t.Log(e)
		case c := <-clicks:
			t.Logf("click %+v", c)
			gotClick = true
		case <-deadline:
			t.Log("no click within 20s (nobody clicked); bridge itself works")
			return
		}
	}
}
