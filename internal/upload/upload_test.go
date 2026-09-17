package upload

import (
	"testing"

	"github.com/eordano/recgo/internal/config"
)

func TestDisplayDropsTheSSHLogin(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"host:/data/sessions/s", "host:/data/sessions/s"},
		{"user@host:/data/sessions/s", "host:/data/sessions/s"},
		{"user@10.0.0.1:/data/x", "10.0.0.1:/data/x"},
		// No host part at all: a local destination is shown as written.
		{"/srv/sessions/s", "/srv/sessions/s"},
		// An @ inside the remote path is not a login and must survive.
		{"host:/data/a@b", "host:/data/a@b"},
	} {
		if got := Display(c.in); got != c.want {
			t.Errorf("Display(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSyncDirIsANoOpWithoutATarget(t *testing.T) {
	// Hosts that only configure an [upload] url push single recordings over
	// HTTP and have nowhere to rsync a session directory; that must stay a
	// silent no-op rather than an error the caller has to special-case.
	dest, err := SyncDir(config.UploadConfig{Enabled: true, URL: "https://x/api"}, t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if dest != "" {
		t.Errorf("expected no destination, got %q", dest)
	}
}

func TestSyncDirRejectsAMissingSession(t *testing.T) {
	if _, err := SyncDir(config.UploadConfig{Target: "host:/data/x"}, t.TempDir()+"/gone"); err == nil {
		t.Error("expected an error for a session directory that is not there")
	}
}

func TestLocalMirrorIsTheDestinationPathWhenMountedHere(t *testing.T) {
	root := t.TempDir()
	if got := LocalMirror("user@host:" + root); got != root {
		t.Errorf("LocalMirror(mounted dir) = %q, want %q", got, root)
	}
	for _, dest := range []string{
		"user@host:" + root + "/missing",
		"host:relative/path",
		root,
	} {
		if got := LocalMirror(dest); got != "" {
			t.Errorf("LocalMirror(%q) = %q, want empty", dest, got)
		}
	}
}
