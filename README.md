# recgo - A btop-inspired Audio Recorder

A terminal UI audio recording application written in Go, featuring real-time
VU meters, device switching, and optional Whisper transcription (local or remote).

## Features

Android phones and emulators: [recgo-android](RECGO-ANDROID.md) records tapped
element metadata, with opt-in screenshots, scrcpy video, app logs and narration.

- **btop-inspired TUI** using Bubbletea + Lipgloss
- **Multi-source recording** - microphone + system audio (PipeWire/PulseAudio)
- **Real-time VU meters** with Unicode block visualization
- **Vim-like keybindings** for navigation and control
- **Live transcription** via FFmpeg whisper filter or OpenAI-compatible API
- **Configurable** via TOML in `$XDG_CONFIG_HOME/recgo/config.toml`
- **Headless** with `recgo -headless <name>`: the same recording without a
  terminal, the state the TUI would draw printed on stderr one line per change
  (`recording -> PATH`, `mic:`/`monitor:`, `HH:MM:SS  narration:`/`system:`/
  `hearing:`, `error:`, `wrote PATH`, `Uploaded to`), SIGINT or `q` on stdin to
  stop, `t` / `transcribe on|off` on stdin like the key, `-mic`/`-system-audio`
  to pin a device and `-transcribe` to start with it on. The desktop apps' Audio
  only mode is this.

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
| `--stt-backend realtime` (session recorders) | works | an OpenAI-compatible endpoint whose `/v1/realtime` websocket does server-VAD transcription (speaches); narration lands within a second, the final transcript still comes from the batch endpoint |
| Default-device resolution | works | `SwitchAudioSource` on PATH (falls back to avfoundation `:default` without it) |
| Default-output watcher / re-assert | works | `SwitchAudioSource` (2s poll; no CoreAudio event API without cgo) |
| Silence watchdog | works (on by default) | — |
| `recgo-desktop` | works | `screencapture(1)` (ships with macOS); Screen Recording + Microphone TCC grants |
| `recgo-desktop` click screenshots | works | macOS: Accessibility (or Input Monitoring) TCC grant; Linux (KDE Plasma): your user in the `input` group (evdev for the press, a KWin script for the pointer position and the window under it); `--click-shots=false` disables |
| `recgo-desktop` focus/dialog screenshots | works | macOS: rides the Screen Recording grant; Linux (KDE Plasma): a KWin script reports focus changes and new windows; `--focus-shots=false` disables |
| `recgo-desktop` window capture | unsupported | interactive `-w` mode removed (blocked waiting for a click) |
| `recgo-alttester` (desktop capture + Unity element under each click) | works on KDE Plasma | same grants as `recgo-desktop`, plus the dev build launched with `--alttester 127.0.0.1:13000` dialing recgo; see [RECGO-ALTTESTER.md](RECGO-ALTTESTER.md) |
| `recgo-window` (one screen, picked at start) | works | same grants as `recgo-desktop`; `-screen N` (numbers from `-list-screens`, or `main`) skips the terminal prompt, which is how the app passes its picker's choice |
| `recgo-browser` / `recgo-tab` | works, no TCC | a Chromium-family browser; auto-discovers Chromium/Chrome/Brave/Edge app bundles when `--chromium` is unset |
| Session default out dir | guarded | Linux and Windows: `~/walk-and-talk` (or `[recording] output_dir` from config.toml); macOS: `~/Documents/walk-and-talk`, falling back to `~/walk-and-talk` with a warning when Documents is iCloud-synced (all three session CLIs and the app) |

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

## Windows

recgo-desktop and recgo-window run on Windows 10/11 from a plain
cross-compiled `recgo-desktop.exe` (`GOOS=windows GOARCH=amd64 CGO_ENABLED=0
go build ./cmd/...` — standard library only, no cgo). Audio goes through
ffmpeg's `dshow` input; the screen through GDI; clicks through a low-level
mouse hook. Nothing needs a permission prompt.

### Support matrix

| Feature | Status | Needs |
|---|---|---|
| `recgo-desktop` mic recording | works | ffmpeg on PATH (`winget install ffmpeg`); the default is the first `dshow` audio device, `-mic "<name>"` picks another (names from `ffmpeg -list_devices true -f dshow -i dummy`) |
| `recgo-desktop` system audio | needs a loopback device | Windows has no monitor sources: `-system-audio default` takes the sound card's **Stereo Mix** pin if it exists, else a virtual cable (`-system-audio "CABLE Output (VB-Audio Virtual Cable)"`); without one the recorder says so and records the microphone alone |
| `recgo-desktop` screenshots | works | GDI `BitBlt` of the virtual screen (or the picked display), physical pixels — the process declares per-monitor DPI awareness, so a 125% display captures at 1920x1200, not 1536x960 |
| `recgo-desktop` click screenshots | works, no permissions | a `WH_MOUSE_LL` hook on a thread of the recorder's own; positions are physical pixels, so they line up with the screenshots directly (`--click-shots=false` disables) |
| `recgo-desktop` focus/dialog screenshots | works, no permissions | the foreground window is polled every 200ms, the top-level window list every 500ms; a window is named `<class>: <title>` (`--focus-shots=false` disables) |
| Frame history (`-before` / `-after` shots, `— screen did not repaint`) | absent | like macOS there is no frame pump: each shot is one screenshot at the instant, so the repaint evidence never appears and the `-before` / `-after` siblings carry a note instead of a file |
| `recgo-window` (one screen, picked at start) | works | `-screen N` from `-list-screens` (EnumDisplayMonitors order), or `main`; one display needs no prompt |
| `recgo-browser` / `recgo-tab` | works | a Chromium-family browser; Chrome, Edge, Brave and Chromium are auto-discovered under Program Files and LocalAppData when `--chromium` is unset |
| `recgo` (the TUI) | audio only, degraded | compiles and records through dshow, but the live system-audio toggle, virtual sinks and the default-output watcher are PipeWire/PulseAudio features and report themselves unsupported |
| Session default out dir | `%USERPROFILE%\walk-and-talk` | or `[recording] output_dir` in `%USERPROFILE%\.config\recgo\config.toml` (XDG layout on Windows too) |

### Interactive session only

A process started from an ssh session, a service or a scheduled task
without the interactive principal lands in **session 0**, where there is no
desktop to capture: `recgo-desktop` refuses to start with `screen capture:
BitBlt: Access is denied. (a service or ssh session has no interactive
desktop)` rather than record black. Run it from a console or RDP session of
the logged-in user, or from a scheduled task created by that user.

### Stopping ffmpeg

The recorder starts ffmpeg in its own console process group and stops it with
`Ctrl-Break`, which ffmpeg treats like `Ctrl-C`; without a console (a hidden
task) it is killed instead. The microphone path streams raw PCM and writes
the WAV itself, so a kill loses nothing.

## Linux (KDE)

The same shell exists for Plasma as `recgo-app` (`linux/`, Python/PySide6):
tray icon and menu, global shortcuts through KGlobalAccel, HUD and live
session window kept above by a KWin script, the Library, and Settings. It
drives the same recorders with the same flags. See `linux/README.md` for the
Mac → KDE mapping and the `desktop.recgo.app` NixOS options.

```bash
nix build .#recgo-app && result/bin/recgo-app
recgo-app --record screen   # verbs forward to the running tray instance
recgo-app --record window   # one screen or window, picked in the portal dialog
```

## recgo-window: one screen, picked at start

`recgo-desktop` records every screen. `recgo-window` is the same recorder
(same flags, same session document, same local-first transcription) pinned
to one source chosen when it starts, and asked for on every start — nothing
is remembered between sessions.

```bash
recgo-window                  # macOS: a numbered list on the terminal; Linux: the portal's picker
recgo-window -screen 2        # macOS: skip the prompt (numbers from -list-screens, or main)
recgo-window -list-screens    # one "N<tab>WxH<tab>main" line per display
```

- **Linux** goes through the desktop's own screencast picker
  (xdg-desktop-portal, without a restore token), which offers every screen
  *and* single windows. Frames come from that stream alone; on KDE the click,
  focus and mark screenshots are `CaptureScreen` of the picked output when it
  can be told apart by size, else the latest stream frame. `-screen` is
  ignored there, with a note.
- **macOS** captures the chosen display with `screencapture -D N`. With one
  display there is no prompt; with several and no terminal (the app), pass
  `-screen`.

Every session says what it captured: `Capture: … (screen DP-1, 2560x1440)`
in `SESSION.md`, and the tool name is `recgo-window`, so the Library filters
on it as its own mode. Clicks outside the picked source are still listed,
position only.

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

## Google Meet prompts

The KDE and Mac apps can ask **Record this Meet call?** after you join a call.
Enable **Settings → General → Ask to record Google Meet calls** (`meetPrompt`).
A NixOS module can enable this and start the tray/menu-bar app at login. The
Mac bundle must be built and installed separately with `mac/build.sh --install`.

Detection uses the local Chromium debugging endpoint on `127.0.0.1:9222`, and
checks for a visible hangup control on `https://meet.google.com/xxx-xxxx-xxx`.
A waiting room, muted microphone, or just an open Meet tab does not trigger it.
The detector reads no transcript or participant information. Meet can change its
UI, so this DOM check needs revisiting if prompts stop appearing.

- **Not now** is the default. Dismissing silences that joined call. A later rejoin
  can prompt again. There is a two-second join debounce and a ten-second startup
  grace (also on detector restarts).
- **Record call** starts audio-only capture with microphone **and** system audio,
  using the existing transcription, upload, and privacy settings. On KDE, enable
  microphone capture in Settings first. No audio is captured before acceptance.
- Leaving for ten confirmed seconds stops only the recording started by that
  prompt. A debugger outage does not count as leaving; use Recgo's Stop control
  if detection is unavailable. A manual stop suppresses restarting that call.
- Existing manual recordings are untouched. Disabling detection hands any active
  Meet recording back to manual control.

A Chromium configured with the loopback debugging flag picks it up on its next
browser start. An already-running browser must be fully quit and opened again
for new flags to take effect; recgo does not restart it. On macOS, start
Chromium with the flag before joining Meet:

```sh
open -a Chromium --args --remote-debugging-port=9222
```

This also requires fully quitting an existing Chromium process first. Firefox
and Safari are not supported by this detector. For Google Chrome (rather than
Chromium), [Chrome requires a non-default profile for remote debugging](https://developer.chrome.com/blog/remote-debugging-port);
use a dedicated profile, for example:

```sh
open -a 'Google Chrome' --args --remote-debugging-port=9222 \
  --user-data-dir="$HOME/Library/Application Support/Recgo/MeetChrome"
```

On macOS, select a **Multi-Output Device** containing your speakers and BlackHole
in Sound settings, and set Recgo's **Audio → Meet system audio device** to the
loopback name (default `BlackHole 2ch`). The microphone stays a separate input.
Unavailable system audio is an error for Meet recordings, rather than a silent
fallback to recording only your side.

For diagnostics, `recgo-meet-watch -once` prints a JSON snapshot and exits without
recording or prompting. The running KDE app also reports `meet=watching` or
`meet=browser_unavailable` in `recgo-app --status`. The helper speaks newline JSON
on stdout for the shells and is included in both the Nix package and Mac bundle.
