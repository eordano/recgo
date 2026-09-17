package tab

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFinalizeDirNoCollision(t *testing.T) {
	root := t.TempDir()
	prov := filepath.Join(root, "prov")
	if err := os.Mkdir(prov, 0o700); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(root, "2026-01-02-15-04-desktop")
	got, err := FinalizeDir(prov, want)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
	if _, err := os.Stat(got); err != nil {
		t.Fatalf("final dir missing: %v", err)
	}
}

func TestFinalizeDirCollisionKeepsEarlierRecording(t *testing.T) {
	root := t.TempDir()
	want := filepath.Join(root, "2026-01-02-15-04-desktop")

	if err := os.Mkdir(want, 0o700); err != nil {
		t.Fatal(err)
	}
	marker := filepath.Join(want, "SESSION.md")
	if err := os.WriteFile(marker, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}

	prov := filepath.Join(root, "prov")
	if err := os.Mkdir(prov, 0o700); err != nil {
		t.Fatal(err)
	}
	got, err := FinalizeDir(prov, want)
	if err != nil {
		t.Fatal(err)
	}
	if got != want+"_2" {
		t.Fatalf("got %s, want %s", got, want+"_2")
	}
	if data, err := os.ReadFile(marker); err != nil || string(data) != "first" {
		t.Fatalf("earlier recording clobbered: %v %q", err, data)
	}

	prov3 := filepath.Join(root, "prov3")
	if err := os.Mkdir(prov3, 0o700); err != nil {
		t.Fatal(err)
	}
	got3, err := FinalizeDir(prov3, want)
	if err != nil {
		t.Fatal(err)
	}
	if got3 != want+"_3" {
		t.Fatalf("got %s, want %s", got3, want+"_3")
	}
}
