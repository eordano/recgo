# recgo-tab — record a browser tab with narration

A second binary in this module. `recgo` records audio; `recgo-tab` records a
browser tab *and* audio, correlates them on one clock, and writes a folder an
LLM can act on.

```sh
recgo-tab --launch http://localhost:5173 --duration 30s
```

Output lands in `$XDG_DOCUMENTS_DIR/walk-and-talk/YYYY-MM-DD-HH-MM-<slug>/`:

```
SESSION.md     the deliverable, and the only document — one line per thing that happened
0001.png       the screen when recording started
NNNN.png       the screen at click N, with -before / -after / -full siblings
audio.wav      narration, 16kHz mono
logs/          raw dev-server websocket frames, including all HMR payloads
session.json   only with --json: full machine-readable timeline and clock report
```

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

## What it captures

- **Clicks with element identity** — `[data-testid="save"] — "Save"`, not
  `left click at (840, 291)`. An in-page listener reports through a CDP binding,
  so no OS input permissions are needed on either platform. On Wayland that
  matters: passive global input capture is not available at all (the
  `InputCapture` portal is exclusive-mode, and would take input *away* from the
  app under test).
- **Screenshots at −100ms / at / +100ms** around every click.
- **HMR traffic** — Vite and webpack-dev-server, classified by payload shape, with
  every raw frame archived.
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
microphone, or `--mic` pointed at a `.monitor` source. On headphones the tone is
never heard, and the anchor falls back to the first-byte estimate — reported
plainly on `SESSION.md`'s closing line (and in `session.json` under `--json`)
rather than papered over.

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
  `~/Documents/walk-and-talk/`, deliberately outside `~/archive` and any
  nightly mirror job that watches it.

`noupstream_test.go` makes these properties of the build rather than promises in
a comment — it fails if the uploader is imported, if a `net/http` client appears
outside the opt-in remote backends (`stt.go`, `title.go`, `cdp.go`), if the
default backend stops being `auto`, if `--title-backend` stops following
`--stt-backend`, or if the output path moves into the archive tree. The failure mode being guarded
against is silent: nobody notices data leaving.

> **recgo-desktop differs.** Everything above is scoped to `recgo-tab`, and so is
> `noupstream_test.go` — every path it checks is `cmd/recgo-tab`. `recgo-desktop`
> defaults to `--stt-backend remote`, so its audio track is uploaded to the
> configured endpoint unless you pass `--stt-backend local` (or `none`). Video
> and screenshots still never leave the machine. Pointing the config at your
> own litellm/speaches rather than a third party is what makes that default
> defensible; `local` remains available and works offline.

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

## recgo-browser — following you across tabs

`recgo-tab` pins one page for the whole recording. Right for reproducing a bug on
one screen; wrong the moment the work spans a dashboard, a docs page and the app,
because the interesting clicks land in tabs nobody is watching.

```sh
recgo-browser --port 9222          # attach to every tab, follow along
recgo-tab --select                 # or: pick one tab from a list
```

Both need a browser started with `--remote-debugging-port=<port>`.

`recgo-browser` attaches a recorder to every open tab, merges everything onto one
clock, and marks in the timeline which tab was in front. Tabs opened mid-session
are picked up within a second; tabs that close are detached and noted. New event
kinds: `tab-attach`, `tab-close`, `tab-switch`, and every event carries a
`targetId`.

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
in front, live click/HMR/error counts, and `m` to mark a moment. `--plain` skips
it for scripted runs.

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
  followed; whole-browser capture is what `recgo-browser` is for.
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
