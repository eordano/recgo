package config

import (
	"runtime"
	"testing"
)

func TestDefaultWatchdogEnabledEverywhere(t *testing.T) {
	if !DefaultConfig().Watchdog.Enabled {
		t.Errorf("watchdog should be enabled by default on %s", runtime.GOOS)
	}
}

func TestNoUploadTargetShipsInTheBinary(t *testing.T) {
	d := DefaultConfig().Upload
	if d.Enabled {
		t.Error("upload should be disabled by default")
	}
	if d.Target != "" || d.URL != "" || d.SSHKey != "" {
		t.Errorf("default upload config must ship no endpoints, got target=%q url=%q ssh_key=%q", d.Target, d.URL, d.SSHKey)
	}
}

func TestDefaultRecordOutputDevice(t *testing.T) {
	got := DefaultConfig().RecordOutputDevice
	if runtime.GOOS == "darwin" {
		if got != "Multi-Output Device" {
			t.Errorf("darwin default record_output_device = %q, want %q", got, "Multi-Output Device")
		}
	} else if got != "" {
		t.Errorf("non-darwin default record_output_device = %q, want empty", got)
	}
}
