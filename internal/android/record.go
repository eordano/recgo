package android

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/eordano/recgo/internal/tab"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func Run(ctx context.Context, o Options, out, progress io.Writer) error {
	if o.ADB == "" {
		o.ADB = "adb"
	}
	if o.Devices {
		cmd := exec.CommandContext(ctx, o.ADB, "devices", "-l")
		cmd.Stdout = out
		cmd.Stderr = progress
		return cmd.Run()
	}
	packages, err := o.validate()
	if err != nil {
		return err
	}
	d := device{o.ADB, o.Serial}
	state, err := d.run(ctx, "get-state")
	if err != nil {
		return err
	}
	if state != "device" {
		return fmt.Errorf("device is not authorized/connected: %s", state)
	}
	if o.APK != "" {
		if _, err = d.run(ctx, "install", "-r", o.APK); err != nil {
			return err
		}
	}
	if o.Setup {
		_, err = d.run(ctx, "shell", "am", "start", "-W", "-f", "0x14000000", "-n", HelperPackage+"/.MainActivity")
		if err == nil {
			fmt.Fprintln(out, "On the device: Accessibility settings → Recgo Android → enable. Then run with --serial and --package. No recording is active yet.")
		}
		return err
	}
	if _, err = d.uid(ctx, o.Package); err != nil {
		return err
	}
	port, err := d.run(ctx, "forward", "tcp:0", "localabstract:recgo_android")
	if err != nil {
		return err
	}
	p, err := strconv.Atoi(port)
	if err != nil || p < 1 || p > 65535 {
		return fmt.Errorf("invalid ADB forward port %q", port)
	}
	defer func() { _, _ = d.run(context.Background(), "forward", "--remove", "tcp:"+port) }()
	clock := tab.NewClock()
	conn, scanner, hello, midpoint, uncertainty, err := connectHelper(ctx, net.JoinHostPort("127.0.0.1", port), packages, clock)
	if err != nil {
		return err
	}
	defer conn.Close()
	if o.Out == "" {
		o.Out = "."
	}
	if err = os.MkdirAll(o.Out, 0700); err != nil {
		return err
	}
	dir, err := os.MkdirTemp(o.Out, time.Now().Format("20060102-150405")+"-android-")
	if err != nil {
		return err
	}
	dir, _ = filepath.Abs(dir)
	s := Session{Version: 1, Events: []Event{}}
	s.Meta.Tool = "recgo-android"
	s.Meta.StartedISO = clock.WallOrigin.Format(time.RFC3339Nano)
	s.Meta.Serial = o.Serial
	s.Meta.TargetURL = o.Package
	s.Meta.Packages = packages
	s.Clock.DeviceToSessionMs = midpoint - hello.ElapsedMs
	s.Clock.HandshakeErrorMs = uncertainty
	s.Clock.Note = "Android elapsedRealtime mapped to host monotonic time. Initial uncertainty excludes drift; avoid suspending either device. Receipt times are preserved separately."
	if o.Screenshots || o.Video || o.Mirror {
		fmt.Fprintln(progress, "Visual capture enabled: whole display, including other apps and sensitive text.")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	if o.Duration > 0 {
		var stop context.CancelFunc
		ctx, stop = context.WithTimeout(ctx, o.Duration)
		defer stop()
	}
	journal, err := os.OpenFile(filepath.Join(dir, "events.jsonl"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer journal.Close()
	encoder := json.NewEncoder(journal)
	var children []*child
	var mic *tab.Mic
	var runErr error
	finish := func() error {
		// Capture duration excludes media finalization and potentially long transcription.
		s.Meta.DurationMs = clock.Now()
		cancel()
		conn.Close()
		for _, c := range children {
			c.stop()
		}
		if mic != nil {
			result := mic.Stop()
			if result.Err != nil {
				s.Warnings = append(s.Warnings, "Narration: "+result.Err.Error())
			}
			if result.WavPath != "" {
				s.Audio = filepath.Base(result.WavPath)
			}
			s.Warnings = append(s.Warnings, result.Note)
			if o.Transcribe {
				fmt.Fprintln(progress, "Transcribing locally with whisper.cpp…")
				model := o.Model
				if model == "" {
					model, _ = tab.DiscoverWhisperModel()
				}
				transcript := tab.TranscribeLocal(clock, result.WavPath, o.Whisper, model, "", "")
				s.Transcript = &transcript
			}
		}
		if runErr != nil {
			s.Warnings = append(s.Warnings, runErr.Error())
		}
		if err := journal.Sync(); err != nil {
			runErr = errors.Join(runErr, err)
		}
		data, err := json.MarshalIndent(s, "", "  ")
		if err != nil {
			return errors.Join(runErr, err)
		}
		if err = os.WriteFile(filepath.Join(dir, "session.json"), data, 0600); err != nil {
			return errors.Join(runErr, err)
		}
		for _, name := range []string{"SESSION.md", "SESSION.live.md"} {
			if err = writeReport(dir, name, Report(s, false)); err != nil {
				return errors.Join(runErr, err)
			}
		}
		fmt.Fprintln(out, "wrote", filepath.Join(dir, "SESSION.md"))
		return runErr
	}
	if o.Video || o.Mirror {
		args := []string{"--serial", o.Serial, "--record", filepath.Join(dir, "video.mkv"), "--no-audio"}
		if !o.Mirror {
			args = append(args, "--no-control", "--no-playback")
		}
		cmd := exec.Command(o.Scrcpy, args...)
		// scrcpy must use the same ADB binary as the metadata recorder.
		cmd.Env = append(os.Environ(), "ADB="+o.ADB)
		c, err := startChild(cmd, filepath.Join(dir, "scrcpy.log"))
		if err != nil {
			runErr = err
			return finish()
		}
		children = append(children, c)
		s.Video = "video.mkv"
	}
	if o.Logs {
		uid, err := d.uid(ctx, o.Package)
		if err != nil {
			runErr = err
			return finish()
		}
		c, err := startChild(d.command(context.Background(), "logcat", "--uid="+uid, "-v", "epoch", "-T", "1"), filepath.Join(dir, "logcat.txt"))
		if err != nil {
			runErr = err
			return finish()
		}
		children = append(children, c)
		s.Warnings = append(s.Warnings, "logcat.txt contains raw app-UID logs (including any shared-UID packages). Logs may contain secrets; they are not redacted.")
	}
	if o.Audio {
		mic, err = tab.StartMic(tab.MicOptions{Clock: clock, OutDir: dir, Device: o.Mic, FFmpegBin: o.FFmpeg})
		if err != nil {
			runErr = err
			return finish()
		}
	}
	type received struct {
		e   Event
		at  float64
		err error
	}
	input := make(chan received, 256)
	go func() {
		defer close(input)
		for scanner.Scan() {
			var e Event
			err := json.Unmarshal(scanner.Bytes(), &e)
			select {
			case input <- received{e, clock.Now(), err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
		err := scanner.Err()
		if err == nil {
			err = io.EOF
		}
		select {
		case input <- received{err: err}:
		case <-ctx.Done():
		}
	}()
	// Fast keepalive also drains helper events every 100ms without blocking its UI thread.
	pingErr := make(chan error, 1)
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			_ = conn.SetWriteDeadline(time.Now().Add(3 * time.Second))
			if _, err := io.WriteString(conn, "ping\n"); err != nil {
				select {
				case pingErr <- err:
				case <-ctx.Done():
				}
				return
			}
			select {
			case <-ticker.C:
			case <-ctx.Done():
				return
			}
		}
	}()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	lastMessage := time.Now()
	appendEvent := func(e Event) error {
		if len(s.Events) >= 100000 {
			return fmt.Errorf("session reached the 100000-event memory bound; start a new session")
		}
		e.Seq = len(s.Events) + 1
		e.sanitize()
		s.Events = append(s.Events, e)
		return encoder.Encode(e)
	}
	if err = writeReport(dir, "SESSION.live.md", Report(s, true)); err != nil {
		runErr = err
		return finish()
	}
	fmt.Fprintln(progress, "Recording locally:", dir)
	for {
		select {
		case <-ctx.Done():
			return finish()
		case err := <-pingErr:
			runErr = fmt.Errorf("helper connection lost: %w", err)
			return finish()
		case r, ok := <-input:
			if !ok || r.err != nil {
				runErr = fmt.Errorf("helper disconnected: %v", r.err)
				return finish()
			}
			lastMessage = time.Now()
			e := r.e
			e.ReceivedMs = r.at
			e.T = e.ElapsedMs + s.Clock.DeviceToSessionMs
			if e.Kind == "heartbeat" {
				if e.Dropped > 0 {
					e.Kind = "warning"
					e.Text = fmt.Sprintf("helper queue dropped %d events", e.Dropped)
					if err = appendEvent(e); err != nil {
						runErr = err
						return finish()
					}
				}
				continue
			}
			allowed := false
			for _, pkg := range packages {
				if e.Package == pkg {
					allowed = true
				}
			}
			if !allowed {
				continue
			}
			switch e.Kind {
			case "click", "long-click", "scroll", "window":
			default:
				continue
			}
			if e.T > e.ReceivedMs+s.Clock.HandshakeErrorMs+100 {
				s.Warnings = append(s.Warnings, "Device clock discontinuity detected; use receivedMs for ordering.")
				e.T = e.ReceivedMs
			}
			if o.Screenshots && (e.Kind == "click" || e.Kind == "long-click") {
				shot := &Shot{StartMs: clock.Now()}
				name := fmt.Sprintf("%04d.png", len(s.Events)+1)
				if err = d.screenshot(ctx, filepath.Join(dir, name)); err != nil {
					shot.Error = err.Error()
				} else {
					shot.File = name
				}
				shot.EndMs = clock.Now()
				e.Screenshot = shot
			}
			if err = appendEvent(e); err != nil {
				runErr = err
				return finish()
			}
			fmt.Fprintf(progress, "%s %s: %s\n", tab.FormatClock(e.T), e.Kind, markdown(label(e)))
		case <-ticker.C:
			if time.Since(lastMessage) > 8*time.Second {
				runErr = fmt.Errorf("helper heartbeat timed out")
				return finish()
			}
			for _, c := range children {
				select {
				case err := <-c.done:
					runErr = fmt.Errorf("capture subprocess exited early (%s): %v", c.cmd.Path, err)
					return finish()
				default:
				}
			}
			s.Meta.DurationMs = clock.Now()
			if err = writeReport(dir, "SESSION.live.md", Report(s, true)); err != nil {
				runErr = err
				return finish()
			}
		}
	}
}

func writeReport(dir, name, body string) error {
	tmp := filepath.Join(dir, name+".tmp")
	if err := os.WriteFile(tmp, []byte(strings.TrimSpace(body)+"\n"), 0600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, name))
}
