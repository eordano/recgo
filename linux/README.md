# Recgo for KDE

The Linux counterpart of `../mac`: the same shell over the Go recorders,
written in Python/PySide6 (Qt 6) so it is native on a Plasma desktop. The
app owns no capture code: it spawns `recgo-desktop` / `recgo-window` /
`recgo-browser` / `recgo-tab` with the flags the Settings window maps to,
writes `m` to their stdin for marks, SIGINTs them to stop, tails
`SESSION.live.md` for the live document, and reads the `wrote <dir>/SESSION.md`
stderr line to know where the packed session landed. Audio only is `recgo
<name>` itself, run `-headless`: it asks for the name, and the recording is
exactly the terminal's (one mkv with mix, system and mic tracks in
config.toml's `[recording] output_dir`, its devices, watchdog, max duration
and `[upload]`), with recgo's `recording ->` / `narration:` / `wrote` lines
standing in for the live document.
Every knob, window and verb of the Mac app has a counterpart here; the table
below lists where the platform forced a different mechanism.

## Build and run

```
nix build .#recgo-app        # from the root of the flake that packages it
result/bin/recgo-app         # tray icon; opens the Library if already running
recgo-app --record screen    # verbs forward to the running instance
recgo-app --mark | --stop | --toggle-hud | --library | --settings
```

With the NixOS module, `desktop.recgo.app.enable` (default: on wherever Plasma is)
installs it with a desktop file and icon; `desktop.recgo.app.autostart` adds
the XDG autostart entry, and `desktop.recgo.shortcuts.*` seed the global
chords exactly as they do the `defaults` keys on darwin.

Settings live in `~/.config/recgo/Recgo.conf` (QSettings INI, same key names
as the Mac app's UserDefaults). The recorders' own config stays in
`~/.config/recgo/config.toml`.

## Mac → KDE mapping

| Mac | KDE |
|---|---|
| `NSStatusItem` + custom popover | StatusNotifierItem tray icon; the popover is the tray's context menu (Plasma renders it natively; left click opens it too). Rows carry the icon theme's icons (a line glyph of our own where the theme has none), and the amber arrow marks every line describing bytes that leave the computer |
| Elapsed time next to the menu bar icon | Plasma tray icons carry no text: the elapsed time is in the tooltip and the menu header, and the icon turns red (recording) / amber (finishing) |
| Carbon `RegisterEventHotKey` | KGlobalAccel over D-Bus (`org.kde.kglobalaccel`), so the chords also appear in System Settings → Shortcuts → Recgo and work on Wayland |
| HUD as a non-activating `NSPanel` excluded from capture | Frameless always-on-top tool window; on Wayland a KWin script (loaded over `org.kde.KWin /Scripting`) keeps it above, off the taskbar and hands focus back. KWin cannot exclude a window from the screencast, so the HUD **is** in the screenshots — collapse it to the pill or hide it (`--toggle-hud`) when that matters |
| `SMAppService` launch at login | `~/.config/autostart/dev.eordano.recgo.desktop` |
| `AVAudioPlayer` / `AVAssetExportSession` | `QMediaPlayer` for playback; clip export shells out to `ffmpeg -c:a aac` |
| Quick Look, Reveal in Finder | in-app lightbox; `org.freedesktop.FileManager1.ShowItems` (Dolphin) |
| `defaults write dev.eordano.recgo …` | `desktop.recgo.app.managed` / `desktop.recgo.shortcuts` → read-only `~/.config/recgo/managed.conf`, which overrides the app's own `Recgo.conf` and greys the row out |
| Click / focus screenshots | evdev for the press (user in the `input` group) plus a one-shot KWin script for the pointer position and the clicked window's title; a persistent KWin script reports focus changes and new windows |
| BlackHole for system audio | Settings → Audio → *Include system audio* passes `-system-audio` (a `.monitor` source, default output by default); the recorder mixes it with the mic, and the tray row / HUD button / `recgo-app --system-audio` mute it in place |
| `recgo-tab --select` TUI picker | a Qt list of the browser's page targets (skipped when only one is open); the pick is passed as `-target <id>`, and the HUD, tray and live window show that tab's title and current URL, followed over `/json/list` |
| Window mode's display picker (an NSAlert over `recgo-window -list-screens`, passed as `-screen N`) | nothing to pick in the app: `recgo-window` opens the portal's own screencast dialog, which offers every screen and single windows; its `capturing …` line feeds the HUD card and tray tooltip |

## Files

- `recgo_app/model.py` — the SESSION.md line grammar (a port of `cmd/recgo-sessions`), library scan, waveform bins
- `recgo_app/recorder.py` — one recorder subprocess: flags, marks, SIGINT, live doc polling, the `wrote` line, the `tab:` / `attached to:` / `system audio:` lines that feed the HUD, the `sysaudio on|off` stdin toggle, and the `hearing:` partials the realtime STT lane prints while an utterance is still open (the HUD shows them dimmed until the line lands)
- `recgo_app/cdp.py` — is anything on 127.0.0.1:9222, and which page targets it has (the tab picker's list, and the attached tab's live title/URL)
- `recgo_app/hotkeys.py` — chord parsing + KGlobalAccel registration (busctl for calls, QtDBus for the signal)
- `recgo_app/kwin.py` — the KWin script that pins the HUD/live windows
- `recgo_app/settings.py` — the store, and `data_lines()`: where this session's bytes go, derived from the same values the recorder flags are built from (plus the recorders' `config.toml` endpoint); the tray menu, HUD, live window and Settings → Data & sharing all show that one list, amber where something leaves the machine
- `recgo_app/tray.py`, `hud.py`, `live.py`, `library.py`, `settings_view.py` — the five surfaces. While recording, the menu and the HUD state the facts of the running session (source, folder, microphone, system audio, which moments are screenshotted) frozen at start, so a setting changed mid-session describes the next one
- `recgo_app/app.py` — entry point, single instance over a local socket, quit flow, `--shoot DIR` renders every window offscreen for tests
