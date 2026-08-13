package ui

import (
	"context"
	"time"

	"github.com/charmbracelet/bubbles/help"
	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/eordano/recgo/internal/audio"
	"github.com/eordano/recgo/internal/config"
	"github.com/eordano/recgo/internal/transcribe"
)

type AppMode int

const (
	ModeNormal AppMode = iota
	ModeSelectMic
	ModeSelectMonitor
	ModeHelp
	ModeSettings
)

type Model struct {
	width  int
	height int

	config     *config.Config
	recordName string

	mode      AppMode
	recording bool
	startTime time.Time
	quitting  bool
	err       error

	mics         []audio.Device
	monitors     []audio.Device
	selectedMic  int
	selectedMon  int
	deviceCursor int
	micLevel     float64
	monitorLevel float64
	micMonitor   *audio.LevelMonitor
	monMonitor   *audio.LevelMonitor

	recorder    *audio.Recorder
	stats       audio.RecordingStats
	virtualSink *audio.VirtualSink

	savedOutput string

	segments         []string
	sessionFinalPath string

	transcribing  bool
	transcriptMic string
	transcriptSys string
	transcribeMic *transcribe.Session
	transcribeSys *transcribe.Session

	help          help.Model
	settingsPanel SettingsPanel
	keyMap        KeyMap
	ctx           context.Context
	cancel        context.CancelFunc

	sinkWatcher *audio.DefaultSinkWatcher

	lastRecalib     time.Time
	recalibAttempts int

	suppressSinkUntil time.Time

	focus FocusTarget
}

type FocusTarget int

const (
	FocusMic FocusTarget = iota
	FocusSys
	FocusTranscribe
)

var focusOrder = []FocusTarget{FocusMic, FocusSys, FocusTranscribe}

type KeyMap struct {
	Quit          key.Binding
	SelectDevice  key.Binding
	SelectMic     key.Binding
	SelectMonitor key.Binding
	Transcribe    key.Binding
	Cancel        key.Binding
	Help          key.Binding
	Settings      key.Binding
	SaveConfig    key.Binding
	CursorUp      key.Binding
	CursorDown    key.Binding
	CursorLeft    key.Binding
	CursorRight   key.Binding
	Confirm       key.Binding
}

func DefaultKeyMap() KeyMap {
	return KeyMap{
		Quit: key.NewBinding(
			key.WithKeys("q", "ctrl+c"),
			key.WithHelp("q", "quit"),
		),
		SelectDevice: key.NewBinding(
			key.WithKeys("1", "2", "3", "4", "5", "6", "7", "8", "9"),
			key.WithHelp("1-9", "select device"),
		),
		SelectMic: key.NewBinding(
			key.WithKeys("m"),
			key.WithHelp("m", "select mic"),
		),
		SelectMonitor: key.NewBinding(
			key.WithKeys("s"),
			key.WithHelp("s", "select system"),
		),
		Transcribe: key.NewBinding(
			key.WithKeys("t"),
			key.WithHelp("t", "toggle transcribe"),
		),
		Cancel: key.NewBinding(
			key.WithKeys("esc"),
			key.WithHelp("esc", "cancel"),
		),
		Help: key.NewBinding(
			key.WithKeys("?"),
			key.WithHelp("?", "help"),
		),
		Settings: key.NewBinding(
			key.WithKeys("c"),
			key.WithHelp("c", "settings"),
		),
		SaveConfig: key.NewBinding(
			key.WithKeys("ctrl+s"),
			key.WithHelp("ctrl+s", "save config"),
		),
		CursorUp: key.NewBinding(
			key.WithKeys("k", "up"),
			key.WithHelp("k/↑", "up"),
		),
		CursorDown: key.NewBinding(
			key.WithKeys("j", "down"),
			key.WithHelp("j/↓", "down"),
		),
		CursorLeft: key.NewBinding(
			key.WithKeys("h", "left"),
			key.WithHelp("h/←", "left"),
		),
		CursorRight: key.NewBinding(
			key.WithKeys("l", "right"),
			key.WithHelp("l/→", "right"),
		),
		Confirm: key.NewBinding(
			key.WithKeys("enter", " "),
			key.WithHelp("enter", "confirm"),
		),
	}
}

func (k KeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.SelectMic, k.SelectMonitor, k.Settings, k.Quit, k.Help}
}

func (k KeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.SelectMic, k.SelectMonitor, k.SelectDevice},
		{k.Settings, k.SaveConfig},
		{k.Cancel, k.Quit, k.Help},
	}
}

func (m Model) FinalRecording() string {
	if m.sessionFinalPath != "" {
		return m.sessionFinalPath
	}
	if m.recorder != nil {
		return m.recorder.OutputPath()
	}
	return ""
}

func NewModel(cfg *config.Config, recordName string) Model {
	ctx, cancel := context.WithCancel(context.Background())

	return Model{
		config:        cfg,
		recordName:    recordName,
		mode:          ModeNormal,
		keyMap:        DefaultKeyMap(),
		help:          help.New(),
		settingsPanel: NewSettingsPanel(cfg),
		ctx:           ctx,
		cancel:        cancel,
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(
		m.loadDevices,
		m.startSinkWatcher,
		diagTickCmd(),
		watchdogTickCmd(),
		tea.EnterAltScreen,
	)
}

func (m Model) startSinkWatcher() tea.Msg {
	w, err := audio.NewDefaultSinkWatcher(m.ctx)
	if err != nil {
		return sinkWatcherReadyMsg{err: err}
	}
	return sinkWatcherReadyMsg{watcher: w}
}

func waitForSinkChange(ch <-chan string) tea.Cmd {
	return func() tea.Msg {
		next, ok := <-ch
		if !ok {
			return nil
		}
		return defaultSinkChangedMsg{newSink: next}
	}
}

type devicesLoadedMsg struct {
	mics     []audio.Device
	monitors []audio.Device
	err      error
}

type recordingStartedMsg struct {
	err           error
	startTime     time.Time
	recorder      *audio.Recorder
	virtualSink   *audio.VirtualSink
	monitorSource string
}

type progressMsg audio.RecordingStats

type tickMsg time.Time

type diagTickMsg time.Time

type watchdogTickMsg time.Time

type recalibrateMsg struct {
	mics     []audio.Device
	monitors []audio.Device
	selMic   int
	selMon   int
	err      error
}

type maxDurationMsg struct{}

type errMsg error

type sinkWatcherReadyMsg struct {
	watcher *audio.DefaultSinkWatcher
	err     error
}

type defaultSinkChangedMsg struct {
	newSink string
}

type transcribeSource int

const (
	transcribeSourceMic transcribeSource = iota
	transcribeSourceSys
)

type transcribeStateMsg struct {
	src   transcribeSource
	state transcribe.State
}

type transcribeErrMsg struct {
	err error
}

func (m Model) loadDevices() tea.Msg {
	if err := audio.CheckBackend(); err != nil {
		return devicesLoadedMsg{err: err}
	}

	mics, monitors, err := audio.ListAllDevices()
	return devicesLoadedMsg{mics: mics, monitors: monitors, err: err}
}

func waitForProgress(ch <-chan audio.RecordingStats) tea.Cmd {
	return func() tea.Msg {
		stats, ok := <-ch
		if !ok {
			return nil
		}
		return progressMsg(stats)
	}
}

func tickCmd() tea.Cmd {
	return tea.Every(100*time.Millisecond, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

const diagInterval = 3 * time.Minute

func diagTickCmd() tea.Cmd {
	return tea.Tick(diagInterval, func(t time.Time) tea.Msg {
		return diagTickMsg(t)
	})
}

const watchdogInterval = 2 * time.Second

const sinkChurnWindow = 3 * time.Second

func watchdogTickCmd() tea.Cmd {
	return tea.Tick(watchdogInterval, func(t time.Time) tea.Msg {
		return watchdogTickMsg(t)
	})
}
