package audio

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/eordano/recgo/internal/logging"
)

type RecordingStats struct {
	Duration time.Duration
	Size     int64
	Bitrate  float64
	Speed    float64
}

type Recorder struct {
	outputPath   string
	micDevice    string
	monitorDev   string
	audioCodec   string
	bitrate      string
	progressChan chan RecordingStats
	cmd          *exec.Cmd
	cancel       context.CancelFunc
	done         chan struct{}
	mu           sync.Mutex
	running      bool
	started      bool
}

var progressRegex = regexp.MustCompile(`size=\s*(\d+)\s*([KMGTkmgt]i?[Bb])?\s+time=(\d+:\d+:\d+\.\d+)\s+bitrate=\s*([\d.]+)kbits/s\s+speed=([\d.]+)x`)

func NewRecorder(outputPath, micDevice, monitorDevice, codec, bitrate string) *Recorder {
	return &Recorder{
		outputPath:   outputPath,
		micDevice:    micDevice,
		monitorDev:   monitorDevice,
		audioCodec:   codec,
		bitrate:      bitrate,
		progressChan: make(chan RecordingStats, 10),
		done:         make(chan struct{}),
	}
}

func (r *Recorder) buildArgs() []string {
	var args []string
	hasMic := r.micDevice != ""
	hasMonitor := r.monitorDev != ""

	if hasMonitor {
		args = append(args, ffmpegInputArgs(r.monitorDev)...)
	}
	if hasMic {
		args = append(args, ffmpegInputArgs(r.micDevice)...)
	}
	if hasMic && hasMonitor {
		args = append(args,
			"-filter_complex", "[0:a:0][1:a:0] amix=inputs=2:duration=longest:normalize=0 [a]",
			"-map", "[a]", "-map", "0:a", "-map", "1:a",
		)
	}

	return append(args, "-c:v", "copy", "-c:a", r.audioCodec, "-ac", "2", "-ar", "48000", "-b:a", r.bitrate, "-y", r.outputPath)
}

func (r *Recorder) Start(ctx context.Context) error {
	r.mu.Lock()
	if r.started {
		r.mu.Unlock()
		return fmt.Errorf("recorder already used - create a new Recorder instance")
	}
	r.started = true

	if err := os.MkdirAll(filepath.Dir(r.outputPath), 0755); err != nil {
		r.mu.Unlock()
		return fmt.Errorf("create output directory: %w", err)
	}

	ctx, r.cancel = context.WithCancel(ctx)
	r.done = make(chan struct{})
	args := r.buildArgs()

	logging.Log("ffmpeg args: %v", args)

	r.cmd = exec.CommandContext(ctx, "ffmpeg", args...)
	r.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stderr, err := r.cmd.StderrPipe()
	if err != nil {
		r.mu.Unlock()
		return fmt.Errorf("stderr pipe: %w", err)
	}

	if err := r.cmd.Start(); err != nil {
		r.mu.Unlock()
		return fmt.Errorf("start ffmpeg: %w", err)
	}

	logging.Log("ffmpeg started, pid=%d", r.cmd.Process.Pid)
	r.running = true
	r.mu.Unlock()

	go r.parseProgress(stderr)

	go func() {
		err := r.cmd.Wait()
		r.mu.Lock()
		r.running = false
		r.mu.Unlock()

		if err != nil && ctx.Err() == nil {
			logging.Log("ffmpeg exited with error: %v", err)
		} else {
			logging.Log("ffmpeg exited cleanly")
		}
		close(r.progressChan)
		close(r.done)
	}()

	return nil
}

func (r *Recorder) parseProgress(stderr io.Reader) {
	scanner := bufio.NewScanner(stderr)
	scanner.Split(scanFFmpegLines)

	for scanner.Scan() {
		if stats := parseProgressLine(scanner.Text()); stats != nil {
			select {
			case r.progressChan <- *stats:
			default:
			}
		}
	}
}

func scanFFmpegLines(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	for i := 0; i < len(data); i++ {
		if data[i] == '\r' || data[i] == '\n' {
			return i + 1, data[:i], nil
		}
	}
	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil
}

func parseProgressLine(line string) *RecordingStats {
	m := progressRegex.FindStringSubmatch(line)
	if m == nil {
		return nil
	}
	sizeVal, _ := strconv.ParseInt(m[1], 10, 64)
	bitrate, _ := strconv.ParseFloat(m[4], 64)
	speed, _ := strconv.ParseFloat(m[5], 64)

	return &RecordingStats{
		Duration: parseFFmpegTime(m[3]),
		Size:     convertToBytes(sizeVal, m[2]),
		Bitrate:  bitrate,
		Speed:    speed,
	}
}

func convertToBytes(size int64, unit string) int64 {
	switch strings.ToUpper(unit) {
	case "B", "":
		return size
	case "KB":
		return size * 1000
	case "KIB":
		return size * 1024
	case "MB":
		return size * 1000 * 1000
	case "MIB":
		return size * 1024 * 1024
	case "GB":
		return size * 1000 * 1000 * 1000
	case "GIB":
		return size * 1024 * 1024 * 1024
	default:
		return size * 1024
	}
}

func parseFFmpegTime(timeStr string) time.Duration {
	parts := strings.Split(timeStr, ":")
	if len(parts) != 3 {
		return 0
	}

	hours, _ := strconv.Atoi(parts[0])
	minutes, _ := strconv.Atoi(parts[1])

	secParts := strings.Split(parts[2], ".")
	seconds, _ := strconv.Atoi(secParts[0])
	var ms int
	if len(secParts) > 1 {
		ms, _ = strconv.Atoi(secParts[1])
		for len(secParts[1]) < 3 {
			ms *= 10
			secParts[1] += "0"
		}
	}

	return time.Duration(hours)*time.Hour +
		time.Duration(minutes)*time.Minute +
		time.Duration(seconds)*time.Second +
		time.Duration(ms)*time.Millisecond
}

func (r *Recorder) Stop() error {
	r.mu.Lock()
	if !r.running {
		r.mu.Unlock()
		return nil
	}
	done := r.done
	cmd := r.cmd
	r.mu.Unlock()

	if cmd != nil && cmd.Process != nil {
		cmd.Process.Signal(syscall.SIGINT)

		select {
		case <-done:
		case <-time.After(5 * time.Second):
			cmd.Process.Kill()
			<-done
		}
	}

	return nil
}

func (r *Recorder) ProgressChan() <-chan RecordingStats {
	return r.progressChan
}

func (r *Recorder) IsRunning() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.running
}

func (r *Recorder) OutputPath() string {
	return r.outputPath
}

func ConcatSegments(segments []string, outputPath string) error {
	if len(segments) < 2 {
		return fmt.Errorf("need at least 2 segments to concat, got %d", len(segments))
	}

	listFile, err := os.CreateTemp("", "recgo-concat-*.txt")
	if err != nil {
		return fmt.Errorf("create concat list: %w", err)
	}
	defer os.Remove(listFile.Name())

	for _, seg := range segments {
		abs, err := filepath.Abs(seg)
		if err != nil {
			listFile.Close()
			return fmt.Errorf("abs path %s: %w", seg, err)
		}
		escaped := strings.ReplaceAll(abs, "'", "'\\''")
		if _, err := fmt.Fprintf(listFile, "file '%s'\n", escaped); err != nil {
			listFile.Close()
			return fmt.Errorf("write concat list: %w", err)
		}
	}
	if err := listFile.Close(); err != nil {
		return fmt.Errorf("close concat list: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ffmpeg",
		"-f", "concat", "-safe", "0", "-i", listFile.Name(),
		"-c", "copy", "-y", outputPath)
	out, err := cmd.CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("ffmpeg concat timed out after 2m")
	}
	if err != nil {
		return fmt.Errorf("ffmpeg concat: %w (output: %s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}
