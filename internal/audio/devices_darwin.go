//go:build darwin

package audio

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

func GetDefaultSource() (string, error) {
	if name := defaultInputName(); name != "" {
		devices, err := listAVFoundationDevices()
		if err == nil {
			for _, d := range devices {
				if d.Description == name {
					return d.Name, nil
				}
			}
		}
	}
	// avfoundation resolves ":default" to the system default input itself.
	return "default", nil
}

func GetDefaultSink() (string, error) { return "", nil }

func DefaultMonitor() (string, error) {
	return "", fmt.Errorf("no default monitor on macOS -- name the loopback device (BlackHole)")
}

func SetStreamMuted(streamName string, pid int, muted bool) error {
	return fmt.Errorf("live system-audio toggle needs PulseAudio or PipeWire")
}

func defaultInputName() string {
	name, err := runTool("SwitchAudioSource", "-c", "-t", "input")
	if err != nil {
		return ""
	}
	return name
}

func IsPipeWireRunning() bool   { return false }
func IsPulseAudioRunning() bool { return false }
func HasPactl() bool            { return false }

func CheckBackend() error {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return fmt.Errorf("ffmpeg not found in PATH - required for avfoundation audio capture")
	}
	return nil
}

var avfoundationAudioLine = regexp.MustCompile(`^\[AVFoundation indev @ [^\]]+\] \[(\d+)\] (.+)$`)

var knownLoopbackNames = []string{"BlackHole", "Loopback", "Soundflower", "VB-Cable"}

func isLoopbackDevice(name string) bool {
	for _, l := range knownLoopbackNames {
		if strings.Contains(name, l) {
			return true
		}
	}
	return false
}

func listAVFoundationDevices() ([]Device, error) {
	ctx, cancel := context.WithTimeout(context.Background(), toolTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ffmpeg", "-hide_banner",
		"-f", "avfoundation", "-list_devices", "true", "-i", "")
	out, _ := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return nil, fmt.Errorf("ffmpeg device listing timed out after %v", toolTimeout)
	}
	return parseAVFoundationAudioDevices(string(out), defaultInputName()), nil
}

func parseAVFoundationAudioDevices(out, defaultName string) []Device {
	var devices []Device
	inAudioSection := false
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.Contains(line, "AVFoundation audio devices:") {
			inAudioSection = true
			continue
		}
		if strings.Contains(line, "AVFoundation video devices:") {
			inAudioSection = false
			continue
		}
		if !inAudioSection {
			continue
		}
		m := avfoundationAudioLine.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		index, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		name := strings.TrimSpace(m[2])

		devType := DeviceTypeMic
		if isLoopbackDevice(name) {
			devType = DeviceTypeMonitor
		}
		devices = append(devices, Device{
			Name:        strconv.Itoa(index),
			Description: name,
			Type:        devType,
			IsDefault:   defaultName != "" && name == defaultName,
			Index:       index,
			State:       "RUNNING",
		})
	}
	return devices
}

func ListSources() ([]Device, error) {
	return listAVFoundationDevices()
}

func ListSinks() ([]Device, error) {
	return nil, nil
}

func ListAllDevices() (mics []Device, monitors []Device, err error) {
	devices, err := listAVFoundationDevices()
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
	return nil, "", fmt.Errorf("virtual sink not supported on darwin")
}

func CleanupOrphanedVirtualSinks() {}
