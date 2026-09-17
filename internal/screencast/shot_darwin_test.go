//go:build darwin

package screencast

import (
	"slices"
	"testing"
)

func TestShotArgs(t *testing.T) {
	args, err := shotArgs(false, true)
	if err != nil {
		t.Fatalf("shotArgs(false, true): %v", err)
	}
	if !slices.Contains(args, "-C") {
		t.Errorf("shotArgs(false, true) = %v, want -C (capture cursor)", args)
	}

	args, err = shotArgs(false, false)
	if err != nil {
		t.Fatalf("shotArgs(false, false): %v", err)
	}
	if slices.Contains(args, "-C") {
		t.Errorf("shotArgs(false, false) = %v, want no -C", args)
	}
	for _, flag := range []string{"-o", "-w"} {
		if slices.Contains(args, flag) {
			t.Errorf("shotArgs(false, false) = %v, want no interactive flag %s", args, flag)
		}
	}

	if _, err := shotArgs(true, true); err == nil {
		t.Error("shotArgs(true, true): want unsupported error, got nil")
	}
	if _, err := shotArgs(true, false); err == nil {
		t.Error("shotArgs(true, false): want unsupported error, got nil")
	}
}

func TestShotArgsDisplay(t *testing.T) {
	args, err := shotArgsDisplay(false, true, 2)
	if err != nil {
		t.Fatalf("shotArgsDisplay(false, true, 2): %v", err)
	}
	if i := slices.Index(args, "-D"); i < 0 || i+1 >= len(args) || args[i+1] != "2" {
		t.Errorf("shotArgsDisplay(false, true, 2) = %v, want -D 2", args)
	}
	args, _ = shotArgsDisplay(false, true, 0)
	if slices.Contains(args, "-D") {
		t.Errorf("shotArgsDisplay(false, true, 0) = %v, want no -D (every display)", args)
	}
}
