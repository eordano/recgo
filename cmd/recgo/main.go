package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/eordano/recgo/internal/audio"
	"github.com/eordano/recgo/internal/config"
	"github.com/eordano/recgo/internal/logging"
	"github.com/eordano/recgo/internal/ui"
	"github.com/eordano/recgo/internal/upload"
)

var (
	version = "0.1.0"
)

func main() {
	showVersion := flag.Bool("version", false, "Show version")
	showHelp := flag.Bool("help", false, "Show help")
	debug := flag.Bool("debug", false, "Enable per-call verbose logging (the standard log file is always written)")
	configPath := flag.String("config", "", "Path to config file")
	noLimit := flag.Bool("no-limit", false, "Disable the max-duration auto-stop; record until you quit (overrides recording.max_duration)")
	doUpload := flag.Bool("upload", false, "Upload the finished recording to the configured remote (overrides upload.enabled)")
	noUpload := flag.Bool("no-upload", false, "Keep the finished recording on this machine even when upload.enabled is set")
	headless := flag.Bool("headless", false, "No terminal UI: the same recording, with the state the TUI would draw printed on stderr one line per change (what the desktop app's Audio only runs); stop with SIGINT or q<enter>")
	micFlag := flag.String("mic", "", "Record this source instead of the default microphone (a name from the m list)")
	monFlag := flag.String("system-audio", "", "Record this monitor source instead of the default output's (a name from the s list)")
	transcribeFlag := flag.Bool("transcribe", false, "Start with live transcription on, as if t were pressed (uploads audio to the configured endpoint)")

	flag.Parse()

	if *showVersion {
		fmt.Printf("recgo v%s\n", version)
		os.Exit(0)
	}

	if *showHelp || flag.NArg() < 1 {
		printUsage()
		os.Exit(0)
	}

	recordName := flag.Arg(0)

	if err := logging.Init(*debug); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: could not init logging: %v\n", err)
	}
	defer logging.Close()

	audio.CleanupOrphanedVirtualSinks()

	logging.Log("recording name: %s", recordName)

	var cfg *config.Config
	var err error

	if *configPath != "" {
		cfg, err = config.LoadFromPath(*configPath)
	} else {
		cfg, err = config.Load()
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	if *noLimit {
		cfg.Recording.MaxDuration = "0"
	}
	if *doUpload {
		cfg.Upload.Enabled = true
	}
	if *noUpload {
		cfg.Upload.Enabled = false
	}

	var out io.Writer = os.Stdout
	var progOpts []tea.ProgramOption
	if *headless {
		out = os.Stderr
		progOpts = []tea.ProgramOption{tea.WithInput(nil), tea.WithoutRenderer(), tea.WithoutSignalHandler()}
	} else {
		progOpts = []tea.ProgramOption{tea.WithAltScreen()}
	}
	model := ui.NewModelWith(cfg, recordName, ui.Options{
		Headless: *headless, Out: os.Stderr, Mic: *micFlag, Monitor: *monFlag, Transcribe: *transcribeFlag,
	})
	p := tea.NewProgram(model, progOpts...)
	if *headless {
		go headlessSignals(p)
		go headlessStdin(p)
	}

	finalModel, err := p.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	fm, _ := finalModel.(ui.Model)
	if *headless && fm.FinalRecording() == "" {
		if fm.Err() != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", fm.Err())
		}
		os.Exit(1)
	}

	if cfg.Upload.Enabled {
		file := fm.FinalRecording()
		target := cfg.Upload.URL
		if target == "" {
			target = cfg.Upload.Target
		}
		if file == "" {
			logging.Log("upload: no recording produced, skipping")
		} else if _, statErr := os.Stat(file); statErr != nil {
			logging.Log("upload: recording %s not found, skipping", file)
		} else {
			fmt.Fprintf(out, "Uploading %s -> %s ...\n", file, target)
			dest, upErr := upload.Upload(cfg.Upload, file)
			if upErr != nil {
				fmt.Fprintf(os.Stderr, "Upload failed: %v\n", upErr)
				logging.Log("upload failed: %v", upErr)
				os.Exit(1)
			}
			fmt.Fprintf(out, "Uploaded to %s\n", dest)
			logging.Log("uploaded -> %s", dest)
		}
	}
}

// The first SIGINT/SIGTERM stops the recording the way q does (the file is
// finalized, the output device restored); a second one while that runs
// gives up, like a second Ctrl-C in the terminal would.
func headlessSignals(p *tea.Program) {
	sig := make(chan os.Signal, 2)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	<-sig
	p.Send(ui.StopMsg{})
	<-sig
	fmt.Fprintln(os.Stderr, "second interrupt: giving up")
	os.Exit(130)
}

func headlessStdin(p *tea.Program) {
	sc := bufio.NewScanner(os.Stdin)
	for sc.Scan() {
		p.Send(ui.CommandMsg(sc.Text()))
	}
}

func printUsage() {
	fmt.Println(`recgo - A btop-inspired terminal audio recorder

Usage:
  recgo [options] <name>

Arguments:
  name          Name for the recording (used in filename)

Options:
  --config PATH         Path to config file
  --debug               Enable verbose per-call tracing (logs always written to ~/.local/state/recgo/recgo.log)
  --no-limit            Disable the max-duration auto-stop (overrides recording.max_duration)
  --upload              Upload the finished recording to the configured remote (overrides upload.enabled)
  --no-upload           Keep the finished recording local even when upload.enabled is set
  --headless            No terminal UI: the same recording, with the state the TUI would draw
                        printed on stderr one line per change; stop with SIGINT or q<enter>
                        (this is what the desktop app's Audio only runs)
  --mic NAME            Record this source instead of the default microphone
  --system-audio NAME   Record this monitor source instead of the default output's
  --transcribe          Start with live transcription on, as if t were pressed
  --version             Show version
  --help                Show this help

Examples:
  recgo meeting-notes
  recgo --debug interview
  recgo --headless --transcribe standup 2>recgo.lines

Keybindings:
  c         Settings
  m / s     Pick the microphone / system audio source
  t         Toggle live transcription
  q         Quit
  ?         Toggle help

Headless lines (stderr):
  recording -> PATH      mic: NAME      monitor: NAME|none
  HH:MM:SS  narration: TEXT | system: TEXT | hearing: PARTIAL
  error: TEXT            wrote PATH     Uploaded to DEST
  stdin: t / transcribe on|off, q / stop

Config:
  Configuration is stored in $XDG_CONFIG_HOME/recgo/config.toml
  Press Ctrl+S in the settings screen to write the current config there.

For more information, see the README.md`)
}
