package desktop

import (
	"context"
	"flag"
	"fmt"
	"image"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/eordano/recgo/internal/config"
	"github.com/eordano/recgo/internal/screencast"
	"github.com/eordano/recgo/internal/sessioncli"
	"github.com/eordano/recgo/internal/tab"
	"github.com/eordano/recgo/internal/transcribe"
	"github.com/eordano/recgo/internal/upload"
)

var shotOffsets = []struct {
	Label  string
	Offset float64
}{
	{"before", -100},
	{"at", 0},
	{"after", 100},
}

// Extension is how a tool built on this recorder (recgo-alttester) joins
// the run. Start gets a func that adds a Note line and may refuse to start;
// ResolveClick runs right after each click event is pushed, off the tap
// goroutine, and what it returns is merged into the event before Pack.
type Extension struct {
	Start        func(note func(string)) error
	Stop         func()
	ResolveClick func(x, y float64) (elem *tab.Element, note string)
}

var extension *Extension

// SetExtension installs the tool's hooks; call it before Main.
func SetExtension(e *Extension) { extension = e }

// applyResolved merges a resolver's answer into the click event: the
// element when one was found, the note after any the tap already set.
func applyResolved(e *tab.Event, el *tab.Element, note string) {
	if el != nil {
		e.Elem = el
	}
	if note != "" {
		if e.Note != "" {
			note = e.Note + "; " + note
		}
		e.Note = note
	}
}

// Main is the whole CLI: recgo-desktop records every screen, recgo-window
// one screen (or window) chosen when it starts; everything else is shared.
func Main(tool string) {
	o := opts{tool: tool, window: tool == "recgo-window"}
	var listScreens bool
	if o.window {
		flag.StringVar(&o.screen, "screen", "",
			"which screen to record: its number in -list-screens, or 'main'; with no value "+
				"recgo-window asks on the terminal (macOS), or in the desktop's own screen/window "+
				"picker (Linux, where this flag is ignored and the picker also offers single windows)")
		flag.BoolVar(&listScreens, "list-screens", false, "print the attached screens, one per line, and exit")
	}
	flag.BoolVar(&o.noVideo, "no-video", false,
		"skip screen capture entirely — record mic narration only (what the Mac app's Audio mode runs)")
	flag.BoolVar(&o.requireSystemAudio, "require-system-audio", false, "fail instead of falling back to microphone-only if system audio is unavailable")
	flag.StringVar(&o.gstLaunch, "gst-launch", "gst-launch-1.0", "GStreamer binary (Linux frame capture)")
	flag.StringVar(&o.tokenPath, "restore-token", "", "file holding the portal restore token (default under $XDG_STATE_HOME)")
	flag.BoolVar(&o.clickShots, "click-shots", true,
		"screenshot every mouse click, like recgo-tab does in the browser; on macOS this "+
			"needs the Accessibility (or Input Monitoring) grant, on Linux (KDE Plasma) read "+
			"access to /dev/input -- the input group (--click-shots=false to disable)")
	flag.BoolVar(&o.focusShots, "focus-shots", true,
		"screenshot when another window, app or browser tab comes to the front, and when "+
			"a new window or dialog appears; rides the Screen Recording grant the recorder "+
			"already holds on macOS, a KWin script on Linux (--focus-shots=false to disable)")
	o.Register(sessioncli.LiveHelp)

	flag.Usage = func() {
		switch tool {
		case "recgo-window":
			fmt.Fprintf(os.Stderr, "recgo-window — record one screen, chosen at start, with narration\n\n")
		case "recgo-alttester":
			fmt.Fprintf(os.Stderr, "recgo-alttester — record the screen with narration, naming the Unity UI\n"+
				"element under every click through the AltTester SDK in the dev build\n\n")
		default:
			fmt.Fprintf(os.Stderr, "recgo-desktop — record the screen with narration\n\n")
		}
		fmt.Fprintf(os.Stderr, "Usage: %s [flags]\n\n", tool)
		flag.PrintDefaults()
		if o.window {
			fmt.Fprintf(os.Stderr, "\nOnly the screen (or window) picked at start is captured: frames and every\n"+
				"screenshot show that one source. The pick is asked for on every start and\n"+
				"never remembered. recgo-desktop records every screen instead.\n")
		}
		fmt.Fprintf(os.Stderr, "\nType m<enter> while recording to mark a moment; every mouse click is\n"+
			"screenshotted automatically (--click-shots=false to disable).\n")
		fmt.Fprintf(os.Stderr, "\nVideo and screenshots stay on this machine. With a local whisper model\n"+
			"nothing else leaves it either; without one, --stt-backend auto (the default)\n"+
			"UPLOADS THE AUDIO to the configured remote endpoint. Pass --stt-backend local\n"+
			"or none to guarantee nothing is uploaded.\n")
		sessioncli.UsageTail()
	}
	flag.Parse()

	if listScreens {
		printScreens(os.Stdout, screencast.DisplayList())
		return
	}
	o.Load()

	if err := run(o); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", tool, err)
		os.Exit(1)
	}
}

type opts struct {
	sessioncli.Options
	tool, screen, gstLaunch, tokenPath string
	window, noVideo, requireSystemAudio bool
	clickShots, focusShots              bool
}

func (o opts) liveFeed() *transcribe.Session {
	if !o.Live || o.NoAudio {
		return nil
	}
	switch o.STTBackend {
	case "remote":
		eps := o.Endpoints()
		if len(eps) == 0 {
			fmt.Fprintln(os.Stderr, "live: narration disabled -- no remote STT endpoint configured "+
				"(set --stt-url, or [transcription.remote] endpoint in the recgo config)")
			return nil
		}
		key := tab.APIKey(o.STTKey, o.CfgKey)
		fmt.Fprintf(os.Stderr, "live: streaming narration to %s as it is spoken -- the final "+
			"transcript is still redone from the full recording (--live=false to disable)\n", eps[0].URL)
		if o.LiveRealtime {
			fmt.Fprintf(os.Stderr, "live: streaming narration to %s over its realtime websocket as it is spoken "+
				"-- the final transcript is still redone from the full recording (--live=false to disable)\n", eps[0].URL)
			return tab.RealtimeLiveFeed(context.Background(), eps[0], key, o.STTModel)
		}
		return tab.RemoteLiveFeed(context.Background(), eps[0], key, o.STTModel)
	case "local":
		feed, reason := tab.LocalLiveFeed(context.Background(), o.WhisperBin, o.WhisperModel, o.WhisperVAD)
		if feed == nil {
			fmt.Fprintf(os.Stderr, "live: narration disabled -- %s\n", reason)
			return nil
		}
		fmt.Fprintf(os.Stderr, "live: transcribing narration locally as it is spoken -- the final "+
			"transcript is still redone from the full recording (--live=false to disable)\n")
		return feed
	}
	return nil
}

func tokenFile(override string) string {
	if override != "" {
		return override
	}
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" {
		home, _ := os.UserHomeDir()
		state = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(state, "recgo", "screencast-token")
}

func systemInfo() *tab.SystemInfo {
	sys := tab.CollectSystemInfo()
	for _, d := range screencast.DisplayList() {
		scale := 0.0
		if d.W > 0 && d.PixelW > d.W {
			scale = float64(d.PixelW) / float64(d.W)
		}
		sys.Displays = append(sys.Displays, tab.Display{W: d.W, H: d.H, Scale: scale, Main: d.Main})
	}
	return sys
}

func run(o opts) error {
	if o.requireSystemAudio && (o.NoAudio || o.SystemAudio == "") {
		return fmt.Errorf("system audio required: provide -system-audio and enable audio capture")
	}
	if o.noVideo {
		if o.NoAudio {
			return fmt.Errorf("--no-video with --no-audio leaves nothing to record")
		}
		o.clickShots, o.focusShots = false, false
	}
	o.ResolveBackend()
	started, root, stamp, provisional, err := o.Provision()
	if err != nil {
		return err
	}
	stopPortal, err := o.StartPortal(root)
	if err != nil {
		return err
	}
	defer stopPortal()

	clock := tab.NewClock()
	rec := tab.NewRecording(clock, provisional)

	// Before the capture is opened: a taken AltTester port is reported before
	// the portal's picker appears, and the launch hint is on screen while the
	// person starts the client.
	ext := extension
	if ext != nil && ext.Start != nil {
		if err := ext.Start(func(text string) {
			rec.Push(tab.Event{T: clock.Now(), Kind: "note", Text: text})
		}); err != nil {
			return err
		}
	}
	if ext != nil && ext.Stop != nil {
		defer ext.Stop()
	}

	var desk *screencast.Desktop
	backend := "audio-only"
	if !o.noVideo && o.window {
		var chosen *screencast.DisplayInfo
		if screencast.ScreenChoiceLocal() {
			if chosen, err = chooseScreen(o.screen, screencast.DisplayList(), os.Stdin, os.Stderr); err != nil {
				return err
			}
		} else if o.screen != "" {
			fmt.Fprintln(os.Stderr, "-screen is ignored here: the desktop's own picker chooses the screen or window")
		}
		desk, err = screencast.OpenDesktop(screencast.DesktopOptions{
			Now:       clock.Now,
			FrameDir:  filepath.Join(provisional, ".frames"),
			WindowMs:  20_000,
			GstLaunch: o.gstLaunch,
			OneSource: true,
			Screen:    chosen,
		})
		if err != nil {
			return err
		}
		defer desk.Close()
		backend = captureLabel(desk.Backend(), desk.Source())
		fmt.Fprintf(os.Stderr, "capturing %s\n", desk.Source())
	} else if !o.noVideo {
		tokPath := tokenFile(o.tokenPath)
		prev, _ := os.ReadFile(tokPath)

		desk, err = screencast.OpenDesktop(screencast.DesktopOptions{
			Now:          clock.Now,
			FrameDir:     filepath.Join(provisional, ".frames"),
			WindowMs:     20_000,
			RestoreToken: strings.TrimSpace(string(prev)),
			GstLaunch:    o.gstLaunch,
		})
		if err != nil {
			return err
		}
		defer desk.Close()

		if tok := desk.RestoreToken(); tok != "" && tok != strings.TrimSpace(string(prev)) {
			os.MkdirAll(filepath.Dir(tokPath), 0o700)
			os.WriteFile(tokPath, []byte(tok), 0o600)
		}
		backend = captureLabel(desk.Backend(), desk.Source())
	}
	frameStream := desk != nil && desk.FrameStream()

	feed := o.liveFeed()
	var tapFn func([]byte)
	if feed != nil {
		tapFn = feed.Feed
	}

	var mic *tab.Mic
	if !o.NoAudio {
		monitor, err := tab.ResolveMonitor(o.SystemAudio)
		if err != nil && o.requireSystemAudio {
			return fmt.Errorf("system audio required: %w", err)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "system audio: %v -- recording the microphone alone\n", err)
		}
		if mic, err = o.StartMic(clock, provisional, monitor, tapFn); err != nil {
			if o.noVideo || o.requireSystemAudio {
				return fmt.Errorf("audio: %w (nothing else to record)", err)
			}
			fmt.Fprintf(os.Stderr, "audio: %v\n", err)
			feed = nil
		}
	}

	start := rec.Push(tab.Event{T: 0, Kind: "record-start", Text: backend})

	// Every capture runs in a goroutine, and KWinShot can block for up to
	// ten seconds: shutdown must wait for the in-flight ones (or the shot
	// is lost, its metadata omitted, or the write lands through the stale
	// provisional path after the rename) and refuse new ones.
	var shots sync.WaitGroup
	var shotMu sync.Mutex
	shotsClosed := false
	startShot := func(fn func()) bool {
		shotMu.Lock()
		if shotsClosed {
			shotMu.Unlock()
			return false
		}
		shots.Add(1)
		shotMu.Unlock()
		go func() {
			defer shots.Done()
			fn()
		}()
		return true
	}
	if desk != nil {
		startShot(func() { captureInitialShot(desk, rec, start, provisional) })
	}

	// One capture set at a time: screencapture(1) takes a few hundred ms, so
	// a burst of clicks and focus flips would otherwise pile up processes.
	// Events that arrive while one is in flight are still recorded, just
	// without their own shots -- the in-flight set is at most 100ms away.
	var shotBusy int32
	shotEvent := func(ev *tab.Event, t float64, seq int) {
		if desk == nil {
			return
		}
		if !atomic.CompareAndSwapInt32(&shotBusy, 0, 1) {
			return
		}
		if !startShot(func() {
			defer atomic.StoreInt32(&shotBusy, 0)
			captureAround(desk, rec, ev, provisional, seq, t, clock)
		}) {
			atomic.StoreInt32(&shotBusy, 0)
		}
	}

	var stopClicks func()
	if o.clickShots {
		stopClicks, err = screencast.StartClickTap(func(c screencast.Click) {
			t := clock.Now()
			seq := rec.NextClickSeq()
			ev := rec.Push(tab.Event{T: t, Kind: "click", Seq: seq, X: c.X, Y: c.Y})
			if c.Button == 2 {
				rec.Update(ev, func(e *tab.Event) { e.Note = "right-click" })
			}
			shotEvent(ev, t, seq)
			if ext != nil && ext.ResolveClick != nil {
				// Shares the in-flight gate with the shots: the answer must
				// land before the snapshot, and never after the rename.
				startShot(func() {
					el, note := ext.ResolveClick(c.X, c.Y)
					rec.Update(ev, func(e *tab.Event) { applyResolved(e, el, note) })
				})
			}
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "click shots disabled: %v\n", err)
		} else {
			defer stopClicks()
		}
	}

	var stopWindows func()
	if o.focusShots {
		watchEvent := func(kind string) func(string) {
			return func(desc string) {
				t := clock.Now()
				seq := rec.NextClickSeq()
				ev := rec.Push(tab.Event{T: t, Kind: kind, Seq: seq, Title: desc})
				shotEvent(ev, t, seq)
			}
		}
		stopWindows, err = screencast.StartWindowWatch(
			watchEvent("focus"), watchEvent("window-appear"))
		if err != nil {
			fmt.Fprintf(os.Stderr, "focus shots disabled: %v\n", err)
		} else {
			defer stopWindows()
		}
	}

	fmt.Fprintf(os.Stderr, "recording via %s — type m<enter> to mark a moment\n", backend)
	if o.Duration > 0 {
		fmt.Fprintf(os.Stderr, "stopping after %s\n", o.Duration)
	} else {
		fmt.Fprintln(os.Stderr, "press Ctrl-C to stop")
	}

	var lv *tab.LiveView
	if o.Live {
		header := fmt.Sprintf("# Session (recording — replaced by SESSION.md at stop)\n\n"+
			"Start: %s\nCapture: %s\n\n", started.Format("2006-01-02 15:04:05"), backend)
		lv = tab.StartLiveView(rec, os.Stderr, header)
	}
	narrate := func(t float64, text string) {
		fmt.Fprintf(os.Stderr, "%s  narration: %s\n", tab.FormatClock(t), text)
	}
	if lv != nil {
		narrate = lv.Utterance
	}
	if feed != nil {
		if err := feed.StartFeed(); err != nil {
			fmt.Fprintf(os.Stderr, "live narration: %v\n", err)
			feed = nil
		} else {
			hearing := func(text string) {
				fmt.Fprintf(os.Stderr, "%s  hearing: %s\n", tab.FormatClock(clock.Now()), text)
			}
			tab.ConsumeLiveWith(feed, clock, narrate, hearing, func(err error) {
				fmt.Fprintf(os.Stderr, "live narration: %v\n", err)
			})
		}
	}

	marks := 0
	mark := func() float64 {
		marks++
		t := clock.Now()
		seq := rec.NextClickSeq()
		ev := rec.Push(tab.Event{T: t, Kind: "mark", Seq: seq})
		if desk != nil {
			startShot(func() { captureAround(desk, rec, ev, provisional, seq, t, clock) })
		}
		return t
	}
	note := func(text string) float64 {
		t := clock.Now()
		rec.Push(tab.Event{T: t, Kind: "note", Text: text})
		return t
	}
	go tab.ReadCommands(os.Stdin, mic, mark, note)

	sessioncli.WaitForStop(o.Duration)
	fmt.Fprintln(os.Stderr, "stopping...")
	if stopClicks != nil {
		stopClicks()
	}
	if stopWindows != nil {
		stopWindows()
	}
	if feed != nil {
		feed.Stop()
	}
	if lv != nil {
		lv.Stop()
	}

	// Refuse new captures, then drain the in-flight ones before touching
	// the capture backend, .frames, the event snapshot, or the rename.
	shotMu.Lock()
	shotsClosed = true
	shotMu.Unlock()
	shots.Wait()
	if desk != nil {
		if err := desk.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "capture: %v\n", err)
		}
		os.RemoveAll(filepath.Join(provisional, ".frames"))
	}

	durationMs := clock.Now()
	rec.Push(tab.Event{T: durationMs, Kind: "record-stop"})
	wavPath, audioNote := o.StopMic(mic)
	transcript := o.Transcribe(clock, wavPath, nil)

	events := rec.Snapshot()
	tool, base := o.tool, "desktop"
	if o.window {
		base = "window"
	}
	if o.noVideo {
		tool, base = "recgo-audio", "audio"
	}
	slug := fmt.Sprintf("%s-%d-marks", base, marks)
	if marks == 0 {
		slug = base
	}
	title := ""
	titleNote := ""
	named := o.Title(transcript, events)
	if named.Title != "" {
		title, titleNote, slug = named.Title, named.Note, named.Slug
	}
	finalDir, err := tab.FinalizeDir(provisional, filepath.Join(root, fmt.Sprintf("%s-%s", stamp, slug)))
	if err != nil {
		return err
	}

	cwd, _ := os.Getwd()
	session, err := tab.Pack(finalDir, events, clock, transcript, tab.Meta{
		Slug: slug, Title: title, StartedISO: started.UTC().Format(time.RFC3339),
		StartedWall: started.Format("2006-01-02 15:04:05"),
		DurationMs:  durationMs, Cwd: cwd, TargetURL: backend, FrameStream: frameStream,
		Tool: tool, AudioNote: audioNote, TitleNote: titleNote, SystemAudio: sessioncli.SystemAudio(mic),
		System: systemInfo(),
	}, tab.PackOptions{JSON: o.JSON})
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "\nwrote %s\n", filepath.Join(finalDir, "SESSION.md"))
	fmt.Fprintf(os.Stderr, "  %d clicks, %d marks, %d utterances\n",
		session.Counts.Clicks, marks, session.Counts.Utterances)
	syncSession(o, finalDir)
	return nil
}

// captureLabel is the SESSION.md Capture line: the backend plus what it
// captures (the display picked at start, or the whole desktop). It never
// names a focused window: on the Windows run the Unity window was the subject
// and a browser only came to the front at the end, yet the line read
// "windows-gdi — ... Microsoft Edge". A backend that already names its
// source (the one-screen modes) is left as is.
func captureLabel(backend, source string) string {
	if source == "" || strings.Contains(backend, source) {
		return backend
	}
	return fmt.Sprintf("%s (%s)", backend, source)
}

// The host may also run a periodic sync over the output root, but the person
// who just recorded wants to hand the remote path to someone, or to an agent,
// now. A failure is reported and swallowed -- the recording is already safely
// on disk, and the periodic sync is still a backstop.
func syncSession(o opts, dir string) {
	if o.NoSync || o.SyncTarget == "" {
		return
	}
	fmt.Fprintf(os.Stderr, "  syncing -> %s ...\n", o.SyncTarget)
	dest, err := upload.SyncDir(config.UploadConfig{
		Target: o.SyncTarget,
		SSHKey: o.SyncKey,
	}, dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  sync failed (kept locally): %v\n", err)
		return
	}
	if dest != "" {
		fmt.Fprintf(os.Stderr, "  synced -> %s\n", upload.Display(dest))
		if local := upload.LocalMirror(dest); local != "" {
			fmt.Fprintf(os.Stderr, "  shared -> %s\n", local)
		}
	}
}

func captureInitialShot(desk *screencast.Desktop, rec *tab.Recording, ev *tab.Event, dir string) {
	var last error
	for i := 0; i < 10; i++ {
		img, err := desk.Snapshot()
		if err == nil {
			if err = writePNG(filepath.Join(dir, tab.InitialShot), img); err == nil {
				rec.Update(ev, func(e *tab.Event) { e.FullShot = tab.InitialShot })
				return
			}
		}
		last = err
		time.Sleep(200 * time.Millisecond)
	}
	fmt.Fprintf(os.Stderr, "initial screenshot: %v\n", last)
}

func writePNG(path string, img image.Image) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}

func captureAround(desk *screencast.Desktop, rec *tab.Recording, ev *tab.Event,
	dir string, seq int, t float64, clock *tab.Clock) {

	if img, err := desk.Snapshot(); err == nil {
		name := tab.ShotName(seq, "full")
		if writePNG(filepath.Join(dir, name), img) == nil {
			lag := clock.Now() - t
			rec.Update(ev, func(e *tab.Event) {
				e.FullShot = name
				e.FullLagMs = lag
			})
		}
	}

	for _, so := range shotOffsets {
		target := t + so.Offset
		if so.Offset > 0 {
			time.Sleep(time.Duration(so.Offset) * time.Millisecond)
		}

		shot := tab.Shot{Label: so.Label, OffsetMs: so.Offset, TargetT: target}

		if path, ok, definitive := desk.FrameAt(target); ok {
			name := tab.ShotName(seq, so.Label)
			if data, err := os.ReadFile(path); err == nil &&
				os.WriteFile(filepath.Join(dir, name), data, 0o600) == nil {
				shot.File, shot.Captured, shot.Definitive = name, true, definitive
			}
		} else if so.Offset == 0 {
			if img, err := desk.Snapshot(); err == nil {
				name := tab.ShotName(seq, so.Label)
				if writePNG(filepath.Join(dir, name), img) == nil {
					shot.File, shot.Captured = name, true
				}
			}
		} else {
			shot.Note = "no frame history on this platform; only the mark instant is captured"
		}

		rec.Update(ev, func(e *tab.Event) { e.Shots = append(e.Shots, shot) })
	}
}

// printScreens lists the attached displays the way -screen numbers them:
// "N<tab>WxH<tab>main" per line, so a GUI can build its own picker from it.
func printScreens(w io.Writer, displays []screencast.DisplayInfo) {
	for i, d := range displays {
		main := ""
		if d.Main {
			main = "main"
		}
		fmt.Fprintf(w, "%d\t%s\t%s\n", i+1, d.Size(), main)
	}
}

// chooseScreen resolves -screen (a number from -list-screens, or "main"); with
// no value it auto-picks a lone display, else asks on the terminal.
func chooseScreen(spec string, displays []screencast.DisplayInfo, in io.Reader, out io.Writer) (*screencast.DisplayInfo, error) {
	if len(displays) == 0 {
		return nil, fmt.Errorf("no displays found")
	}
	pick := func(n int) (*screencast.DisplayInfo, error) {
		if n < 1 || n > len(displays) {
			return nil, fmt.Errorf("-screen %d: there are %d displays (see -list-screens)", n, len(displays))
		}
		return &displays[n-1], nil
	}
	switch {
	case spec == "main":
		for i := range displays {
			if displays[i].Main {
				return &displays[i], nil
			}
		}
		return &displays[0], nil
	case spec != "":
		n, err := strconv.Atoi(spec)
		if err != nil {
			return nil, fmt.Errorf("-screen %q: want a number from -list-screens, or main", spec)
		}
		return pick(n)
	case len(displays) == 1:
		return &displays[0], nil
	}
	if f, ok := in.(*os.File); ok {
		if st, err := f.Stat(); err != nil || st.Mode()&os.ModeCharDevice == 0 {
			return nil, fmt.Errorf("%d displays and no terminal to ask on: pass -screen N (see -list-screens)", len(displays))
		}
	}
	fmt.Fprintln(out, "Which screen?")
	for i, d := range displays {
		main := ""
		if d.Main {
			main = "  (main)"
		}
		fmt.Fprintf(out, "  %d  %s%s\n", i+1, d.Size(), main)
	}
	fmt.Fprintf(out, "screen [1-%d]: ", len(displays))
	var line string
	if _, err := fmt.Fscanln(in, &line); err != nil {
		return nil, fmt.Errorf("no screen chosen")
	}
	n, err := strconv.Atoi(strings.TrimSpace(line))
	if err != nil {
		return nil, fmt.Errorf("%q is not a screen number", line)
	}
	return pick(n)
}
