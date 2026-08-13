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
