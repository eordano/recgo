# recgo-android

Record what you do in an Android app, on a USB/Wi-Fi ADB phone or emulator,
with a readable interaction timeline and machine-readable element metadata.
Everything stays local. This command does **not** read Recgo's upload or remote
transcription settings.

## Build and setup

Requires Go, ADB, and an Android 12+ device. Building the helper requires a JDK
and an Android SDK with platform `android-35` and build-tools `35.0.0`.
`ANDROID_PLATFORM` / `ANDROID_BUILD_TOOLS` can select other installed versions.

```sh
go build -o recgo-android ./cmd/recgo-android
export ANDROID_SDK_ROOT=/path/to/android-sdk
bash android-helper/build.sh
./recgo-android --devices
./recgo-android --serial emulator-5580 \
  --helper-apk android-helper/build/recgo-android-helper.apk --setup
```

On the device, open **Accessibility settings → Recgo Android** and approve the
service. Android grants broad screen-reading access; enable it only on a device
you control, and disable it in Settings when you no longer need it. The tool
never enables the service for you. You may need to approve Android's additional
restrictions on a sideloaded accessibility app.

The helper has **no Internet permission**. It accepts a local socket only from
Android's shell/root ADB peer and collects nothing until the desktop sends an
explicit package allowlist. It becomes idle when the desktop disconnects (or
its keepalive expires). Any process with access to your authorized host ADB can
start capture while the service is enabled; this is not a per-session device
consent boundary. Do not expose the host ADB server to untrusted users/networks.

The OS event subscription stays enabled to avoid losing the visible app's first
clicks. While idle, received event headers are discarded without looking up
their source nodes, retaining payloads, or writing files.

The build script generates a separate, persistent **local development key** at
`$XDG_DATA_HOME/recgo/android-helper/debug.p12` (default
`~/.local/share/recgo/android-helper/debug.p12`). It uses the conventional public
development password, is protected by filesystem permissions, and is **not a
production release key**. Preserve it to update the installed helper. It is
not the Woot signing key. Build products and keys are not committed.

## Record

Metadata only, no visual or audio capture:

```sh
./recgo-android --serial emulator-5580 --package com.woot.dataharvester \
  --out ~/walk-and-talk
```

Stop with Ctrl-C or set `--duration 30s`. The serial is mandatory even when only
one device is attached; the recorder never guesses phone versus emulator.

Full narrated session with an interactive scrcpy window:

```sh
./recgo-android --serial emulator-5580 --package com.woot.dataharvester \
  --mirror --screenshots --logs --audio --transcribe --out ~/walk-and-talk
```

- `--mirror`: scrcpy display/control and video recording. `--video` records
  without opening a window or forwarding input. Requires scrcpy on PATH or
  `--scrcpy /path/to/scrcpy`.
- `--screenshots`: a whole-display PNG after each click/long press.
- `--logs`: raw logcat for the primary app's UID, surviving app process restarts.
  Shared-UID packages also appear. Logs use device wall timestamps, not the
  accessibility timeline clock, and can contain secrets.
- `--audio`: desktop microphone, not phone microphone; use `--mic` to choose one.
- `--transcribe`: local whisper.cpp after capture, never remote fallback. Use
  `--whisper-model` or Recgo's locally discovered models. A transcription failure
  leaves the recording and an explanatory report intact.
- `--extra-packages com.android.settings,com.android.permissioncontroller`:
  explicitly include other packages' accessibility events. Package names vary
  between Android builds; use the actual names on your device. This does not
  expand the primary app's logcat UID filter.

**Screenshots/video are not package-cropped or redacted.** They can show other
apps, keyboards, status bars, notifications and private content even when those
packages are not in the metadata allowlist. Logs are also unredacted. Keep
these options off when testing with real credentials.

## Output and accuracy

A new private directory (mode 0700) contains:

- `SESSION.md`, `SESSION.live.md`: final/live Recgo-readable timeline.
- `session.json`: events with package, resource ID, class, label, description,
  screen bounds, checked/enabled/clickable state and target availability.
- `events.jsonl`: append-only event journal, retained even if capture fails.
- Optional `0001.png` etc., `video.mkv`, `scrcpy.log`, `logcat.txt`, `audio.wav`,
  and local transcription products.

Android accessibility reports **semantic actions**, not every physical touch.
A click may come from touch, keyboard or automation. Bounds are the node's
rectangle, **not** a measured finger coordinate. Node state is read when the
event is handled and may already reflect the action. No node means `unknown`;
there is no coordinate-based guessing or fallback to potentially private event
text. Password/editable values (including sensitive ancestors) are omitted.
Custom views must expose accessibility semantics for useful labels. Apps that
put secrets in ordinary labels can still expose them.

Compose apps can expose stable test tags as resource IDs with
`testTagsAsResourceId`; without IDs, Recgo retains available semantic labels and
bounds. Canvas/WebView internals, nested child-label resolution, raw gestures,
and complete view trees are **not** implemented in this first version.

Events use Android elapsed time mapped to the desktop's monotonic clock via a
handshake, with its measured uncertainty and host receipt timestamps preserved.
Avoid suspending either device. This is approximate synchronization, not exact
video/audio frame alignment. Screenshots start **after receiving** a click and
record start/end timestamps; rapid clicks can queue behind earlier captures.
They may depict a later screen. There is no pre-click screenshot in this version.
Scrcpy uses variable frame timing; a static ending may not extend the video to
the full session duration.
Secure windows may be black or unavailable. Queue overflow and disconnects are
reported; recording stops at 100,000 retained events to bound memory.

The CLI does not start an emulator, launch the target app, upload sessions, or
add an Android mode to the desktop application's recording picker. Start your
AVD normally and choose its serial. Existing Recgo viewers can read the report.

## Verification

```sh
go test ./...
go test -race ./internal/android
python3 android-helper/test-emulator.py --serial emulator-5580
```

The integration test requires the helper already installed and manually enabled.
It refuses physical phone serials. It exercises the helper demo button, toggle
and a synthetic password, verifies package filtering and capture files, and
checks ADB-forward cleanup. It leaves only local test artifacts under `/tmp`.
Video requires scrcpy. Disable the helper's accessibility service after testing
if you do not intend to keep using it.
