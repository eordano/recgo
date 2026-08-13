package transcribe

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/eordano/recgo/internal/audio"
	"github.com/eordano/recgo/internal/logging"
)

const (
	sampleRate      = 16000
	bytesPerSample  = 2
	qualityInterval = 3 * time.Second
	minChunkSeconds = 0.5
	maxWindow       = 25 * time.Second
	maxRequestWait  = 90 * time.Second
	stderrTailMax   = 2048
)

type Config struct {
	Endpoint string
	APIKey   string
	Model    string
}

func (c Config) withDefaults() Config {
	if c.Model == "" {
		c.Model = "whisper"
	}
	return c
}

type State struct {
	Locked  string
	Pending string
	Fast    string
	Err     error
}

type Session struct {
	ctx    context.Context
	cancel context.CancelFunc

	mic string
	cfg Config

	cmd      *exec.Cmd
	procDone chan struct{}

	pcmMu sync.Mutex
	pcm   []byte

	stderrMu   sync.Mutex
	stderrTail []byte

	stateMu sync.RWMutex
	locked  string
	lockedN int
	gen     int

	inflight sync.Mutex

	updates chan State
	once    sync.Once
}

func New(parent context.Context, mic string, cfg Config) *Session {
	ctx, cancel := context.WithCancel(parent)
	return &Session{
		ctx:     ctx,
		cancel:  cancel,
		mic:     mic,
		cfg:     cfg.withDefaults(),
		updates: make(chan State, 16),
	}
}

func (s *Session) Start() error {
	if s.cfg.Endpoint == "" {
		return fmt.Errorf("no transcription endpoint configured — set [transcription.remote] endpoint in the recgo config")
	}
	var args []string
	if runtime.GOOS == "darwin" {
		args = append(audio.FFmpegInputArgs(s.mic),
			"-ac", "1", "-ar", fmt.Sprintf("%d", sampleRate),
			"-f", "s16le",
			"-loglevel", "error",
			"-",
		)
	} else {
		args = []string{
			"-f", "pulse", "-i", s.mic,
			"-ac", "1", "-ar", fmt.Sprintf("%d", sampleRate),
			"-f", "s16le",
			"-loglevel", "error",
			"-",
		}
	}
	cmd := exec.CommandContext(s.ctx, "ffmpeg", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("ffmpeg stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("ffmpeg stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("ffmpeg start: %w", err)
	}
	s.cmd = cmd
	s.procDone = make(chan struct{})
	logging.Log("transcribe: ffmpeg pid=%d mic=%s", cmd.Process.Pid, s.mic)

	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		s.consumeStderr(stderr)
	}()
	go func() {
		defer close(s.procDone)
		s.consumePCM(stdout)
		<-stderrDone
		err := cmd.Wait()
		if s.ctx.Err() != nil {
			return
		}
		if err == nil {
			err = fmt.Errorf("stream ended")
		}
		s.stderrMu.Lock()
		tail := strings.TrimSpace(string(s.stderrTail))
		s.stderrMu.Unlock()
		msg := fmt.Errorf("ffmpeg exited: %v", err)
		if tail != "" {
			msg = fmt.Errorf("ffmpeg exited: %v: %s", err, tail)
		}
		logging.Log("transcribe: %v", msg)
		s.emit(State{Err: msg})
	}()
	go s.transcribeLoop()
	return nil
}

func (s *Session) Updates() <-chan State {
	return s.updates
}

func (s *Session) Stop() {
	s.once.Do(func() {
		s.stateMu.Lock()
		s.gen++
		s.stateMu.Unlock()
		s.cancel()
		if s.cmd != nil && s.cmd.Process != nil {
			_ = s.cmd.Process.Kill()
			<-s.procDone
		}
		close(s.updates)
	})
}

func (s *Session) consumePCM(r io.Reader) {
	buf := make([]byte, 8192)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			s.pcmMu.Lock()
			s.pcm = append(s.pcm, buf[:n]...)
			s.pcmMu.Unlock()
		}
		if err != nil {
			return
		}
		if s.ctx.Err() != nil {
			return
		}
	}
}

func (s *Session) consumeStderr(r io.Reader) {
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			s.stderrMu.Lock()
			s.stderrTail = append(s.stderrTail, buf[:n]...)
			if len(s.stderrTail) > stderrTailMax {
				s.stderrTail = s.stderrTail[len(s.stderrTail)-stderrTailMax:]
			}
			s.stderrMu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

func (s *Session) transcribeLoop() {
	ticker := time.NewTicker(qualityInterval)
	defer ticker.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
			if !s.inflight.TryLock() {
				continue
			}
			s.doPass()
			s.inflight.Unlock()
		}
	}
}

func (s *Session) doPass() {
	s.stateMu.RLock()
	skipSamples := s.lockedN
	gen0 := s.gen
	prompt := strings.TrimSpace(s.locked)
	s.stateMu.RUnlock()

	chunk := s.snapshotTail(skipSamples)
	minBytes := int(minChunkSeconds * float64(sampleRate*bytesPerSample))
	if len(chunk) < minBytes {
		return
	}

	fullSamples := len(chunk) / bytesPerSample

	maxBytes := int(maxWindow.Seconds()) * sampleRate * bytesPerSample
	dropped := 0
	if len(chunk) > maxBytes {
		dropped = len(chunk) - maxBytes
		chunk = chunk[dropped:]
	}

	t0 := time.Now()
	result, err := s.whisper(chunk, s.cfg.Model, prompt)
	dur := time.Since(t0)
	sentSec := float64(len(chunk)) / float64(sampleRate*bytesPerSample)
	unreadSec := float64(fullSamples) / float64(sampleRate)
	if err != nil {
		logging.Log("transcribe: pass failed (%dms, sent=%.1fs unread=%.1fs): %v", dur.Milliseconds(), sentSec, unreadSec, err)
		return
	}
	if dropped > 0 {
		logging.Log("transcribe: pass ok (%dms, sent=%.1fs of %.1fs unread, dropped %.1fs) -> %q", dur.Milliseconds(), sentSec, unreadSec, float64(dropped)/float64(sampleRate*bytesPerSample), result)
	} else {
		logging.Log("transcribe: pass ok (%dms, sent=%.1fs) -> %q", dur.Milliseconds(), sentSec, result)
	}

	s.stateMu.Lock()
	if gen0 != s.gen {
		s.stateMu.Unlock()
		return
	}
	if s.locked != "" && result != "" {
		s.locked += " "
	}
	s.locked += result
	s.lockedN += fullSamples
	snap := s.snapshot()
	s.stateMu.Unlock()
	s.emit(snap)
}

func (s *Session) snapshotTail(skipSamples int) []byte {
	skipBytes := skipSamples * bytesPerSample
	s.pcmMu.Lock()
	defer s.pcmMu.Unlock()
	if skipBytes >= len(s.pcm) {
		return nil
	}
	out := make([]byte, len(s.pcm)-skipBytes)
	copy(out, s.pcm[skipBytes:])
	return out
}

func (s *Session) snapshot() State {
	return State{Locked: s.locked}
}

func (s *Session) emit(st State) {
	select {
	case s.updates <- st:
	case <-s.ctx.Done():
	default:
	}
}

func (s *Session) whisper(pcm []byte, model, prompt string) (string, error) {
	wav := pcmToWAV(pcm, sampleRate)

	var body bytes.Buffer
	mp := multipart.NewWriter(&body)
	_ = mp.WriteField("model", model)
	_ = mp.WriteField("response_format", "json")
	if prompt != "" {
		_ = mp.WriteField("prompt", prompt)
	}
	fw, err := mp.CreateFormFile("file", "audio.wav")
	if err != nil {
		return "", err
	}
	if _, err := fw.Write(wav); err != nil {
		return "", err
	}
	if err := mp.Close(); err != nil {
		return "", err
	}

	reqCtx, cancel := context.WithTimeout(s.ctx, maxRequestWait)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, "POST", strings.TrimRight(s.cfg.Endpoint, "/")+"/audio/transcriptions", &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", mp.FormDataContentType())
	if s.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.cfg.APIKey)
	}

	client := &http.Client{Timeout: maxRequestWait}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		snip := string(data)
		if len(snip) > 200 {
			snip = snip[:200]
		}
		return "", fmt.Errorf("whisper http %d: %s", resp.StatusCode, snip)
	}

	var out struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		return "", fmt.Errorf("parse whisper json: %w", err)
	}
	return strings.TrimSpace(out.Text), nil
}

func pcmToWAV(pcm []byte, rate int) []byte {
	n := uint32(len(pcm))
	buf := bytes.NewBuffer(make([]byte, 0, 44+len(pcm)))
	buf.WriteString("RIFF")
	_ = binary.Write(buf, binary.LittleEndian, uint32(36+n))
	buf.WriteString("WAVE")
	buf.WriteString("fmt ")
	_ = binary.Write(buf, binary.LittleEndian, uint32(16))
	_ = binary.Write(buf, binary.LittleEndian, uint16(1))
	_ = binary.Write(buf, binary.LittleEndian, uint16(1))
	_ = binary.Write(buf, binary.LittleEndian, uint32(rate))
	_ = binary.Write(buf, binary.LittleEndian, uint32(rate*bytesPerSample))
	_ = binary.Write(buf, binary.LittleEndian, uint16(bytesPerSample))
	_ = binary.Write(buf, binary.LittleEndian, uint16(16))
	buf.WriteString("data")
	_ = binary.Write(buf, binary.LittleEndian, n)
	buf.Write(pcm)
	return buf.Bytes()
}
