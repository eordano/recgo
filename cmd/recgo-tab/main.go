package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/eordano/recgo/internal/audio"
	"github.com/eordano/recgo/internal/config"
	"github.com/eordano/recgo/internal/tab"
	"github.com/eordano/recgo/internal/tabui"
	"github.com/eordano/recgo/internal/transcribe"
	"github.com/eordano/recgo/internal/upload"
)

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

type options struct {
	launch       string
	chromium     string
	headless     bool
	port         int
	match        string
	target       string
	systemAudio  string
	out          string
	duration     time.Duration
	noAudio      bool
	micDevice    string
	ffmpegBin    string
	sttBackend   string
	liveRealtime bool
	sttURL       string
	sttModel     string
	sttKey       string
	sttLanguage  string
	noVAD        bool
	keepRaw      bool
	pick         bool
	json         bool
	live         bool

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
	return tab.Endpoints(o.sttURL, o.sttModel, o.cfgURL, o.cfgModel)
}

func main() {
	var o options
	flag.StringVar(&o.launch, "launch", "", "launch chromium on this URL with remote debugging")
	flag.StringVar(&o.chromium, "chromium", "chromium", "chromium binary")
	flag.BoolVar(&o.headless, "headless", false, "launch headless (implies --launch)")
	flag.IntVar(&o.port, "port", 9222, "CDP port")
	flag.StringVar(&o.match, "match", "", "attach to the tab whose URL/title contains this")
	flag.StringVar(&o.target, "target", "", "attach to the tab with this CDP target id (exact; from /json/list)")
	flag.StringVar(&o.out, "out", "", "output root (default: [recording] output_dir in config.toml, else ~/walk-and-talk; ~/Documents/walk-and-talk on macOS)")
	flag.DurationVar(&o.duration, "duration", 0, "stop automatically after this long; otherwise Ctrl-C")
	flag.BoolVar(&o.noAudio, "no-audio", false, "skip microphone capture")
	flag.StringVar(&o.micDevice, "mic", "", "capture device (default: system default source)")
	flag.StringVar(&o.systemAudio, "system-audio", "",
		"mix what you hear into the narration track: a monitor source (<sink>.monitor, or a "+
			"loopback device on macOS), or 'default' for the default output; 'sysaudio off' / "+
			"'sysaudio on' on stdin mutes and unmutes it while recording")
	flag.StringVar(&o.ffmpegBin, "ffmpeg", "ffmpeg", "ffmpeg binary")
	flag.StringVar(&o.sttBackend, "stt-backend", "auto",
		"auto (local if a whisper model is found, else remote) | local (whisper.cpp, nothing leaves this machine) | remote (uploads audio) | realtime (uploads audio; streams narration over the endpoint's /v1/realtime websocket as you speak, final transcript via the batch endpoint) | none")
	flag.StringVar(&o.whisperBin, "whisper-bin", "whisper-cli", "whisper.cpp CLI")
	flag.StringVar(&o.whisperModel, "whisper-model", "", "ggml model (default: discovered, see below)")
	flag.StringVar(&o.whisperVADModel, "whisper-vad-model", "", "silero VAD model (default: discovered)")
	flag.StringVar(&o.sttURL, "stt-url", "", "remote only: override the OpenAI-compatible endpoint list")
	flag.StringVar(&o.sttModel, "stt-model", "", "remote only: override the per-endpoint model")
	flag.StringVar(&o.sttKey, "stt-api-key", "", "remote only: bearer token (falls back to $OPENAI_API_KEY / $LLM_API_KEY)")
	flag.StringVar(&o.sttLanguage, "stt-language", "", "force a language instead of autodetecting")
	flag.BoolVar(&o.noVAD, "no-vad-correct", false, "remote only: skip the local silencedetect pass (NOT ADVISED)")
	flag.BoolVar(&o.keepRaw, "keep-raw", false, "keep audio.pcm alongside audio.wav")
	flag.BoolVar(&o.pick, "select", false, "pick a tab from a list of what the browser has open")
	flag.BoolVar(&o.json, "json", false, "also write session.json (full machine-readable timeline)")
	flag.BoolVar(&o.live, "live", true,
		"build the session document as it is recorded: print each line as it happens and keep "+
			"SESSION.live.md current in the session folder; narration is transcribed while you "+
			"speak (locally with --stt-backend local); with --stt-backend remote this also "+
			"STREAMS NARRATION AUDIO to the STT endpoint while recording (--live=false to disable)")
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
		fmt.Fprintf(os.Stderr, "recgo-tab — record a browser tab with narration\n\n")
		fmt.Fprintf(os.Stderr, "Usage: recgo-tab [flags]\n\n")
		flag.PrintDefaults()
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
		fmt.Fprintf(os.Stderr, "recgo-tab: %v\n", err)
		os.Exit(1)
	}
}

func run(o options) error {
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

	var browser *exec.Cmd
	var profile string
	if o.launch != "" || o.headless {
		var err error
		browser, profile, err = launchChromium(o)
		if err != nil {
			return err
		}
		defer func() { tab.StopChromium(browser, profile) }()
	}

	if o.pick && o.launch == "" {
		chosen, err := pickTab(o.port)
		if err != nil {
			return err
		}
		if chosen == nil {
			return nil
		}
		o.match = chosen.URL
	}

	var cdp *tab.CDP
	var target *tab.Target
	var err error
	if o.target != "" {
		cdp, target, err = tab.AttachTarget(o.port, o.target)
	} else {
		cdp, target, err = tab.Attach(o.port, o.match)
	}
	if err != nil {
		return err
	}
	defer cdp.Close()
	fmt.Fprintf(os.Stderr, "attached to: %s\n", tab.FirstNonEmpty(target.Title, target.URL))
	fmt.Fprintf(os.Stderr, "tab: %s %s\n", target.ID, target.URL)

	monitor, err := tab.ResolveMonitor(o.systemAudio)
	if err != nil {
		fmt.Fprintf(os.Stderr, "system audio: %v -- recording the microphone alone\n", err)
		monitor = ""
	}

	clock := tab.NewClock()
	rec := tab.NewRecorder(cdp, clock, provisional)

	var feed *transcribe.Session
	var tapFn func([]byte)
	if o.live && !o.noAudio {
		switch o.sttBackend {
		case "remote":
			if eps := o.sttEndpoints(); len(eps) > 0 {
				key := tab.APIKey(o.sttKey, o.cfgKey)
				if o.liveRealtime {
					feed = tab.RealtimeLiveFeed(context.Background(), eps[0], key, o.sttModel)
					fmt.Fprintf(os.Stderr, "live: streaming narration to %s over its realtime websocket as it is spoken "+
						"-- the final transcript is still redone from the full recording (--live=false to disable)\n", eps[0].URL)
				} else {
					feed = tab.RemoteLiveFeed(context.Background(), eps[0], key, o.sttModel)
				}
				fmt.Fprintf(os.Stderr, "live: streaming narration to %s as it is spoken -- the final "+
					"transcript is still redone from the full recording (--live=false to disable)\n", eps[0].URL)
			} else {
				fmt.Fprintln(os.Stderr, "live: narration disabled -- no remote STT endpoint configured "+
					"(set --stt-url, or [transcription.remote] endpoint in the recgo config)")
			}
		case "local":
			var reason string
			feed, reason = tab.LocalLiveFeed(context.Background(), o.whisperBin, o.whisperModel, o.whisperVADModel)
			if feed != nil {
				fmt.Fprintf(os.Stderr, "live: transcribing narration locally as it is spoken -- the final "+
					"transcript is still redone from the full recording (--live=false to disable)\n")
			} else {
				fmt.Fprintf(os.Stderr, "live: narration disabled -- %s\n", reason)
			}
		}
		if feed != nil {
			tapFn = feed.Feed
		}
	}

	var mic *tab.Mic
	if !o.noAudio {
		mic, err = tab.StartMic(tab.MicOptions{
			Clock:     clock,
			OutDir:    provisional,
			Device:    o.micDevice,
			Monitor:   monitor,
			FFmpegBin: o.ffmpegBin,
			Tap:       tapFn,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "audio: %v\n", err)
			mic = nil
			feed = nil
		} else if mic.Monitor() != "" {
			fmt.Fprintf(os.Stderr, "audio: system audio mixed in from %s\n", mic.Monitor())
		}
	}

	offset, errMs, err := rec.Start()
	if err != nil {
		return err
	}

	if browser != nil && o.launch != "" {
		if err := cdp.Send("Page.navigate", map[string]any{"url": o.launch}, nil); err != nil {
			return err
		}
	}

	fmt.Fprintf(os.Stderr, "recording — browser clock offset %.1fms ±%.1fms\n", offset, errMs)
	if o.duration > 0 {
		fmt.Fprintf(os.Stderr, "stopping after %s\n", o.duration)
	} else {
		fmt.Fprintf(os.Stderr, "press Ctrl-C to stop\n")
	}

	if mic != nil {
		if err := mic.EmitCalibrationTone(cdp, clock); err != nil {
			fmt.Fprintf(os.Stderr, "calibration tone: %v\n", err)
		}
	}

	var lv *tab.LiveView
	if o.live {
		header := fmt.Sprintf("# Session (recording — replaced by SESSION.md at stop)\n\n"+
			"Start: %s\nPage: %s\n\n", started.Format("2006-01-02 15:04:05"),
			tab.FirstNonEmpty(o.launch, target.URL))
		lv = tab.StartLiveView(rec.Recording(), os.Stderr, header)
		if feed != nil {
			if err := feed.StartFeed(); err != nil {
				fmt.Fprintf(os.Stderr, "live narration: %v\n", err)
				feed = nil
			} else {
				tab.ConsumeLiveWith(feed, clock, lv.Utterance, func(text string) {
					fmt.Fprintf(os.Stderr, "%s  hearing: %s\n", tab.FormatClock(clock.Now()), text)
				}, func(err error) {
					fmt.Fprintf(os.Stderr, "live narration: %v\n", err)
				})
			}
		}
	}

	go tab.ReadCommands(os.Stdin, mic, func() float64 { return rec.Mark("") }, rec.Note)
	waitForStop(o.duration)

	fmt.Fprintln(os.Stderr, "stopping...")
	events := rec.Stop()
	if feed != nil {
		feed.Stop()
	}
	if lv != nil {
		lv.Stop()
	}
	durationMs := clock.Now()

	audioNote := ""
	wavPath := ""
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
	finalDir, err := tab.FinalizeDir(provisional, filepath.Join(root, fmt.Sprintf("%s-%s", stamp, named.Slug)))
	if err != nil {
		return err
	}

	cwd, _ := os.Getwd()
	session, err := tab.Pack(finalDir, events, clock, transcript, tab.Meta{
		Slug:        named.Slug,
		Title:       named.Title,
		StartedISO:  started.UTC().Format(time.RFC3339),
		StartedWall: started.Format("2006-01-02 15:04:05"),
		DurationMs:  durationMs,
		Cwd:         cwd,
		TargetURL:   tab.FirstNonEmpty(o.launch, target.URL),
		TargetTitle: target.Title,
		Tool:        "recgo-tab",
		AudioNote:   audioNote,
		SystemAudio: systemAudioOf(mic),
		TitleNote:   named.Note,
		System:      tab.CollectSystemInfo(),
	}, tab.PackOptions{JSON: o.json})
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "\nwrote %s/SESSION.md\n", finalDir)
	fmt.Fprintf(os.Stderr, "  %d clicks, %d HMR, %d errors, %d utterances\n",
		session.Counts.Clicks, session.Counts.HMR, session.Counts.Errors, session.Counts.Utterances)
	syncSession(o, finalDir)
	return nil
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
		if local := upload.LocalMirror(dest); local != "" {
			fmt.Fprintf(os.Stderr, "  shared -> %s\n", local)
		}
	}
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
	key := tab.APIKey(o.sttKey, o.cfgKey)
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

func systemAudioOf(mic *tab.Mic) string {
	if mic == nil {
		return ""
	}
	return mic.Monitor()
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

func launchChromium(o options) (*exec.Cmd, string, error) {
	return tab.LaunchChromium(tab.LaunchOptions{
		Chromium: o.chromium, Port: o.port, Headless: o.headless,
		Profile: filepath.Join(os.TempDir(), fmt.Sprintf("recgo-tab-profile-%d", os.Getpid())),
	})
}

var _ = audio.CheckBackend
