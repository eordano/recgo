//go:build linux

package audio

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

const (
	headsetMic     = "bluez_input.00:11:22:33:44:55"
	headsetMonitor = "bluez_output.00_11_22_33_44_55.1.monitor"
	blueMic        = "alsa_input.usb-Generic_Blue_Microphones_LT_0000000000000000000A_111000-00.analog-stereo"
	blueMonitor    = "alsa_output.usb-Generic_Blue_Microphones_LT_0000000000000000000A_111000-00.analog-stereo.monitor"
	webcamMic      = "alsa_input.usb-046d_C922_Pro_Stream_Webcam_0000ABCD-02.analog-stereo"

	headsetMicIdx     = 389
	headsetMonitorIdx = 215600
	blueMicIdx        = 165997
	blueMonitorIdx    = 165996
	webcamMicIdx      = 166120
)

// Trimmed from `pactl --format=json list sources` on a desktop (PipeWire 1.6.8,
// WirePlumber 0.5.17 with bluetooth.autoswitch-to-headset-profile).
var pactlSources = fmt.Sprintf(`[
  {"index": %d, "state": "SUSPENDED", "name": %q, "description": "Monitor of Blue Microphones Analog Stereo",
   "driver": "PipeWire", "sample_specification": "s16le 2ch 48000Hz", "monitor_source": "",
   "properties": {"media.class": "Audio/Sink", "device.class": "monitor", "device.api": "alsa",
     "device.name": "alsa_card.usb-Generic_Blue_Microphones_LT_0000000000000000000A_111000-00"}},
  {"index": %d, "state": "SUSPENDED", "name": %q, "description": "Px8",
   "driver": "PipeWire", "sample_specification": "float32le 1ch 48000Hz", "monitor_source": "",
   "properties": {"media.class": "Audio/Source", "bluez5.loopback": "true", "bluez5.profile": "off",
     "node.group": "loopback-4277-18", "device.api": "bluez5", "device.bus": "bluetooth",
     "device.name": "bluez_card.00_11_22_33_44_55", "api.bluez5.address": "00:11:22:33:44:55"}},
  {"index": %d, "state": "IDLE", "name": %q, "description": "Blue Microphones Analog Stereo",
   "driver": "PipeWire", "sample_specification": "s16le 2ch 48000Hz", "monitor_source": "",
   "properties": {"media.class": "Audio/Source", "device.class": "sound", "device.api": "alsa",
     "device.name": "alsa_card.usb-Generic_Blue_Microphones_LT_0000000000000000000A_111000-00"}},
  {"index": %d, "state": "SUSPENDED", "name": %q, "description": "C922 Pro Stream Webcam Analog Stereo",
   "driver": "PipeWire", "sample_specification": "s16le 2ch 32000Hz", "monitor_source": "",
   "properties": {"media.class": "Audio/Source", "device.class": "sound", "device.api": "alsa",
     "device.name": "alsa_card.usb-046d_C922_Pro_Stream_Webcam_0000ABCD-02"}},
  {"index": %d, "state": "RUNNING", "name": %q, "description": "Monitor of Px8",
   "driver": "PipeWire", "sample_specification": "float32le 2ch 48000Hz", "monitor_source": "",
   "properties": {"media.class": "Audio/Sink", "device.class": "monitor", "device.api": "bluez5",
     "device.name": "bluez_card.00_11_22_33_44_55"}}
]`, blueMonitorIdx, blueMonitor, headsetMicIdx, headsetMic, blueMicIdx, blueMic,
	webcamMicIdx, webcamMic, headsetMonitorIdx, headsetMonitor)

func pactlCards(headsetProfile string) string {
	return fmt.Sprintf(`[
  {"index": 387, "name": "bluez_card.00_11_22_33_44_55", "driver": "module-bluez5-device.c", "owner_module": null,
   "properties": {"device.api": "bluez5", "device.description": "Px8", "api.bluez5.connection": "connected"},
   "active_profile": %q},
  {"index": 165995, "name": "alsa_card.usb-Generic_Blue_Microphones_LT_0000000000000000000A_111000-00",
   "driver": "alsa", "owner_module": null, "properties": {"device.api": "alsa"},
   "active_profile": "output:analog-stereo+input:analog-stereo"}
]`, headsetProfile)
}

func chromiumInput(index, source int, corked bool) string {
	return fmt.Sprintf(`{"index": %d, "driver": "PipeWire", "owner_module": null, "client": "%d",
  "source": %d, "sample_specification": "s16le 1ch 48000Hz", "corked": %t, "mute": false,
  "properties": {"application.name": "Chromium input", "application.process.binary": "chromium",
    "media.name": "RecordStream", "node.name": "Chromium input"}}`, index, index-1, source, corked)
}

func peakMeter(index, source int) string {
	return fmt.Sprintf(`{"index": %d, "driver": "PipeWire", "owner_module": null, "client": "%d",
  "source": %d, "sample_specification": "float32le 1ch 25Hz", "corked": false, "mute": false,
  "properties": {"application.name": "PulseAudio Volume Control", "media.name": "Peak detect",
    "stream.monitor": "true"}}`, index, index-1, source)
}

func ffmpegCapture(index, source int) string {
	return fmt.Sprintf(`{"index": %d, "driver": "PipeWire", "owner_module": null, "client": "%d",
  "source": %d, "sample_specification": "s16le 2ch 48000Hz", "corked": false, "mute": false,
  "properties": {"application.name": "Lavf61.7.100", "media.name": "record system audio"}}`, index, index-1, source)
}

func routing(t *testing.T, headsetProfile string, outputs ...string) micRouting {
	t.Helper()
	var r micRouting
	for _, doc := range []struct {
		json string
		into any
	}{
		{pactlSources, &r.sources},
		{pactlCards(headsetProfile), &r.cards},
		{"[" + strings.Join(outputs, ",") + "]", &r.outputs},
	} {
		if err := json.Unmarshal([]byte(doc.json), doc.into); err != nil {
			t.Fatalf("fixture: %v", err)
		}
	}
	return r
}

func headsetDefault(t *testing.T, r micRouting) ([]Device, *Device) {
	t.Helper()
	devices := sourceDevices(r.sources, headsetMic)
	for i := range devices {
		if devices[i].IsDefault {
			return devices, &devices[i]
		}
	}
	t.Fatal("fixture has no default source")
	return nil, nil
}

func TestPactlJSONFieldsDecode(t *testing.T) {
	r := routing(t, "a2dp-sink", chromiumInput(900, blueMicIdx, true), peakMeter(901, blueMicIdx))
	if o := r.outputs[0]; o.Source != blueMicIdx || !o.Corked {
		t.Errorf("source-output decoded as %+v", o)
	}
	if r.outputs[1].Properties["stream.monitor"] != "true" {
		t.Errorf("peak meter lost stream.monitor: %+v", r.outputs[1])
	}
	if r.cards[0].ActiveProfile != "a2dp-sink" {
		t.Errorf("card decoded as %+v", r.cards[0])
	}
	if p := r.sources[1].Properties; p["bluez5.loopback"] != "true" || p["device.name"] != "bluez_card.00_11_22_33_44_55" {
		t.Errorf("source properties decoded as %v", p)
	}
}

func TestPickLiveMicOnlyReplacesAHeadsetThatWouldLeaveA2DP(t *testing.T) {
	for _, tc := range []struct {
		name    string
		profile string
		outputs []string
		want    string
	}{
		{"the call's mic, held by two Chromium streams", "a2dp-sink",
			[]string{chromiumInput(900, blueMicIdx, false), chromiumInput(902, blueMicIdx, false)}, blueMic},
		{"any A2DP codec profile switches", "a2dp-sink-aac",
			[]string{chromiumInput(900, blueMicIdx, false)}, blueMic},
		{"a corked call stream still holds the mic", "a2dp-sink",
			[]string{chromiumInput(900, blueMicIdx, true)}, blueMic},
		{"nothing holds a mic", "a2dp-sink", nil, headsetMic},
		{"only a peak meter watches another mic", "a2dp-sink",
			[]string{peakMeter(900, blueMicIdx)}, headsetMic},
		{"the headset is already in HSP/HFP", "headset-head-unit",
			[]string{chromiumInput(900, blueMicIdx, false)}, headsetMic},
		{"two different mics are held", "a2dp-sink",
			[]string{chromiumInput(900, blueMicIdx, false), chromiumInput(904, webcamMicIdx, false)}, headsetMic},
		{"a held monitor is never chosen", "a2dp-sink",
			[]string{ffmpegCapture(900, headsetMonitorIdx), chromiumInput(904, blueMonitorIdx, false)}, headsetMic},
		{"a held monitor does not make the call's mic ambiguous", "a2dp-sink",
			[]string{ffmpegCapture(900, headsetMonitorIdx), chromiumInput(904, blueMicIdx, false)}, blueMic},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := routing(t, tc.profile, tc.outputs...)
			devices, def := headsetDefault(t, r)
			if got := pickLiveMic(devices, def, r); got.Name != tc.want {
				t.Fatalf("pickLiveMic = %s, want %s", got.Name, tc.want)
			}
			mics := micsOnly(devices)
			if got := pickLiveMic(mics, findMic(mics, headsetMic), r); got.Name != tc.want {
				t.Fatalf("over the mic list alone: pickLiveMic = %s, want %s", got.Name, tc.want)
			}
		})
	}
}

func micsOnly(devices []Device) (mics []Device) {
	for _, d := range devices {
		if d.Type == DeviceTypeMic {
			mics = append(mics, d)
		}
	}
	return mics
}

func TestPreferLiveMicReadsNothingUnlessTheDefaultIsAHeadsetMic(t *testing.T) {
	devices := sourceDevices(routing(t, "a2dp-sink").sources, "")
	unread := func() (micRouting, error) {
		t.Fatal("queried pactl for a default that cannot switch a headset profile")
		return micRouting{}, nil
	}
	for _, name := range []string{blueMic, blueMonitor, headsetMonitor} {
		def := &devices[0]
		for i := range devices {
			if devices[i].Name == name {
				def = &devices[i]
			}
		}
		if got := preferLiveMicWith(devices, def, unread); got != def {
			t.Errorf("default %s became %s", name, got.Name)
		}
	}
}

func TestPreferLiveMicKeepsTheHeadsetWhenPactlFails(t *testing.T) {
	r := routing(t, "a2dp-sink", chromiumInput(900, blueMicIdx, false))
	devices, def := headsetDefault(t, r)
	failing := func() (micRouting, error) { return micRouting{}, errors.New("pactl: connection refused") }
	if got := preferLiveMicWith(devices, def, failing); got != def {
		t.Fatalf("got %s, want the headset kept", got.Name)
	}
	read := func() (micRouting, error) { return r, nil }
	if got := preferLiveMicWith(devices, def, read); got.Name != blueMic {
		t.Fatalf("got %s, want %s", got.Name, blueMic)
	}
}
