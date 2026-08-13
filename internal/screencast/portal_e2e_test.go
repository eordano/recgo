package screencast

import (
	"os"
	"testing"
	"time"
)

func TestOpenAgainstLivePortal(t *testing.T) {
	if os.Getenv("RECGO_PORTAL_E2E") == "" {
		t.Skip("set RECGO_PORTAL_E2E=1 inside a session with a ScreenCast portal")
	}

	sess, err := Open(Options{
		Types:   SourceMonitor,
		Cursor:  CursorHidden,
		Persist: PersistUntilRevoked,
		Timeout: 30 * time.Second,
	})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer sess.Close()

	if len(sess.Streams) == 0 {
		t.Fatal("portal granted no streams")
	}
	for i, s := range sess.Streams {
		t.Logf("stream %d: node=%d size=%dx%d type=%d", i, s.NodeID, s.W, s.H, s.Type)
		if s.NodeID == 0 {
			t.Errorf("stream %d has node id 0", i)
		}
	}

	if sess.RestoreToken == "" {
		t.Logf("WARNING: no restore token; every run will re-prompt on backends that ask")
	} else {
		t.Logf("restore token issued (%d chars)", len(sess.RestoreToken))
	}

	fd, err := sess.OpenPipeWireRemote()
	if err != nil {
		t.Fatalf("OpenPipeWireRemote: %v", err)
	}
	defer fd.Close()
	if fd.Fd() == 0 {
		t.Error("got fd 0 for the PipeWire remote")
	}
	t.Logf("pipewire remote fd=%d", fd.Fd())
}

func TestRestoreTokenSuppressesThePrompt(t *testing.T) {
	if os.Getenv("RECGO_PORTAL_E2E") == "" {
		t.Skip("set RECGO_PORTAL_E2E=1 inside a session with a ScreenCast portal")
	}

	first, err := Open(Options{Types: SourceMonitor, Cursor: CursorHidden,
		Persist: PersistUntilRevoked, Timeout: 30 * time.Second})
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}
	tok := first.RestoreToken
	first.Close()

	if tok == "" {
		t.Skip("backend issued no restore token; nothing to verify")
	}

	second, err := Open(Options{Types: SourceMonitor, Cursor: CursorHidden,
		Persist: PersistUntilRevoked, RestoreToken: tok,
		Timeout: 15 * time.Second})
	if err != nil {
		t.Fatalf("second Open with a restore token: %v", err)
	}
	defer second.Close()

	if len(second.Streams) == 0 {
		t.Error("restored session granted no streams")
	}
}
