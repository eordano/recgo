package desktop

import (
	"context"
	"flag"
	"fmt"
	"image"
	"image/png"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/eordano/recgo/internal/config"
	"github.com/eordano/recgo/internal/screencast"
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

// Main is the whole CLI: recgo-desktop records every screen, recgo-window
// one screen (or window) chosen when it starts; everything else is shared.
func Main(tool string) {
	window := tool == "recgo-window"
	out := flag.String("out", "", "output root (default: [recording] output_dir in config.toml, else ~/walk-and-talk; ~/Documents/walk-and-talk on macOS)")
	var screen *string
	var listScreens *bool
	if window {
		screen = flag.String("screen", "",
			"which screen to record: its number in -list-screens, or 'main'; with no value "+
				"recgo-window asks on the terminal (macOS), or in the desktop's own screen/window "+
				"picker (Linux, where this flag is ignored and the picker also offers single windows)")
		listScreens = flag.Bool("list-screens", false, "print the attached screens, one per line, and exit")
	}
	duration := flag.Duration("duration", 0, "stop automatically after this long; otherwise Ctrl-C")
	noAudio := flag.Bool("no-audio", false, "skip microphone capture")
	noVideo := flag.Bool("no-video", false,
		"skip screen capture entirely — record mic narration only (what the Mac app's Audio mode runs)")
	micDevice := flag.String("mic", "", "capture device (default: system default source)")
	requireSystemAudio := flag.Bool("require-system-audio", false, "fail instead of falling back to microphone-only if system audio is unavailable")
	systemAudio := flag.String("system-audio", "",
		"mix what you hear into the narration track: a monitor source (<sink>.monitor, or a "+
			"loopback device on macOS), or 'default' for the default output; 'sysaudio off' / "+
			"'sysaudio on' on stdin mutes and unmutes it while recording")
	ffmpegBin := flag.String("ffmpeg", "ffmpeg", "ffmpeg binary")
	gstLaunch := flag.String("gst-launch", "gst-launch-1.0", "GStreamer binary (Linux frame capture)")
	sttBackend := flag.String("stt-backend", "auto",
		"auto (local if a whisper model is found, else remote) | local (whisper.cpp, nothing leaves this machine) | remote (uploads audio) | realtime (uploads audio; streams narration over the endpoint's /v1/realtime websocket as you speak, final transcript via the batch endpoint) | none")
	whisperBin := flag.String("whisper-bin", "whisper-cli", "whisper.cpp CLI")
	whisperModel := flag.String("whisper-model", "", "ggml model (default: discovered, see below)")
	whisperVAD := flag.String("whisper-vad-model", "", "silero VAD model (default: discovered)")
	sttURL := flag.String("stt-url", "", "remote only: override the OpenAI-compatible endpoint list")
	sttModel := flag.String("stt-model", "", "remote only: override the per-endpoint model")
	sttKey := flag.String("stt-api-key", "", "remote only: bearer token (falls back to $OPENAI_API_KEY / $LLM_API_KEY)")
	sttLanguage := flag.String("stt-language", "", "force a language instead of autodetecting")
	noVAD := flag.Bool("no-vad-correct", false, "remote only: skip the local silencedetect pass (NOT ADVISED)")
	keepRaw := flag.Bool("keep-raw", false, "keep audio.pcm alongside audio.wav")
	tokenPath := flag.String("restore-token", "", "file holding the portal restore token (default under $XDG_STATE_HOME)")
	jsonOut := flag.Bool("json", false, "also write session.json (full machine-readable timeline)")
	clickShots := flag.Bool("click-shots", true,
		"screenshot every mouse click, like recgo-tab does in the browser; on macOS this "+
			"needs the Accessibility (or Input Monitoring) grant, on Linux (KDE Plasma) read "+
			"access to /dev/input -- the input group (--click-shots=false to disable)")
	focusShots := flag.Bool("focus-shots", true,
		"screenshot when another window, app or browser tab comes to the front, and when "+
			"a new window or dialog appears; rides the Screen Recording grant the recorder "+
			"already holds on macOS, a KWin script on Linux (--focus-shots=false to disable)")
	live := flag.Bool("live", true,
		"build the session document as it is recorded: print each line as it happens and keep "+
			"SESSION.live.md current in the session folder; narration is transcribed while you "+
			"speak (locally with --stt-backend local); with --stt-backend remote this also "+
			"STREAMS NARRATION AUDIO to the STT endpoint while recording (--live=false to disable)")
	titleBackend := flag.String("title-backend", "",
		"name the session with an LLM: remote (UPLOADS THE TRANSCRIPT) | none (default: follow --stt-backend)")
	titleURL := flag.String("title-url", "", "override the OpenAI-compatible endpoint list used for the title")
	titleModel := flag.String("title-model", "", "override the chat model (default: whatever the endpoint serves)")
	portal := flag.String("portal", "",
		"portal server URL (wss://host or https://host): dial out and serve read-only file "+
			"tools over the output root while recording, so an agent in that portal room can "+
			"follow the live session. EXPOSES THE OUTPUT ROOT (all sessions, read-only) to "+
			"everyone in the room for as long as the recording runs; requires --portal-room")
	portalRoom := flag.String("portal-room", "",
		"portal room to join; the room name is the only credential, so pick an unguessable one")
	syncTarget := flag.String("sync-target", "",
		"rsync destination (host:/path) for the finished session; defaults to the [upload] "+
			"target in the config file, and is empty unless an operator set one. PUSHES THE "+
			"WHOLE SESSION (transcript, events, screenshots) to that host as soon as the "+
			"recording is written, instead of waiting for whatever periodic sync the machine "+
			"runs; --no-sync keeps it local")
	syncKey := flag.String("sync-key", "", "ssh identity for --sync-target")
	noSync := flag.Bool("no-sync", false,
		"keep the finished session on this machine even when a sync target is configured")

	flag.Usage = func() {
		if window {
			fmt.Fprintf(os.Stderr, "recgo-window — record one screen, chosen at start, with narration\n\n")
		} else {
			fmt.Fprintf(os.Stderr, "recgo-desktop — record the screen with narration\n\n")
		}
		fmt.Fprintf(os.Stderr, "Usage: %s [flags]\n\n", tool)
		flag.PrintDefaults()
		if window {
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
		fmt.Fprintf(os.Stderr, "\nLocal models are discovered from, in order:\n")
		for _, d := range tab.ModelSearchPaths() {
			fmt.Fprintf(os.Stderr, "  %s\n", d)
		}
		fmt.Fprintf(os.Stderr, "\nRemote endpoints come from --stt-url / --title-url, else from\n"+
			"[transcription.remote] in %s.\n", config.ConfigPath())
	}
	flag.Parse()

	if listScreens != nil && *listScreens {
		printScreens(os.Stdout, screencast.DisplayList())
		return
	}

	o := opts{
		tool: tool, window: window,
		out: *out, duration: *duration, noAudio: *noAudio, noVideo: *noVideo, micDevice: *micDevice,
		systemAudio: *systemAudio, requireSystemAudio: *requireSystemAudio,
		ffmpegBin: *ffmpegBin, gstLaunch: *gstLaunch, sttBackend: *sttBackend,
		whisperBin: *whisperBin, whisperModel: *whisperModel, whisperVAD: *whisperVAD,
		sttURL: *sttURL, sttModel: *sttModel, sttKey: *sttKey, sttLanguage: *sttLanguage,
		noVAD: *noVAD, keepRaw: *keepRaw, tokenPath: *tokenPath, json: *jsonOut,
		clickShots: *clickShots, focusShots: *focusShots, live: *live,
		titleBackend: *titleBackend, titleURL: *titleURL, titleModel: *titleModel,
		portal: *portal, portalRoom: *portalRoom,
		syncTarget: *syncTarget, syncKey: *syncKey, noSync: *noSync,
	}
	if screen != nil {
		o.screen = *screen
	}

	if cfg, err := config.Load(); err == nil {
		o.cfgURL = cfg.Transcription.Remote.Endpoint
		o.cfgModel = cfg.Transcription.Remote.Model
		o.cfgKey = cfg.Transcription.Remote.APIKey
		if o.syncTarget == "" && cfg.Upload.Enabled {
			o.syncTarget = cfg.Upload.Target
			o.syncKey = cfg.Upload.SSHKey
		}
	}

	if err := run(o); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", tool, err)
		os.Exit(1)
	}
}

type opts struct {
	tool, screen                         string
	window                               bool
	out, micDevice, ffmpegBin, gstLaunch string
	systemAudio                          string
	requireSystemAudio                   bool
	sttBackend, whisperBin               string
	liveRealtime                         bool
	whisperModel, whisperVAD, tokenPath  string
	sttURL, sttModel, sttKey             string
	sttLanguage                          string
	titleBackend, titleURL, titleModel   string
	cfgURL, cfgModel, cfgKey             string
	portal, portalRoom                   string
	syncTarget, syncKey                  string
	noAudio, noVideo, noVAD, json, live  bool
	keepRaw, clickShots, noSync          bool
	focusShots                           bool
	duration                             time.Duration
}

func (o opts) liveFeed() *transcribe.Session {
	if !o.live || o.noAudio {
		return nil
	}
	switch o.sttBackend {
	case "remote":
		eps := o.sttEndpoints()
		if len(eps) == 0 {
			fmt.Fprintln(os.Stderr, "live: narration disabled -- no remote STT endpoint configured "+
				"(set --stt-url, or [transcription.remote] endpoint in the recgo config)")
			return nil
		}
		key := tab.APIKey(o.sttKey, o.cfgKey)
		fmt.Fprintf(os.Stderr, "live: streaming narration to %s as it is spoken -- the final "+
			"transcript is still redone from the full recording (--live=false to disable)\n", eps[0].URL)
		if o.liveRealtime {
			fmt.Fprintf(os.Stderr, "live: streaming narration to %s over its realtime websocket as it is spoken "+
				"-- the final transcript is still redone from the full recording (--live=false to disable)\n", eps[0].URL)
			return tab.RealtimeLiveFeed(context.Background(), eps[0], key, o.sttModel)
		}
		return tab.RemoteLiveFeed(context.Background(), eps[0], key, o.sttModel)
	case "local":
		feed, reason := tab.LocalLiveFeed(context.Background(), o.whisperBin, o.whisperModel, o.whisperVAD)
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

func (o opts) sttEndpoints() []tab.Endpoint {
	return tab.Endpoints(o.sttURL, o.sttModel, o.cfgURL, o.cfgModel)
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
	if o.requireSystemAudio && (o.noAudio || o.systemAudio == "") {
		return fmt.Errorf("system audio required: provide -system-audio and enable audio capture")
	}
	if o.noVideo {
		if o.noAudio {
			return fmt.Errorf("--no-video with --no-audio leaves nothing to record")
		}
		o.clickShots, o.focusShots = false, false
	}
	if o.sttBackend == "realtime" {
		o.liveRealtime = true
		o.sttBackend = "remote"
	}
	if o.sttBackend == "auto" {
		if m, _ := tab.DiscoverWhisperModel(); m != "" || o.whisperModel != "" {
			o.sttBackend = "local"
		} else {
			o.sttBackend = "remote"
			if eps := o.sttEndpoints(); len(eps) > 0 {
				fmt.Fprintf(os.Stderr, "stt: no local whisper model found — audio will be uploaded to %s\n"+
					"     (pass --stt-backend local or none to keep everything on this machine)\n",
					eps[0].URL)
			}
		}
	}

	started := time.Now()
	root := o.out
	if root == "" {
		root = tab.DefaultOutRoot()
	}
	stamp := started.Format("2006-01-02-15-04")
	provisional := filepath.Join(root, fmt.Sprintf("%s-recording-%d", stamp, os.Getpid()))
	if err := os.MkdirAll(provisional, 0o700); err != nil {
		return err
	}

	if o.portal != "" {
		if o.portalRoom == "" {
			return fmt.Errorf("--portal requires --portal-room (the room name is the credential)")
		}
		bridge, err := tab.StartPortalBridge(o.portal, o.portalRoom, root,
			func(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...) })
		if err != nil {
			return err
		}
		defer bridge.Close()
	}

	clock := tab.NewClock()
	rec := tab.NewRecording(clock, provisional)

	var desk *screencast.Desktop
	var err error
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
		backend = desk.Backend()
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
		backend = desk.Backend()
	}

	feed := o.liveFeed()
	var tapFn func([]byte)
	if feed != nil {
		tapFn = feed.Feed
	}

	var mic *tab.Mic
	if !o.noAudio {
		monitor, err := tab.ResolveMonitor(o.systemAudio)
		if err != nil && o.requireSystemAudio {
			return fmt.Errorf("system audio required: %w", err)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "system audio: %v -- recording the microphone alone\n", err)
		}
		mic, err = tab.StartMic(tab.MicOptions{
			Clock: clock, OutDir: provisional, Device: o.micDevice, Monitor: monitor,
			FFmpegBin: o.ffmpegBin, Tap: tapFn,
		})
		if err != nil {
			if o.noVideo || o.requireSystemAudio {
				return fmt.Errorf("audio: %w (nothing else to record)", err)
			}
			fmt.Fprintf(os.Stderr, "audio: %v\n", err)
			mic = nil
			feed = nil
		} else if mic.Monitor() != "" {
			fmt.Fprintf(os.Stderr, "audio: system audio mixed in from %s\n", mic.Monitor())
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
	if o.duration > 0 {
		fmt.Fprintf(os.Stderr, "stopping after %s\n", o.duration)
	} else {
		fmt.Fprintln(os.Stderr, "press Ctrl-C to stop")
	}

	var lv *tab.LiveView
	if o.live {
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

	waitForStop(o.duration)
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
	wavPath, audioNote := "", ""
	if mic != nil {
		res := mic.Stop()
		audioNote = res.Note
		if res.Err != nil {
			fmt.Fprintf(os.Stderr, "audio: %v\n", res.Err)
		} else {
			wavPath = res.WavPath
			if !o.keepRaw {
				os.Remove(res.RawPath)
			}
		}
	}

	if wavPath != "" && o.sttBackend != "none" {
		fmt.Fprintln(os.Stderr, "transcribing audio...")
	}
	transcript := transcribeWav(o, clock, wavPath)
	if transcript != nil && !transcript.OK {
		fmt.Fprintf(os.Stderr, "transcription: %s\n", transcript.Reason)
	}

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
	if o.titleBackend == "remote" || (o.titleBackend == "" && o.sttBackend == "remote") {
		fmt.Fprintln(os.Stderr, "naming the session...")
	}
	named := titleFor(o, transcript, events)
	if named.Note != "" {
		fmt.Fprintf(os.Stderr, "title: %s\n", named.Note)
	}
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
		DurationMs:  durationMs, Cwd: cwd, TargetURL: backend,
		Tool: tool, AudioNote: audioNote, TitleNote: titleNote, SystemAudio: systemAudioOf(mic),
		System: systemInfo(),
	}, tab.PackOptions{JSON: o.json})
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "\nwrote %s/SESSION.md\n", finalDir)
	fmt.Fprintf(os.Stderr, "  %d clicks, %d marks, %d utterances\n",
		session.Counts.Clicks, marks, session.Counts.Utterances)
	syncSession(o, finalDir)
	return nil
}

// The host may also run a periodic sync over the output root, but the person
// who just recorded wants to hand the remote path to someone, or to an agent,
// now. A failure is reported and swallowed -- the recording is already safely
// on disk, and the periodic sync is still a backstop.
func syncSession(o opts, dir string) {
	if o.noSync || o.syncTarget == "" {
		return
	}
	fmt.Fprintf(os.Stderr, "  syncing -> %s ...\n", o.syncTarget)
	dest, err := upload.SyncDir(config.UploadConfig{
		Target: o.syncTarget,
		SSHKey: o.syncKey,
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

func titleFor(o opts, transcript *tab.Transcript, events []tab.Event) tab.TitleResult {
	backend := o.titleBackend
	if backend == "" {
		backend = o.sttBackend
	}
	if backend != "remote" {
		return tab.GenerateTitle(tab.TitleOptions{}, transcript, events)
	}

	var eps []tab.Endpoint
	if o.titleURL != "" {
		eps = []tab.Endpoint{{URL: o.titleURL, Model: o.titleModel}}
	} else if o.cfgURL != "" {
		eps = []tab.Endpoint{{URL: o.cfgURL}}
	}
	key := tab.APIKey(o.sttKey, o.cfgKey)
	return tab.GenerateTitle(tab.TitleOptions{
		Enabled: true, Endpoints: eps, Model: o.titleModel, APIKey: key,
	}, transcript, events)
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

func transcribeWav(o opts, clock *tab.Clock, wavPath string) *tab.Transcript {
	switch o.sttBackend {
	case "none":
		return &tab.Transcript{Reason: "transcription disabled (--stt-backend none)"}
	case "local", "remote":
	default:
		return &tab.Transcript{Reason: fmt.Sprintf(
			"unknown --stt-backend %q (want auto, local, remote or none)", o.sttBackend)}
	}
	if wavPath == "" {
		return &tab.Transcript{Reason: "no audio was captured"}
	}

	if o.sttBackend == "local" {
		model, vad := tab.ResolveWhisperModel(o.whisperModel, o.whisperVAD)
		t := tab.TranscribeLocal(clock, wavPath, o.whisperBin, model, vad)
		if t.OK && vad == "" {
			fmt.Fprintf(os.Stderr, "warning: %s\n", t.AccuracyNote)
		}
		return &t
	}

	eps := o.sttEndpoints()
	if len(eps) == 0 {
		return &tab.Transcript{Reason: "no remote STT endpoint configured — set --stt-url, or " +
			"[transcription.remote] endpoint in " + config.ConfigPath()}
	}
	fmt.Fprintf(os.Stderr, "stt: --stt-backend remote — uploading %s to %s\n", wavPath, eps[0].URL)

	key := tab.APIKey(o.sttKey, o.cfgKey)

	t := tab.TranscribeFallback(clock, wavPath, tab.STTOptions{
		Endpoints: eps, Model: o.sttModel, APIKey: key, Language: o.sttLanguage,
	})
	if !t.OK {
		return &t
	}
	if len(t.Attempts) > 0 {
		fmt.Fprintf(os.Stderr, "stt: fell back to %s after %d failure(s)\n", t.Endpoint, len(t.Attempts))
	}
	if !o.noVAD {
		t = tab.VADCorrect(t, wavPath, o.ffmpegBin, clock)
		if !t.VADCorrected {
			fmt.Fprintf(os.Stderr, "warning: VAD correction skipped — %s\n", t.VADReason)
		}
	}
	return &t
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

func waitForStop(d time.Duration) {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if d > 0 {
		select {
		case <-ctx.Done():
		case <-time.After(d):
		}
		return
	}
	<-ctx.Done()
}

func systemAudioOf(mic *tab.Mic) string {
	if mic == nil {
		return ""
	}
	return mic.Monitor()
}
