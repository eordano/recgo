# Recgo for Mac — Design Document

Turning the recgo surface — `recgo` (audio), `recgo-browser` (whole browser),
`recgo-tab` (one tab), `recgo-desktop` (the screen) — into one native macOS
app. The four CLIs stay as the engine; the app is the face: capture, narrate,
review, and hand a session to a person or an agent, without ever seeing a
terminal.

Working name: **Recgo**. Tagline candidate: *"Walk and talk. Everything
becomes a document."*

---

## 1. What the engine already does (the raw material)

The app invents no new capture capability — everything below exists and is
tested today:

| Engine | Capability |
|---|---|
| `recgo` | Microphone + system audio (BlackHole), live local transcription, btop-style level UI |
| `recgo-browser` | Whole-browser recording over CDP: clicks *with element identity*, navigations, tab switches, console errors, network failures, HMR events, per-click before/at/after frames |
| `recgo-tab` | The same recorder pinned to one tab (`--match`/`--select`) |
| `recgo-desktop` | Screen recording via `screencapture`/portal, system-wide click screenshots (event tap), focus-change and new-window/dialog screenshots (window list), marks |
| shared | One session clock; `SESSION.md` + `SESSION.live.md` + `session.json`; local-first Whisper STT with explicit, announced remote fallback; LLM titling; host/display system info; `--portal` live bridge for agents; `--sync-target` push; iCloud-safe output root |
| `recgo-sessions` | A static HTML library of all sessions: filmstrip, click crosshairs, transcript |

The unit of value is not "a video". It is **a session folder a human or a
coding agent can act on**: a chronological markdown document interleaving
narration, clicks, errors and screenshots, plus machine-readable JSON. The Mac
app must protect that identity — it is a *session recorder*, not a screen
recorder.

## 2. Design principles (research-backed)

Drawn from Apple's HIG structure (Foundations / Patterns / Components /
Technologies), the Liquid Glass design language shipped with macOS Tahoe 26,
and studying the two best-in-class Mac capture apps (CleanShot X, Screen
Studio):

1. **Be invisible until needed.** CleanShot X's core lesson: a capture tool
   is great when it "becomes an extension of your work". Recgo lives in the
   menu bar, launches nothing at login but a status item, and gets out of the
   way while recording.
2. **One continuous flow.** Capture → annotate/narrate → share must be a
   single gesture chain, never "open the app, find the file, export".
   CleanShot's *Quick Access overlay* (a floating thumbnail of the thing you
   just captured, ready to drag, name, or discard) is the pattern to adopt
   for finished sessions.
3. **Great by default.** Screen Studio's lesson: the output should look
   professional with zero editing. Recgo's equivalent: `SESSION.md` reads
   well with zero cleanup — titled by the LLM, time-sorted, screenshots
   inline. The app's job is to *show* that document beautifully, not to add
   an editing suite.
4. **The menu bar is for status and verbs, not the app.** Apple's guidance:
   a menu bar extra shows live status and a small set of task-focused
   actions; the full experience lives in a real window with a real menu bar
   (File/Edit/View/Window/Help), keyboard shortcuts for everything, and
   drag-and-drop.
5. **Native material, restrained motion, no capsule farm.** Adopt Liquid
   Glass by using stock SwiftUI toolbar/sidebar/menu components (they get the
   material for free when built with current Xcode) rather than custom
   chrome. Recording UI must never draw attention — the person is
   presenting. Status is a quiet line of text, not a collection of colored
   pills and badges; the app earns exactly one floating element (the
   post-capture thumbnail) and one colored signal (amber when bytes leave
   the machine).
6. **Privacy is the product.** The CLIs already treat "bytes leaving the
   machine" as an event to announce. The app upgrades those stderr warnings
   into first-class UI: one always-present line saying where bytes go, and a
   default path where they go nowhere.

## 3. App structure

Four surfaces, smallest first:

### 3.1 Menu bar extra (the daily driver)

A status item (template SF Symbol: `record.circle`; red-filled while
recording, with elapsed time optionally shown). Clicking opens a compact
Liquid Glass popover:

```
● Record
   ⌘⇧1  Screen            (recgo-desktop)
   ⌘⇧2  Browser           (recgo-browser)
   ⌘⇧3  This Tab…         (recgo-tab --select)
   ⌘⇧4  Audio only        (recgo)
── while recording ──────────────────────
   ◉ 03:42  Screen · narrating locally
   Mark this moment            ⌘⇧M
   Pause narration
   ■ Stop and open session     ⌘⇧⏎
──────────────────────────────────────────
   Last session: "Login popup dismisses…"  →
   Library…                    ⌘L
   Settings…                   ⌘,
```

Rules: the popover never blocks; every verb has a global hotkey so the menu
is optional; the status item is the *only* recording indicator Recgo adds
(macOS already shows its own screen-capture indicator — don't compete with
it).

### 3.2 Recording HUD

On start: a 3-2-1 countdown that fades in place of the HUD (skippable, off
by default for audio-only).
While recording, an optional floating mini-HUD (draggable, snaps to screen
corners, auto-fades):

- elapsed time, input level meter (the `recgo` TUI's soul, miniaturized)
- last narration line as it decodes — live proof that speech is being heard,
  the single most reassuring element the CLIs already print to stderr
- a mark button and a stop button

The HUD is `NSPanel`-style, non-activating, excluded from capture
(`sharingType = .none`) so it never appears in the recording itself.

### 3.3 The Library (main window)

Replaces `recgo-sessions`' static HTML with a native three-pane window —
stock sidebar + toolbar so Liquid Glass comes free:

- **Sidebar**: All Sessions, per-tool smart groups (Screen / Browser / Tab /
  Audio), narrated, synced, plus user folders. Search field filters across
  titles *and transcripts* — narration becomes a personal knowledge base.
- **Session list**: title, date, duration, shot count — status folded into
  the subtitle as plain text ("narrated · 2 errors · synced"), not a badge
  row.
- **Detail view**: the filmstrip (start + every shot-bearing event, click
  crosshairs overlaid), under it the chronological stream exactly as
  SESSION.md orders it — narration, clicks with element identity, Focus/
  Window lines, errors — each row jumping the filmstrip to its shot.
  A "Reading view" toggle renders SESSION.md itself.

Mac-app table stakes: every file is a real file — sessions drag out of the
library as folders; ⌘C on a shot copies the image; Share menu (Messages,
Mail, AirDrop); QuickLook on space; Open in Finder everywhere; full keyboard
navigation.

### 3.4 Live session window

Opened from the menu bar during a recording: `SESSION.live.md` rendered live
(the file already rewrites every ~1s). This is the "second screen" for a
presenter, and the human-side mirror of what a portal-connected agent sees.

## 4. Onboarding and permissions (the make-or-break flow)

TCC realities that shape the design (from current macOS behavior): Screen
Recording can only be granted by the user, the app only appears in System
Settings *after* it first calls the capture API, standard users may be unable
to approve some panes, and newer macOS adds "Allow for One Session" plus
periodic re-confirmation for screen capture.

Design: a **permissions checklist, asked in context, never all at once.**

- First launch: a single welcome screen — "Recgo records your screen, your
  voice, and what you click, into a document. Everything stays on this Mac
  unless you say otherwise." One button: *Try a 20-second walk-and-talk*.
- Each capture mode requests only what it needs, at the moment of first use:
  - Audio → Microphone
  - Screen → Screen Recording (trigger the API first so the app appears in
    the pane, then deep-link `x-apple.systempreferences:` to it)
  - Click shots → Accessibility/Input Monitoring — presented as an
    *optional enhancement* with a live preview of what it adds; recording
    proceeds without it, exactly as the CLI degrades today
- Every permission row shows live state (granted / needs relaunch / off) and
  a "why we ask" disclosure. Denial is never a dead end: the session records
  with whatever is granted, and the session document notes what was off —
  the engine's graceful-degradation behavior, surfaced honestly.
- System audio still needs BlackHole; ship the guided Audio MIDI setup from
  the README as an in-app assistant with a "test tone → did you hear it in
  the recording?" verification loop.

## 5. Privacy UX

- **One destination line**: the popover footer always carries a single quiet
  caption stating where bytes go. Default: "narrating locally — nothing
  leaves this Mac" (first-run offers a one-click Whisper model download to
  make that path the default reality). When remote STT, remote titling,
  portal, or sync is active, the same line turns amber and names the host —
  no separate pill, badge, or icon; the color change *is* the signal, in the
  popover and mirrored as a tint on the HUD's elapsed time. The CLI's
  capitalized help-text warnings (UPLOADS THE AUDIO, EXPOSES THE OUTPUT
  ROOT…) become the copy of the confirmation sheets — written once, enforced
  by the same tests.
- Settings has a single master switch mirroring `--stt-backend local`:
  "Never upload anything" — grays out every remote option.
- Sessions contain unredacted screenshots and narration: the library offers
  per-session "Delete shots containing…" (OCR-assisted) before sharing, as a
  later phase.

## 6. Use cases

The catalog the app is designed around. Each names the capture mode and what
the session document uniquely provides.

**For working with coding agents (the differentiating family):**

1. **Bug report an agent can act on** — Browser/Tab mode. Narrate while
   reproducing; the agent receives clicks with element identity, the console
   error, the network failure, and your words on one clock. Hand-off = drag
   the session folder into a Claude Code conversation, or "Copy path".
2. **Live walk-and-talk with an agent** — any mode + portal. The agent joins
   the portal room and follows `SESSION.live.md` while you talk; you watch it
   respond in its own pane. The app makes the room name a QR/one-click share.
3. **"Why is it doing this?" ops capture** — Desktop mode. Terminal focus
   events, dialogs appearing, timestamps against narration; attach to an
   incident or feed to the on-call agent.
4. **Review an agent's work** — Desktop/Browser mode. Record yourself
   reviewing the agent's output (the existing recgo-review flow), sync the
   session, and let the next agent session consume the review.

**For teams:**

5. **PR / design-review narration** — Tab mode on the PR or Figma; reviewer
   thinks aloud; the author reads a time-stamped document instead of
   scrubbing a video.
6. **QA regression evidence** — Browser mode; the click-by-click record with
   before/at/after frames and "screen did not repaint" verdicts is the
   reproduction script.
7. **Usability testing** — Browser mode; element-identity clicks + narrated
   confusion, searchable across sessions ("find every session where someone
   said 'where is'").
8. **Support hand-off** — Desktop mode; "show me what happened" becomes a
   folder the support engineer reads in two minutes; system info answers the
   which-macOS-which-display questions before they're asked.
9. **Team session library** — sync-target pushes finished sessions to a
   shared host; the Library gains a "Team" source reading the same tree.

**For individuals:**

10. **Meeting / demo capture** — Audio mode with system audio; live local
    transcript; the LLM title makes the library scannable.
11. **Voice memos with screen context** — quick Desktop recordings where the
    narration is the point and the shots are anchors ("the file I was
    looking at when I had this idea").
12. **Tutorial & documentation source** — Desktop/Browser mode; SESSION.md
    with inline screenshots is 80% of a how-to doc; export as the draft.
13. **Personal knowledge base** — every narrated session is full-text
    searchable; the library becomes "what did I say about X in March".
14. **Standup pre-record** — one-minute audio or screen session, auto-titled,
    link dropped in chat.

**Explicitly out of scope** (protects the identity): video editing, camera
bubbles, cinematic zoom (Screen Studio owns that), livestreaming, meeting
*attendance* bots. Recgo records *you, working, thinking aloud* — and makes
it legible.

## 7. Interaction details

- Global hotkeys (Settings-rebindable) for start/stop per mode, mark, and
  "open last session". Registered via standard shortcut APIs; conflicts
  surfaced inline.
- **Quick Access thumbnail** on stop (the app's one floating element): the
  initial shot + title-in-progress; drag it anywhere to export the folder,
  click to open in Library, swipe to dismiss. (The LLM title resolves async
  and updates it in place.)
- App Intents / Shortcuts: Start Recording (mode), Stop Recording, Mark
  Moment, Get Last Session Path — so Raycast/Shortcuts/automation users can
  drive it, and Siri "start a walk and talk".
- Services menu + Finder Quick Action: "Record narration about this file".
- Focus filter integration: auto-pause upload/sync targets when a Private
  focus is on.
- Menu bar count-up is optional (some people find timers stressful — Screen
  Studio hides them by default).

## 8. Visual design

- **Liquid Glass, by adoption not imitation**: stock toolbar, sidebar,
  popover and menu components on Tahoe get the material automatically;
  custom surfaces limited to the HUD and Quick Access thumbnail, using system
  materials (`.ultraThinMaterial`-class) so they refract wallpaper the same
  way. On pre-Tahoe macOS everything falls back to standard vibrancy.
- **Icon**: layered icon template (Tahoe's new icon system) — a document
  sheet with a red record dot; reads as "recording that becomes a document",
  not "another camera app".
- SF Symbols throughout (`record.circle`, `waveform`, `macwindow`,
  `globe`, `cursorarrow.click.2`, `doc.text`); Dynamic Type respected in the
  Library; full dark/light support; crosshair overlays and level meters use
  system accent color.
- Motion: one signature animation only — the record button morphing into the
  status-item dot. Everything else is instant.

## 9. Architecture

- **Shell**: SwiftUI app (AppKit where needed: NSStatusItem, NSPanel,
  global hotkeys). Menu-bar-first with `LSUIElement` semantics; the Dock icon
  appears only while the Library window is open (user-toggleable).
- **Engine**: the existing Go binaries ship inside the bundle
  (`Contents/Helpers/`), unchanged, driven as subprocesses. Contract v1 is
  what already exists: flags in, stderr lines + `SESSION.live.md` +
  `session.json` out. Phase 2 adds `--control stdio` (JSON commands:
  mark/stop/status) so the HUD doesn't parse stderr. This keeps the entire
  tested engine, its privacy tests, and CLI/app parity — the CLIs remain
  first-class for fleet Linux hosts.
- **Capture backend evolution**: `screencapture(1)` → ScreenCaptureKit
  inside the Go engine (or a tiny Swift helper feeding frames), unlocking
  window-scoped capture, higher shot rates, and removing the per-shot
  process spawn. The engine's FrameAt/before-after contract already models
  this — darwin just gains frame history.
- **Distribution**: Developer ID + notarization + Sparkle. The Mac App Store
  is ruled out for v1: the sandbox denies the click event tap; a
  reduced MAS build (no click shots) is a possible later SKU.
- **Permissions**: hardened runtime with mic/screen entitlements; TCC
  prompts triggered from the app bundle so grants attach to Recgo.app, not a
  terminal.
- **Nix**: the app builds from this repo; the Go engine derivation is shared
  with the fleet, the Swift shell is a separate darwin-only derivation, and
  the existing NixOS/help-text tests keep guarding the engine contract.

## 10. Phasing

1. **MVP**: menu bar extra + hotkeys + engine subprocesses + Quick Access
   thumbnail + permissions checklist + native Library (read-only, reusing
   `recgo-sessions` parsing rules) + local-model download.
2. **Live**: HUD with live narration, live session window, portal room
   share, Shortcuts/App Intents.
3. **Polish**: ScreenCaptureKit backend, transcript-wide search index, team
   library over sync targets, redaction tools, MAS-safe SKU evaluation.

---

### Sources

- [Apple Newsroom — Apple introduces a delightful and elegant new software design (Liquid Glass)](https://www.apple.com/newsroom/2025/06/apple-introduces-a-delightful-and-elegant-new-software-design/)
- [Liquid Glass — Wikipedia](https://en.wikipedia.org/wiki/Liquid_Glass)
- [Liquid Glass in Swift: official best practices for iOS 26 / macOS Tahoe](https://dev.to/diskcleankit/liquid-glass-in-swift-official-best-practices-for-ios-26-macos-tahoe-1coo)
- [Apple Human Interface Guidelines overview / structure](https://gist.github.com/eonist/f4ba31012815731284d867232f6c70e4)
- [Menu extra — Wikipedia](https://en.wikipedia.org/wiki/Menu_extra)
- [CleanShot X](https://cleanshot.com/)
- [Screen Studio vs CleanShot X — Efficient App](https://efficient.app/compare/screen-studio-vs-cleanshot)
- [Screen Studio vs CleanShot X — Docsie](https://www.docsie.io/vs/screen-studio-vs-cleanshot-x/)
- [macOS Screen Recording Permissions: Complete Guide — Screenify](https://www.screenify.studio/blog/2026-04-23-macos-screen-recording-permissions)
- [Zero-touch guided setup for Camera/Microphone/Screen Sharing TCC — Rocketman](https://www.rocketman.tech/post/you-down-with-tcc-or-providing-zero-touch-guided-setup-for-camera-microphone-and-screen-sharing)
- [macOS TCC/PPPC permissions — Hexnode](https://www.hexnode.com/mobile-device-management/help/automate-macos-tcc-pppc-permissions-deployment/)
