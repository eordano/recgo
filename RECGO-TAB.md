# recgo-tab — record a browser tab with narration

A second binary in this module. `recgo` records audio; `recgo-tab` records a
browser tab *and* audio, correlates them on one clock, and writes a folder an
LLM can act on.

```sh
recgo-tab --launch http://localhost:5173 --duration 30s
```

Output lands in `~/walk-and-talk/YYYY-MM-DD-HH-MM-<slug>/` (`[recording] output_dir` in `~/.config/recgo/config.toml` overrides; macOS keeps `~/Documents/walk-and-talk`):

```
SESSION.md     the deliverable, and the only document — one line per thing that happened
0001.png       the screen when recording started
NNNN.png       the screen at click N, with -before / -after / -full siblings
audio.wav      narration, 16kHz mono
logs/          raw dev-server websocket frames, including all HMR payloads
session.json   only with --json: full machine-readable timeline and clock report
```

When the recorder pushes the finished session (`--sync-target`, or the
`[upload] target` in `config.toml`) it prints `synced -> host:/path`, and when
that remote directory is also mounted on this machine it adds
`shared -> /path/<session>`: when the session root is mounted at the same
absolute path everywhere, that line pastes unchanged into an agent on any
other host.

## The document

One chronological stream, narration and events on the same clock:

```
# Session: Login popup dismisses itself with no feedback

Start: 2026-04-30 15:23:23
Folder: /home/user/src/acme
Page: http://localhost:5173/ — Acme dashboard
Recorded 47s by recgo-tab · 6 clicks · 2 errors · 14 utterances

Initial screenshot: 0001.png
00.00.01: Ok, so when we see this tab
00.00.03  Click: 34,23 on a#header-login text: Log in → 0002.png
00.00.04: we see the new login tab, but when i click
00.00.06  Click: 830,200 on button.modal__close → 0003.png — screen did not repaint
00.00.06  Error: network 500 http://localhost:5173/api/session
00.00.06: the pop up gets dismissed with no clear feedback
```

Times are `HH.MM.SS` since recording started. Narration lines carry a colon;
event lines name what happened. `— screen did not repaint` means all three
screencast frames around the click were the same frame: positive evidence
nothing on screen changed, not a missing capture.

The image a click line points at is the frame *at* the click, named for the
click and nothing else: `0002.png`. Its `-before.png` / `-after.png` siblings are
the -100ms / +100ms frames and `-full.png` is a full-resolution capture; they
stay on disk without cluttering the line.

Console output appears inline as `console.log:` / `console.warn:`; consecutive
identical lines collapse to one with a `(×3)` count. A `Failed to load resource`
browser log is dropped when the matching request failure is already on the line
above it. HMR payloads and clock error bounds are not in the document: the raw
websocket frames are named in the closing block when there are any, and the
clock measurements go to `session.json` under `--json`.

The session name comes from an LLM reading the narration
(`--title-backend remote`, which uploads the transcript). Without it the name is
derived from the most-clicked element, as before.

## Watching it build

The document is not only written at stop — it is assembled while you record, so
you can see the shape of what the LLM will get *before* pressing Ctrl-C:

- **Every line prints to stderr as it happens**, in the exact shape it will have
  in SESSION.md: clicks (once their three shots have settled, so the image name
  and the `screen did not repaint` evidence are already on the line), errors,
  console output, navigations, HMR — and narration.
- **`SESSION.live.md` in the session folder is rewritten every ~1s** with the
  full current document, properly interleaved and time-sorted. `tail -f` it in
  another pane, or point an agent at it mid-session. It is replaced by the real
  `SESSION.md` at stop.

Narration appears live under both STT backends: the microphone PCM is teed
into the same rolling-window transcription loop recgo's TUI uses
(`internal/transcribe`), a whisper pass every ~3s, each pass primed with the
locked text before it. With `--stt-backend local` the pass runs `whisper-cli`
on this machine and nothing is uploaded; with `--stt-backend remote` the pass
hits the configured endpoint, which means audio streams to the endpoint
*while* you record instead of only at stop -- same destination, announced on
stderr at start. Live narration lines are a *preview* -- stamped with the
session time of the audio window they came from, accurate to a few seconds.
The final document still re-transcribes the complete `audio.wav` in one shot
with word timings and VAD correction; nothing from the preview leaks into it.
`--live=false` turns all of it off. `recgo-browser` and `recgo-desktop` show
the same live decode -- in the follow TUI's narration panel and in the same
stderr stream + `SESSION.live.md` recgo-tab writes, respectively -- so the
terminal confirms that speech is actually being decoded while you record.

## Picking the tab

Without flags the first page target the browser lists is recorded. `--match`
takes the first tab whose URL or title contains the string, `--select` opens a
picker in the terminal, and `--target <id>` attaches to one exact CDP target
id (what `/json/list` reports) -- the handle a GUI picker should pass, since a
URL substring can hit a sibling tab. Whichever way, two lines announce the
choice on stderr:

```
attached to: Acme dashboard
tab: 1CB04B15172C3CCDF6C7E401DBEE46BB http://localhost:5173/
```

The id stays valid across navigations, so a shell keeps the title and URL
current by looking it up in `/json/list`; `Navigate:` lines in the document
carry the same URL changes.

`--launch` / `--headless` refuse a port that already answers: the throwaway
browser would lose the port to the one already there, and the session would
attach to -- and navigate -- one of *its* tabs. Pass `--port` for a launched
instance whenever a browser of your own sits on 9222.

## System audio

`--system-audio <source>` mixes what you hear into the narration track:
a `<sink>.monitor` source on PipeWire/PulseAudio (a loopback device such as
BlackHole on macOS), or `default` for the default output's monitor. ffmpeg
opens it as a second input and `amix`es both at unity gain into the same
16kHz mono `audio.wav`, so the clock and the calibration tone are unchanged --
the tone is in fact heard more reliably, since the monitor carries it even on
headphones. Bluetooth outputs on PipeWire yield nothing from their monitor;
those go through a temporary null sink that becomes the default output for
the session and is removed at stop, the recipe the `recgo` TUI already uses.

While recording, `sysaudio off` / `sysaudio on` on stdin mute and unmute the
monitor capture stream in place (`pactl set-source-output-mute` on the stream
named `recgo-system-audio`), the mix keeps running, and each toggle lands in
the document as `Note: system audio off` / `on`, so a reader knows why the
room went quiet. `stderr` echoes `system audio: off|on`. The closing block of
`SESSION.md` names the monitor when one was mixed in. All three recorders
(`recgo-tab`, `recgo-browser`, `recgo-desktop`) take the flag and the command.

## What it captures

- **Clicks with element identity** — `[data-testid="save"] — "Save"`, not
  `left click at (840, 291)`. An in-page listener reports through a CDP binding,
  so no OS input permissions are needed on either platform. On Wayland that
  matters: passive global input capture is not available at all (the
  `InputCapture` portal is exclusive-mode, and would take input *away* from the
  app under test).
- **Screenshots at −100ms / at / +100ms** around every click.
- **HMR traffic** — Vite, webpack-dev-server and Phoenix live-reload, classified
  by payload shape, with every raw frame archived. Phoenix channel frames are
  arrays (`[joinRef, ref, topic, event, payload]`); only the
  `phoenix:live_reload` topic counts as dev-server traffic — `assets_change`
  pushes, and error replies such as *"live reload backend not running"* (a
  Phoenix app without the live-reload backend would otherwise read as a
  suspicious `0 HMR`). LiveView diffs and heartbeats are the application itself
  and stay unclassified, though the raw frames are all in `logs/`.
- **Console, uncaught exceptions, and failed requests**, attributed to the click
  that caused them.
- **Narration**, transcribed and aligned to the clicks.

## Reuse from recgo

`internal/audio` verbatim: backend checks, `pactl --format=json` device
discovery, and the platform-split input args (`-ar` belongs on the *input* for
pulse and on the *output* for avfoundation; `-thread_queue_size 1024` prevents
dropped packets). `internal/audio/exports.go` exposes only what a sibling
package needs.

Capture therefore follows recgo's existing path exactly: same backend check, same
default-source resolution, same input flags, same `Setpgid`, same
SIGINT-then-`Kill`-after-5s stop.

One deliberate divergence: recgo encodes to a file, recgo-tab takes raw s16le on
`pipe:1`. That is what yields a timestamp for the first captured sample, which
anchors the transcript to the session clock — a file gives no such signal. It is
also the only part of the audio path not inherited from recgo.

Not yet adopted: the silence watchdog and the Bluetooth null-sink path. Both
matter for long sessions — see "Known gaps".

## The three findings worth knowing

### Screencast frames make −100ms exact, not best-effort

Chrome emits a screencast frame only when the page *repaints*. So the absence of
a frame between the last one and time T is positive evidence the screen did not
change — the last frame **is** the screen at T. `frameAt` waits for a frame with
`t > T` to prove nothing was missed and marks the shot `definitive`. A `+100ms`
shot on a page that then goes static can never be proven that way; it falls back
to a grace timeout and is flagged rather than quietly claiming certainty.

### Every STT backend gets word timings wrong, the same way

Both whisper.cpp and speaches return timings that tile the
audio contiguously: leading silence folded into the first word, inter-utterance
pauses folded into the word before. Measured against a fixture whose speech
starts at 2001ms:

| backend | first word | error |
| --- | --- | --- |
| whisper.cpp `-ml 1` | 0ms | **2001ms** |
| whisper.cpp `+ --vad` + silero | 2000ms | 1ms |
| speaches, as returned | 0ms | **2001ms** |
| speaches + local correction | 2001ms | **0ms** |

Speaches ignores faster-whisper's `vad_filter` — 200 with byte-identical output,
with or without a litellm proxy in front. So `vadalign.go` does the
equivalent locally: find speech regions with `ffmpeg silencedetect`, then stretch
each transcribed utterance onto the region it belongs to. Utterance onsets become
exact; word positions inside an utterance stay interpolated. On a count mismatch
it declines to guess and leaves timings untouched.

Uncorrected, this silently attributes narration to the wrong click — the exact
failure this tool exists to prevent.

### The audio anchor is measured, not assumed

Session time would otherwise be pinned to ffmpeg's first emitted PCM byte, which
trails the true first sample by input buffering. `recgo-tab` instead has the page
emit a 1kHz tone at a known browser timestamp and locates it in the capture with
a Goertzel filter, making ffmpeg's capture latency a measurement.

This needs the tone to actually reach the capture device: speakers into a
microphone, `--mic` pointed at a `.monitor` source, or `--system-audio` mixing
one in. On headphones without it the tone is
never heard, and the anchor falls back to the first-byte estimate — reported
plainly in `SESSION.md`'s "How this was captured" block (and in `session.json`
under `--json`) rather than papered over.

The onset search is confined to the first 15s of the capture. The tone plays
within the first seconds, so a 1kHz match beyond that window is not the tone —
it is speech. Before this guard, a loud 1kHz-heavy moment two minutes into a
headphone session was "found" as the tone, shifting the anchor by minutes and
pinning every narration line to a negative timestamp (rendered `00.00.00`).

Two more guards close the other observed failure mode. The page can report a
stale tone timestamp: `AudioContext.getOutputTimestamp()` returns zeros on a
context that has not rendered audio yet, which stamps the tone "at page load"
-- minutes early when recording a long-open tab. The injected script now
ignores the output timestamp until it has actually advanced, and on the Go
side `toneAnchor` rejects any correction implying more than 15s of capture
latency (or a negative one beyond clock error), keeping the first-byte
estimate and saying so in the anchor note.

## Serving the session to an agent elsewhere: --portal

```sh
recgo-tab --launch http://localhost:5173 \
  --portal wss://portal.example --portal-room "$(openssl rand -hex 8)"
```

The recorder itself dials out to a [portal](../portal/) server's console-bridge
lane and serves three read-only tools over the output root — `list_dir`,
`read_file`, `read_bytes` (base64 with a 4MB cap, so screenshots survive) —
for exactly as long as the recording runs. An agent anywhere portal is
reachable (portal's own chat, or Claude Code through `portal-mcp`) reads
`SESSION.live.md` and the screenshots live, with no inbound port on this
machine, no rsync, and no copies.

This is deliberate egress and is treated exactly like the remote STT backend:
off by default, the destination and what is exposed announced on stderr
before the connection opens, and pinned by `noupstream_test.go`. The exposure
is bounded three ways: the tool set is read-only by construction (the hello
declares `write:false` and the server filters advertisements against the same
rule), every path is confined to the output root with symlinks resolved, and
`read_bytes` refuses oversize files rather than truncating them. The portal
room name is the only credential — `--portal-room` is mandatory and the help
text says to pick an unguessable one. Reconnection is automatic with backoff;
the bridge dies with the recording.

## recgo-tab does not upstream recordings

A session folder holds screenshots of whatever was on screen, raw dev-server
websocket traffic, and unredacted narration — categorically more sensitive than a
meeting recording. So:

- **Transcription is local-first.** `--stt-backend auto` (the default) runs
  whisper.cpp on this machine whenever a ggml model is discovered; the wrapper
  puts `whisper-cli` on PATH so it works out of the box. Only when no local
  model exists does it fall back to the configured remote endpoint — announcing
  the destination on stderr before recording starts. `--stt-backend local`
  pins it, guaranteeing nothing is uploaded.
- **The LLM session name follows it.** `--title-backend` defaults to whatever
  `--stt-backend` is, so naming the session never uploads a transcript that
  transcription itself kept local. `--title-backend remote` opts in explicitly.
- **Nothing is uploaded.** recgo's `internal/upload` ships finished recordings to
  a remote endpoint; recgo-tab never imports it, and a test enforces that.
- **The output path stays out of backup sweeps.** Sessions land in
  `~/walk-and-talk/` (macOS: `~/Documents/walk-and-talk/`), deliberately
  outside `~/archive` and any nightly mirror job that watches it.

`noupstream_test.go` makes these properties of the build rather than promises in
a comment — it fails if the uploader is imported, if a `net/http` client appears
outside the opt-in remote backends (`stt.go`, `title.go`, `cdp.go`), if the
live-narration streaming (`internal/transcribe`) is wired anywhere but a
CLI's `main.go` (recgo-tab, recgo-browser, recgo-desktop), if the default
backend stops being `auto`, if
`--title-backend` stops following `--stt-backend`, if the help text stops
warning that `--live` streams narration under a remote backend or that
`--portal` exposes the output root, if the websocket client is imported
anywhere but `cdp.go` and `portal.go`, or if the
output path moves into the archive tree. Every one of those runs against
`recgo-tab`, `recgo-browser`, `recgo-desktop` **and** `recgo-window`: they are
the same tool at four settings, so a promise that holds for one and not the
others is the worst possible outcome. The failure mode being guarded
against is silent: nobody notices data leaving.

> **recgo-desktop is the same recorder pointed at the screen** (and
> `recgo-window` the same binary, `internal/desktop`, pinned to one screen or
> window picked on every start -- see the README). Same
> `--stt-backend auto` local-first default, same live document, same
> `--portal` / `--sync-target`, same output root, and a dedicated test pins
> its extra promise: video and screenshots never leave the machine. What
> differs is the capture surface. Clicks come from a listen-only OS event tap
> (position only -- outside a browser nothing names the control under the
> cursor), which on macOS needs the Accessibility (or Input Monitoring) grant
> and on Linux is not available yet (`--click-shots=false` to turn it off).
> Focus changes -- another window or app coming to the front, or the front
> window's title changing, which is what a browser tab switch looks like from
> outside -- and newly appearing windows and dialogs are screenshotted too,
> via the window list the Screen Recording grant already exposes
> (`--focus-shots=false` to turn it off; Linux not yet). Marks are typed as
> `m<enter>`; and there is no calibration tone, since there is no page to
> emit it through. Each session also records the machine
> it was captured on -- host, user, OS, hardware model, display layout -- in
> `SESSION.md` and `session.json`, so a screenshot's pixel scale is never a
> guess.

### Local models

Discovered automatically, largest first
(`ggml-large-v3-turbo-q8_0` → … → `ggml-tiny.en`), from:

```
$XDG_DATA_HOME/recgo/models   ~/.local/share/recgo/models
~/.local/share/whisper        ~/models
```

A `ggml-silero*.bin` alongside them is picked up as the VAD model — get one, or
timings are seconds off (see the table above). Both are overridable with
`--whisper-model` / `--whisper-vad-model`.

### Sending audio off the machine, deliberately

`--stt-backend remote` uploads `audio.wav` to an OpenAI-compatible endpoint. It
announces the destination on stderr before doing so. The binary ships with no
endpoints baked in — a test enforces that — so the destination comes from
`--stt-url`, or from the config file:

```toml
# ~/.config/recgo/config.toml
[transcription.remote]
endpoint = "https://llm.example.com/v1"
model = "whisper"
```

The same endpoint serves `--title-backend remote` (its chat model is
discovered from `/v1/models`, or pinned with `--title-model`). The bearer is
`--stt-api-key`, else `$OPENAI_API_KEY` / `$LLM_API_KEY`, else `api_key` in the
same config section. Note that a host firewall which allowlists the recgo
binaries by path also permits this egress — narrow such a rule if you want
remote transcription blocked.

## Tests

```sh
go test ./internal/tab/...                              # hermetic
CHROMIUM=$(which chromium) go test ./internal/tab/...   # + real headless e2e
FFMPEG=... SPEECH_WAV=... go test ./internal/tab/...    # + silencedetect tests
go test ./internal/tab/... -race                        # clean, e2e included
```

The e2e stands up a fixture dev server with a Vite-shaped HMR websocket, drives a
scripted session through real Chromium, and asserts against the packed folder.
`SPEECH_WAV` wants an 8.2s fixture laid out as
`2s silence | speech | 1.5s silence | speech`; build it with `espeak-ng` and
`ffmpeg -f lavfi anullsrc`, and establish ground truth with
`ffmpeg -af silencedetect`.

The websocket transport is `github.com/gorilla/websocket` — the only dependency
recgo-tab adds. It is worth it: CDP screencast frames routinely exceed 64KiB and
Chrome fragments messages across continuation frames, so the codec has to get
extended lengths, masking and interleaved control frames right on every single
message. `cdp_test.go` tests the client against a real websocket server rather
than a codec fake, covering out-of-order reply correlation under concurrency,
900KB messages in both directions, dropped connections, and use after close.

Note gorilla permits one concurrent reader and one concurrent writer; `CDP`
serialises writes behind a mutex because `SendAsync` (screencastFrameAck) fires
from the read loop while callers may be sending commands.

## recgo-browser — the default, following you across tabs

`recgo-tab` pins one page for the whole recording. Right for reproducing a bug on
one screen; wrong the moment the work spans a dashboard, a docs page and the app,
because the interesting clicks land in tabs nobody is watching. Reviewing a
product is the second case, so **`recgo-browser` is the one to reach for**:

```sh
recgo-browser                                  # follow the whole browser
recgo-browser --launch http://localhost:5173   # ...or start one and follow it
recgo-browser --match localhost:5173           # pin to one tab (recgo-tab's shape)
recgo-browser --select                         # ...or pick that tab from a list
```

Without `--launch` the browser must already be running with
`--remote-debugging-port=<port>`.

`recgo-browser` attaches a recorder to every open tab, merges everything onto one
clock, and marks in the timeline which tab was in front. Tabs opened mid-session
are picked up within a second; tabs that close are detached and noted. New event
kinds: `tab-attach`, `tab-close`, `tab-switch`, and every event carries a
`targetId`.

**It carries recgo-tab's whole flag set**, and a NixOS subtest fails the build if
that stops being true: same `--stt-backend auto` local-first transcription, same
`--title-backend` following it, same `--portal` live bridge, same `--sync-target`
push, same `SESSION.live.md`, same calibration-tone anchor (emitted through the
tab in front), same `--keep-raw` / `--json` / `--duration` / `--headless`.
`recgo-tab` remains as the single-tab recorder; `recgo-browser --match` is the
same thing, so there is one binary to remember.

`--match` and `--select` do more than filter: they turn the tab watcher off, so a
popup opened by a click is deliberately *not* followed. That is what makes a
pinned recording a faithful stand-in for `recgo-tab`. With `--launch` there is
only ever the one launched tab, so either flag pins to it whatever its value.

**How it knows where you are.** CDP's `/json/list` says nothing about focus, so
the question is answered from inside the page: only the foreground tab of a
focused window reports `document.visibilityState === "visible"`. `inject.js`
reports that on `visibilitychange`, so following costs no polling — the browser
says so the instant you switch.

**Screenshots only come from the tab you are looking at.** Chrome does not paint
background tabs, so a click in a hidden tab produces no screencast frame and
therefore no image. Clicks, console, network and HMR are still captured from
every tab. The missing shots are recorded as `captured: false` with a note rather
than silently absent — verified in `follow_test.go`, which sees 10 images across
12 shot slots with two tabs open.

The TUI (`internal/tabui`) shows the attached tabs with a filled dot on the one
in front, live click/HMR/error counts, and `m` to mark a moment --- the mark is
attributed to the tab that was in front when the key was pressed. A narration
panel shows the last few lines as they are decoded. `--plain` skips the TUI for
scripted runs, streams the session document to stderr the way `recgo-tab` does,
and takes `mark` on stdin instead of the keypress.

`SESSION.live.md` is written under both front ends. Only `--plain` also streams
it to stderr, because under the TUI bubbletea owns the terminal --- so pointing
an agent (or a `tail`) at the live document works no matter which one is up.

## recgo-sessions — browse what was recorded

A session folder is written for an LLM; `recgo-sessions` renders it for a
person. It scans the output root, parses every `SESSION.md`, and writes a
single self-contained `index.html` next to the session folders — no server, no
uploads, all references relative:

```sh
recgo-sessions                 # ~/walk-and-talk → index.html
recgo-sessions --dir /path     # any sessions root
```

The viewer shows, per session: the screenshot at each click with a marker on
the clicked element (viewport coordinates scaled against the `-full.png`
capture, device-pixel-ratio detected per session so 1x Linux displays and
Retina Macs both land exactly), the −100ms / +100ms siblings behind a
segmented control, a filmstrip, and a timeline interleaving narration with
clicks — element text, selector, and the `no repaint` evidence as badges.
Playing `audio.wav` steps the frames in sync with the recording clock. Light
and dark themes follow the system and toggle persistently; the URL hash deep
links a session, frame, and theme (`#s=1&f=9&theme=dark`).

The page styles follow nur studio's design tokens, and `index.html` is written
`0600` like everything else in the folder.

## Screenshots are lossless PNG

Measured on a real app UI, captured through CDP at several settings:

| encoding | size | PSNR (luma) vs lossless |
| --- | --- | --- |
| JPEG q50 | 13.4 KB | — |
| JPEG q70 | 15.5 KB | 46.8 dB |
| JPEG q85 | 18.2 KB | — |
| JPEG q95 | 22.5 KB | 60.6 dB |
| **PNG** | **14.9 KB** | lossless |

PNG is both smaller than JPEG q70 *and* lossless, because an app screenshot is
flat colour and text — what PNG compresses well and JPEG handles badly. 46.8 dB
means visible ringing around exactly the glyphs the reading agent has to make
out. The screencast therefore runs `format: png`, capped at 2560x1600 so only
enormous displays are downscaled; below that the capture is 1:1.

This does cost CPU per frame versus JPEG, and on a page dominated by photos or
video PNG would be considerably larger. Both are the wrong shape for this tool's
use case, which is app UIs.

## Permissions

Every file in a session folder is `0600` and every directory `0700`. The folder
holds unredacted narration, screenshots of whatever was on screen, and raw
dev-server traffic that can carry tokens; the default umask's `0644` was a real
leak, and a silent one. `TestEverythingWrittenIsOwnerOnly` walks a packed
session and fails on anything group- or world-accessible.

recgo-tab needs **no special group membership**. Clicks come from CDP inside the
page rather than from evdev, so there is no `input` group requirement — the
NixOS test asserts the recording user is in no extra groups, so that property
cannot regress silently.

## Testing against a real desktop

Two harnesses, because the parts that need a compositor cannot run in the
package's `checkPhase`:

```sh
# throwaway session on this machine: private bus, private XDG_RUNTIME_DIR,
# headless compositor, PipeWire with a virtual capture device
./test/mock-session.sh sway -- go test ./internal/...
./test/mock-session.sh kde  -- go test ./internal/...

# full VM
nix-build nixos-test.nix -A sway
nix-build nixos-test.nix -A kde
```

sway runs end to end because `xdg-desktop-portal-wlr` can be told to pick an
output without asking (`chooser_type=none`).

KDE's portal asks a human before sharing a screen, and that is the security
model rather than a gap in the test. KDE does have a pre-authorization
mechanism — a bespoke `kde-authorized` table in the portal permission store,
written with `flatpak permission-set kde-authorized <type> <app_id> yes` — but
the [documented permission type is `remote-desktop`, not ScreenCast][kde-perms].
ScreenCast persistence works through restore tokens instead, and the *first*
grant still needs a person. So the KDE variant asserts everything up to that
gate: PipeWire, a capture device, the compositor, the backend being selected,
`ScreenCast` being exported, and microphone capture.

One thing worth knowing if you extend these: `xdg-desktop-portal` chooses its
backend from `XDG_CURRENT_DESKTOP` in the **systemd user environment**, not from
whatever the compositor was launched with. Setting it only on the kwin process
leaves ScreenCast unexported and the test times out with no useful error.

[kde-perms]: https://develop.kde.org/docs/administration/portal-permissions/

## Capture backends, per platform

Three, because no single API covers all of them, and they differ in the one
property that matters — whether you can look *backwards* in time.

| platform | backend | stream? | consent |
| --- | --- | --- | --- |
| any Wayland | `xdg-desktop-portal` ScreenCast | **yes** | dialog once, then a restore token |
| KDE | `org.kde.KWin.ScreenShot2` | no, one frame per call | none, but see below |
| macOS | `screencapture(1)` | no, one frame per call | TCC prompt once per binary |

Only the portal gives a continuous timestamped stream, which is what the
`-100ms` shot needs: a snapshot API cannot go back in time. So the portal is the
ring buffer, and the per-platform snapshot backends serve the click instant and
the full-resolution shot, where the extra fidelity and the absent dialog are
worth more.

### KWin needs a desktop file, not a permission

`org.kde.KWin.ScreenShot2` has no consent dialog, which makes it far nicer than
the portal on KDE — but it is not unauthenticated. KWin maps the caller's
`/proc/PID/exe` back to a `.desktop` file and requires that file to declare:

```ini
Exec=/absolute/path/to/binary %f
X-KDE-DBUS-Restricted-Interfaces=org.kde.KWin.ScreenShot2
```

Without it every call fails with *"The process is not authorized to take a
screenshot"*. The `Exec=` path must be absolute, so `default.nix` installs the
desktop files with store-path `Exec=` lines for both binaries.

### macOS

`screencapture(1)` is built in, needs no cgo, and writes PNG. ScreenCaptureKit
would provide a real stream — and is what a macOS ring buffer would need — but
that means cgo against objc, a much larger commitment than the snapshot path
earns. A denied Screen Recording grant does not error: it silently yields
wallpaper with no windows, so `CheckPermission` inspects the result rather than
trusting the exit code.

## Known gaps

- **recgo-tab follows one tab.** New tabs, popups and iframes are not
  followed; whole-browser capture is what `recgo-browser` is for. The same
  applies to `recgo-browser --match` / `--select`, which is that mode by
  another name. Iframes are followed by neither.
- **Pre-attach traffic is lost** when using `--match` against an already-open
  tab. Only `--launch` guarantees a complete record — it lands on `about:blank`,
  instruments, then navigates, because launching straight onto the target URL
  loses the dev server's HMR handshake.
- **No watchdog yet.** recgo restarts a flatlined capture and concatenates
  segments; that would break a single audio anchor, so adopting it needs a
  per-segment anchor and a restart event on the timeline.
- **No system-audio capture yet**, which is what would make the calibration tone
  reliable regardless of headphones.
- **No video** — frames only.
- **Audio capture is recgo's, but the PCM pipe is not.** Device handling and
  the ffmpeg invocation are inherited from recgo; reading raw samples off
  `pipe:1`, and the calibration-tone anchor built on it, are specific to
  recgo-tab.
- **Keystrokes are not captured.** Clicks only, by design so far.
