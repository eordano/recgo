package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/eordano/recgo/internal/audio"
	"github.com/eordano/recgo/internal/config"
	"github.com/eordano/recgo/internal/sessioncli"
	"github.com/eordano/recgo/internal/tab"
	"github.com/eordano/recgo/internal/tabui"
	"github.com/eordano/recgo/internal/transcribe"
	"github.com/eordano/recgo/internal/upload"
)

type options struct {
	sessioncli.Options
	launch, chromium, match, target string
	headless, pick                  bool
	port                            int
}

func main() {
	var o options
	flag.StringVar(&o.launch, "launch", "", "launch chromium on this URL with remote debugging")
	flag.StringVar(&o.chromium, "chromium", "chromium", "chromium binary")
	flag.BoolVar(&o.headless, "headless", false, "launch headless (implies --launch)")
	flag.IntVar(&o.port, "port", 9222, "CDP port")
	flag.StringVar(&o.match, "match", "", "attach to the tab whose URL/title contains this")
	flag.StringVar(&o.target, "target", "", "attach to the tab with this CDP target id (exact; from /json/list)")
	flag.BoolVar(&o.pick, "select", false, "pick a tab from a list of what the browser has open")
	o.Register(sessioncli.LiveHelp)

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "recgo-tab — record a browser tab with narration\n\n")
		fmt.Fprintf(os.Stderr, "Usage: recgo-tab [flags]\n\n")
		flag.PrintDefaults()
		fmt.Fprint(os.Stderr, sessioncli.UploadNote)
		sessioncli.UsageTail()
	}
	flag.Parse()
	sessioncli.ResolveChromium(&o.chromium)
	o.Load()

	if err := run(o); err != nil {
		fmt.Fprintf(os.Stderr, "recgo-tab: %v\n", err)
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
			Profile: filepath.Join(os.TempDir(), fmt.Sprintf("recgo-tab-profile-%d", os.Getpid())),
		})
		if err != nil {
			return err
		}
		defer func() { tab.StopChromium(browser, profile) }()
	}

	if o.pick && o.launch == "" {
		chosen, err := tabui.Pick(o.port)
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

	monitor, err := tab.ResolveMonitor(o.SystemAudio)
	if err != nil {
		fmt.Fprintf(os.Stderr, "system audio: %v -- recording the microphone alone\n", err)
		monitor = ""
	}

	clock := tab.NewClock()
	rec := tab.NewRecorder(cdp, clock, provisional)

	feed := o.liveFeed()
	var tapFn func([]byte)
	if feed != nil {
		tapFn = feed.Feed
	}

	var mic *tab.Mic
	if !o.NoAudio {
		if mic, err = o.StartMic(clock, provisional, monitor, tapFn); err != nil {
			fmt.Fprintf(os.Stderr, "audio: %v\n", err)
			feed = nil
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
	if o.Duration > 0 {
		fmt.Fprintf(os.Stderr, "stopping after %s\n", o.Duration)
	} else {
		fmt.Fprintf(os.Stderr, "press Ctrl-C to stop\n")
	}

	if mic != nil {
		if err := mic.EmitCalibrationTone(cdp, clock); err != nil {
			fmt.Fprintf(os.Stderr, "calibration tone: %v\n", err)
		}
	}

	var lv *tab.LiveView
	if o.Live {
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
	sessioncli.WaitForStop(o.Duration)

	fmt.Fprintln(os.Stderr, "stopping...")
	events := rec.Stop()
	if feed != nil {
		feed.Stop()
	}
	if lv != nil {
		lv.Stop()
	}
	durationMs := clock.Now()

	wavPath, audioNote := o.StopMic(mic)
	transcript := o.Transcribe(clock, wavPath, func() string { return tab.Vocabulary(events, target.Title) })
	named := o.Title(transcript, events)
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
		SystemAudio: sessioncli.SystemAudio(mic),
		TitleNote:   named.Note,
		System:      tab.CollectSystemInfo(),
	}, tab.PackOptions{JSON: o.JSON})
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "\nwrote %s\n", filepath.Join(finalDir, "SESSION.md"))
	fmt.Fprintf(os.Stderr, "  %d clicks, %d HMR, %d errors, %d utterances\n",
		session.Counts.Clicks, session.Counts.HMR, session.Counts.Errors, session.Counts.Utterances)
	syncSession(o, finalDir)
	return nil
}

// liveFeed picks the narration preview backend. It follows --stt-backend, so
// the preview never sends audio anywhere the final transcription would not.
func (o options) liveFeed() *transcribe.Session {
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
		var feed *transcribe.Session
		if o.LiveRealtime {
			feed = tab.RealtimeLiveFeed(context.Background(), eps[0], key, o.STTModel)
			fmt.Fprintf(os.Stderr, "live: streaming narration to %s over its realtime websocket as it is spoken "+
				"-- the final transcript is still redone from the full recording (--live=false to disable)\n", eps[0].URL)
		} else {
			feed = tab.RemoteLiveFeed(context.Background(), eps[0], key, o.STTModel)
		}
		fmt.Fprintf(os.Stderr, "live: streaming narration to %s as it is spoken -- the final "+
			"transcript is still redone from the full recording (--live=false to disable)\n", eps[0].URL)
		return feed
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
	dest, err := upload.SyncDir(config.UploadConfig{Target: o.SyncTarget, SSHKey: o.SyncKey}, dir)
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

var _ = audio.CheckBackend
