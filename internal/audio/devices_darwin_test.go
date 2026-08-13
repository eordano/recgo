//go:build darwin

package audio

import (
	"os/exec"
	"testing"
)

const sampleAVFoundationListing = `[AVFoundation indev @ 0x7dac24140] AVFoundation video devices:
[AVFoundation indev @ 0x7dac24140] [0] FaceTime HD Camera
[AVFoundation indev @ 0x7dac24140] [1] Capture screen 0
[AVFoundation indev @ 0x7dac24140] AVFoundation audio devices:
[AVFoundation indev @ 0x7dac24140] [0] BlackHole 2ch
[AVFoundation indev @ 0x7dac24140] [1] MacBook Pro Microphone
[AVFoundation indev @ 0x7dac24140] [2] Loopback Audio
[AVFoundation indev @ 0x7dac24140] [3] Soundflower (2ch)
[AVFoundation indev @ 0x7dac24140] [4] VB-Cable
[AVFoundation indev @ 0x7dac24140] [5] Px8
[in#0 @ 0x7dac24000] Error opening input: Input/output error
Error opening input file .
Error opening input files: Input/output error`

func TestParseAVFoundationAudioDevices(t *testing.T) {
	devices := parseAVFoundationAudioDevices(sampleAVFoundationListing, "Px8")
	if len(devices) != 6 {
		t.Fatalf("expected 6 audio devices, got %d: %+v", len(devices), devices)
	}

	wantType := map[string]DeviceType{
		"BlackHole 2ch":          DeviceTypeMonitor,
		"MacBook Pro Microphone": DeviceTypeMic,
		"Loopback Audio":         DeviceTypeMonitor,
		"Soundflower (2ch)":      DeviceTypeMonitor,
		"VB-Cable":               DeviceTypeMonitor,
		"Px8":                    DeviceTypeMic,
	}
	defaults := 0
	for _, d := range devices {
		if want, ok := wantType[d.Description]; !ok || d.Type != want {
			t.Errorf("%q classified as %v, want %v", d.Description, d.Type, want)
		}
		if d.IsDefault {
			defaults++
			if d.Description != "Px8" {
				t.Errorf("IsDefault set on %q, want Px8", d.Description)
			}
		}
	}
	if defaults != 1 {
		t.Errorf("expected exactly 1 default device, got %d", defaults)
	}

	if d := devices[1]; d.Name != "1" || d.Index != 1 {
		t.Errorf("device Name/Index mismatch: %+v", d)
	}

	for _, d := range parseAVFoundationAudioDevices(sampleAVFoundationListing, "") {
		if d.IsDefault {
			t.Errorf("no default name given, but %q has IsDefault", d.Description)
		}
	}
}

func TestParseAVFoundationAudioDevicesIgnoresVideo(t *testing.T) {
	for _, d := range parseAVFoundationAudioDevices(sampleAVFoundationListing, "") {
		if d.Description == "FaceTime HD Camera" || d.Description == "Capture screen 0" {
			t.Errorf("video device %q leaked into audio devices", d.Description)
		}
	}
}

func requireTools(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"ffmpeg", "SwitchAudioSource"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not in PATH", tool)
		}
	}
}

func TestListAllDevicesMarksSystemDefault(t *testing.T) {
	requireTools(t)

	want, err := runTool("SwitchAudioSource", "-c", "-t", "input")
	if err != nil || want == "" {
		t.Skipf("SwitchAudioSource -c -t input unusable: %q %v", want, err)
	}

	mics, monitors, err := ListAllDevices()
	if err != nil {
		t.Fatalf("ListAllDevices: %v", err)
	}

	defaults := 0
	var defaultDesc string
	for _, d := range append(append([]Device(nil), mics...), monitors...) {
		if d.IsDefault {
			defaults++
			defaultDesc = d.Description
		}
	}
	if defaults != 1 {
		t.Fatalf("expected exactly 1 device with IsDefault, got %d (mics=%+v monitors=%+v)",
			defaults, mics, monitors)
	}
	if defaultDesc != want {
		t.Errorf("default device is %q, system default is %q", defaultDesc, want)
	}

	if !isLoopbackDevice(want) {
		micDefaults := 0
		for _, d := range mics {
			if d.IsDefault {
				micDefaults++
			}
		}
		if micDefaults != 1 {
			t.Errorf("expected exactly 1 mic with IsDefault, got %d", micDefaults)
		}
		picked := FindDefaultMic(mics)
		if picked == nil || picked.Description != want {
			t.Errorf("FindDefaultMic picked %+v, want description %q", picked, want)
		}
	}
}

func TestGetDefaultSourceNonEmpty(t *testing.T) {
	requireTools(t)

	src, err := GetDefaultSource()
	if err != nil {
		t.Fatalf("GetDefaultSource: %v", err)
	}
	if src == "" {
		t.Fatal("GetDefaultSource returned empty device")
	}
}
