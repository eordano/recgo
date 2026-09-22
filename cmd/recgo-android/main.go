// recgo-android records local Android interaction sessions through ADB.
package main

import (
	"context"
	"flag"
	"fmt"
	"github.com/eordano/recgo/internal/android"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	var o android.Options
	flag.StringVar(&o.Serial, "serial", "", "required ADB serial (phone or emulator)")
	flag.StringVar(&o.Package, "package", "", "required application package to inspect")
	flag.StringVar(&o.ExtraPackages, "extra-packages", "", "comma-separated additional packages (e.g. permission dialogs); explicit opt-in")
	flag.StringVar(&o.ADB, "adb", "adb", "ADB executable")
	flag.StringVar(&o.Scrcpy, "scrcpy", "scrcpy", "scrcpy executable")
	flag.StringVar(&o.APK, "helper-apk", "", "install/update this helper APK before setup or capture")
	flag.BoolVar(&o.Setup, "setup", false, "open helper setup; manually enable its accessibility service")
	flag.BoolVar(&o.Devices, "devices", false, "list connected ADB devices and exit")
	flag.StringVar(&o.Out, "out", ".", "parent directory for a new private session folder")
	flag.DurationVar(&o.Duration, "duration", 0, "stop after this duration (e.g. 30s); otherwise Ctrl-C")
	flag.BoolVar(&o.Screenshots, "screenshots", false, "opt in to whole-display screenshots after clicks (may expose secrets)")
	flag.BoolVar(&o.Video, "video", false, "opt in to whole-display scrcpy video (may expose other apps)")
	flag.BoolVar(&o.Mirror, "mirror", false, "show/control device in a scrcpy window (implies video)")
	flag.BoolVar(&o.Logs, "logs", false, "opt in to target app UID logcat (may expose secrets)")
	flag.BoolVar(&o.Audio, "audio", false, "record desktop microphone narration")
	flag.BoolVar(&o.Transcribe, "transcribe", false, "transcribe narration with local whisper.cpp only (requires --audio)")
	flag.StringVar(&o.Mic, "mic", "", "desktop microphone device")
	flag.StringVar(&o.FFmpeg, "ffmpeg", "ffmpeg", "ffmpeg executable")
	flag.StringVar(&o.Whisper, "whisper-bin", "whisper-cli", "local whisper.cpp executable")
	flag.StringVar(&o.Model, "whisper-model", "", "local whisper.cpp model; auto-discovered if empty")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "unexpected positional arguments")
		os.Exit(2)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := android.Run(ctx, o, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "recgo-android:", err)
		os.Exit(1)
	}
}
