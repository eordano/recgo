package main

import (
	"flag"
	"fmt"
	"os"

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

	model := ui.NewModel(cfg, recordName)
	p := tea.NewProgram(model, tea.WithAltScreen())

	finalModel, err := p.Run()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if cfg.Upload.Enabled {
		file := ""
		if fm, ok := finalModel.(ui.Model); ok {
			file = fm.FinalRecording()
		}
		if file == "" {
			logging.Log("upload: no recording produced, skipping")
		} else if _, statErr := os.Stat(file); statErr != nil {
			logging.Log("upload: recording %s not found, skipping", file)
		} else {
			fmt.Printf("Uploading %s -> %s ...\n", file, cfg.Upload.Target)
			dest, upErr := upload.Upload(cfg.Upload, file)
			if upErr != nil {
				fmt.Fprintf(os.Stderr, "Upload failed: %v\n", upErr)
				logging.Log("upload failed: %v", upErr)
				os.Exit(1)
			}
			fmt.Printf("Uploaded to %s\n", dest)
			logging.Log("uploaded -> %s", dest)
		}
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
  --version             Show version
  --help                Show this help

Examples:
  recgo meeting-notes
  recgo --debug interview

Keybindings:
  c         Settings
  q         Quit
  ?         Toggle help

Config:
  Configuration is stored in $XDG_CONFIG_HOME/recgo/config.toml
  Press Ctrl+S in the settings screen to write the current config there.

For more information, see the README.md`)
}
