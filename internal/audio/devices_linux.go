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
	"sync"
	"syscall"
	"time"
)

type paSource struct {
	Index       int    `json:"index"`
	Name        string `json:"name"`
	Description string `json:"description"`
	State       string `json:"state"`
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
	Properties map[string]string `json:"properties"`
}

func findSourceOutput(streamName string, pid int) (int, error) {
	out, err := runPactl("--format=json", "list", "source-outputs")
	if err != nil {
		return 0, err
	}
	var outputs []paSourceOutput
	if err := json.Unmarshal([]byte(out), &outputs); err != nil {
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

func ListSources() ([]Device, error) {
	out, err := runPactl("--format=json", "list", "sources")
	if err != nil {
		return nil, err
	}

	var sources []paSource
	if err := json.Unmarshal([]byte(out), &sources); err != nil {
		return nil, err
	}

	defaultSource, _ := GetDefaultSource()

	devices := make([]Device, 0, len(sources))
	for _, src := range sources {
		devType := DeviceTypeMic
		if strings.HasSuffix(src.Name, ".monitor") {
			devType = DeviceTypeMonitor
		}
		devices = append(devices, Device{
			Name:        src.Name,
			Description: src.Description,
			Type:        devType,
			IsDefault:   src.Name == defaultSource,
			Index:       src.Index,
			State:       src.State,
		})
	}

	return devices, nil
}

func ListSinks() ([]Device, error) {
	out, err := runPactl("--format=json", "list", "sinks")
	if err != nil {
		return nil, err
	}

	var sinks []paSink
	if err := json.Unmarshal([]byte(out), &sinks); err != nil {
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

type VirtualSink struct {
	nullModuleID     string
	loopbackModuleID string
	originalSink     string
	sinkName         string
	mu               sync.Mutex
	cleaned          bool
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

func (v *VirtualSink) saveState() error {
	state := virtualSinkState{
		PID:              os.Getpid(),
		SinkName:         v.sinkName,
		NullModuleID:     v.nullModuleID,
		LoopbackModuleID: v.loopbackModuleID,
		OriginalSink:     v.originalSink,
	}

	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}

	statePath, err := getStateFilePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(statePath), 0755); err != nil {
		return err
	}

	return os.WriteFile(statePath, data, 0644)
}

func removeState() {
	if statePath, err := getStateFilePath(); err == nil {
		os.Remove(statePath)
	}
}

func IsBluetoothMonitor(deviceName string) bool {
	return strings.HasPrefix(strings.TrimSuffix(deviceName, ".monitor"), "bluez_")
}

func NeedsVirtualSink(monitorName string) bool {
	return IsPipeWireRunning() && IsBluetoothMonitor(monitorName)
}

func NewVirtualSink(realSinkName string) (*VirtualSink, string, error) {
	CleanupOrphanedVirtualSinks()

	originalDefault, err := runPactl("get-default-sink")
	if err != nil || IsOwnSink(originalDefault) {
		originalDefault = realSinkName
	}

	sinkName := sinkNamePrefix + "_" + strconv.Itoa(os.Getpid())

	nullModuleID, err := runPactl("load-module", "module-null-sink",
		"sink_name="+sinkName,
		"sink_properties=device.description=RecgoRecordSink")
	if err != nil {
		return nil, "", fmt.Errorf("load null sink: %w", err)
	}

	loopbackModuleID, err := runPactl("load-module", "module-loopback",
		"source="+sinkName+".monitor",
		"sink="+realSinkName,
		"latency_msec=30")
	if err != nil {
		runPactl("unload-module", nullModuleID)
		return nil, "", fmt.Errorf("load loopback: %w", err)
	}

	if _, err := runPactl("set-default-sink", sinkName); err != nil {
		runPactl("unload-module", loopbackModuleID)
		runPactl("unload-module", nullModuleID)
		return nil, "", fmt.Errorf("set default sink: %w", err)
	}

	vs := &VirtualSink{
		nullModuleID:     nullModuleID,
		loopbackModuleID: loopbackModuleID,
		originalSink:     originalDefault,
		sinkName:         sinkName,
	}

	if err := vs.saveState(); err != nil {
		fmt.Fprintf(os.Stderr, "recgo: warning: failed to save state: %v\n", err)
	}

	monitorSource := sinkName + ".monitor"

	if err := WaitForSource(monitorSource, 2*time.Second); err != nil {
		fmt.Fprintf(os.Stderr, "recgo: warning: %v\n", err)
	}

	return vs, monitorSource, nil
}

func (v *VirtualSink) Cleanup() {
	if v == nil {
		return
	}

	v.mu.Lock()
	if v.cleaned {
		v.mu.Unlock()
		return
	}
	v.cleaned = true
	v.mu.Unlock()

	if v.originalSink != "" {
		if _, err := runPactl("set-default-sink", v.originalSink); err != nil {
			fmt.Fprintf(os.Stderr, "recgo: failed to restore default sink: %v\n", err)
		}
	}

	if v.loopbackModuleID != "" {
		if _, err := runPactl("unload-module", v.loopbackModuleID); err != nil {
			fmt.Fprintf(os.Stderr, "recgo: failed to unload loopback: %v\n", err)
		}
	}
	if v.nullModuleID != "" {
		if _, err := runPactl("unload-module", v.nullModuleID); err != nil {
			fmt.Fprintf(os.Stderr, "recgo: failed to unload null sink: %v\n", err)
		}
	}

	removeState()
}

func WaitForSource(sourceName string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		sources, err := ListSources()
		if err == nil {
			for _, src := range sources {
				if src.Name == sourceName {
					return nil
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("source %q not found after %v", sourceName, timeout)
}
