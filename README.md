# recgo - A btop-inspired Audio Recorder

A terminal UI audio recording application written in Go, featuring real-time
VU meters, device switching, and optional Whisper transcription (local or remote).

## Features

- **btop-inspired TUI** using Bubbletea + Lipgloss
- **Multi-source recording** - microphone + system audio (PipeWire/PulseAudio)
- **Real-time VU meters** with Unicode block visualization
- **Vim-like keybindings** for navigation and control
- **Live transcription** via FFmpeg whisper filter or OpenAI-compatible API
- **Configurable** via TOML in `$XDG_CONFIG_HOME/recgo/config.toml`

## macOS

recgo runs on macOS using ffmpeg's `avfoundation` input. Microphone capture
works out of the box; **system audio needs a loopback device** because macOS
has no monitor sources.

### Support matrix

| Feature | Status | Needs |
|---|---|---|
| `recgo` mic recording | works | ffmpeg; Microphone TCC grant for the terminal app |
| `recgo` system audio | works | BlackHole + a Multi-Output Device + `switchaudio-osx` (below) |
| `recgo` live transcription (`t`) | works | same as mic/system capture |
| Default-device resolution | works | `SwitchAudioSource` on PATH (falls back to avfoundation `:default` without it) |
| Default-output watcher / re-assert | works | `SwitchAudioSource` (2s poll; no CoreAudio event API without cgo) |
| Silence watchdog | works (on by default) | — |
| `recgo-desktop` | works | `screencapture(1)` (ships with macOS); Screen Recording + Microphone TCC grants |
| `recgo-desktop` window capture | unsupported | interactive `-w` mode removed (blocked waiting for a click) |
| `recgo-tab` / `recgo-browser` | works, no TCC | a Chromium-family browser; auto-discovers Chromium/Chrome/Brave/Edge app bundles when `--chromium` is unset |
| `recgo-tab` default out dir | guarded | if `~/Documents` is iCloud-synced, defaults to `~/walk-and-talk` with a warning |

### System audio: BlackHole + Multi-Output Device

1. Install [BlackHole](https://github.com/ExistentialAudio/BlackHole) and the
   default-output switcher recgo shells out to:

   ```bash
   brew install blackhole-2ch switchaudio-osx
   ```

2. Open **Audio MIDI Setup**, click `+` → *Create Multi-Output Device*, tick
   both your real speakers/headphones and *BlackHole 2ch*, and name the device
   exactly `Multi-Output Device` (the default value of the
   `record_output_device` config key).

3. Record. On start, recgo saves your current default output, switches the
   default output to `record_output_device` (so audio flows both to your
   speakers and into BlackHole), records system audio from the BlackHole
   source, and restores the original output on quit.

While recording, recgo polls the default output every ~2s (via
`SwitchAudioSource -c -t output`) and re-asserts `record_output_device` if
something else (AirPods auto-switching, another app) steals the default. If
*you* deliberately switch outputs twice within a few seconds, your choice
wins and recgo stops re-asserting. Set `record_output_device = ""` to disable
all output switching.

If no BlackHole device is present, the `S` (system audio) row in the TUI
shows a hint instead of a device — mic-only recording still works.

### TCC permissions

The first real capture makes macOS prompt for **Microphone** access for your
terminal app (Terminal/iTerm/kitty/…). Grant it under System Settings →
Privacy & Security → Microphone; without it ffmpeg's avfoundation capture
fails or records silence. Screen-recording permission is not needed for
audio-only recording.

### Paths

recgo uses XDG paths on macOS too (not `~/Library/Application Support`):
config at `$XDG_CONFIG_HOME/recgo/config.toml` (default
`~/.config/recgo/config.toml`), logs at `$XDG_STATE_HOME/recgo/recgo.log`
(default `~/.local/state/recgo/recgo.log`).

```toml
# macOS-relevant config keys
record_output_device = "Multi-Output Device"  # "" disables output switching

[watchdog]
enabled = true   # silence watchdog re-asserts the output device on recovery
```

## Architecture

```
┌─────────────────────────────────────────────────────────────────────┐
│  recgo v0.1.0                              Recording ● 00:12:34     │
├─────────────────────────────────────────────────────────────────────┤
│  ┌─ Audio Sources ──────────────────┐  ┌─ Recording Stats ───────┐ │
│  │ ● [M] Mic: Blue Yeti (default)   │  │  Duration: 00:12:34     │ │
│  │   [S] Sys: pipewire-sink.monitor │  │  File size: 12.4 MB     │ │
│  │                                  │  │  Bitrate: 128 kbps      │ │
│  │  VU Meter (mic):                 │  │  Format: AAC stereo     │ │
│  │  ▁▂▃▅▆▇█▇▅▃▂▁▂▃▅▆▇█▇▅▃▂▁        │  │                         │ │
│  │                                  │  │  Output:                │ │
│  │  VU Meter (sys):                 │  │  ~/archive/recordings/  │ │
│  │  ▁▁▂▃▄▅▆▇████▇▆▅▄▃▂▁▁           │  │  2026.01.07-ariel.mkv   │ │
│  └──────────────────────────────────┘  └─────────────────────────┘ │
│                                                                     │
│  ┌─ Live Transcription (whisper) ───────────────────────────────┐  │
│  │ So as I was saying, the architecture needs to handle both    │  │
│  │ local and remote processing. We could use a ring buffer...   │  │
│  │ █                                                            │  │
│  └──────────────────────────────────────────────────────────────┘  │
│                                                                     │
├─────────────────────────────────────────────────────────────────────┤
│ [t] toggle transcribe  [1-9] sources  [c] settings  [q] quit  [?]  │
└─────────────────────────────────────────────────────────────────────┘
```

## Data Flow

```
┌──────────────────────────────────────────────────────────────────────────┐
│                           INFORMATION FLOWS                              │
└──────────────────────────────────────────────────────────────────────────┘

1. AUDIO CAPTURE PIPELINE
   ┌─────────┐      ┌─────────────┐      ┌──────────────┐
   │ PipeWire│──────│ FFmpeg      │──────│ Output File  │
   │ /ALSA   │      │ (subprocess)│      │ (.mkv/.opus) │
   └─────────┘      └──────┬──────┘      └──────────────┘
                          │
                    stderr parsing
                          │
                    ┌─────▼─────┐
                    │ Progress  │──────► TUI Update
                    │ Channel   │        (size, time, bitrate)
                    └───────────┘

2. DEVICE DISCOVERY (via pactl --format=json)
   ┌──────────────┐      ┌───────────┐      ┌─────────────┐
   │ pactl list   │──────│ JSON Parse│──────│ Device List │
   │ sources/sinks│      │           │      │ Model       │
   └──────────────┘      └───────────┘      └─────────────┘

3. TRANSCRIPTION PIPELINE (two modes)

   MODE A: Local (ffmpeg whisper filter)
   ┌─────────┐      ┌─────────────────────┐      ┌───────────┐
   │ Audio   │──────│ ffmpeg -af whisper  │──────│ SRT File  │
   │ Stream  │      │ (model=base.en)     │      │           │
   └─────────┘      └──────────┬──────────┘      └───────────┘
                               │
                         HTTP POST output
                               │
                         ┌─────▼─────┐
                         │ Text Chan │──────► TUI Transcript Panel
                         └───────────┘

   MODE B: Remote (OpenAI-compatible API)
   ┌─────────┐      ┌───────────┐      ┌─────────────────────┐
   │ Audio   │──────│ 20s chunks│──────│ POST /v1/audio/     │
   │ Stream  │      │ Ring Buf  │      │ transcriptions      │
   └─────────┘      └───────────┘      │ @ llm.example.com   │
                                       └──────────┬──────────┘
                                                  │
                                            JSON response
                                                  │
                                            ┌─────▼─────┐
                                            │ Text Chan │──► TUI
                                            └───────────┘
```

---

## Technical Research

### 1. Audio Device Discovery

#### PulseAudio/PipeWire Device Listing

Use `pactl` with JSON output:
```bash
LC_NUMERIC=C pactl --format=json list sources
LC_NUMERIC=C pactl --format=json list sinks
```

Get default devices:
```bash
pactl get-default-source
pactl get-default-sink
```

Special device names:
- `@DEFAULT_SINK@` - default output
- `@DEFAULT_SOURCE@` - default input
- `@DEFAULT_MONITOR@` - default monitor

#### PipeWire-Specific Tools

- `pw-dump` - Complete state as JSON (best for comprehensive info)
- `pw-cli dump short Node` - List all nodes
- `pw-mon` - Monitor device changes in real-time

#### Device Hotplug Detection

PipeWire: Use `pw-mon` to monitor events
```bash
pw-mon | grep -E "(Device|added|removed)"
```

#### Go Libraries for PulseAudio (Pure Go, no CGO)

- **github.com/jfreymuth/pulse** - Recommended, pure Go
- **github.com/lawl/pulseaudio** - Used by noisetorch

### 2. FFmpeg Integration

#### Audio Capture

```bash
# PulseAudio/PipeWire
ffmpeg -f pulse -i default output.wav
ffmpeg -f pulse -i alsa_output.pci-0000_00_1f.3.analog-stereo.monitor system.wav

# Mixed (mic + system)
ffmpeg -f pulse -i default.monitor -f pulse -i default \
  -filter_complex "[0:a][1:a]amerge=inputs=2[a]" \
  -map "[a]" output.mkv
```

#### Progress Parsing

FFmpeg outputs to stderr:
```
size=    1280KiB time=00:01:24.98 bitrate= 123.4kbits/s speed=0.999x
```

Parse with regex:
```go
re := regexp.MustCompile(`size=\s*(\d+)KiB\s+time=(\d+:\d+:\d+\.\d+)\s+bitrate=\s*([\d.]+)kbits/s\s+speed=([\d.]+)x`)
```

#### Audio Level Detection

Use `astats` filter for real-time analysis:
```bash
ffmpeg -f pulse -i default -af "astats=metadata=1:reset=10" -f null -
```

Or `volumedetect` for simpler peak detection:
```bash
ffmpeg -f pulse -i default -af "volumedetect" -f null /dev/null
```

#### Subprocess Management

- Use `signal.NotifyContext` for graceful shutdown
- Send SIGINT first (FFmpeg handles gracefully)
- Escalate to SIGKILL after timeout

```go
ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
defer stop()
cmd := exec.CommandContext(ctx, "ffmpeg", args...)
```

### 3. Bubbletea TUI Design

#### Project Structure

```
recgo/
├── cmd/recgo/main.go              # Entry point
├── internal/
│   ├── audio/
│   │   ├── devices_{linux,darwin}.go  # device discovery (pactl / avfoundation)
│   │   ├── ffmpeg.go              # FFmpeg subprocess
│   │   └── levels*.go             # VU level sampling
│   ├── transcribe/
│   │   └── realtime.go            # OpenAI-compatible realtime client
│   ├── ui/
│   │   ├── model.go               # Bubbletea model
│   │   ├── update.go              # Message handling
│   │   ├── view.go                # Rendering
│   │   ├── styles.go              # Lipgloss btop styling
│   │   ├── settings.go            # Settings screen
│   │   └── watchdog.go            # Silence watchdog
│   └── config/
│       └── config.go              # TOML config
├── go.mod
└── default.nix
```

#### Model Structure

```go
type Model struct {
    width, height int

    // Subcomponents
    devicePanel     DevicePanelModel
    statsPanel      StatsPanelModel
    transcriptPanel TranscriptPanelModel

    // State
    recording       bool
    startTime       time.Time

    // Audio
    devices         []AudioDevice
    selectedMic     int
    selectedSys     int
    micLevel        float64
    sysLevel        float64

    // FFmpeg
    ffmpeg          *exec.Cmd
    progressChan    chan RecordingStats

    // Transcription
    transcriptMode  TranscriptMode
    transcriptChan  chan string
    transcriptText  []string
    viewport        viewport.Model

    // UI
    help            help.Model
    mode            AppMode
}
```

#### Concurrent Updates

Use channels and `tea.Cmd` for background tasks:

```go
func waitForProgress(ch chan RecordingStats) tea.Cmd {
    return func() tea.Msg {
        return ProgressMsg(<-ch)
    }
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    switch msg := msg.(type) {
    case ProgressMsg:
        m.stats = RecordingStats(msg)
        return m, waitForProgress(m.progressChan)
    }
}
```

#### Lipgloss Styling (btop-inspired)

```go
var (
    borderColor = lipgloss.Color("#5C7CFA")

    panelStyle = lipgloss.NewStyle().
        Border(lipgloss.RoundedBorder()).
        BorderForeground(borderColor).
        Padding(0, 1)

    titleStyle = lipgloss.NewStyle().
        Bold(true).
        Foreground(lipgloss.Color("#FFFFFF"))

    // VU meter colors
    greenColor  = lipgloss.Color("#00FF00")
    yellowColor = lipgloss.Color("#FFFF00")
    redColor    = lipgloss.Color("#FF0000")
)

// VU meter blocks: ▁▂▃▄▅▆▇█
var vuBlocks = []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}
```

#### Vim-like Keybindings

```go
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
    switch msg := msg.(type) {
    case tea.KeyMsg:
        switch msg.String() {
        case "q", "ctrl+c":
            return m, m.gracefulShutdown()
        case "j":
            m.viewport.LineDown(1)
        case "k":
            m.viewport.LineUp(1)
        case "g":
            m.viewport.GotoTop()
        case "G":
            m.viewport.GotoBottom()
        case "1", "2", "3", "4", "5", "6", "7", "8", "9":
            return m.selectDevice(int(msg.String()[0] - '0'))
        case "t":
            return m.toggleTranscription()
        case "c":
            return m.showSettings()
        case "?":
            m.showHelp = !m.showHelp
        }
    }
    return m, nil
}
```

### 4. Whisper Transcription

#### Local: FFmpeg Whisper Filter

Syntax:
```bash
ffmpeg -f pulse -i default -vn \
  -af "whisper=model=/path/to/ggml-base.en.bin:language=en:queue=5:destination=transcript.srt:format=srt" \
  -f null -
```

Parameters:
- `model` (required): Path to whisper.cpp model file
- `language`: Language code or "auto" (default: "auto")
- `queue`: Buffer size in seconds before processing (default: 3)
- `destination`: Output file or HTTP endpoint
- `format`: `text`, `srt`, or `json` (default: "text")
- `use_gpu`: Enable GPU acceleration (default: true)

Available models:

| Model | Size | Memory | Quality |
|-------|------|--------|---------|
| tiny | 75 MB | 273 MB | Fast, lower quality |
| base | 142 MB | 388 MB | Good balance |
| small | 466 MB | 852 MB | High quality |
| medium | 1.5 GB | 2.1 GB | Very high quality |
| large | 2.9 GB | 3.9 GB | Best (human-level) |

English-only variants (`.en`) are faster for English.

#### Remote: OpenAI-Compatible API

Endpoint: `POST https://llm.example.com/v1/audio/transcriptions`

```go
import openai "github.com/sashabaranov/go-openai"

config := openai.DefaultConfig("your-api-key")
config.BaseURL = "https://llm.example.com/v1"
client := openai.NewClientWithConfig(config)

req := openai.AudioRequest{
    Model:    openai.Whisper1,
    FilePath: "chunk.wav",
}
resp, _ := client.CreateTranscription(ctx, req)
```

Supported formats: `flac`, `mp3`, `m4a`, `mp4`, `ogg`, `wav`, `webm`
Max file size: 25 MB

#### Real-time Chunking Strategy

Use overlapping chunks to avoid cutting words:

```
Chunk 1: [0-20s] ─────────────┐
Chunk 2: [15-35s] ────────────┼─── 5s overlap
Chunk 3: [30-50s] ────────────┘
```

Deduplication: Drop last 5s of previous chunk's text when merging.

#### SRT Output

Save transcripts alongside recordings:
```
/home/user/archive/recordings/
├── 2026.01.07-15.54-meeting.mkv
└── 2026.01.07-15.54-meeting.srt
```

Use `github.com/asticode/go-astisub` for SRT manipulation.

---

## Configuration

Config file: `$XDG_CONFIG_HOME/recgo/config.toml`

```toml
[recording]
output_dir = "~/archive/recordings"
format = "mkv"           # mkv, opus, wav
audio_codec = "aac"      # aac, opus, flac
bitrate = "128k"
max_duration = "60m"

[audio]
prefer_pipewire = true
default_mic = ""         # empty = auto-detect
default_output = ""      # empty = auto-detect

[transcription.remote]
endpoint = "https://llm.example.com/v1"
api_key = ""             # or use OPENAI_API_KEY env var
model = "whisper"

[upload]
enabled = false
target = ""              # rsync/scp destination, e.g. host:/path
ssh_key = ""
organize_by_month = true

[ui]
show_vu_meters = true
refresh_rate = 10        # FPS for VU meters
theme = "dark"           # dark, light

[watchdog]
# Auto-recover a dead system-audio (monitor) capture mid-recording. If the
# monitor track flatlines at/below silence_db for silence_window, recgo
# re-resolves the default devices and restarts the capture (rebuilding the
# null-sink/loopback). Guards against restart storms on a genuinely silent call.
enabled = true
silence_db = -90.0       # dB floor treated as "dead" (≈16-bit noise floor; raise toward -75 to also catch barely-alive tracks)
silence_window = "30s"   # flatline duration before re-calibrating
cooldown = "60s"         # minimum gap between auto re-calibrations
max_attempts = 3         # consecutive attempts before giving up until audio returns (0 = no cap)
```

---

## Keybindings

| Key | Action |
|-----|--------|
| `t` | Toggle transcription |
| `1-9` | Select audio device |
| `c` | Settings |
| `j/k` | Scroll transcript down/up |
| `g/G` | Go to top/bottom of transcript |
| `?` | Toggle help |
| `q` | Quit (graceful stop) |
| `Ctrl+C` | Force quit |

---

## Dependencies

### Go Libraries

- `github.com/charmbracelet/bubbletea` - TUI framework
- `github.com/charmbracelet/bubbles` - UI components
- `github.com/charmbracelet/lipgloss` - Styling
- `github.com/BurntSushi/toml` - Config parsing
- `github.com/gorilla/websocket` - CDP connection (recgo-tab/browser)
- `github.com/godbus/dbus/v5` - KDE screenshot portal (recgo-desktop)

### System Requirements

- FFmpeg 8.0+ (with whisper filter for local transcription)
- PipeWire or PulseAudio
- `pactl` command available

---

## Building

```bash
# With Nix
nix build .#recgo

# With Go
go build -o recgo ./cmd/recgo
```

## Usage

```bash
# Basic recording
recgo meeting-notes

# Record until quit, ignoring recording.max_duration
recgo --no-limit meeting-notes

# Upload the finished recording to the configured [upload] target
recgo --upload meeting-notes
```
