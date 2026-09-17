package ui

import (
	"context"
	"fmt"
	"io"
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

	// Headless: no terminal at all, the state the TUI would draw goes out
	// as one line per change so a GUI (the desktop app) can show it.
	headless       bool
	out            io.Writer
	wantMic        string
	wantMon        string
	autoTranscribe bool
	lastErrText    string

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
	speakerMic    string
	speakerSys    string
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
	return NewModelWith(cfg, recordName, Options{})
}

// Options is what `recgo -headless` adds over the TUI: the protocol writer,
// a device picked up front instead of with the m/s keys, and transcription
// on from the start instead of with the t key.
type Options struct {
	Headless   bool
	Out        io.Writer
	Mic        string
	Monitor    string
	Transcribe bool
}

func NewModelWith(cfg *config.Config, recordName string, o Options) Model {
	ctx, cancel := context.WithCancel(context.Background())

	return Model{
		config:         cfg,
		recordName:     recordName,
		mode:           ModeNormal,
		keyMap:         DefaultKeyMap(),
		help:           help.New(),
		settingsPanel:  NewSettingsPanel(cfg),
		ctx:            ctx,
		cancel:         cancel,
		headless:       o.Headless,
		out:            o.Out,
		wantMic:        o.Mic,
		wantMon:        o.Monitor,
		autoTranscribe: o.Transcribe,
		transcribing:   o.Transcribe,
	}
}

// Err is the last error the TUI would be showing; headless callers exit
// non-zero on it when no recording was produced.
func (m Model) Err() error { return m.err }

// StopMsg asks for the same shutdown the q key does: stop the recorder,
// restore the output device, concat the segments.
type StopMsg struct{}

// CommandMsg is one line typed on the headless recorder's stdin.
type CommandMsg string

func (m Model) say(format string, a ...any) {
	if m.headless && m.out != nil {
		fmt.Fprintf(m.out, format+"\n", a...)
	}
}

func (m Model) clock() string {
	if m.startTime.IsZero() {
		return "00:00:00"
	}
	s := int(time.Since(m.startTime).Seconds())
	return fmt.Sprintf("%02d:%02d:%02d", s/3600, s/60%60, s%60)
}

func indexByName(devices []audio.Device, name string) int {
	for i, d := range devices {
		if d.Name == name {
			return i
		}
	}
	return -1
}

func (m Model) Init() tea.Cmd {
	cmds := []tea.Cmd{m.loadDevices, m.startSinkWatcher, diagTickCmd(), watchdogTickCmd()}
	if !m.headless {
		cmds = append(cmds, tea.EnterAltScreen)
	}
	return tea.Batch(cmds...)
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
