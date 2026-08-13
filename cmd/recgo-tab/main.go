package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/eordano/recgo/internal/audio"
	"github.com/eordano/recgo/internal/config"
	"github.com/eordano/recgo/internal/tab"
	"github.com/eordano/recgo/internal/tabui"
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

	titleBackend string
	titleURL     string
	titleModel   string

	whisperBin      string
	whisperModel    string
	whisperVADModel string

	cfgURL   string
	cfgModel string
	cfgKey   string
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

func main() {
	var o options
	flag.StringVar(&o.launch, "launch", "", "launch chromium on this URL with remote debugging")
	flag.StringVar(&o.chromium, "chromium", "chromium", "chromium binary")
	flag.BoolVar(&o.headless, "headless", false, "launch headless (implies --launch)")
	flag.IntVar(&o.port, "port", 9222, "CDP port")
	flag.StringVar(&o.match, "match", "", "attach to the tab whose URL/title contains this")
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
	flag.BoolVar(&o.pick, "select", false, "pick a tab from a list of what the browser has open")
	flag.BoolVar(&o.json, "json", false, "also write session.json (full machine-readable timeline)")
	flag.StringVar(&o.titleBackend, "title-backend", "",
		"name the session with an LLM: remote (UPLOADS THE TRANSCRIPT) | none (default: follow --stt-backend)")
	flag.StringVar(&o.titleURL, "title-url", "", "override the OpenAI-compatible endpoint list used for the title")
	flag.StringVar(&o.titleModel, "title-model", "", "override the chat model (default: whatever the endpoint serves)")

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
		o.chromium = defaultChromium()
	}

	if cfg, err := config.Load(); err == nil {
		o.cfgURL = cfg.Transcription.Remote.Endpoint
		o.cfgModel = cfg.Transcription.Remote.Model
		o.cfgKey = cfg.Transcription.Remote.APIKey
	}

	if err := run(o); err != nil {
		fmt.Fprintf(os.Stderr, "recgo-tab: %v\n", err)
		os.Exit(1)
	}
}

func documentsDir() string {
	if d := os.Getenv("XDG_DOCUMENTS_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Documents")
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
		root = defaultOutRoot()
	}
	stamp := started.Format("2006-01-02-15-04")
	provisional := filepath.Join(root, fmt.Sprintf("%s-recording-%d", stamp, os.Getpid()))
	if err := os.MkdirAll(provisional, 0o700); err != nil {
		return err
	}

	var browser *exec.Cmd
	var profile string
	if o.launch != "" || o.headless {
		var err error
		browser, profile, err = launchChromium(o)
		if err != nil {
			return err
		}
		defer func() {
			if browser != nil && browser.Process != nil {
				browser.Process.Signal(syscall.SIGTERM)
				browser.Wait()
			}
			os.RemoveAll(profile)
		}()
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

	cdp, target, err := tab.Attach(o.port, o.match)
	if err != nil {
		return err
	}
	defer cdp.Close()
	fmt.Fprintf(os.Stderr, "attached to: %s\n", firstNonEmpty(target.Title, target.URL))

	clock := tab.NewClock()
	rec := tab.NewRecorder(cdp, clock, provisional)

	var mic *tab.Mic
	if !o.noAudio {
		mic, err = tab.StartMic(tab.MicOptions{
			Clock:     clock,
			OutDir:    provisional,
			Device:    o.micDevice,
			FFmpegBin: o.ffmpegBin,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "audio: %v\n", err)
			mic = nil
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

	go readMarks(rec)
	waitForStop(o.duration)

	fmt.Fprintln(os.Stderr, "stopping…")
	events := rec.Stop()
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

	transcript := transcribe(o, clock, wavPath)
	if transcript != nil && !transcript.OK {
		fmt.Fprintf(os.Stderr, "transcription: %s\n", transcript.Reason)
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

	cwd, _ := os.Getwd()
	session, err := tab.Pack(finalDir, events, clock, transcript, tab.Meta{
		Slug:        named.Slug,
		Title:       named.Title,
		StartedISO:  started.UTC().Format(time.RFC3339),
		StartedWall: started.Format("2006-01-02 15:04:05"),
		DurationMs:  durationMs,
		Cwd:         cwd,
		TargetURL:   firstNonEmpty(o.launch, target.URL),
		TargetTitle: target.Title,
		Tool:        "recgo-tab",
		AudioNote:   audioNote,
		TitleNote:   named.Note,
	}, tab.PackOptions{JSON: o.json})
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "\nwrote %s/SESSION.md\n", finalDir)
	fmt.Fprintf(os.Stderr, "  %d clicks, %d HMR, %d errors, %d utterances\n",
		session.Counts.Clicks, session.Counts.HMR, session.Counts.Errors, session.Counts.Utterances)
	return nil
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

func transcribe(o options, clock *tab.Clock, wavPath string) *tab.Transcript {
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
		model, vad := o.whisperModel, o.whisperVADModel
		if model == "" || vad == "" {
			dm, dv := tab.DiscoverWhisperModel()
			if model == "" {
				model = dm
			}
			if vad == "" {
				vad = dv
			}
		}
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

func readMarks(rec *tab.Recorder) {
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line == "mark" || line == "m" {
			fmt.Fprintf(os.Stderr, "marked at %.1fs\n", rec.Mark("")/1000)
		}
	}
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
	profile := filepath.Join(os.TempDir(), fmt.Sprintf("recgo-tab-profile-%d", os.Getpid()))
	args := []string{
		fmt.Sprintf("--remote-debugging-port=%d", o.port),
		"--user-data-dir=" + profile,
		"--no-first-run",
		"--no-default-browser-check",
		"--window-size=1280,800",
	}
	if o.headless {
		args = append(args, "--headless=new", "--disable-gpu", "--no-sandbox")
	}
	args = append(args, "about:blank")

	cmd := exec.Command(o.chromium, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, profile, fmt.Errorf("launch chromium: %w", err)
	}

	for i := 0; i < 100; i++ {
		if targets, err := tab.ListTargets(o.port); err == nil {
			for _, t := range targets {
				if t.Type == "page" {
					return cmd, profile, nil
				}
			}
		}
		if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
			return nil, profile, fmt.Errorf("chromium exited: %s", tailString(stderr.String(), 800))
		}
		time.Sleep(100 * time.Millisecond)
	}
	cmd.Process.Kill()
	return nil, profile, fmt.Errorf("chromium exposed no page target on port %d: %s",
		o.port, tailString(stderr.String(), 800))
}

func tailString(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

var _ = audio.CheckBackend
