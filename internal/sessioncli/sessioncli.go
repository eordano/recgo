// Package sessioncli is the flag and session plumbing recgo-tab, recgo-browser
// and internal/desktop share. It must not import internal/upload or
// internal/transcribe: live narration and session sync stay wired in each
// main.go, where noupstream_test pins them.
package sessioncli

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/eordano/recgo/internal/config"
	"github.com/eordano/recgo/internal/tab"
)

type Options struct {
	Out, Mic, SystemAudio, FFmpeg        string
	STTBackend, STTURL, STTModel, STTKey string
	STTLanguage                          string
	WhisperBin, WhisperModel, WhisperVAD string
	TitleBackend, TitleURL, TitleModel   string
	CfgURL, CfgModel, CfgKey             string
	SyncTarget, SyncKey                  string
	Portal, PortalRoom                   string
	Duration                             time.Duration
	LiveRealtime, NoAudio, NoVAD         bool
	KeepRaw, JSON, Live, NoSync          bool
}

// LiveHelp is --live's text for the recorders that print as they record.
const LiveHelp = "build the session document as it is recorded: print each line as it happens and keep " +
	"SESSION.live.md current in the session folder; narration is transcribed while you " +
	"speak (locally with --stt-backend local); with --stt-backend remote this also " +
	"STREAMS NARRATION AUDIO to the STT endpoint while recording (--live=false to disable)"

// UploadNote is the browser recorders' -h paragraph on what leaves the machine.
const UploadNote = "\nThe session folder is written outside any archive-sync path. With a local\n" +
	"whisper model nothing leaves this machine; without one, --stt-backend auto\n" +
	"(the default) UPLOADS THE AUDIO to the configured remote endpoint. Pass\n" +
	"--stt-backend local or none to guarantee nothing is uploaded.\n"

// Register defines the flags every recorder takes; liveHelp is the one text
// that differs between them.
func (o *Options) Register(liveHelp string) {
	flag.StringVar(&o.Out, "out", "", "output root (default: [recording] sessions_dir in config.toml, else ~/walk-and-talk; ~/Documents/walk-and-talk on macOS)")
	flag.DurationVar(&o.Duration, "duration", 0, "stop automatically after this long; otherwise Ctrl-C")
	flag.BoolVar(&o.NoAudio, "no-audio", false, "skip microphone capture")
	flag.StringVar(&o.Mic, "mic", "", "capture device (default: the system default source; a playing Bluetooth headset's mic yields to the one other mic an app has open)")
	flag.StringVar(&o.SystemAudio, "system-audio", "",
		"mix what you hear into the narration track: a monitor source (<sink>.monitor, or a "+
			"loopback device on macOS), or 'default' for the default output; 'sysaudio off' / "+
			"'sysaudio on' on stdin mutes and unmutes it while recording")
	flag.StringVar(&o.FFmpeg, "ffmpeg", "ffmpeg", "ffmpeg binary")
	flag.StringVar(&o.STTBackend, "stt-backend", "auto",
		"auto (local if a whisper model is found, else remote) | local (whisper.cpp, nothing leaves this machine) | remote (uploads audio) | realtime (uploads audio; streams narration over the endpoint's /v1/realtime websocket as you speak, final transcript via the batch endpoint) | none")
	flag.StringVar(&o.WhisperBin, "whisper-bin", "whisper-cli", "whisper.cpp CLI")
	flag.StringVar(&o.WhisperModel, "whisper-model", "", "ggml model (default: discovered, see below)")
	flag.StringVar(&o.WhisperVAD, "whisper-vad-model", "", "silero VAD model (default: discovered)")
	flag.StringVar(&o.STTURL, "stt-url", "", "remote only: override the OpenAI-compatible endpoint list")
	flag.StringVar(&o.STTModel, "stt-model", "", "remote only: override the per-endpoint model")
	flag.StringVar(&o.STTKey, "stt-api-key", "", "remote only: bearer token (falls back to $OPENAI_API_KEY / $LLM_API_KEY)")
	flag.StringVar(&o.STTLanguage, "stt-language", "", "force a language instead of autodetecting")
	flag.BoolVar(&o.NoVAD, "no-vad-correct", false, "remote only: skip the local silencedetect pass (NOT ADVISED)")
	flag.BoolVar(&o.KeepRaw, "keep-raw", false, "keep audio.pcm alongside audio.wav")
	flag.BoolVar(&o.JSON, "json", false, "also write session.json (full machine-readable timeline)")
	flag.BoolVar(&o.Live, "live", true, liveHelp)
	flag.StringVar(&o.TitleBackend, "title-backend", "",
		"name the session with an LLM: remote (UPLOADS THE TRANSCRIPT) | none (default: follow --stt-backend)")
	flag.StringVar(&o.TitleURL, "title-url", "", "override the OpenAI-compatible endpoint list used for the title")
	flag.StringVar(&o.TitleModel, "title-model", "", "override the chat model (default: whatever the endpoint serves)")
	flag.StringVar(&o.Portal, "portal", "",
		"portal server URL (wss://host or https://host): dial out and serve read-only file "+
			"tools over the output root while recording, so an agent in that portal room can "+
			"follow the live session. EXPOSES THE OUTPUT ROOT (all sessions, read-only) to "+
			"everyone in the room for as long as the recording runs; requires --portal-room")
	flag.StringVar(&o.PortalRoom, "portal-room", "",
		"portal room to join; the room name is the only credential, so pick an unguessable one")
	flag.StringVar(&o.SyncTarget, "sync-target", "",
		"rsync destination (host:/path) for the finished session; defaults to the [upload] "+
			"target in the config file, and is empty unless an operator set one. PUSHES THE "+
			"WHOLE SESSION (transcript, events, screenshots) to that host as soon as the "+
			"recording is written, instead of waiting for whatever periodic sync the machine "+
			"runs; --no-sync keeps it local")
	flag.StringVar(&o.SyncKey, "sync-key", "", "ssh identity for --sync-target")
	flag.BoolVar(&o.NoSync, "no-sync", false,
		"keep the finished session on this machine even when a sync target is configured")
}

// UsageTail ends every recorder's -h: where local models and remote endpoints come from.
func UsageTail() {
	fmt.Fprintf(os.Stderr, "\nLocal models are discovered from, in order:\n")
	for _, d := range tab.ModelSearchPaths() {
		fmt.Fprintf(os.Stderr, "  %s\n", d)
	}
	fmt.Fprintf(os.Stderr, "\nRemote endpoints come from --stt-url / --title-url, else from\n"+
		"[transcription.remote] in %s.\n", config.ConfigPath())
}

// ResolveChromium swaps the --chromium default for the platform's browser
// unless the flag was given.
func ResolveChromium(bin *string) {
	set := false
	flag.Visit(func(f *flag.Flag) { set = set || f.Name == "chromium" })
	if !set {
		*bin = tab.DefaultChromium()
	}
}

// Load takes the remote endpoint and sync defaults from config.toml.
func (o *Options) Load() {
	cfg, err := config.Load()
	if err != nil {
		return
	}
	o.CfgURL = cfg.Transcription.Remote.Endpoint
	o.CfgModel = cfg.Transcription.Remote.Model
	o.CfgKey = cfg.Transcription.Remote.APIKey
	if o.SyncTarget == "" && cfg.Upload.Enabled {
		o.SyncTarget = cfg.Upload.Target
		o.SyncKey = cfg.Upload.SSHKey
	}
}

func (o Options) Endpoints() []tab.Endpoint {
	return tab.Endpoints(o.STTURL, o.STTModel, o.CfgURL, o.CfgModel)
}

// ResolveBackend turns realtime into remote plus live streaming, and auto into
// local when a whisper model exists, else remote (saying where audio will go).
func (o *Options) ResolveBackend() {
	if o.STTBackend == "realtime" {
		o.LiveRealtime = true
		o.STTBackend = "remote"
	}
	if o.STTBackend != "auto" {
		return
	}
	if m, _ := tab.DiscoverWhisperModel(); m != "" || o.WhisperModel != "" {
		o.STTBackend = "local"
		return
	}
	o.STTBackend = "remote"
	if eps := o.Endpoints(); len(eps) > 0 {
		fmt.Fprintf(os.Stderr, "stt: no local whisper model found — audio will be uploaded to %s\n"+
			"     (pass --stt-backend local or none to keep everything on this machine)\n",
			eps[0].URL)
	}
}

// Provision creates the provisional session folder under the output root.
func (o Options) Provision() (started time.Time, root, stamp, dir string, err error) {
	started = time.Now()
	root = o.Out
	if root == "" {
		root = tab.DefaultOutRoot()
	}
	stamp = started.Format("2006-01-02-15-04")
	dir = filepath.Join(root, fmt.Sprintf("%s-recording-%d", stamp, os.Getpid()))
	err = os.MkdirAll(dir, 0o700)
	return
}

// StartPortal joins --portal-room when --portal is set; stop is never nil.
func (o Options) StartPortal(root string) (stop func(), err error) {
	if o.Portal == "" {
		return func() {}, nil
	}
	if o.PortalRoom == "" {
		return nil, fmt.Errorf("--portal requires --portal-room (the room name is the credential)")
	}
	bridge, err := tab.StartPortalBridge(o.Portal, o.PortalRoom, root,
		func(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...) })
	if err != nil {
		return nil, err
	}
	return bridge.Close, nil
}

func (o Options) StartMic(clock *tab.Clock, dir, monitor string, tap func([]byte)) (*tab.Mic, error) {
	mic, err := tab.StartMic(tab.MicOptions{
		Clock: clock, OutDir: dir, Device: o.Mic, Monitor: monitor, FFmpegBin: o.FFmpeg, Tap: tap,
	})
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(os.Stderr, "audio: microphone %s\n", mic.Device())
	if mic.Monitor() != "" {
		fmt.Fprintf(os.Stderr, "audio: system audio mixed in from %s\n", mic.Monitor())
	}
	return mic, nil
}

// StopMic finishes the audio track: the WAV to transcribe ("" when there is
// none) and the note Pack records about it.
func (o Options) StopMic(mic *tab.Mic) (wav, note string) {
	if mic == nil {
		return "", ""
	}
	res := mic.Stop()
	if res.Err != nil {
		fmt.Fprintf(os.Stderr, "audio: %v\n", res.Err)
		return "", res.Note
	}
	if !o.KeepRaw {
		os.Remove(res.RawPath)
	}
	return res.WavPath, res.Note
}

// Transcribe produces the final transcript. vocab, when non-nil, supplies the
// STT prompt hint once there is audio to transcribe.
func (o Options) Transcribe(clock *tab.Clock, wav string, vocab func() string) *tab.Transcript {
	if wav != "" && o.STTBackend != "none" {
		fmt.Fprintln(os.Stderr, "transcribing audio...")
	}
	t := o.transcribe(clock, wav, vocab)
	if t != nil && !t.OK {
		fmt.Fprintf(os.Stderr, "transcription: %s\n", t.Reason)
	}
	return t
}

func (o Options) transcribe(clock *tab.Clock, wav string, vocab func() string) *tab.Transcript {
	switch o.STTBackend {
	case "none":
		return &tab.Transcript{Reason: "transcription disabled (--stt-backend none)"}
	case "local", "remote":
	default:
		return &tab.Transcript{Reason: fmt.Sprintf(
			"unknown --stt-backend %q (want auto, local, remote or none)", o.STTBackend)}
	}
	if wav == "" {
		return &tab.Transcript{Reason: "no audio was captured"}
	}

	prompt := ""
	if vocab != nil {
		if prompt = vocab(); prompt != "" {
			fmt.Fprintf(os.Stderr, "stt: vocabulary hint, %d terms\n", strings.Count(prompt, ", ")+1)
		}
	}

	if o.STTBackend == "local" {
		model, vad := tab.ResolveWhisperModel(o.WhisperModel, o.WhisperVAD)
		t := tab.TranscribeLocal(clock, wav, o.WhisperBin, model, vad, prompt)
		if t.OK && vad == "" {
			fmt.Fprintf(os.Stderr, "warning: %s\n", t.AccuracyNote)
		}
		return &t
	}

	eps := o.Endpoints()
	if len(eps) == 0 {
		return &tab.Transcript{Reason: "no remote STT endpoint configured — set --stt-url, or " +
			"[transcription.remote] endpoint in " + config.ConfigPath()}
	}
	fmt.Fprintf(os.Stderr, "stt: --stt-backend remote — uploading %s to %s\n", wav, eps[0].URL)

	t := tab.TranscribeFallback(clock, wav, tab.STTOptions{
		Endpoints: eps, Model: o.STTModel, APIKey: tab.APIKey(o.STTKey, o.CfgKey),
		Language: o.STTLanguage, Prompt: prompt,
	})
	if !t.OK {
		return &t
	}
	if len(t.Attempts) > 0 {
		fmt.Fprintf(os.Stderr, "stt: fell back to %s after %d failure(s)\n", t.Endpoint, len(t.Attempts))
	}
	if !o.NoVAD {
		t = tab.VADCorrect(t, wav, o.FFmpeg, clock)
		if !t.VADCorrected {
			fmt.Fprintf(os.Stderr, "warning: VAD correction skipped — %s\n", t.VADReason)
		}
	}
	return &t
}

// Title names the session, with an LLM only when --title-backend (or, by
// default, --stt-backend) is remote.
func (o Options) Title(transcript *tab.Transcript, events []tab.Event) tab.TitleResult {
	backend := o.TitleBackend
	if backend == "" {
		backend = o.STTBackend
	}
	if backend == "remote" {
		fmt.Fprintln(os.Stderr, "naming the session...")
	}
	named := o.title(backend, transcript, events)
	if named.Note != "" {
		fmt.Fprintf(os.Stderr, "title: %s\n", named.Note)
	}
	return named
}

func (o Options) title(backend string, transcript *tab.Transcript, events []tab.Event) tab.TitleResult {
	if backend != "remote" {
		return tab.GenerateTitle(tab.TitleOptions{}, transcript, events)
	}
	var eps []tab.Endpoint
	if o.TitleURL != "" {
		eps = []tab.Endpoint{{URL: o.TitleURL, Model: o.TitleModel}}
	} else if o.CfgURL != "" {
		eps = []tab.Endpoint{{URL: o.CfgURL}}
	}
	return tab.GenerateTitle(tab.TitleOptions{
		Enabled: true, Endpoints: eps, Model: o.TitleModel, APIKey: tab.APIKey(o.STTKey, o.CfgKey),
	}, transcript, events)
}

func SystemAudio(mic *tab.Mic) string {
	if mic == nil {
		return ""
	}
	return mic.Monitor()
}

// WaitForStop returns on Ctrl-C, SIGTERM or after d when d > 0.
func WaitForStop(d time.Duration) {
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
