package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/eordano/recgo/internal/audio"
	"github.com/eordano/recgo/internal/logging"
	"github.com/eordano/recgo/internal/transcribe"
)

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		return m.handleKey(msg)

	case devicesLoadedMsg:
		if msg.err != nil {
			logging.Log("device loading failed: %v", msg.err)
			m.err = msg.err
			return m, nil
		}
		logging.Log("loaded %d mics, %d monitors", len(msg.mics), len(msg.monitors))
		for i, d := range msg.mics {
			logging.Log("  mic[%d]: %s (%s) default=%v", i, d.Name, d.Description, d.IsDefault)
		}
		for i, d := range msg.monitors {
			logging.Log("  mon[%d]: %s (%s) default=%v", i, d.Name, d.Description, d.IsDefault)
		}
		m.mics = msg.mics
		m.monitors = msg.monitors

		if def := audio.FindDefaultMic(m.mics); def != nil {
			for i := range m.mics {
				if m.mics[i].Name == def.Name {
					m.selectedMic = i
					break
				}
			}
		}
		if def := audio.FindDefaultMonitor(m.monitors); def != nil {
			for i := range m.monitors {
				if m.monitors[i].Name == def.Name {
					m.selectedMon = i
					break
				}
			}
		}

		if m.config.RecordOutputDevice != "" && m.savedOutput == "" {
			if prev, err := audio.GetDefaultOutput(); err != nil {
				logging.Log("could not read current output device (skipping switch): %v", err)
			} else if prev != "" {
				m.savedOutput = prev
				logging.Log("saved current output device %q; switching to %q for recording", prev, m.config.RecordOutputDevice)
				if err := audio.SetDefaultOutput(m.config.RecordOutputDevice); err != nil {
					logging.Log("failed to switch output to %q: %v", m.config.RecordOutputDevice, err)
					m.err = fmt.Errorf("switch output to %q: %w", m.config.RecordOutputDevice, err)
				}
			}
		}
		return m, m.startRecording

	case recordingStartedMsg:
		if msg.err != nil {
			logging.Log("recording start failed: %v", msg.err)
			m.err = msg.err
			return m, nil
		}
		logging.Log("recording started at %s", msg.startTime.Format("15:04:05"))
		m.recording = true
		m.startTime = msg.startTime
		m.recorder = msg.recorder
		m.virtualSink = msg.virtualSink
		m.err = nil
		m.stats = audio.RecordingStats{}
		m.micLevel = 0
		m.monitorLevel = 0

		segPath := msg.recorder.OutputPath()
		m.segments = append(m.segments, segPath)
		if m.sessionFinalPath == "" {
			m.sessionFinalPath = segPath
		}

		if m.selectedMic < len(m.mics) {
			if mon, err := audio.NewLevelMonitor(m.ctx, m.mics[m.selectedMic].Name); err == nil {
				m.micMonitor = mon
			} else {
				logging.Log("mic level monitor failed: %v", err)
			}
		}
		if msg.monitorSource != "" {
			if mon, err := audio.NewLevelMonitor(m.ctx, msg.monitorSource); err == nil {
				mon.SetSilenceThreshold(m.config.Watchdog.SilenceDB)
				m.monMonitor = mon
			} else {
				logging.Log("monitor level monitor failed: %v", err)
			}
		}

		cmds = append(cmds, tickCmd())
		if m.recorder != nil {
			cmds = append(cmds, waitForProgress(m.recorder.ProgressChan()))
		}
		maxDur := m.config.MaxDurationParsed()
		if maxDur > 0 {
			cmds = append(cmds, tea.Tick(maxDur, func(_ time.Time) tea.Msg {
				return maxDurationMsg{}
			}))
		}
		if m.transcribing && m.transcribeMic == nil && m.transcribeSys == nil {
			newM, tcmd := m.startTranscribe()
			m = newM
			if tcmd != nil {
				cmds = append(cmds, tcmd)
			}
		}
		return m, tea.Batch(cmds...)

	case progressMsg:
		m.stats = audio.RecordingStats(msg)
		if m.recorder != nil && m.recorder.IsRunning() {
			cmds = append(cmds, waitForProgress(m.recorder.ProgressChan()))
		}
		return m, tea.Batch(cmds...)

	case tickMsg:
		if m.recording {
			if m.micMonitor != nil {
				m.micLevel = m.micMonitor.Level()
			}
			if m.monMonitor != nil {
				m.monitorLevel = m.monMonitor.Level()
			}
			cmds = append(cmds, tickCmd())
		}
		return m, tea.Batch(cmds...)

	case diagTickMsg:
		m.logDiagnostics()
		cmds = append(cmds, diagTickCmd())
		return m, tea.Batch(cmds...)

	case watchdogTickMsg:
		cmds = append(cmds, watchdogTickCmd())
		if m.recording && m.config.Watchdog.Enabled && m.monMonitor != nil {
			silent := m.monMonitor.SilentFor()
			window := m.config.WatchdogSilenceWindow()
			if silent < window {
				if m.recalibAttempts != 0 {
					logging.Log("watchdog: system-audio recovered after %d attempt(s)", m.recalibAttempts)
					m.recalibAttempts = 0
				}
			} else if shouldRecalibrate(silent, window, time.Since(m.lastRecalib), m.config.WatchdogCooldown(), m.recalibAttempts, m.config.Watchdog.MaxAttempts) {
				m.lastRecalib = time.Now()
				m.recalibAttempts++
				logging.Log("watchdog: system-audio silent %s (>= %s) — re-calibrating (attempt %d/%d)",
					silent.Truncate(time.Second), window, m.recalibAttempts, m.config.Watchdog.MaxAttempts)
				m = m.pauseTranscribeForRestart()
				cmds = append(cmds, m.recalibrateScan)
			}
		}
		return m, tea.Batch(cmds...)

	case recalibrateMsg:
		if msg.err != nil {
			logging.Log("watchdog: device rescan failed: %v", msg.err)
			return m, nil
		}
		m.mics = msg.mics
		m.monitors = msg.monitors
		m.selectedMic = msg.selMic
		m.selectedMon = msg.selMon
		if m.recording {
			if runtime.GOOS == "darwin" && m.savedOutput != "" && m.config.RecordOutputDevice != "" {
				logging.Log("watchdog: re-asserting output device %q", m.config.RecordOutputDevice)
				if err := audio.SetDefaultOutput(m.config.RecordOutputDevice); err != nil {
					logging.Log("watchdog: failed to re-assert output %q: %v", m.config.RecordOutputDevice, err)
					m.err = fmt.Errorf("re-assert output %q: %w", m.config.RecordOutputDevice, err)
				}
			}
			m = m.armSinkChurn()
			cmds = append(cmds, m.restartRecording)
		}
		return m, tea.Batch(cmds...)

	case maxDurationMsg:
		if m.recording {
			return m.handleQuit()
		}
		return m, nil

	case errMsg:
		m.err = msg
		return m, nil

	case sinkWatcherReadyMsg:
		if msg.err != nil {
			logging.Log("sink watcher init failed: %v", msg.err)
			return m, nil
		}
		m.sinkWatcher = msg.watcher
		return m, waitForSinkChange(m.sinkWatcher.Changes())

	case transcribeStateMsg:
		if msg.state.Err != nil {
			m.err = msg.state.Err
			m = m.stopTranscribe()
			return m, nil
		}
		switch msg.src {
		case transcribeSourceMic:
			m.transcriptMic = msg.state.Locked
			if m.transcribeMic != nil {
				cmds = append(cmds, waitForTranscribe(m.transcribeMic.Updates(), transcribeSourceMic))
			}
		case transcribeSourceSys:
			m.transcriptSys = msg.state.Locked
			if m.transcribeSys != nil {
				cmds = append(cmds, waitForTranscribe(m.transcribeSys.Updates(), transcribeSourceSys))
			}
		}
		return m, tea.Batch(cmds...)

	case transcribeErrMsg:
		m.err = msg.err
		m = m.stopTranscribe()
		return m, nil

	case defaultSinkChangedMsg:
		logging.Log("default sink changed -> %s", msg.newSink)
		cmds = append(cmds, waitForSinkChange(m.sinkWatcher.Changes()))
		if runtime.GOOS == "darwin" {
			target := m.config.RecordOutputDevice
			switch {
			case shouldReassertOutput(m.recording, m.savedOutput, target, msg.newSink, time.Now(), m.suppressSinkUntil):
				m = m.armSinkChurn()
				logging.Log("default output drifted to %q while recording; re-asserting %q", msg.newSink, target)
				if err := audio.SetDefaultOutput(target); err != nil {
					logging.Log("failed to re-assert output %q: %v", target, err)
					m.err = fmt.Errorf("re-assert output %q: %w", target, err)
				}
			case m.recording && m.savedOutput != "" && target != "" && msg.newSink != target:
				logging.Log("ignoring default-output change %q during churn window (user override wins)", msg.newSink)
			}
			return m, tea.Batch(cmds...)
		}
		if audio.IsOwnSink(msg.newSink) {
			return m, tea.Batch(cmds...)
		}
		if sinkChurnActive(time.Now(), m.suppressSinkUntil) {
			logging.Log("ignoring default-sink change %q during self-induced restart window", msg.newSink)
			return m, tea.Batch(cmds...)
		}
		monName := msg.newSink + ".monitor"
		newIdx := -1
		for i, d := range m.monitors {
			if d.Name == monName {
				newIdx = i
				break
			}
		}
		if newIdx < 0 {
			cmds = append(cmds, m.loadDevices)
			return m, tea.Batch(cmds...)
		}
		if newIdx == m.selectedMon {
			return m, tea.Batch(cmds...)
		}
		m.selectedMon = newIdx
		if m.recording {
			m = m.armSinkChurn()
			m = m.pauseTranscribeForRestart()
			cmds = append(cmds, m.restartRecording)
		}
		return m, tea.Batch(cmds...)
	}

	return m, nil
}

func (m Model) handleKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.mode == ModeSettings {
		return m.handleSettingsKey(msg)
	}

	switch {
	case key.Matches(msg, m.keyMap.Quit):
		return m.handleQuit()

	case key.Matches(msg, m.keyMap.Help):
		m.help.ShowAll = !m.help.ShowAll
		return m, nil

	case key.Matches(msg, m.keyMap.Settings):
		m.mode = ModeSettings
		return m, nil

	case key.Matches(msg, m.keyMap.SaveConfig):
		return m.saveConfig()

	case key.Matches(msg, m.keyMap.Cancel):
		if m.mode == ModeSelectMic || m.mode == ModeSelectMonitor {
			m.mode = ModeNormal
		}
		return m, nil

	case key.Matches(msg, m.keyMap.SelectMic):
		if len(m.mics) > 0 {
			m.mode = ModeSelectMic
			m.deviceCursor = m.selectedMic
		}
		return m, nil

	case key.Matches(msg, m.keyMap.SelectMonitor):
		if len(m.monitors) > 0 {
			m.mode = ModeSelectMonitor
			m.deviceCursor = m.selectedMon
		}
		return m, nil

	case key.Matches(msg, m.keyMap.CursorUp):
		switch m.mode {
		case ModeSelectMic, ModeSelectMonitor:
			if m.deviceCursor > 0 {
				m.deviceCursor--
			}
		case ModeNormal:
			if i := indexOfFocus(m.focus); i > 0 {
				m.focus = focusOrder[i-1]
			}
		}
		return m, nil

	case key.Matches(msg, m.keyMap.CursorDown):
		switch m.mode {
		case ModeSelectMic:
			if m.deviceCursor < len(m.mics)-1 {
				m.deviceCursor++
			}
		case ModeSelectMonitor:
			if m.deviceCursor < len(m.monitors)-1 {
				m.deviceCursor++
			}
		case ModeNormal:
			if i := indexOfFocus(m.focus); i < len(focusOrder)-1 {
				m.focus = focusOrder[i+1]
			}
		}
		return m, nil

	case key.Matches(msg, m.keyMap.CursorLeft):
		return m, nil

	case key.Matches(msg, m.keyMap.CursorRight):
		return m, nil

	case key.Matches(msg, m.keyMap.Confirm):
		switch m.mode {
		case ModeSelectMic, ModeSelectMonitor:
			return m.commitDeviceCursor()
		case ModeNormal:
			return m.activateFocus()
		}
		return m, nil

	case key.Matches(msg, m.keyMap.Transcribe):
		if m.transcribing {
			return m.stopTranscribe(), nil
		}
		return m.startTranscribe()

	case key.Matches(msg, m.keyMap.SelectDevice):
		s := msg.String()
		if len(s) == 0 {
			return m, nil
		}
		idx := int(s[0] - '1')
		if idx < 0 || idx > 8 {
			return m, nil
		}
		changed := false
		switch m.mode {
		case ModeSelectMic:
			if idx < len(m.mics) && idx != m.selectedMic {
				m.selectedMic = idx
				changed = true
			}
			m.mode = ModeNormal
		case ModeSelectMonitor:
			if idx < len(m.monitors) && idx != m.selectedMon {
				m.selectedMon = idx
				changed = true
			}
			m.mode = ModeNormal
		}
		if changed {
			m = m.armSinkChurn()
			m = m.pauseTranscribeForRestart()
			return m, m.restartRecording
		}
		return m, nil
	}

	return m, nil
}

func indexOfFocus(f FocusTarget) int {
	for i, x := range focusOrder {
		if x == f {
			return i
		}
	}
	return 0
}

func (m Model) activateFocus() (Model, tea.Cmd) {
	switch m.focus {
	case FocusMic:
		if len(m.mics) > 0 {
			m.mode = ModeSelectMic
			m.deviceCursor = m.selectedMic
		}
		return m, nil
	case FocusSys:
		if len(m.monitors) > 0 {
			m.mode = ModeSelectMonitor
			m.deviceCursor = m.selectedMon
		}
		return m, nil
	case FocusTranscribe:
		if m.transcribing {
			return m.stopTranscribe(), nil
		}
		return m.startTranscribe()
	}
	return m, nil
}

func (m Model) commitDeviceCursor() (Model, tea.Cmd) {
	changed := false
	switch m.mode {
	case ModeSelectMic:
		if m.deviceCursor >= 0 && m.deviceCursor < len(m.mics) && m.deviceCursor != m.selectedMic {
			m.selectedMic = m.deviceCursor
			changed = true
		}
	case ModeSelectMonitor:
		if m.deviceCursor >= 0 && m.deviceCursor < len(m.monitors) && m.deviceCursor != m.selectedMon {
			m.selectedMon = m.deviceCursor
			changed = true
		}
	}
	m.mode = ModeNormal
	if changed {
		m = m.armSinkChurn()
		m = m.pauseTranscribeForRestart()
		return m, m.restartRecording
	}
	return m, nil
}

func (m Model) handleSettingsKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "c", "esc":
		m.mode = ModeNormal
		return m, nil

	case "ctrl+s":
		return m.saveConfig()

	default:
		var cmd tea.Cmd
		m.settingsPanel, cmd = m.settingsPanel.Update(msg)
		return m, cmd
	}
}

func (m Model) handleQuit() (tea.Model, tea.Cmd) {
	m.quitting = true

	if m.micMonitor != nil {
		m.micMonitor.Stop()
	}
	if m.monMonitor != nil {
		m.monMonitor.Stop()
	}

	if m.recorder != nil {
		m.recorder.Stop()
	}

	if m.virtualSink != nil {
		logging.Log("Cleaning up virtual sink")
		m.virtualSink.Cleanup()
	}

	if m.savedOutput != "" {
		logging.Log("restoring output device to %q", m.savedOutput)
		if err := audio.SetDefaultOutput(m.savedOutput); err != nil {
			logging.Log("failed to restore output device to %q: %v", m.savedOutput, err)
		}
		m.savedOutput = ""
	}

	if m.sinkWatcher != nil {
		m.sinkWatcher.Stop()
	}

	if m.transcribeMic != nil {
		m.transcribeMic.Stop()
		m.transcribeMic = nil
	}
	if m.transcribeSys != nil {
		m.transcribeSys.Stop()
		m.transcribeSys = nil
	}

	if len(m.segments) > 1 && m.sessionFinalPath != "" {
		if err := concatSession(m.segments, m.sessionFinalPath); err != nil {
			logging.Log("session concat failed: %v (segments preserved)", err)
		} else {
			logging.Log("session concat -> %s", m.sessionFinalPath)
		}
	}

	m.cancel()
	return m, tea.Quit
}

func concatSession(segments []string, finalPath string) error {
	parts := make([]string, len(segments))
	for i, seg := range segments {
		ext := filepath.Ext(seg)
		stem := strings.TrimSuffix(seg, ext)
		partPath := fmt.Sprintf("%s.part%d%s", stem, i+1, ext)
		if err := os.Rename(seg, partPath); err != nil {
			for j := 0; j < i; j++ {
				if rbErr := os.Rename(parts[j], segments[j]); rbErr != nil {
					logging.Log("rollback rename %s -> %s failed: %v", parts[j], segments[j], rbErr)
				}
			}
			return fmt.Errorf("rename segment %d (%s): %w", i+1, seg, err)
		}
		parts[i] = partPath
	}

	if err := audio.ConcatSegments(parts, finalPath); err != nil {
		for i, p := range parts {
			if rbErr := os.Rename(p, segments[i]); rbErr != nil {
				logging.Log("rollback rename %s -> %s failed: %v", p, segments[i], rbErr)
			}
		}
		return err
	}

	for _, p := range parts {
		if err := os.Remove(p); err != nil {
			logging.Log("remove part %s: %v", p, err)
		}
	}
	return nil
}

func (m Model) logDiagnostics() {
	snap := audio.Snapshot{
		Recording: m.recording,
	}
	if m.selectedMic < len(m.mics) {
		snap.SelectedMic = m.mics[m.selectedMic].Name
	}
	if m.selectedMon < len(m.monitors) {
		snap.SelectedMonitor = m.monitors[m.selectedMon].Name
	}
	if m.micMonitor != nil {
		st := m.micMonitor.StatsSinceLast()
		snap.MicStats = &st
	}
	if m.monMonitor != nil {
		st := m.monMonitor.StatsSinceLast()
		snap.MonStats = &st
	}
	if m.recorder != nil {
		snap.RecorderRunning = m.recorder.IsRunning()
		snap.OutputPath = m.recorder.OutputPath()
		snap.Duration = m.stats.Duration
		snap.SizeBytes = m.stats.Size
		snap.Bitrate = m.stats.Bitrate
		snap.Speed = m.stats.Speed
	}
	audio.LogSnapshot(snap)
}

func (m Model) recalibrateScan() tea.Msg {
	mics, monitors, err := audio.ListAllDevices()
	if err != nil {
		return recalibrateMsg{err: err}
	}
	selMic, selMon := m.selectedMic, m.selectedMon
	if def := audio.FindDefaultMic(mics); def != nil {
		for i := range mics {
			if mics[i].Name == def.Name {
				selMic = i
				break
			}
		}
	}
	if def := audio.FindDefaultMonitor(monitors); def != nil {
		for i := range monitors {
			if monitors[i].Name == def.Name {
				selMon = i
				break
			}
		}
	}
	return recalibrateMsg{mics: mics, monitors: monitors, selMic: selMic, selMon: selMon}
}

func (m Model) restartRecording() tea.Msg {
	logging.Log("restarting recording: mic=%d mon=%d", m.selectedMic, m.selectedMon)
	if m.micMonitor != nil {
		m.micMonitor.Stop()
	}
	if m.monMonitor != nil {
		m.monMonitor.Stop()
	}
	if m.recorder != nil {
		m.recorder.Stop()
	}
	if m.virtualSink != nil {
		m.virtualSink.Cleanup()
	}
	return m.startRecording()
}

func shouldReassertOutput(recording bool, savedOutput, target, newOutput string, now, suppressUntil time.Time) bool {
	if !recording || savedOutput == "" || target == "" || newOutput == target {
		return false
	}
	return !sinkChurnActive(now, suppressUntil)
}

func (m Model) armSinkChurn() Model {
	m.suppressSinkUntil = time.Now().Add(sinkChurnWindow)
	return m
}

func (m Model) pauseTranscribeForRestart() Model {
	if m.transcribeMic != nil {
		m.transcribeMic.Stop()
		m.transcribeMic = nil
	}
	if m.transcribeSys != nil {
		m.transcribeSys.Stop()
		m.transcribeSys = nil
	}
	return m
}

func (m Model) startRecording() tea.Msg {
	var micDevice, monDevice string
	var virtualSink *audio.VirtualSink
	var monitorSource string

	if m.selectedMic < len(m.mics) {
		micDevice = m.mics[m.selectedMic].Name
	}
	if m.selectedMon < len(m.monitors) {
		monDevice = m.monitors[m.selectedMon].Name
		monitorSource = monDevice

		if audio.NeedsVirtualSink(monDevice) {
			logging.Log("Bluetooth monitor detected under PipeWire, setting up virtual sink")
			realSink := audio.GetSinkNameFromMonitor(monDevice)
			var recordSource string
			var err error
			virtualSink, recordSource, err = audio.NewVirtualSink(realSink)
			if err != nil {
				logging.Log("failed to create virtual sink: %v", err)
				return recordingStartedMsg{err: err}
			}
			logging.Log("Virtual sink created, recording from %s instead of %s", recordSource, monDevice)
			monDevice = recordSource
			monitorSource = recordSource
		}
	}

	now := time.Now()
	outputPath := m.config.OutputPath(m.recordName, now)
	recorder := audio.NewRecorder(
		outputPath,
		micDevice,
		monDevice,
		m.config.Recording.AudioCodec,
		m.config.Recording.Bitrate,
	)

	if err := recorder.Start(m.ctx); err != nil {
		if virtualSink != nil {
			virtualSink.Cleanup()
		}
		return recordingStartedMsg{err: err}
	}

	return recordingStartedMsg{
		startTime:     now,
		recorder:      recorder,
		virtualSink:   virtualSink,
		monitorSource: monitorSource,
	}
}

func (m Model) saveConfig() (tea.Model, tea.Cmd) {
	if err := m.settingsPanel.SaveConfig(); err != nil {
		m.err = err
	}
	return m, nil
}

func (m Model) startTranscribe() (Model, tea.Cmd) {
	if m.selectedMic >= len(m.mics) && m.selectedMon >= len(m.monitors) {
		m.err = fmt.Errorf("transcribe: no inputs selected")
		return m, nil
	}
	cfg := transcribe.Config{
		Endpoint: m.config.Transcription.Remote.Endpoint,
		APIKey:   m.config.Transcription.Remote.APIKey,
		Model:    m.config.Transcription.Remote.Model,
	}

	var cmds []tea.Cmd

	if m.selectedMic < len(m.mics) {
		mic := m.mics[m.selectedMic].Name
		s := transcribe.New(m.ctx, mic, cfg)
		if err := s.Start(); err != nil {
			logging.Log("transcribe mic start failed: %v", err)
			return m, func() tea.Msg { return transcribeErrMsg{err: err} }
		}
		m.transcribeMic = s
		cmds = append(cmds, waitForTranscribe(s.Updates(), transcribeSourceMic))
	}

	if m.selectedMon < len(m.monitors) {
		mon := m.monitors[m.selectedMon].Name
		s := transcribe.New(m.ctx, mon, cfg)
		if err := s.Start(); err != nil {
			logging.Log("transcribe sys start failed: %v", err)
		} else {
			m.transcribeSys = s
			cmds = append(cmds, waitForTranscribe(s.Updates(), transcribeSourceSys))
		}
	}

	m.transcribing = true
	m.transcriptMic = ""
	m.transcriptSys = ""
	return m, tea.Batch(cmds...)
}

func (m Model) stopTranscribe() Model {
	if m.transcribeMic != nil {
		m.transcribeMic.Stop()
		m.transcribeMic = nil
	}
	if m.transcribeSys != nil {
		m.transcribeSys.Stop()
		m.transcribeSys = nil
	}
	m.transcribing = false
	return m
}

func waitForTranscribe(ch <-chan transcribe.State, src transcribeSource) tea.Cmd {
	return func() tea.Msg {
		st, ok := <-ch
		if !ok {
			return nil
		}
		return transcribeStateMsg{src: src, state: st}
	}
}
