package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/eordano/recgo/internal/config"
	"github.com/eordano/recgo/internal/tab"
	"github.com/eordano/recgo/internal/tabui"
	"github.com/eordano/recgo/internal/transcribe"
	"github.com/eordano/recgo/internal/upload"
)

type options struct {
	launch      string
	chromium    string
	headless    bool
	port        int
	match       string
	out         string
	duration    time.Duration
	noAudio     bool
	micDevice   string
	ffmpegBin   string
	sttBackend  string
	sttURL      string
	sttModel    string
	sttKey      string
	sttLanguage string
	noVAD       bool
	keepRaw     bool
	pick        bool
	json        bool
	live        bool
	plain       bool

	titleBackend string
	titleURL     string
	titleModel   string

	whisperBin      string
	whisperModel    string
	whisperVADModel string

	cfgURL   string
	cfgModel string
	cfgKey   string

	syncTarget string
	syncKey    string
	noSync     bool

	portal     string
	portalRoom string
}

func (o options) sttEndpoints() []tab.Endpoint {
	if o.sttURL != "" {
		return []tab.Endpoint{{URL: o.sttURL, Model: firstNonEmpty(o.sttModel, "whisper")}}
	}
	if o.cfgURL != "" {
		return []tab.Endpoint{{URL: o.cfgURL, Model: firstNonEmpty(o.cfgModel, "whisper")}}
	}
	return tab.DefaultEndpoints
}

// single reports whether the recording is pinned to one tab instead of
// following the browser.
func (o options) single() bool { return o.match != "" || o.pick }

func main() {
	var o options
	flag.StringVar(&o.launch, "launch", "", "launch chromium on this URL with remote debugging")
	flag.StringVar(&o.chromium, "chromium", "chromium", "chromium binary")
	flag.BoolVar(&o.headless, "headless", false, "launch headless (implies --launch)")
	flag.IntVar(&o.port, "port", 9222, "CDP port of a running browser")
	flag.StringVar(&o.match, "match", "",
		"record only the tab whose URL/title contains this, instead of following the browser; "+
			"with --launch there is only the launched tab, so any value pins to it")
	flag.StringVar(&o.out, "out", "", "output root (default $XDG_DOCUMENTS_DIR/walk-and-talk)")
	flag.DurationVar(&o.duration, "duration", 0, "stop automatically after this long; otherwise Ctrl-C")
	flag.BoolVar(&o.noAudio, "no-audio", false, "skip microphone capture")
	flag.StringVar(&o.micDevice, "mic", "", "capture device (default: system default source)")
	flag.StringVar(&o.ffmpegBin, "ffmpeg", "ffmpeg", "ffmpeg binary")
	flag.StringVar(&o.sttBackend, "stt-backend", "auto",
		"auto (local if a whisper model is found, else remote) | local (whisper.cpp, nothing leaves this machine) | remote (uploads audio) | none")
	flag.StringVar(&o.whisperBin, "whisper-bin", "whisper-cli", "whisper.cpp CLI")
	flag.StringVar(&o.whisperModel, "whisper-model", "", "ggml model (default: discovered, see below)")
	flag.StringVar(&o.whisperVADModel, "whisper-vad-model", "", "silero VAD model (default: discovered)")
	flag.StringVar(&o.sttURL, "stt-url", "", "remote only: override the OpenAI-compatible endpoint list")
	flag.StringVar(&o.sttModel, "stt-model", "", "remote only: override the per-endpoint model")
	flag.StringVar(&o.sttKey, "stt-api-key", "", "remote only: bearer token (falls back to $OPENAI_API_KEY / $LLM_API_KEY)")
	flag.StringVar(&o.sttLanguage, "stt-language", "", "force a language instead of autodetecting")
	flag.BoolVar(&o.noVAD, "no-vad-correct", false, "remote only: skip the local silencedetect pass (NOT ADVISED)")
	flag.BoolVar(&o.keepRaw, "keep-raw", false, "keep audio.pcm alongside audio.wav")
	flag.BoolVar(&o.pick, "select", false,
		"pick one tab from a list of what the browser has open and record only that one")
	flag.BoolVar(&o.json, "json", false, "also write session.json (full machine-readable timeline)")
	flag.BoolVar(&o.plain, "plain", false, "no TUI; stream the session document to stderr and stop on Ctrl-C")
	flag.BoolVar(&o.live, "live", true,
		"build the session document as it is recorded: keep SESSION.live.md current in the "+
			"session folder (and with --plain print each line as it happens); narration is "+
			"transcribed while you speak (locally with --stt-backend local); with --stt-backend "+
			"remote this also STREAMS NARRATION AUDIO to the STT endpoint while recording "+
			"(--live=false to disable)")
	flag.StringVar(&o.titleBackend, "title-backend", "",
		"name the session with an LLM: remote (UPLOADS THE TRANSCRIPT) | none (default: follow --stt-backend)")
	flag.StringVar(&o.titleURL, "title-url", "", "override the OpenAI-compatible endpoint list used for the title")
	flag.StringVar(&o.titleModel, "title-model", "", "override the chat model (default: whatever the endpoint serves)")
	flag.StringVar(&o.portal, "portal", "",
		"portal server URL (wss://host or https://host): dial out and serve read-only file "+
			"tools over the output root while recording, so an agent in that portal room can "+
			"follow the live session. EXPOSES THE OUTPUT ROOT (all sessions, read-only) to "+
			"everyone in the room for as long as the recording runs; requires --portal-room")
	flag.StringVar(&o.portalRoom, "portal-room", "",
		"portal room to join; the room name is the only credential, so pick an unguessable one")
	flag.StringVar(&o.syncTarget, "sync-target", "",
		"rsync destination (host:/path) for the finished session; defaults to the [upload] "+
			"target in the config file, and is empty unless an operator set one. PUSHES THE "+
			"WHOLE SESSION (transcript, events, screenshots) to that host as soon as the "+
			"recording is written, instead of waiting for whatever periodic sync the machine "+
			"runs; --no-sync keeps it local")
	flag.StringVar(&o.syncKey, "sync-key", "", "ssh identity for --sync-target")
	flag.BoolVar(&o.noSync, "no-sync", false,
		"keep the finished session on this machine even when a sync target is configured")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "recgo-browser — record a browsing session, following you across tabs\n\n")
		fmt.Fprintf(os.Stderr, "Usage: recgo-browser [flags]\n\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nEvery open tab is recorded and the timeline marks which one was in front;\n"+
			"tabs opened later are picked up within a second. --match or --select pins the\n"+
			"recording to one tab instead, which is what recgo-tab does.\n")
		fmt.Fprintf(os.Stderr, "\nWithout --launch, start your browser with --remote-debugging-port=<port> first.\n")
		fmt.Fprintf(os.Stderr, "\nThe session folder is written outside any archive-sync path. With a local\n"+
			"whisper model nothing leaves this machine; without one, --stt-backend auto\n"+
			"(the default) UPLOADS THE AUDIO to the configured remote endpoint. Pass\n"+
			"--stt-backend local or none to guarantee nothing is uploaded.\n")
		fmt.Fprintf(os.Stderr, "\nLocal models are discovered from, in order:\n")
		for _, d := range tab.ModelSearchPaths() {
			fmt.Fprintf(os.Stderr, "  %s\n", d)
		}
		fmt.Fprintf(os.Stderr, "\nRemote endpoints come from --stt-url / --title-url, else from\n"+
			"[transcription.remote] in %s.\n", config.ConfigPath())
	}
	flag.Parse()

	chromiumSet := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "chromium" {
			chromiumSet = true
		}
	})
	if !chromiumSet {
		o.chromium = tab.DefaultChromium()
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
		fmt.Fprintf(os.Stderr, "recgo-browser: %v\n", err)
		os.Exit(1)
	}
}

func pickTab(port int) (*tab.TabInfo, error) {
	model := tabui.NewPicker(port)
	if _, err := tea.NewProgram(model).Run(); err != nil {
		return nil, err
	}
	if model.Cancelled || model.Chosen == nil {
		return nil, nil
	}
	return model.Chosen, nil
}

func run(o options) error {
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

	var browser *exec.Cmd
	if o.launch != "" || o.headless {
		var profile string
		var err error
		browser, profile, err = tab.LaunchChromium(tab.LaunchOptions{
			Chromium: o.chromium, Port: o.port, Headless: o.headless,
			Profile: filepath.Join(os.TempDir(), fmt.Sprintf("recgo-browser-profile-%d", os.Getpid())),
		})
		if err != nil {
			return err
		}
		defer func() { tab.StopChromium(browser, profile) }()
	}

	clock := tab.NewClock()
	rec := tab.NewRecording(clock, provisional)
	follower := tab.NewFollower(o.port, rec)

	// A pinned recording is recgo-tab's shape: one tab for the whole session,
	// no watcher, so a popup the click opens is deliberately not followed.
	pinnedTitle, pinnedURL := "", ""
	if o.single() {
		chosen, err := pinTarget(o, browser != nil)
		if err != nil {
			return err
		}
		if chosen == nil {
			return nil
		}
		follower.Pin = chosen.ID
		pinnedTitle, pinnedURL = chosen.Title, chosen.URL
	}

	feed, tapFn := o.liveFeed()

	var mic *tab.Mic
	if !o.noAudio {
		var err error
		mic, err = tab.StartMic(tab.MicOptions{
			Clock: clock, OutDir: provisional, Device: o.micDevice, FFmpegBin: o.ffmpegBin,
			Tap: tapFn,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "audio: %v\n", err)
			mic = nil
			feed = nil
		}
	}

	if err := follower.Start(); err != nil {
		return err
	}
	if follower.Pin != "" {
		// With --launch the pinned tab is still on about:blank at this
		// point; naming it that would be true and useless.
		fmt.Fprintf(os.Stderr, "recording one tab: %s\n",
			firstNonEmpty(o.launch, pinnedTitle, pinnedURL))
	} else {
		fmt.Fprintf(os.Stderr, "following %d tab(s) on port %d — new tabs are picked up as they open\n",
			follower.Count(), o.port)
	}

	cdp := follower.ActiveCDP()
	if browser != nil && o.launch != "" && cdp != nil {
		if err := cdp.Send("Page.navigate", map[string]any{"url": o.launch}, nil); err != nil {
			return err
		}
	}
	if mic != nil && cdp != nil {
		if err := mic.EmitCalibrationTone(cdp, clock); err != nil {
			fmt.Fprintf(os.Stderr, "calibration tone: %v\n", err)
		}
	}

	// The live document is written whichever front end is running; only
	// --plain also streams it to stderr, because the TUI owns the terminal.
	// Both it and the narration feed exist only under --live.
	var lv *tab.LiveView
	if o.live {
		page := firstNonEmpty(o.launch, pinnedURL)
		if page == "" {
			page = fmt.Sprintf("(%d tab(s) on port %d)", follower.Count(), o.port)
		}
		header := fmt.Sprintf("# Session (recording — replaced by SESSION.md at stop)\n\n"+
			"Start: %s\nPage: %s\n\n", started.Format("2006-01-02 15:04:05"), page)
		var out io.Writer = io.Discard
		if o.plain {
			out = os.Stderr
		}
		lv = tab.StartLiveView(rec, out, header)
		if feed != nil {
			if err := feed.StartFeed(); err != nil {
				fmt.Fprintf(os.Stderr, "live narration: %v\n", err)
				feed = nil
			}
		}
	}

	if o.plain {
		go readMarks(follower)
		runPlain(follower, feed, lv, clock, o.duration)
	} else if err := runTUI(follower, rec, feed, lv, clock, o.port); err != nil {
		return err
	}

	fmt.Fprintln(os.Stderr, "stopping...")
	events := follower.Stop()
	if feed != nil {
		feed.Stop()
	}
	if lv != nil {
		lv.Stop()
	}
	durationMs := clock.Now()

	wavPath := ""
	audioNote := ""
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

	if o.titleBackend == "remote" || (o.titleBackend == "" && o.sttBackend == "remote") {
		fmt.Fprintln(os.Stderr, "naming the session...")
	}
	named := titleFor(o, transcript, events)
	if named.Note != "" {
		fmt.Fprintf(os.Stderr, "title: %s\n", named.Note)
	}
	finalDir := filepath.Join(root, fmt.Sprintf("%s-%s", stamp, named.Slug))
	os.RemoveAll(finalDir)
	if err := os.Rename(provisional, finalDir); err != nil {
		return err
	}

	targetURL := firstNonEmpty(o.launch, pinnedURL)
	if targetURL == "" {
		targetURL = fmt.Sprintf("(%d tabs on port %d)", countTabs(events), o.port)
	}

	cwd, _ := os.Getwd()
	session, err := tab.Pack(finalDir, events, clock, transcript, tab.Meta{
		Slug:        named.Slug,
		Title:       named.Title,
		StartedISO:  started.UTC().Format(time.RFC3339),
		StartedWall: started.Format("2006-01-02 15:04:05"),
		DurationMs:  durationMs,
		Cwd:         cwd,
		TargetURL:   targetURL,
		TargetTitle: pinnedTitle,
		Tool:        "recgo-browser",
		AudioNote:   audioNote,
		TitleNote:   named.Note,
		System:      tab.CollectSystemInfo(),
	}, tab.PackOptions{JSON: o.json})
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "\nwrote %s/SESSION.md\n", finalDir)
	fmt.Fprintf(os.Stderr, "  %d clicks, %d HMR, %d errors, %d utterances across %d tab(s)\n",
		session.Counts.Clicks, session.Counts.HMR, session.Counts.Errors,
		session.Counts.Utterances, countTabs(events))
	syncSession(o, finalDir)
	return nil
}

// pinTarget resolves --select / --match to the one tab the recording is
// confined to. A browser we launched ourselves has exactly one tab, sitting on
// about:blank until we navigate it, so neither the match string nor a picker
// has anything to choose between: pinning means that tab.
func pinTarget(o options, launched bool) (*tab.TabInfo, error) {
	if launched {
		t, err := tab.MatchTab(o.port, "")
		if err != nil {
			return nil, err
		}
		return &t, nil
	}
	if o.pick {
		return pickTab(o.port)
	}
	t, err := tab.MatchTab(o.port, o.match)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// liveFeed picks the narration preview backend. It follows --stt-backend, so
// the preview never sends audio anywhere the final transcription would not.
func (o options) liveFeed() (*transcribe.Session, func([]byte)) {
	if !o.live || o.noAudio {
		return nil, nil
	}
	var feed *transcribe.Session
	switch o.sttBackend {
	case "remote":
		eps := o.sttEndpoints()
		if len(eps) == 0 {
			fmt.Fprintln(os.Stderr, "live: narration disabled -- no remote STT endpoint configured "+
				"(set --stt-url, or [transcription.remote] endpoint in the recgo config)")
			return nil, nil
		}
		key := firstNonEmpty(o.sttKey, os.Getenv("OPENAI_API_KEY"), os.Getenv("LLM_API_KEY"), o.cfgKey)
		feed = tab.RemoteLiveFeed(context.Background(), eps[0], key, o.sttModel)
		fmt.Fprintf(os.Stderr, "live: streaming narration to %s as it is spoken -- the final "+
			"transcript is still redone from the full recording (--live=false to disable)\n", eps[0].URL)
	case "local":
		var reason string
		feed, reason = tab.LocalLiveFeed(context.Background(), o.whisperBin, o.whisperModel, o.whisperVADModel)
		if feed == nil {
			fmt.Fprintf(os.Stderr, "live: narration disabled -- %s\n", reason)
			return nil, nil
		}
		fmt.Fprintf(os.Stderr, "live: transcribing narration locally as it is spoken -- the final "+
			"transcript is still redone from the full recording (--live=false to disable)\n")
	default:
		return nil, nil
	}
	return feed, feed.Feed
}

// The host may also run a periodic sync over the output root, but "it will be
// there within five minutes" is not something the person who just recorded can
// act on: they want to hand the remote path to someone, or to an agent, now.
// So the session is pushed here and the destination is printed next to the
// local one. A failure is reported and swallowed -- the recording is already
// safely on disk, and the periodic sync is still a backstop, so this is not
// worth failing the whole run over.
func syncSession(o options, dir string) {
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
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func titleFor(o options, transcript *tab.Transcript, events []tab.Event) tab.TitleResult {
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
	key := firstNonEmpty(o.sttKey, os.Getenv("OPENAI_API_KEY"), os.Getenv("LLM_API_KEY"), o.cfgKey)
	return tab.GenerateTitle(tab.TitleOptions{
		Enabled: true, Endpoints: eps, Model: o.titleModel, APIKey: key,
	}, transcript, events)
}

func transcribeWav(o options, clock *tab.Clock, wavPath string) *tab.Transcript {
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
		model, vad := tab.ResolveWhisperModel(o.whisperModel, o.whisperVADModel)
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

	key := firstNonEmpty(o.sttKey, os.Getenv("OPENAI_API_KEY"), os.Getenv("LLM_API_KEY"), o.cfgKey)

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

func countTabs(events []tab.Event) int {
	seen := map[string]bool{}
	for _, e := range events {
		if e.TargetID != "" {
			seen[e.TargetID] = true
		}
	}
	return len(seen)
}

func runTUI(f *tab.Follower, rec *tab.Recording, feed *transcribe.Session, lv *tab.LiveView,
	clock *tab.Clock, port int) error {
	model := tabui.NewFollow(port, f, rec)
	p := tea.NewProgram(model)
	f.OnUpdate = func() { p.Send(tabui.Refresh()) }
	rec.OnChange(func() { p.Send(tabui.Refresh()) })
	if feed != nil {
		tab.ConsumeLive(feed, clock, func(t float64, text string) {
			if lv != nil {
				lv.Utterance(t, text)
			}
			p.Send(tabui.Narration{T: t, Text: text})
		}, func(err error) {
			p.Send(tabui.Narration{T: clock.Now(), Text: fmt.Sprintf("error: %v", err)})
		})
	}
	_, err := p.Run()
	return err
}

func runPlain(f *tab.Follower, feed *transcribe.Session, lv *tab.LiveView,
	clock *tab.Clock, d time.Duration) {
	fmt.Fprintln(os.Stderr, "press Ctrl-C to stop; type 'mark' to stamp the timeline")
	if feed != nil {
		// lv is non-nil whenever feed is, and under --plain it is the thing
		// printing to stderr, so narration goes through it rather than being
		// printed twice in two shapes.
		tab.ConsumeLive(feed, clock, lv.Utterance, func(err error) {
			fmt.Fprintf(os.Stderr, "live narration: %v\n", err)
		})
	}
	waitForStop(d)
}

func readMarks(f *tab.Follower) {
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line == "mark" || line == "m" {
			fmt.Fprintf(os.Stderr, "marked at %.1fs\n", f.Mark("")/1000)
		}
	}
}
