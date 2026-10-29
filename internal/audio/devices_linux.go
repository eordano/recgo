//go:build linux

package audio

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/eordano/recgo/internal/logging"
)

type paSource struct {
	Index       int               `json:"index"`
	Name        string            `json:"name"`
	Description string            `json:"description"`
	State       string            `json:"state"`
	Properties  map[string]string `json:"properties"`
}

type paSink struct {
	Index       int    `json:"index"`
	Name        string `json:"name"`
	Description string `json:"description"`
	State       string `json:"state"`
}

func IsPipeWireRunning() bool {
	return exec.Command("pidof", "pipewire").Run() == nil
}

func IsPulseAudioRunning() bool {
	return exec.Command("pidof", "pulseaudio").Run() == nil
}

func HasPactl() bool {
	_, err := exec.LookPath("pactl")
	return err == nil
}

func CheckBackend() error {
	if !HasPactl() {
		return fmt.Errorf("pactl not found in PATH - install pulseaudio or pipewire-pulse")
	}
	if !IsPipeWireRunning() && !IsPulseAudioRunning() {
		return fmt.Errorf("no audio server running (need PipeWire or PulseAudio)")
	}
	return nil
}

func GetDefaultSource() (string, error) {
	return runPactl("get-default-source")
}

func GetDefaultSink() (string, error) {
	return runPactl("get-default-sink")
}

func DefaultMonitor() (string, error) {
	sink, err := GetDefaultSink()
	if err != nil {
		return "", err
	}
	if sink == "" {
		return "", fmt.Errorf("no default sink")
	}
	return sink + ".monitor", nil
}

type paSourceOutput struct {
	Index      int               `json:"index"`
	Source     int               `json:"source"`
	Corked     bool              `json:"corked"`
	Properties map[string]string `json:"properties"`
}

type paCard struct {
	Name          string `json:"name"`
	ActiveProfile string `json:"active_profile"`
}

func pactlList(what string, into any) error {
	out, err := runPactl("--format=json", "list", what)
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(out), into)
}

func findSourceOutput(streamName string, pid int) (int, error) {
	var outputs []paSourceOutput
	if err := pactlList("source-outputs", &outputs); err != nil {
		return 0, err
	}
	for _, o := range outputs {
		if o.Properties["media.name"] == streamName &&
			o.Properties["application.process.id"] == strconv.Itoa(pid) {
			return o.Index, nil
		}
	}
	return 0, fmt.Errorf("no capture stream %q owned by pid %d", streamName, pid)
}

func SetStreamMuted(streamName string, pid int, muted bool) error {
	var idx int
	var err error
	for attempt := 0; attempt < 10; attempt++ {
		if idx, err = findSourceOutput(streamName, pid); err == nil {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}
	if err != nil {
		return err
	}
	flag := "0"
	if muted {
		flag = "1"
	}
	_, err = runPactl("set-source-output-mute", strconv.Itoa(idx), flag)
	return err
}

func sourceType(name string) DeviceType {
	if strings.HasSuffix(name, ".monitor") {
		return DeviceTypeMonitor
	}
	return DeviceTypeMic
}

func sourceDevices(sources []paSource, defaultSource string) []Device {
	devices := make([]Device, 0, len(sources))
	for _, src := range sources {
		devices = append(devices, Device{
			Name:        src.Name,
			Description: src.Description,
			Type:        sourceType(src.Name),
			IsDefault:   src.Name == defaultSource,
			Index:       src.Index,
			State:       src.State,
		})
	}
	return devices
}

func ListSources() ([]Device, error) {
	var sources []paSource
	if err := pactlList("sources", &sources); err != nil {
		return nil, err
	}
	defaultSource, _ := GetDefaultSource()
	return sourceDevices(sources, defaultSource), nil
}

func ListSinks() ([]Device, error) {
	var sinks []paSink
	if err := pactlList("sinks", &sinks); err != nil {
		return nil, err
	}

	defaultSink, _ := GetDefaultSink()

	devices := make([]Device, 0, len(sinks))
	for _, sink := range sinks {
		devices = append(devices, Device{
			Name:        sink.Name,
			Description: sink.Description,
			Type:        DeviceTypeOutput,
			IsDefault:   sink.Name == defaultSink,
			Index:       sink.Index,
			State:       sink.State,
		})
	}

	return devices, nil
}

func ListAllDevices() (mics []Device, monitors []Device, err error) {
	sources, err := ListSources()
	if err != nil {
		return nil, nil, err
	}

	for _, src := range sources {
		if src.Type == DeviceTypeMic {
			mics = append(mics, src)
		} else if src.Type == DeviceTypeMonitor {
			monitors = append(monitors, src)
		}
	}

	return mics, monitors, nil
}

// DefaultMic is the source a recording without an explicit mic captures.
func DefaultMic() (string, error) {
	name, err := GetDefaultSource()
	if err != nil || !mayAutoswitchProfile(&Device{Name: name, Type: sourceType(name)}) {
		return name, err
	}
	sources, err := ListSources()
	if err != nil {
		return name, nil
	}
	for i := range sources {
		if sources[i].Name == name {
			return preferLiveMic(sources, &sources[i]).Name, nil
		}
	}
	return name, nil
}

// WirePlumber's bluetooth.autoswitch-to-headset-profile exposes a headset's
// mic as a bluez_input.* loopback even while the headset plays A2DP. Opening
// it switches the headset to HSP/HFP: what it plays drops to mono call audio
// and its sink is torn down under every stream playing to it. When exactly
// one other mic is already held open (the call's), that is what the other
// side hears, so record it and leave the headset alone.
func preferLiveMic(devices []Device, def *Device) *Device {
	return preferLiveMicWith(devices, def, readMicRouting)
}

func mayAutoswitchProfile(d *Device) bool {
	return d != nil && d.Type == DeviceTypeMic && strings.HasPrefix(d.Name, "bluez_input.")
}

func preferLiveMicWith(devices []Device, def *Device, read func() (micRouting, error)) *Device {
	if !mayAutoswitchProfile(def) {
		return def
	}
	r, err := read()
	if err != nil {
		logging.Log("mic: keeping %s, reading the audio graph failed: %v", def.Name, err)
		return def
	}
	pick := pickLiveMic(devices, def, r)
	if pick != def {
		logging.Log("mic: recording %s, which another app holds open, instead of %s, which would switch the headset out of A2DP", pick.Name, def.Name)
	}
	return pick
}

type micRouting struct {
	sources []paSource
	cards   []paCard
	outputs []paSourceOutput
}

func readMicRouting() (micRouting, error) {
	var r micRouting
	if err := pactlList("sources", &r.sources); err != nil {
		return r, err
	}
	if err := pactlList("cards", &r.cards); err != nil {
		return r, err
	}
	return r, pactlList("source-outputs", &r.outputs)
}

// Several held mics are not guessed between: the wrong one could record a
// conversation the user never meant to capture.
func pickLiveMic(devices []Device, def *Device, r micRouting) *Device {
	if !switchesHeadsetProfile(def.Name, r) {
		return def
	}
	names := make(map[int]string, len(r.sources))
	for _, s := range r.sources {
		names[s.Index] = s.Name
	}
	var pick *Device
	for _, o := range r.outputs {
		if o.Properties["stream.monitor"] == "true" {
			continue
		}
		d := findMic(devices, names[o.Source])
		if d == nil || strings.HasPrefix(d.Name, "bluez_") || d == pick {
			continue
		}
		if pick != nil {
			return def
		}
		pick = d
	}
	if pick == nil {
		return def
	}
	return pick
}

func switchesHeadsetProfile(source string, r micRouting) bool {
	for _, s := range r.sources {
		if s.Name != source || s.Properties["bluez5.loopback"] != "true" {
			continue
		}
		for _, c := range r.cards {
			if c.Name == s.Properties["device.name"] {
				return strings.HasPrefix(c.ActiveProfile, "a2dp")
			}
		}
	}
	return false
}

func findMic(devices []Device, name string) *Device {
	for i := range devices {
		if devices[i].Name == name && devices[i].Type == DeviceTypeMic {
			return &devices[i]
		}
	}
	return nil
}

type virtualSinkState struct {
	PID              int    `json:"pid"`
	SinkName         string `json:"sink_name"`
	NullModuleID     string `json:"null_module_id"`
	LoopbackModuleID string `json:"loopback_module_id"`
	OriginalSink     string `json:"original_sink"`
}

const stateFileName = "virtual-sink.json"

func getStateFilePath() (string, error) {
	stateHome := os.Getenv("XDG_STATE_HOME")
	if stateHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cannot determine state directory: %w", err)
		}
		stateHome = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(stateHome, "recgo", stateFileName), nil
}

func runPactl(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), toolTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, "pactl", args...)
	cmd.Env = append(os.Environ(), "LC_NUMERIC=C")
	return runCmd(ctx, cmd)
}

// CleanupOrphanedVirtualSinks undoes the Bluetooth null sink + loopback an
// older recgo left behind when it died mid-recording; recgo no longer makes one.
func CleanupOrphanedVirtualSinks() {
	statePath, err := getStateFilePath()
	if err != nil {
		return
	}
	data, err := os.ReadFile(statePath)
	if err != nil {
		return
	}

	var state virtualSinkState
	if err := json.Unmarshal(data, &state); err != nil {
		os.Remove(statePath)
		return
	}

	if isProcessRunning(state.PID) {
		return
	}

	fmt.Fprintf(os.Stderr, "recgo: cleaning up orphaned virtual sink from PID %d\n", state.PID)

	if state.OriginalSink != "" {
		runPactl("set-default-sink", state.OriginalSink)
	}
	if state.LoopbackModuleID != "" {
		runPactl("unload-module", state.LoopbackModuleID)
	}
	if state.NullModuleID != "" {
		runPactl("unload-module", state.NullModuleID)
	}

	os.Remove(statePath)
}

func isProcessRunning(pid int) bool {
	process, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return process.Signal(syscall.Signal(0)) == nil
}
