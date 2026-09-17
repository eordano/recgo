# Recgo for Mac

The menu-bar shell over the Go recorders, implemented from the
"Live walk-and-talk demo" design (see ../APP-DESIGN.md for the rationale).
The app owns no capture code: it spawns `recgo-desktop` / `recgo-window` /
`recgo-browser` / `recgo-tab` with the flags the Settings window maps to,
writes `m` to their stdin for marks, SIGINTs them to stop, tails
`SESSION.live.md` for the live document, and reads the `wrote <dir>/SESSION.md`
stderr line to know where the packed session landed. Audio only is `recgo
<name>` itself, run `-headless`: it asks for the name, and the recording is
exactly the terminal's (one mkv with mix, system and mic tracks, recgo's own
config.toml for devices, output folder, watchdog and upload), with recgo's
`recording ->` / `narration:` / `wrote` lines standing in for the live
document.

## Build

```
./build.sh              # swift build + Recgo.app with the Go CLIs bundled in Contents/Helpers
./build.sh --no-helpers # SwiftUI shell only; recorders are found on PATH instead
./build.sh --install    # also copy the result to ~/Applications/Recgo.app
open .build/Recgo.app
```

cgo (the click tap) needs `LIBRARY_PATH="$(xcrun --show-sdk-path)/usr/lib"`;
build.sh sets it.

The app icon is drawn by `icon.swift` (the record.circle motif in the
Theme.swift palette) and packed with `iconutil`, so no binary artwork is
checked in. `--install` puts the bundle at a stable path, which is what
Spotlight, Launchpad, TCC grants, and a Dock pin all want; from there,
drag it to the Dock (or right-click → Options → Keep in Dock) to pin it.

Recgo launches as a menu-bar accessory (`LSUIElement`), but the Dock icon
follows the real windows: opening the Library, Settings, or the live session
window flips the activation policy to regular — icon in the Dock and ⌘Tab —
and closing the last of them hands it back.

## What is implemented

- Menu bar item (red dot + elapsed while recording; the timer can be turned
  off in Settings) opening a panel-styled popover: Screen / Window… /
  Browser / This Tab… / Audio only, audio track section, Library…, Settings…,
  and while recording Mark / HUD toggle / live-session toggle / Stop.
- Window… records one display: the popover asks which (a popup over
  `recgo-window -list-screens`, skipped with a single display) and passes it
  as `-screen N`; the pick is made fresh every time.
- Browser and Tab rows disable themselves (and their hotkeys explain) when nothing
  answers CDP on 127.0.0.1:9222, probed with the same /json/list the recorders use.
- Global hotkeys (Carbon, no Accessibility needed) for start/mark/HUD/stop/
  library — none bound by default; each verb takes a chord like
  `cmd+shift+1` in Settings → Shortcuts, or via
  `defaults write dev.eordano.recgo shortcutRecordScreen cmd+shift+1`
  (keys: shortcutRecordScreen/Window/Browser/Tab/Audio, shortcutMark,
  shortcutToggleHUD, shortcutStop, shortcutOpenLibrary).
- Recording HUD: floating non-activating panel, excluded from capture
  (`sharingType = .none`), with elapsed, level meter, the live narration
  line tailed from SESSION.live.md, portal state, Mark and Stop. Collapses
  to a pill.
- Live session window: SESSION.live.md parsed with the recgo-sessions line
  grammar and rendered as the demo's timeline, with a portal column.
- Library: sidebar (all/mode filters), search over titles + transcripts,
  session list, detail with screenshot strip (real PNGs), audio playback of
  `audio.wav` with waveform, rate cycling, clip in/out + play + export
  (AAC via AVAssetExportSession into `clips/`), timeline and reading views,
  and a shot lightbox with copy / reveal.
- When sync is on, the library toolbar gains "Copy remote" — the rsync
  location the selected session synced to, for handing to an agent elsewhere.
- Settings: General / Recording / Audio / Transcription / Privacy /
  Agents & Portal / Shortcuts / Library panes; every knob maps to a real
  recorder flag, and "Never upload anything" hard-forces `-stt-backend
  local`, `-no-sync`, and no portal.

## Deliberate divergences from the demo

- No countdown: recording starts immediately (dropped on request).
- The HUD's mode segment shows the current engine but cannot switch
  mid-session: the engines are separate binaries on one session clock each.
- No pause: the CLIs have no pause verb yet.
- The agent chat column shows portal/room state only; the portal is a
  file-read surface, not a message bus.
- Audio-only mode drives `recgo -headless <name>`, which has its own config
  surface: no marks, no session folder; the live transcript lines it prints
  are what the HUD shows.
