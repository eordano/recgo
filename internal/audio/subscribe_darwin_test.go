//go:build darwin

package audio

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeShimState(t *testing.T, statePath, name string) {
	t.Helper()
	if err := os.WriteFile(statePath, []byte(name+"\n"), 0644); err != nil {
		t.Fatalf("write shim state: %v", err)
	}
}

func installSwitchAudioSourceShim(t *testing.T, initial string) string {
	t.Helper()
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state")
	writeShimState(t, statePath, initial)

	shim := "#!/bin/sh\n/bin/cat " + statePath + "\n"
	if err := os.WriteFile(filepath.Join(dir, "SwitchAudioSource"), []byte(shim), 0755); err != nil {
		t.Fatalf("write shim: %v", err)
	}
	t.Setenv("PATH", dir)
	return statePath
}

func TestDefaultSinkWatcherEmitsOnOutputChange(t *testing.T) {
	statePath := installSwitchAudioSourceShim(t, "Multi-Output Device")

	oldInterval := defaultOutputPollInterval
	defaultOutputPollInterval = 25 * time.Millisecond
	defer func() { defaultOutputPollInterval = oldInterval }()

	w, err := NewDefaultSinkWatcher(context.Background())
	if err != nil {
		t.Fatalf("NewDefaultSinkWatcher: %v", err)
	}
	defer w.Stop()

	select {
	case got := <-w.Changes():
		t.Fatalf("unexpected change before any flip: %q", got)
	case <-time.After(150 * time.Millisecond):
	}

	writeShimState(t, statePath, "Px8")

	select {
	case got := <-w.Changes():
		if got != "Px8" {
			t.Fatalf("change = %q, want %q", got, "Px8")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no change emitted within 3s of default-output flip")
	}
}

func TestDefaultSinkWatcherStopClosesChannel(t *testing.T) {
	installSwitchAudioSourceShim(t, "Multi-Output Device")

	oldInterval := defaultOutputPollInterval
	defaultOutputPollInterval = 25 * time.Millisecond
	defer func() { defaultOutputPollInterval = oldInterval }()

	w, err := NewDefaultSinkWatcher(context.Background())
	if err != nil {
		t.Fatalf("NewDefaultSinkWatcher: %v", err)
	}
	w.Stop()

	select {
	case _, ok := <-w.Changes():
		if ok {
			t.Fatal("expected closed channel after Stop, got a value")
		}
	case <-time.After(time.Second):
		t.Fatal("Changes() not closed within 1s of Stop")
	}
}

func TestDefaultSinkWatcherInitFailsWithoutTool(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, err := NewDefaultSinkWatcher(context.Background()); err == nil {
		t.Fatal("expected error when SwitchAudioSource is missing")
	}
}
