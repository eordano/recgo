package audio

import (
	"regexp"
	"strings"
)

// dshowDevice is one line of `ffmpeg -list_devices true -f dshow -i dummy`:
// the name ffmpeg's dshow input takes after `audio=`, and whether it is an
// audio or a video pin.
type dshowDevice struct {
	Name  string
	Audio bool
}

var (
	// ffmpeg 5+: [in#0 @ 0x...] "Microphone (Realtek)" (audio)
	// ffmpeg 4:  [dshow @ 0x...]  "Microphone (Realtek)" (audio)
	// A device with several pins lists every media type, comma-joined:
	// "Game Capture HD60 S" (audio, video); an unrecognised one is (unknown).
	dshowDeviceLine = regexp.MustCompile(`^\[[^\]]+\]\s+"(.+)"\s+\(([a-z, ]+)\)\s*$`)
	// ffmpeg <4.3 has no "(audio)" suffix and relies on section headers:
	// [dshow @ 0x...] DirectShow audio devices (some may be both video and audio devices)
	// [dshow @ 0x...]  "Microphone (Realtek)"
	dshowBareLine = regexp.MustCompile(`^\[[^\]]+\]\s+"(.+)"\s*$`)
)

// parseDshowDevices reads the device listing off ffmpeg's stderr. Both the
// per-line `(audio)` tag and the older section headers are honoured, and
// the `Alternative name` lines are skipped: the friendly name is what dshow
// accepts after `audio=`.
func parseDshowDevices(stderr string) []dshowDevice {
	var out []dshowDevice
	section := ""
	for _, line := range strings.Split(stderr, "\n") {
		line = strings.TrimRight(line, "\r")
		switch {
		case strings.Contains(line, "DirectShow video devices"):
			section = "video"
			continue
		case strings.Contains(line, "DirectShow audio devices"):
			section = "audio"
			continue
		case strings.Contains(line, "Alternative name"):
			continue
		}
		if m := dshowDeviceLine.FindStringSubmatch(line); m != nil {
			out = append(out, dshowDevice{Name: m[1], Audio: strings.Contains(m[2], "audio")})
			continue
		}
		if m := dshowBareLine.FindStringSubmatch(line); m != nil && section != "" {
			out = append(out, dshowDevice{Name: m[1], Audio: section == "audio"})
		}
	}
	return out
}

// Windows has no monitor sources: what you hear is only capturable through
// the sound card's own loopback pin or a virtual cable, which show up in
// dshow under these names.
var windowsLoopbackNames = []string{
	"Stereo Mix", "What U Hear", "Wave Out Mix", "Loopback",
	"CABLE Output", "VB-Audio", "Virtual Cable",
}

func isWindowsLoopbackDevice(name string) bool {
	l := strings.ToLower(name)
	for _, k := range windowsLoopbackNames {
		if strings.Contains(l, strings.ToLower(k)) {
			return true
		}
	}
	return false
}

// dshowAudioDevices turns the listing into recgo devices: the dshow name is
// both Name and Description, loopback pins are monitors, and the first mic
// is the default since dshow has no notion of one.
func dshowAudioDevices(stderr string) []Device {
	var devices []Device
	for _, d := range parseDshowDevices(stderr) {
		if !d.Audio {
			continue
		}
		t := DeviceTypeMic
		if isWindowsLoopbackDevice(d.Name) {
			t = DeviceTypeMonitor
		}
		devices = append(devices, Device{
			Name: d.Name, Description: d.Name, Type: t,
			Index: len(devices), State: "RUNNING",
		})
	}
	for i := range devices {
		if devices[i].Type == DeviceTypeMic {
			devices[i].IsDefault = true
			break
		}
	}
	return devices
}
