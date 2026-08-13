package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/eordano/recgo/internal/tab"
	"github.com/eordano/recgo/internal/tabui"
)

func main() {
	port := flag.Int("port", 9222, "CDP port of a running browser")
	out := flag.String("out", "", "output root (default $XDG_DOCUMENTS_DIR/walk-and-talk)")
	noAudio := flag.Bool("no-audio", false, "skip microphone capture")
	micDevice := flag.String("mic", "", "capture device (default: system default source)")
	ffmpegBin := flag.String("ffmpeg", "ffmpeg", "ffmpeg binary")
	sttBackend := flag.String("stt-backend", "local",
		"local (whisper.cpp, nothing leaves this machine) | none")
	whisperBin := flag.String("whisper-bin", "whisper-cli", "whisper.cpp CLI")
	whisperModel := flag.String("whisper-model", "", "ggml model (default: discovered)")
	whisperVAD := flag.String("whisper-vad-model", "", "silero VAD model (default: discovered)")
	duration := flag.Duration("duration", 0, "stop automatically after this long")
	plain := flag.Bool("plain", false, "no TUI; log to stderr and stop on Ctrl-C")
	jsonOut := flag.Bool("json", false, "also write session.json (full machine-readable timeline)")
	titleBackend := flag.String("title-backend", "none",
		"name the session with an LLM: remote (UPLOADS THE TRANSCRIPT) | none")
	titleURL := flag.String("title-url", "", "override the OpenAI-compatible endpoint list used for the title")
	titleModel := flag.String("title-model", "", "override the chat model (default: whatever the endpoint serves)")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "recgo-browser — record a browsing session across every tab\n\n")
		fmt.Fprintf(os.Stderr, "Usage: recgo-browser [flags]\n\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nRecordings stay on this machine. Nothing is uploaded.\n")
		fmt.Fprintf(os.Stderr, "\nStart your browser with --remote-debugging-port=<port> first.\n")
	}
	flag.Parse()

	if err := run(opts{
		port: *port, out: *out, noAudio: *noAudio, micDevice: *micDevice,
		ffmpegBin: *ffmpegBin, sttBackend: *sttBackend, whisperBin: *whisperBin,
		whisperModel: *whisperModel, whisperVAD: *whisperVAD,
		duration: *duration, plain: *plain, json: *jsonOut,
		titleBackend: *titleBackend, titleURL: *titleURL, titleModel: *titleModel,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "recgo-browser: %v\n", err)
		os.Exit(1)
	}
}

type opts struct {
	port                      int
	out, micDevice, ffmpegBin string
	sttBackend, whisperBin    string
	whisperModel, whisperVAD  string
	titleBackend, titleURL    string
	titleModel                string
	noAudio, plain, json      bool
	duration                  time.Duration
}

func documentsDir() string {
	if d := os.Getenv("XDG_DOCUMENTS_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Documents")
}

func run(o opts) error {
	started := time.Now()
	root := o.out
	if root == "" {
		root = filepath.Join(documentsDir(), "walk-and-talk")
	}
	stamp := started.Format("2006-01-02-15-04")
	provisional := filepath.Join(root, fmt.Sprintf("%s-recording-%d", stamp, os.Getpid()))
	if err := os.MkdirAll(provisional, 0o700); err != nil {
		return err
	}

	clock := tab.NewClock()
	rec := tab.NewRecording(clock, provisional)
	follower := tab.NewFollower(o.port, rec)

	var mic *tab.Mic
	if !o.noAudio {
		var err error
		mic, err = tab.StartMic(tab.MicOptions{
			Clock: clock, OutDir: provisional, Device: o.micDevice, FFmpegBin: o.ffmpegBin,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "audio: %v\n", err)
			mic = nil
		}
	}

	if err := follower.Start(); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "following %d tab(s) on port %d\n", follower.Count(), o.port)

	if o.plain {
		runPlain(follower, o.duration)
	} else if err := runTUI(follower, rec, o.port); err != nil {
		return err
	}

	events := follower.Stop()
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
			os.Remove(res.RawPath)
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
		TargetURL:   fmt.Sprintf("(%d tabs on port %d)", countTabs(events), o.port),
		Tool:        "recgo-browser",
		AudioNote:   audioNote,
		TitleNote:   named.Note,
	}, tab.PackOptions{JSON: o.json})
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "\nwrote %s/SESSION.md\n", finalDir)
	fmt.Fprintf(os.Stderr, "  %d clicks, %d HMR, %d errors, %d utterances across %d tab(s)\n",
		session.Counts.Clicks, session.Counts.HMR, session.Counts.Errors,
		session.Counts.Utterances, countTabs(events))
	return nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func titleFor(o opts, transcript *tab.Transcript, events []tab.Event) tab.TitleResult {
	if o.titleBackend != "remote" {
		return tab.GenerateTitle(tab.TitleOptions{}, transcript, events)
	}
	var eps []tab.Endpoint
	if o.titleURL != "" {
		eps = []tab.Endpoint{{URL: o.titleURL, Model: o.titleModel}}
	}
	return tab.GenerateTitle(tab.TitleOptions{
		Enabled: true, Endpoints: eps, Model: o.titleModel,
		APIKey: firstNonEmpty(os.Getenv("OPENAI_API_KEY"), os.Getenv("LLM_API_KEY")),
	}, transcript, events)
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

func runTUI(f *tab.Follower, rec *tab.Recording, port int) error {
	model := tabui.NewFollow(port, f, rec)
	p := tea.NewProgram(model)
	f.OnUpdate = func() { p.Send(tabui.Refresh()) }
	rec.OnChange(func() { p.Send(tabui.Refresh()) })
	_, err := p.Run()
	return err
}

func runPlain(f *tab.Follower, d time.Duration) {
	fmt.Fprintln(os.Stderr, "press Ctrl-C to stop")
	waitForStop(d)
}

func transcribe(o opts, clock *tab.Clock, wavPath string) *tab.Transcript {
	if o.sttBackend == "none" {
		return &tab.Transcript{Reason: "transcription disabled (--stt-backend none)"}
	}
	if wavPath == "" {
		return &tab.Transcript{Reason: "no audio was captured"}
	}
	model, vad := o.whisperModel, o.whisperVAD
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
	return &t
}
