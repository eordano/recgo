# recgo-alttester — desktop capture that names the Unity element under each click

`recgo-alttester` is [`recgo-desktop`](README.md) for an instrumented Unity
build: the same screen capture, narration and click screenshots, and every
click line also names the UI control it landed on, asked from the game
itself over the AltTester SDK 2.3 wire protocol. No AltTester Desktop (the
licensed relay) is involved: recgo is the server the game dials.

```sh
recgo-alttester --duration 5m
# in another terminal, the unity-explorer dev build:
<explorer binary> --alttester 127.0.0.1:13000
```

A click on the backpack then reads, in `SESSION.md`:

```
00.00.03  Click: 812,340 on /Root/Panel/Button text: Backpack → 0002.png
00.00.06  Click: 400,300 on uitk:/root/backpack/card-3 → 0003.png
00.00.09  Click: 20,20 → 0004.png — outside ~ — Konsole
```

Everything else about the document is the desktop format (marks, focus and
window lines, `-before`/`-after`/`-full` siblings, narration anchoring); see
[RECGO-TAB.md](RECGO-TAB.md) for the stream and the closing block.

## How it works

Both AltTester ends are WebSocket *clients*. The instrumented app dials
`ws://host:port/altws/app?appName=..&platform=..&platformVersion=..&deviceInstanceId=..&appId=..`;
a driver dials `/altws?appName=..&driverType=..`. AltTester Desktop sits in
the middle and pairs them. `recgo-alttester` listens on `--alt-host:--alt-port`
(default `127.0.0.1:13000`) and plays that middle:

- **The app** connects on `/altws/app`. One app at a time; a second
  connection replaces the first (so a relaunched client just takes over).
  `--alt-app NAME` accepts only that `appName`.
- **recgo's own driver** rides the app socket directly. Every command it
  sends carries its own `driverId` and a unique `messageId`; responses are
  matched back by `messageId`, so nothing it sends is visible to, or mixed
  with, an external driver.
- **External drivers** (a raw-protocol driver, or the SDK's `AltDriver`)
  connect on `/altws` and are paired exactly like `altrelay.py` does: the
  app receives `{"commandName":"DriverConnectedNotification","isNotification":true,"driverId":"<id>"}`,
  the driver receives the `driverRegistered` notification, frames are
  forwarded verbatim both ways, the app hears `DriverDisconnectedNotification`
  when the driver leaves, and when the app drops the driver is closed with
  code **4002 "App disconnected"**. So `altdrive.py tree` keeps working while
  recgo records — point it at recgo's port (`DCL_ALT_PORT`).

Clicks come from the system tap (evdev + a KWin script on Linux, the event
tap on macOS). For each one, off the tap goroutine:

1. `screencast.WindowAt(x, y)` asks the compositor for the window under the
   click and its **client** area (KWin: `stackingOrder`, topmost hit,
   `clientGeometry`; the per-click cursor script already reports it, so the
   usual case costs no extra round trip). Other compositors (and macOS)
   answer "unknown", and the app is then assumed to fill the main display
   (the usual fullscreen case), whose size in **points** is the rect: clicks
   arrive in points, and on a HiDPI display the app's own pixel size would
   be off by the backing scale.
2. The click is converted to Unity screen space (pixels, origin bottom-left,
   the space of `Input.mousePosition` and `findObjectAtCoordinates`):
   `ux = (x - Left) * sx`, `uy = (Height - (y - Top)) * sy`, with
   `sx = appW / Width`, `sy = appH / Height` from `getApplicationScreenSize`,
   queried once and again whenever the window's client size changes.
3. The element is resolved (next section) and merged into the click event
   before `SESSION.md` is written.

The app window is recognised by its `appName` or `--alt-window` (default
`decentraland`; the explorer dials with the SDK's default `__default__`,
which names nothing) in the title or class, by the window class
`UnityWndClass` that every Unity player on Windows registers (the Windows
player's window was titled "Explorer", which no substring matched), or by
a client size equal to the app's own `Screen.width x Screen.height`. The
first window so
corroborated is locked as the app; a window named by title or class takes
the lock over from an earlier one (the app relaunched under a new window).
No other window is ever probed: a click there is recorded position-only
with `— outside <title>`, since its coordinates mapped into Unity space
would name whatever HUD element sits under the mapped point. A click inside
the app window where nothing is hit is position-only too. Until the app
connects, and after it disconnects, clicks are plain desktop clicks. What
the AltTester side did is on record as `Note:` lines (and stderr messages):

- `AltTester app connected: <appName> (<platform>), server <version>` and
  `AltTester app disconnected: <appName> (<platform>)`;
- `AltTester app never connected (listened on <host:port>)` when the
  recording ends without an app (wrong port, an un-instrumented build): the
  document would otherwise read like one with no UI under any click;
- `AltTester probe missing in this build (<reason>); clicks resolved by
  findObjectAtCoordinates: selectors are UGUI transform paths from the
  object tree (bare object names when the tree does not know the hit), UI
  Toolkit elements are not seen`, once, the first time the build answers
  that it has no probe (next section).

## The probe

`findObjectAtCoordinates` in the SDK is a UGUI `EventSystem` raycast: it
does not see UI Toolkit. So the dev build carries a small static probe that
recgo calls first. It lives in the explorer at
`Explorer/Assets/DCL/Input/AltTesterUiProbe.cs`, compiled only under
`#if ALTTESTER` (the `DCL.Input` assembly; its asmdef references TextMeshPro
for it). Contract, implemented on both sides exactly:

```
static string DCL.Input.AltTesterUiProbe.ElementAtScreenPoint(float x, float y)   in assembly "DCL.Input"
static string DCL.Input.AltTesterUiProbe.Snapshot()                                  in assembly "DCL.Input"
x,y are Unity screen coordinates (pixels, origin bottom-left, the same space as Input.mousePosition and AltTester's findObjectAtCoordinates); the probe flips y (Screen.height - y) before RuntimePanelUtils.ScreenToPanel for UI Toolkit, as the client's InteractionCache does.
ElementAtScreenPoint returns a JSON object or the literal "null":
{"kind":"ugui"|"uitk", "path":"/Root/Panel/Button", "name":"Button", "type":"Button", "text":"Backpack", "id":12345, "classes":"card muted", "testId":""}
kind ugui: the topmost EventSystem.RaycastAll hit (results[0]); path = transform names from the scene root joined by "/"; type = the concrete Selectable type on the hit object, else its concrete Graphic type, else the first component that is not a Transform or CanvasRenderer; text = first TMP_Text/Text in the object or its children, clipped to 80 chars; id = AltTester's own EntityId-derived int, the same AltObject.id findObjectAtCoordinates returns for that object; classes empty.
kind uitk: the topmost VisualElement from panel.PickAll (picked[0]) over every active UIDocument, one pick per shared panel, highest UIDocument.sortingOrder between panels; path = ancestor names ("#Type" when unnamed) from the panel root joined by "/"; type = the element's C# type name; text = TextElement.text, else the first TextElement descendant's text; id = the UIDocument GameObject id (the same for every element in that document); classes = GetClasses() joined by spaces.
When both a UGUI and a UITK hit exist, uitk wins unless the hit's root Canvas.sortingOrder is strictly greater than the UIDocument.sortingOrder (equal orders -> uitk).
testId is always "" (the client has no test-id convention). UnityEventSystem's 15px screen-edge safe margin is not applied: points in the margin still hit.
Snapshot() returns the same shape of JSON with, among others, "screen" = the Unity screen size, to compare with the captured framebuffer when scaling is suspected.
Any exception inside the probe returns "null" (the string, not JSON null); it never throws to the caller.
```

The frame recgo sends is byte-for-byte what the SDK's own
`AltCallStaticMethod` sends (`callComponentMethodForObject` with
`altObject` null):

```json
{"commandName":"callComponentMethodForObject","messageId":"…","driverId":"…","isNotification":false,
 "altObject":null,"component":"DCL.Input.AltTesterUiProbe","method":"ElementAtScreenPoint",
 "assembly":"DCL.Input","parameters":["812.5","340"],"typeOfParameters":["System.Single","System.Single"]}
```

Each `parameters` entry is the JSON encoding of that argument (the C#
driver does `JsonConvert.SerializeObject(p)` per argument): the float 812.5
travels as the text `812.5`, a string would travel with its quotes. The app
deserializes each entry to the declared parameter type with Newtonsoft
(InvariantCulture), so `812.5`, `"812.5"` and `340` all parse to `float`.
`typeOfParameters` must be exactly `["System.Single","System.Single"]`:
method resolution matches `Type.GetType` of each entry against the declared
`float` parameters, and `System.Int32` fails with
`MethodWithGivenParametersNotFound`. `Snapshot()` uses the same envelope with
`"method":"Snapshot"`, `"parameters":[]` and `"typeOfParameters":[]`. The
return value arrives JSON-encoded as a string inside the response `data`
(recgo decodes the string, then parses the object; the literal `null` string
is a miss).

`--probe-type` and `--probe-assembly` override the class and assembly.

The answer maps onto the document's element fields: selector = `path`
(prefixed `uitk:` for UI Toolkit hits, so the line shows which UI system was
hit), tag = `type`, id, classes, testId and text as given. The ugui hit is
the topmost raycast target, often a Button's child Image or TMP text rather
than the Button itself; the SDK's `findObjectAtCoordinates` walks up to the
`IPointerClickHandler`, the probe does not, so walk `path` upwards when the
handler matters.

## Fallback when the build has no probe

If the app answers `componentNotFound`, `assemblyNotFound`, `methodNotFound`
or "Assembly not found", the build predates the probe. recgo remembers that
for the rest of the session and resolves clicks through the SDK alone:
`findObjectAtCoordinates` (UGUI raycast), then `getAllComponents` on the hit
to pick the most specific UI component as the type (`Button` over `Image`
over `RectTransform`), then `getText` when a text or input-field component
is present. The selector is the same `/Root/Panel/Button` transform path
the probe would report: recgo fetches the object tree once when the app
connects (`getAllLoadedScenesAndObjects`, the way altdrive.py's `tree`
does; lazily on the first fallback hit otherwise), keeps
`transformId -> (name, transformParentId)`, and walks the hit's parent
chain to its scene root. A hit whose transform, or whose ancestor, the tree
does not know (an object created since) refetches the tree, at most once
every 5 seconds; a hit still unknown after that keeps its bare object name
(the Windows run, which predates this, shows `on PreviewRawImage` for what is
`/Canvas/AuthScreen/Preview/PreviewRawImage`). UI Toolkit elements are
invisible.

## Ports

recgo binds `127.0.0.1:13000` by default because that is what the client's
`--alttester host:port` defaults to. AltTester Desktop, or any relay of your
own that stands in for it, binds the same port; only one can own it. If
recgo refuses to start with `cannot listen on 127.0.0.1:13000`, either

- stop the other server, or
- pick another port: `recgo-alttester --alt-port 13100` and launch the
  client with `--alttester 127.0.0.1:13100`.

While recgo runs it *is* the relay: any `AltDriver` client (the SDK's, or a
raw-protocol one) reaches the same app through `/altws` on recgo's port.

## Verifying without a Unity build

`go test ./internal/alt/` drives the server and resolver with a fake app
that answers like the SDK (pairing, both notification shapes, the
double-answer of `getScreenshot`, the probe and its fallback). With a
spec-shaped mock app of your own, point `RECGO_ALT_RIG` at a directory
holding `mockapp.py` and `altdrive.py` and the same package runs them
through recgo end to end; without it that test skips.

The Go tests in `internal/alt` do the same with an in-process fake
(pairing, notifications, messageId routing, the two-part `getScreenshot`
answer, probe hits, the no-probe fallback, error envelopes, 4002 on app
loss).

## Flags

| Flag | Default | |
|---|---|---|
| `--alt-host` | `127.0.0.1` | address the app dials |
| `--alt-port` | `13000` | port the app dials |
| `--alt-app` | any | accept only this `appName` |
| `--alt-window` | `decentraland` | substring of the app window's title or class, when `appName` is the SDK default; a window of class `UnityWndClass` (every Unity player on Windows, whatever its title) is taken as the app regardless |
| `--probe-type` | `DCL.Input.AltTesterUiProbe` | static class holding `ElementAtScreenPoint` |
| `--probe-assembly` | `DCL.Input` | its assembly |

Every `recgo-desktop` flag applies too (`--duration`, `--no-audio`,
`--click-shots`, `--focus-shots`, `--stt-backend`, `--sync-target`, ...).
