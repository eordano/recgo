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
| `recgo-desktop` click screenshots | works (macOS) | Accessibility (or Input Monitoring) TCC grant; not on Linux yet; `--click-shots=false` disables |
| `recgo-desktop` focus/dialog screenshots | works (macOS) | rides the Screen Recording grant; not on Linux yet; `--focus-shots=false` disables |
| `recgo-desktop` window capture | unsupported | interactive `-w` mode removed (blocked waiting for a click) |
| `recgo-browser` / `recgo-tab` | works, no TCC | a Chromium-family browser; auto-discovers Chromium/Chrome/Brave/Edge app bundles when `--chromium` is unset |
| Session default out dir | guarded | if `~/Documents` is iCloud-synced, defaults to `~/walk-and-talk` with a warning (all three session CLIs) |

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

The TUI is one btop-style screen: an Audio Sources panel (mic and system
rows with per-source VU meters), a Recording Stats panel (duration, file
size, bitrate, format, output path), a Live Transcription panel, and a
keybinding footer.

ffmpeg runs as a subprocess capturing from PulseAudio/PipeWire (avfoundation
on macOS) into the output file; its stderr progress lines are parsed into a
channel that drives the stats panel. Devices come from
`LC_NUMERIC=C pactl --format=json list sources/sinks` plus
`pactl get-default-source` / `-sink`.

Live transcription has two modes. Local runs ffmpeg's `whisper` audio filter
(`-af "whisper=model=<ggml model>:language=…:queue=…:destination=…:format=srt"`),
which needs FFmpeg 8.0+ built with the filter and a whisper.cpp ggml model.
Remote POSTs overlapping audio chunks to the OpenAI-compatible
`/v1/audio/transcriptions` endpoint configured under `[transcription.remote]`,
deduplicating the overlap on merge. Either way the transcript is saved as an
`.srt` next to the recording.

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
