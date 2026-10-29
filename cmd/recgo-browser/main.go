package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/eordano/recgo/internal/config"
	"github.com/eordano/recgo/internal/sessioncli"
	"github.com/eordano/recgo/internal/tab"
	"github.com/eordano/recgo/internal/tabui"
	"github.com/eordano/recgo/internal/transcribe"
	"github.com/eordano/recgo/internal/upload"
)

type options struct {
	sessioncli.Options
	launch, chromium, match string
	headless, pick, plain   bool
	port                    int
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
	flag.BoolVar(&o.pick, "select", false,
		"pick one tab from a list of what the browser has open and record only that one")
	flag.BoolVar(&o.plain, "plain", false, "no TUI; stream the session document to stderr and stop on Ctrl-C")
	o.Register("build the session document as it is recorded: keep SESSION.live.md current in the " +
		"session folder (and with --plain print each line as it happens); narration is " +
		"transcribed while you speak (locally with --stt-backend local); with --stt-backend " +
		"remote this also STREAMS NARRATION AUDIO to the STT endpoint while recording " +
		"(--live=false to disable)")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "recgo-browser — record a browsing session, following you across tabs\n\n")
		fmt.Fprintf(os.Stderr, "Usage: recgo-browser [flags]\n\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nEvery open tab is recorded and the timeline marks which one was in front;\n"+
			"tabs opened later are picked up within a second. --match or --select pins the\n"+
			"recording to one tab instead, which is what recgo-tab does.\n")
		fmt.Fprintf(os.Stderr, "\nWithout --launch, start your browser with --remote-debugging-port=<port> first.\n")
		fmt.Fprint(os.Stderr, sessioncli.UploadNote)
		sessioncli.UsageTail()
	}
	flag.Parse()
	sessioncli.ResolveChromium(&o.chromium)
	o.Load()

	if err := run(o); err != nil {
		fmt.Fprintf(os.Stderr, "recgo-browser: %v\n", err)
		os.Exit(1)
	}
}

func run(o options) error {
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

	var browser *exec.Cmd
	if o.launch != "" || o.headless {
		var profile string
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
	if !o.NoAudio {
		monitor, err := tab.ResolveMonitor(o.SystemAudio)
		if err != nil {
			fmt.Fprintf(os.Stderr, "system audio: %v -- recording the microphone alone\n", err)
		}
		if mic, err = o.StartMic(clock, provisional, monitor, tapFn); err != nil {
			fmt.Fprintf(os.Stderr, "audio: %v\n", err)
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
			tab.FirstNonEmpty(o.launch, pinnedTitle, pinnedURL))
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
	if o.Live {
		page := tab.FirstNonEmpty(o.launch, pinnedURL)
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
		go tab.ReadCommands(os.Stdin, mic, func() float64 { return follower.Mark("") }, follower.Note)
		runPlain(follower, feed, lv, clock, o.Duration)
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

	wavPath, audioNote := o.StopMic(mic)
	transcript := o.Transcribe(clock, wavPath, func() string { return tab.Vocabulary(events, pinnedTitle) })
	named := o.Title(transcript, events)
	finalDir, err := tab.FinalizeDir(provisional, filepath.Join(root, fmt.Sprintf("%s-%s", stamp, named.Slug)))
	if err != nil {
		return err
	}

	targetURL := tab.FirstNonEmpty(o.launch, pinnedURL)
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
		SystemAudio: sessioncli.SystemAudio(mic),
		TitleNote:   named.Note,
		System:      tab.CollectSystemInfo(),
	}, tab.PackOptions{JSON: o.JSON})
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "\nwrote %s\n", filepath.Join(finalDir, "SESSION.md"))
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
		return tabui.Pick(o.port)
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
	if !o.Live || o.NoAudio {
		return nil, nil
	}
	var feed *transcribe.Session
	switch o.STTBackend {
	case "remote":
		eps := o.Endpoints()
		if len(eps) == 0 {
			fmt.Fprintln(os.Stderr, "live: narration disabled -- no remote STT endpoint configured "+
				"(set --stt-url, or [transcription.remote] endpoint in the recgo config)")
			return nil, nil
		}
		key := tab.APIKey(o.STTKey, o.CfgKey)
		if o.LiveRealtime {
			feed = tab.RealtimeLiveFeed(context.Background(), eps[0], key, o.STTModel)
			fmt.Fprintf(os.Stderr, "live: streaming narration to %s over its realtime websocket as it is spoken "+
				"-- the final transcript is still redone from the full recording (--live=false to disable)\n", eps[0].URL)
		} else {
			feed = tab.RemoteLiveFeed(context.Background(), eps[0], key, o.STTModel)
		}
		fmt.Fprintf(os.Stderr, "live: streaming narration to %s as it is spoken -- the final "+
			"transcript is still redone from the full recording (--live=false to disable)\n", eps[0].URL)
	case "local":
		var reason string
		feed, reason = tab.LocalLiveFeed(context.Background(), o.WhisperBin, o.WhisperModel, o.WhisperVAD)
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
		tab.ConsumeLiveWith(feed, clock, lv.Utterance, func(text string) {
			fmt.Fprintf(os.Stderr, "%s  hearing: %s\n", tab.FormatClock(clock.Now()), text)
		}, func(err error) {
			fmt.Fprintf(os.Stderr, "live narration: %v\n", err)
		})
	}
	sessioncli.WaitForStop(d)
}
