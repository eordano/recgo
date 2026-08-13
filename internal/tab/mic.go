package tab

import (
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/eordano/recgo/internal/audio"
)

const (
	micRate     = 16_000
	micChannels = 1
	toneFreqHz  = 1000.0
)

type MicOptions struct {
	Clock     *Clock
	OutDir    string
	Device    string
	FFmpegBin string
}

type Mic struct {
	opts    MicOptions
	device  string
	cmd     *exec.Cmd
	rawPath string
	wavPath string

	mu        sync.Mutex
	bytes     int64
	stderr    strings.Builder
	firstByte bool

	toneSessionMs float64
	toneRequested bool

	done chan struct{}
}

type MicResult struct {
	Err      error
	WavPath  string
	RawPath  string
	Bytes    int64
	Duration float64
	Note     string
}

func StartMic(o MicOptions) (*Mic, error) {
	if o.FFmpegBin == "" {
		o.FFmpegBin = "ffmpeg"
	}

	device := o.Device
	if device == "" {
		if runtime.GOOS == "darwin" {
			// darwin CheckBackend hardcodes LookPath("ffmpeg"); honor -ffmpeg here.
			if _, err := exec.LookPath(o.FFmpegBin); err != nil {
				return nil, fmt.Errorf("ffmpeg binary not found (%s): %w", o.FFmpegBin, err)
			}
		} else if err := audio.CheckBackend(); err != nil {
			return nil, err
		}
		d, err := audio.GetDefaultSource()
		if err != nil {
			return nil, fmt.Errorf("could not resolve default capture source: %w", err)
		}
		if d == "" {
			return nil, fmt.Errorf("could not resolve default capture source: backend returned no device")
		}
		device = d
	}

	m := &Mic{
		opts:    o,
		device:  device,
		rawPath: filepath.Join(o.OutDir, "audio.pcm"),
		wavPath: filepath.Join(o.OutDir, "audio.wav"),
		done:    make(chan struct{}),
	}

	args := append([]string{"-hide_banner", "-loglevel", "warning"}, audio.FFmpegInputArgs(device)...)
	args = append(args,
		"-ac", fmt.Sprint(micChannels),
		"-ar", fmt.Sprint(micRate),
		"-f", "s16le", "pipe:1")

	m.cmd = exec.Command(o.FFmpegBin, args...)
	m.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	stdout, err := m.cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderrPipe, err := m.cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := m.cmd.Start(); err != nil {
		return nil, fmt.Errorf("start ffmpeg: %w", err)
	}

	raw, err := os.OpenFile(m.rawPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, sessionFileMode)
	if err != nil {
		return nil, err
	}

	go func() {
		defer close(m.done)
		defer raw.Close()
		buf := make([]byte, 32<<10)
		for {
			n, err := stdout.Read(buf)
			if n > 0 {
				m.mu.Lock()
				if !m.firstByte {
					m.firstByte = true
					m.opts.Clock.SetAudioStart(m.opts.Clock.Now(),
						"Anchored to ffmpeg's first PCM byte. True capture start is earlier by "+
							"ffmpeg's input buffering (tens of ms, device dependent). Uncorrected.")
				}
				m.bytes += int64(n)
				m.mu.Unlock()
				raw.Write(buf[:n])
			}
			if err != nil {
				return
			}
		}
	}()

	go func() {
		b, _ := io.ReadAll(stderrPipe)
		m.mu.Lock()
		m.stderr.Write(b)
		m.mu.Unlock()
	}()

	return m, nil
}

func (m *Mic) EmitCalibrationTone(cdp *CDP, clock *Clock) error {
	var res struct {
		Result struct {
			Value struct {
				TimeOrigin float64 `json:"timeOrigin"`
				PageTime   float64 `json:"pageTime"`
			} `json:"value"`
		} `json:"result"`
	}
	if err := cdp.Send("Runtime.evaluate", map[string]any{
		"expression":    fmt.Sprintf("window.__rtTone && window.__rtTone(%g, 120)", toneFreqHz),
		"returnByValue": true,
	}, &res); err != nil {
		return err
	}
	if res.Result.Value.TimeOrigin == 0 {
		return fmt.Errorf("page did not emit a tone (injected script missing?)")
	}
	t, ok := clock.FromPageTime(res.Result.Value.TimeOrigin, res.Result.Value.PageTime)
	if !ok {
		return fmt.Errorf("browser clock not calibrated")
	}

	m.mu.Lock()
	m.toneSessionMs = t
	m.toneRequested = true
	m.mu.Unlock()
	return nil
}

func (m *Mic) Stop() MicResult {
	if m.cmd == nil || m.cmd.Process == nil {
		return MicResult{Err: fmt.Errorf("mic never started")}
	}

	m.cmd.Process.Signal(syscall.SIGINT)
	select {
	case <-m.done:
	case <-time.After(5 * time.Second):
		m.cmd.Process.Kill()
		<-m.done
	}
	m.cmd.Wait()

	m.mu.Lock()
	n := m.bytes
	stderr := strings.TrimSpace(m.stderr.String())
	toneAt := m.toneSessionMs
	toneWanted := m.toneRequested
	m.mu.Unlock()

	if n == 0 {
		msg := "ffmpeg produced no audio"
		if stderr != "" {
			msg += ": " + tail(stderr, 400)
		}
		return MicResult{Err: fmt.Errorf("%s", msg), RawPath: m.rawPath}
	}

	if err := writeWAV(m.rawPath, m.wavPath, n); err != nil {
		return MicResult{Err: err, RawPath: m.rawPath}
	}

	note := m.opts.Clock.AudioStartNote
	if toneWanted {
		if onsetMs, ok := findToneOnset(m.wavPath, toneFreqHz); ok {
			corrected := toneAt - onsetMs
			prev := m.opts.Clock.AudioStartMs
			note = fmt.Sprintf(
				"Anchored by calibration tone: played at t=%.1fms, found %.1fms into the "+
					"recording, so sample zero is t=%.1fms. First-byte estimate was %.1fms "+
					"(ffmpeg capture latency measured at %.1fms).",
				toneAt, onsetMs, corrected, prev, prev-corrected)
			m.opts.Clock.SetAudioStart(corrected, note)
		} else {
			note += " Calibration tone was emitted but not found in the capture " +
				"(headphones, or the tone was inaudible to this device); anchor left uncorrected."
			m.opts.Clock.SetAudioStart(m.opts.Clock.AudioStartMs, note)
		}
	}

	return MicResult{
		WavPath:  m.wavPath,
		RawPath:  m.rawPath,
		Bytes:    n,
		Duration: float64(n) / float64(micRate*micChannels*2),
		Note:     note,
	}
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

func writeWAV(rawPath, wavPath string, n int64) error {
	src, err := os.Open(rawPath)
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := os.OpenFile(wavPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, sessionFileMode)
	if err != nil {
		return err
	}
	defer dst.Close()

	byteRate := uint32(micRate * micChannels * 2)
	h := make([]byte, 44)
	copy(h[0:], "RIFF")
	binary.LittleEndian.PutUint32(h[4:], uint32(36+n))
	copy(h[8:], "WAVE")
	copy(h[12:], "fmt ")
	binary.LittleEndian.PutUint32(h[16:], 16)
	binary.LittleEndian.PutUint16(h[20:], 1)
	binary.LittleEndian.PutUint16(h[22:], micChannels)
	binary.LittleEndian.PutUint32(h[24:], micRate)
	binary.LittleEndian.PutUint32(h[28:], byteRate)
	binary.LittleEndian.PutUint16(h[32:], micChannels*2)
	binary.LittleEndian.PutUint16(h[34:], 16)
	copy(h[36:], "data")
	binary.LittleEndian.PutUint32(h[40:], uint32(n))

	if _, err := dst.Write(h); err != nil {
		return err
	}
	_, err = io.Copy(dst, src)
	return err
}

func findToneOnset(wavPath string, freq float64) (float64, bool) {
	data, err := os.ReadFile(wavPath)
	if err != nil || len(data) <= 44 {
		return 0, false
	}
	pcm := data[44:]
	total := len(pcm) / 2

	const windowSamples = 160
	if total < windowSamples*4 {
		return 0, false
	}

	k := math.Round(float64(windowSamples) * freq / micRate)
	coeff := 2 * math.Cos(2*math.Pi*k/float64(windowSamples))

	windows := total / windowSamples
	mags := make([]float64, windows)
	for w := 0; w < windows; w++ {
		var s0, s1, s2 float64
		for i := 0; i < windowSamples; i++ {
			idx := (w*windowSamples + i) * 2
			sample := float64(int16(binary.LittleEndian.Uint16(pcm[idx:]))) / 32768.0
			s0 = coeff*s1 - s2 + sample
			s2, s1 = s1, s0
		}
		mags[w] = math.Sqrt(s1*s1 + s2*s2 - coeff*s1*s2)
	}

	sorted := append([]float64(nil), mags...)
	for i := 1; i < len(sorted); i++ {
		for j := i; j > 0 && sorted[j] < sorted[j-1]; j-- {
			sorted[j], sorted[j-1] = sorted[j-1], sorted[j]
		}
	}
	median := sorted[len(sorted)/2]
	peak := sorted[len(sorted)-1]

	if peak < median*8 || peak < 0.01 {
		return 0, false
	}
	threshold := median + (peak-median)*0.5

	for w, mag := range mags {
		if mag >= threshold {
			return float64(w*windowSamples) / micRate * 1000, true
		}
	}
	return 0, false
}
