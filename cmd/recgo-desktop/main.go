package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"image"
	"image/png"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/eordano/recgo/internal/config"
	"github.com/eordano/recgo/internal/screencast"
	"github.com/eordano/recgo/internal/tab"
)

var shotOffsets = []struct {
	Label  string
	Offset float64
}{
	{"before", -100},
	{"at", 0},
	{"after", 100},
}

func main() {
	out := flag.String("out", "", "output root (default $XDG_DOCUMENTS_DIR/walk-and-talk)")
	duration := flag.Duration("duration", 0, "stop automatically after this long; otherwise Ctrl-C")
	noAudio := flag.Bool("no-audio", false, "skip microphone capture")
	micDevice := flag.String("mic", "", "capture device (default: system default source)")
	ffmpegBin := flag.String("ffmpeg", "ffmpeg", "ffmpeg binary")
	gstLaunch := flag.String("gst-launch", "gst-launch-1.0", "GStreamer binary (Linux frame capture)")
	sttBackend := flag.String("stt-backend", "remote",
		"remote (uploads audio) | local (whisper.cpp, nothing leaves this machine) | none")
	whisperBin := flag.String("whisper-bin", "whisper-cli", "whisper.cpp CLI")
	whisperModel := flag.String("whisper-model", "", "ggml model (default: discovered)")
	whisperVAD := flag.String("whisper-vad-model", "", "silero VAD model (default: discovered)")
	sttURL := flag.String("stt-url", "", "remote only: override the OpenAI-compatible endpoint list")
	sttModel := flag.String("stt-model", "", "remote only: override the per-endpoint model")
	sttKey := flag.String("stt-api-key", "", "remote only: bearer token (falls back to $OPENAI_API_KEY / $LLM_API_KEY)")
	sttLanguage := flag.String("stt-language", "", "force a language instead of autodetecting")
	noVAD := flag.Bool("no-vad-correct", false, "remote only: skip the local silencedetect pass (NOT ADVISED)")
	tokenPath := flag.String("restore-token", "", "file holding the portal restore token (default under $XDG_STATE_HOME)")
	jsonOut := flag.Bool("json", false, "also write session.json (full machine-readable timeline)")
	titleBackend := flag.String("title-backend", "",
		"name the session with an LLM: remote (UPLOADS THE TRANSCRIPT) | none (default: follow --stt-backend)")
	titleURL := flag.String("title-url", "", "override the OpenAI-compatible endpoint list used for the title")
	titleModel := flag.String("title-model", "", "override the chat model (default: whatever the endpoint serves)")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "recgo-desktop — record the screen with narration\n\n")
		fmt.Fprintf(os.Stderr, "Usage: recgo-desktop [flags]\n\n")
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nType m<enter> while recording to mark a moment.\n")
		fmt.Fprintf(os.Stderr, "Video and screenshots stay on this machine. Audio is uploaded for\n")
		fmt.Fprintf(os.Stderr, "transcription unless you pass --stt-backend local (or none).\n")
	}
	flag.Parse()

	cfgURL, cfgModel, cfgKey := "", "", ""
	if cfg, err := config.Load(); err == nil {
		cfgURL = cfg.Transcription.Remote.Endpoint
		cfgModel = cfg.Transcription.Remote.Model
		cfgKey = cfg.Transcription.Remote.APIKey
	}

	if err := run(opts{
		out: *out, duration: *duration, noAudio: *noAudio, micDevice: *micDevice,
		ffmpegBin: *ffmpegBin, gstLaunch: *gstLaunch, sttBackend: *sttBackend,
		whisperBin: *whisperBin, whisperModel: *whisperModel, whisperVAD: *whisperVAD,
		sttURL: *sttURL, sttModel: *sttModel, sttKey: *sttKey, sttLanguage: *sttLanguage,
		noVAD: *noVAD, tokenPath: *tokenPath, json: *jsonOut,
		titleBackend: *titleBackend, titleURL: *titleURL, titleModel: *titleModel,
		cfgURL: cfgURL, cfgModel: cfgModel, cfgKey: cfgKey,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "recgo-desktop: %v\n", err)
		os.Exit(1)
	}
}

type opts struct {
	out, micDevice, ffmpegBin, gstLaunch string
	sttBackend, whisperBin               string
	whisperModel, whisperVAD, tokenPath  string
	sttURL, sttModel, sttKey             string
	sttLanguage                          string
	titleBackend, titleURL, titleModel   string
	cfgURL, cfgModel, cfgKey             string
	noAudio, noVAD, json                 bool
	duration                             time.Duration
}

func (o opts) sttEndpoints() []tab.Endpoint {
	if o.sttURL != "" {
		return []tab.Endpoint{{URL: o.sttURL, Model: firstNonEmpty(o.sttModel, "whisper")}}
	}
	if o.cfgURL != "" {
		return []tab.Endpoint{{URL: o.cfgURL, Model: firstNonEmpty(o.cfgModel, "whisper")}}
	}
	return tab.DefaultEndpoints
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func documentsDir() string {
	if d := os.Getenv("XDG_DOCUMENTS_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Documents")
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

	tokPath := tokenFile(o.tokenPath)
	prev, _ := os.ReadFile(tokPath)

	desk, err := screencast.OpenDesktop(screencast.DesktopOptions{
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

	var mic *tab.Mic
	if !o.noAudio {
		mic, err = tab.StartMic(tab.MicOptions{
			Clock: clock, OutDir: provisional, Device: o.micDevice, FFmpegBin: o.ffmpegBin,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "audio: %v\n", err)
			mic = nil
		}
	}

	start := rec.Push(tab.Event{T: 0, Kind: "record-start", Text: desk.Backend()})
	go captureInitialShot(desk, rec, start, provisional)
	fmt.Fprintf(os.Stderr, "recording via %s — type m<enter> to mark a moment\n", desk.Backend())
	if o.duration > 0 {
		fmt.Fprintf(os.Stderr, "stopping after %s\n", o.duration)
	} else {
		fmt.Fprintln(os.Stderr, "press Ctrl-C to stop")
	}

	marks := 0
	go func() {
		sc := bufio.NewScanner(os.Stdin)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line != "m" && line != "mark" {
				continue
			}
			marks++
			t := clock.Now()
			ev := rec.Push(tab.Event{T: t, Kind: "mark", Seq: marks})
			fmt.Fprintf(os.Stderr, "marked at %.1fs\n", t/1000)
			go captureAround(desk, rec, ev, provisional, marks, t, clock)
		}
	}()

	waitForStop(o.duration)
	fmt.Fprintln(os.Stderr, "stopping…")

	time.Sleep(400 * time.Millisecond)
	if err := desk.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "capture: %v\n", err)
	}
	os.RemoveAll(filepath.Join(provisional, ".frames"))

	durationMs := clock.Now()
	wavPath, audioNote := "", ""
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

	events := rec.Snapshot()
	slug := fmt.Sprintf("desktop-%d-marks", marks)
	if marks == 0 {
		slug = "desktop"
	}
	title := ""
	titleNote := ""
	named := titleFor(o, transcript, events)
	if named.Note != "" {
		fmt.Fprintf(os.Stderr, "title: %s\n", named.Note)
	}
	if named.Title != "" {
		title, titleNote, slug = named.Title, named.Note, named.Slug
	}
	finalDir := filepath.Join(root, fmt.Sprintf("%s-%s", stamp, slug))
	os.RemoveAll(finalDir)
	if err := os.Rename(provisional, finalDir); err != nil {
		return err
	}

	cwd, _ := os.Getwd()
	session, err := tab.Pack(finalDir, events, clock, transcript, tab.Meta{
		Slug: slug, Title: title, StartedISO: started.UTC().Format(time.RFC3339),
		StartedWall: started.Format("2006-01-02 15:04:05"),
		DurationMs:  durationMs, Cwd: cwd, TargetURL: desk.Backend(),
		Tool: "recgo-desktop", AudioNote: audioNote, TitleNote: titleNote,
	}, tab.PackOptions{JSON: o.json})
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "\nwrote %s/SESSION.md\n", finalDir)
	fmt.Fprintf(os.Stderr, "  %d marks, %d utterances\n", marks, session.Counts.Utterances)
	return nil
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
	key := firstNonEmpty(o.sttKey, os.Getenv("OPENAI_API_KEY"), os.Getenv("LLM_API_KEY"), o.cfgKey)
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

func transcribe(o opts, clock *tab.Clock, wavPath string) *tab.Transcript {
	switch o.sttBackend {
	case "none":
		return &tab.Transcript{Reason: "transcription disabled (--stt-backend none)"}
	case "local", "remote":
	default:
		return &tab.Transcript{Reason: fmt.Sprintf(
			"unknown --stt-backend %q (want local, remote or none)", o.sttBackend)}
	}
	if wavPath == "" {
		return &tab.Transcript{Reason: "no audio was captured"}
	}

	if o.sttBackend == "local" {
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
