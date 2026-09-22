package tab

import (
	"path/filepath"
	"testing"
)

func TestChromiumCandidatesOrderAndSkipsUnsetRoots(t *testing.T) {
	got := chromiumCandidates(`C:\Program Files`, "", `C:\Users\u\AppData\Local`)
	if len(got) != 8 {
		t.Fatalf("got %d candidates, want 8 (two roots × four browsers): %v", len(got), got)
	}
	if want := filepath.Join(`C:\Program Files`, "Chromium", "Application", "chrome.exe"); got[0] != want {
		t.Errorf("first candidate = %q, want %q", got[0], want)
	}
	if want := filepath.Join(`C:\Users\u\AppData\Local`, "Microsoft", "Edge", "Application", "msedge.exe"); got[7] != want {
		t.Errorf("last candidate = %q, want %q", got[7], want)
	}
	if got := chromiumCandidates("", "", ""); len(got) != 0 {
		t.Errorf("no roots should give no candidates, got %v", got)
	}
}
