package screencast

import (
	"strings"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

func TestRequestPathMatchesPortalConvention(t *testing.T) {
	got := requestPath(":1.234", "recgo_abcd")
	want := dbus.ObjectPath("/org/freedesktop/portal/desktop/request/1_234/recgo_abcd")
	if got != want {
		t.Errorf("requestPath = %q, want %q", got, want)
	}

	if got := requestPath(":1.2.3", "t"); got != "/org/freedesktop/portal/desktop/request/1_2_3/t" {
		t.Errorf("multi-dot: %q", got)
	}
	if got := requestPath("1.5", "t"); got != "/org/freedesktop/portal/desktop/request/1_5/t" {
		t.Errorf("no colon: %q", got)
	}

	if !requestPath(":1.234", "recgo_abcd").IsValid() {
		t.Error("derived path is not a valid D-Bus object path")
	}
}

func TestTokensAreUniqueAndPathSafe(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 500; i++ {
		tok := token()
		if seen[tok] {
			t.Fatalf("duplicate token %q — two concurrent calls would cross wires", tok)
		}
		seen[tok] = true

		for _, r := range tok {
			isOK := r == '_' ||
				(r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
			if !isOK {
				t.Fatalf("token %q contains %q, which is illegal in an object path", tok, r)
			}
		}
		if !requestPath(":1.1", tok).IsValid() {
			t.Fatalf("token %q yields an invalid path", tok)
		}
	}
}

func TestOptionDefaults(t *testing.T) {
	var o Options
	o.withDefaults()

	if o.Types&SourceMonitor == 0 || o.Types&SourceWindow == 0 {
		t.Errorf("types = %d, want monitor|window", o.Types)
	}
	if o.Cursor != CursorEmbedded {
		t.Errorf("cursor = %d, want embedded", o.Cursor)
	}
	if o.Persist != PersistUntilRevoked {
		t.Errorf("persist = %d, want until-revoked", o.Persist)
	}
	if o.Timeout < 30*time.Second {
		t.Errorf("timeout %s is too short for a human to answer a dialog", o.Timeout)
	}
}

func TestExplicitOptionsSurviveDefaults(t *testing.T) {
	o := Options{
		Types:   SourceWindow,
		Cursor:  CursorHidden,
		Persist: PersistWhileRunning,
		Timeout: time.Minute,
	}
	o.withDefaults()

	if o.Types != SourceWindow || o.Cursor != CursorHidden ||
		o.Persist != PersistWhileRunning || o.Timeout != time.Minute {
		t.Errorf("defaults clobbered explicit options: %+v", o)
	}
}

func TestResponseErrorDistinguishesCancelFromFailure(t *testing.T) {
	if err := responseError("Start", 0); err != nil {
		t.Errorf("code 0 should succeed, got %v", err)
	}
	err := responseError("Start", 1)
	if err == nil || !strings.Contains(err.Error(), "cancelled by the user") {
		t.Errorf("code 1: %v", err)
	}
	err = responseError("Start", 2)
	if err == nil || !strings.Contains(err.Error(), "code 2") {
		t.Errorf("code 2: %v", err)
	}
}

func TestParseStreams(t *testing.T) {
	results := map[string]dbus.Variant{
		"streams": dbus.MakeVariant([]struct {
			Node  uint32
			Props map[string]dbus.Variant
		}{
			{Node: 42, Props: map[string]dbus.Variant{
				"size":        dbus.MakeVariant([]int32{1920, 1080}),
				"source_type": dbus.MakeVariant(uint32(SourceMonitor)),
			}},
			{Node: 43, Props: map[string]dbus.Variant{}},
		}),
	}

	got := parseStreams(results)
	if len(got) != 2 {
		t.Fatalf("parsed %d streams, want 2", len(got))
	}
	if got[0].NodeID != 42 || got[0].W != 1920 || got[0].H != 1080 {
		t.Errorf("stream 0 = %+v", got[0])
	}
	if got[0].Type != SourceMonitor {
		t.Errorf("stream 0 type = %d", got[0].Type)
	}
	if got[1].NodeID != 43 {
		t.Errorf("stream 1 = %+v", got[1])
	}
}

func TestParseStreamsHandlesMissingOrWrongShape(t *testing.T) {
	if got := parseStreams(map[string]dbus.Variant{}); got != nil {
		t.Errorf("no streams key should yield nil, got %+v", got)
	}
	bad := map[string]dbus.Variant{"streams": dbus.MakeVariant("not a stream array")}
	if got := parseStreams(bad); got != nil {
		t.Errorf("malformed streams should yield nil, got %+v", got)
	}
}

func TestOpenFailsClearlyWithoutASessionBus(t *testing.T) {
	t.Setenv("DBUS_SESSION_BUS_ADDRESS", "unix:path=/nonexistent/recgo-test-bus")
	if _, err := Open(Options{Timeout: time.Second}); err == nil {
		t.Error("expected a failure with no session bus")
	} else if !strings.Contains(err.Error(), "desktop session") {
		t.Errorf("unhelpful error: %v", err)
	}
}

func TestSessionCloseIsIdempotent(t *testing.T) {
	s := &Session{closed: true}
	if err := s.Close(); err != nil {
		t.Errorf("close on an already-closed session: %v", err)
	}
}
