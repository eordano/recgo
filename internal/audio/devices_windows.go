//go:build windows

package audio

import (
	"context"
	"fmt"
	"os/exec"
)

// Windows audio goes through ffmpeg's dshow input. Devices are the friendly
// names dshow prints; there is no default-device query short of COM, so the
// first microphone listed stands in for it.

func listDshowDevices() ([]Device, error) {
	ctx, cancel := context.WithTimeout(context.Background(), toolTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "ffmpeg", "-hide_banner",
		"-list_devices", "true", "-f", "dshow", "-i", "dummy")
	// The listing lands on stderr and ffmpeg exits non-zero ("Error opening
	// input file dummy"), so the exit error carries no information.
	out, _ := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return nil, fmt.Errorf("ffmpeg device listing timed out after %v", toolTimeout)
	}
	return dshowAudioDevices(string(out)), nil
}

func GetDefaultSource() (string, error) {
	devices, err := listDshowDevices()
	if err != nil {
		return "", err
	}
	if d := FindDefaultMic(devices); d != nil {
		return d.Name, nil
	}
	return "", fmt.Errorf("no dshow audio device found (ffmpeg -list_devices true -f dshow -i dummy lists none)")
}

func GetDefaultSink() (string, error) { return "", nil }

// DefaultMonitor is the sound card's own loopback pin when it exists;
// Windows has no monitor sources otherwise.
func DefaultMonitor() (string, error) {
	devices, err := listDshowDevices()
	if err != nil {
		return "", err
	}
	for _, d := range devices {
		if d.Type == DeviceTypeMonitor {
			return d.Name, nil
		}
	}
	return "", fmt.Errorf("no loopback device: system audio needs Stereo Mix or a virtual cable")
}

func SetStreamMuted(streamName string, pid int, muted bool) error {
	return fmt.Errorf("live system-audio toggle needs PulseAudio or PipeWire")
}

func IsPipeWireRunning() bool   { return false }
func IsPulseAudioRunning() bool { return false }
func HasPactl() bool            { return false }

func CheckBackend() error {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return fmt.Errorf("ffmpeg not found in PATH - required for dshow audio capture")
	}
	return nil
}

func ListSources() ([]Device, error) { return listDshowDevices() }

func ListSinks() ([]Device, error) { return nil, nil }

func ListAllDevices() (mics []Device, monitors []Device, err error) {
	devices, err := listDshowDevices()
	if err != nil {
		return nil, nil, err
	}
	for _, d := range devices {
		if d.Type == DeviceTypeMonitor {
			monitors = append(monitors, d)
		} else {
			mics = append(mics, d)
		}
	}
	return mics, monitors, nil
}

type VirtualSink struct{}

func (v *VirtualSink) Cleanup() {}

func NeedsVirtualSink(monitorName string) bool { return false }

func NewVirtualSink(realSinkName string) (*VirtualSink, string, error) {
	return nil, "", fmt.Errorf("virtual sink not supported on windows")
}

func CleanupOrphanedVirtualSinks() {}
