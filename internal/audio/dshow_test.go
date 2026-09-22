package audio

import (
	"strings"
	"testing"
)

// Captured on a Windows 11 box (ffmpeg 9.0.1, gyan.dev build) with
// `ffmpeg -hide_banner -list_devices true -f dshow -i dummy`, plus a Stereo
// Mix and a VB-Cable line of the same shape, a capture card exposing both
// pins (comma-joined types, ffmpeg >= 4.4) and an (unknown) device.
const sampleDshowListing = `[in#0 @ 00000150816dfec0] "ASUS FHD webcam" (video)
[in#0 @ 00000150816dfec0]   Alternative name "@device_pnp_\\?\usb#vid_13d3&pid_52b8&mi_00#6&2070fbc0&0&0000#{65e8773d-8f56-11d0-a3b9-00a0c9223196}\global"
[in#0 @ 00000150816dfec0] "Microphone Array (Intel® Smart Sound Technology for Digital Microphones)" (audio)
[in#0 @ 00000150816dfec0]   Alternative name "@device_cm_{33D9A762-90C8-11D0-BD43-00A0C911CE86}\wave_{ED11F459-2F83-4592-8A73-547AEFF5317F}"
[in#0 @ 00000150816dfec0] "Game Capture HD60 S" (audio, video)
[in#0 @ 00000150816dfec0]   Alternative name "@device_pnp_\\?\usb#vid_0fd9&pid_004f#{65e8773d-8f56-11d0-a3b9-00a0c9223196}\global"
[in#0 @ 00000150816dfec0] "Mystery" (unknown)
[in#0 @ 00000150816dfec0] "Stereo Mix (Realtek(R) Audio)" (audio)
[in#0 @ 00000150816dfec0]   Alternative name "@device_cm_{33D9A762-90C8-11D0-BD43-00A0C911CE86}\wave_{1111}"
[in#0 @ 00000150816dfec0] "CABLE Output (VB-Audio Virtual Cable)" (audio)
[in#0 @ 00000150816dfec0]   Alternative name "@device_cm_{33D9A762-90C8-11D0-BD43-00A0C911CE86}\wave_{2222}"
Error opening input file dummy.
`

const sampleDshowListingFFmpeg4 = `[dshow @ 000001d2b8a0] DirectShow video devices (some may be both video and audio devices)
[dshow @ 000001d2b8a0]  "Integrated Camera"
[dshow @ 000001d2b8a0]     Alternative name "@device_pnp_\\?\usb#vid_04f2"
[dshow @ 000001d2b8a0] DirectShow audio devices
[dshow @ 000001d2b8a0]  "Microphone (Realtek High Definition Audio)"
[dshow @ 000001d2b8a0]     Alternative name "@device_cm_{33D9A762}\wave_{AAAA}"
dummy: Immediate exit requested
`

func TestParseDshowDevicesModernFormat(t *testing.T) {
	devs := parseDshowDevices(sampleDshowListing)
	want := []dshowDevice{
		{"ASUS FHD webcam", false},
		{"Microphone Array (Intel® Smart Sound Technology for Digital Microphones)", true},
		{"Game Capture HD60 S", true},
		{"Mystery", false},
		{"Stereo Mix (Realtek(R) Audio)", true},
		{"CABLE Output (VB-Audio Virtual Cable)", true},
	}
	if len(devs) != len(want) {
		t.Fatalf("got %d devices %+v, want %d", len(devs), devs, len(want))
	}
	for i := range want {
		if devs[i] != want[i] {
			t.Errorf("device %d = %+v, want %+v", i, devs[i], want[i])
		}
	}
}

func TestParseDshowDevicesCRLF(t *testing.T) {
	// ffmpeg on Windows writes CRLF; the pulled fixture had "\r\n" on every line.
	crlf := strings.ReplaceAll(sampleDshowListing, "\n", "\r\n")
	devs := parseDshowDevices(crlf)
	if len(devs) != 6 || devs[1].Name != "Microphone Array (Intel® Smart Sound Technology for Digital Microphones)" {
		t.Fatalf("CRLF listing parsed as %+v", devs)
	}
}

func TestParseDshowDevicesSectionFormat(t *testing.T) {
	devs := parseDshowDevices(sampleDshowListingFFmpeg4)
	if len(devs) != 2 || devs[0].Audio || !devs[1].Audio ||
		devs[1].Name != "Microphone (Realtek High Definition Audio)" {
		t.Fatalf("ffmpeg 4 listing parsed as %+v", devs)
	}
}

func TestDshowAudioDevicesClassifiesAndDefaults(t *testing.T) {
	devs := dshowAudioDevices(sampleDshowListing)
	if len(devs) != 4 {
		t.Fatalf("expected 4 audio devices, got %+v", devs)
	}
	if devs[0].Type != DeviceTypeMic || !devs[0].IsDefault {
		t.Errorf("first mic must be the default: %+v", devs[0])
	}
	if devs[0].Name != devs[0].Description {
		t.Errorf("dshow takes the friendly name after audio=, so Name must equal Description: %+v", devs[0])
	}
	if devs[1].Name != "Game Capture HD60 S" || devs[1].Type != DeviceTypeMic || devs[1].IsDefault {
		t.Errorf("the capture card's audio pin is a non-default mic: %+v", devs[1])
	}
	for _, d := range devs[2:] {
		if d.Type != DeviceTypeMonitor || d.IsDefault {
			t.Errorf("%q should be a non-default monitor: %+v", d.Name, d)
		}
	}
	if devs[3].Index != 3 {
		t.Errorf("indexes must count audio devices only: %+v", devs[3])
	}
	if got := dshowAudioDevices(sampleDshowListingFFmpeg4); len(got) != 1 || got[0].Type != DeviceTypeMic {
		t.Errorf("ffmpeg 4 listing = %+v", got)
	}
}

func TestIsWindowsLoopbackDevice(t *testing.T) {
	for name, want := range map[string]bool{
		"Stereo Mix (Realtek(R) Audio)":         true,
		"What U Hear (Sound Blaster)":           true,
		"CABLE Output (VB-Audio Virtual Cable)": true,
		"Microphone Array (Intel)":              false,
		"Headset (Jabra)":                       false,
	} {
		if got := isWindowsLoopbackDevice(name); got != want {
			t.Errorf("isWindowsLoopbackDevice(%q) = %v, want %v", name, got, want)
		}
	}
}
