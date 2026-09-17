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
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/eordano/recgo/internal/audio"
	"github.com/eordano/recgo/internal/logging"
)

const (
	SampleRate      = 16000
	sampleRate      = SampleRate
	bytesPerSample  = 2
	qualityInterval = 3 * time.Second
	minChunkSeconds = 0.5
	maxWindow       = 25 * time.Second
	maxRequestWait  = 90 * time.Second
	stderrTailMax   = 2048

	// whisper only reads the trailing ~224 tokens of the prompt; shipping the
	// whole session transcript every pass grows quadratically (and would
	// approach ARG_MAX as a whisper-cli argv element), so only the tail goes.
	promptTailBytes = 1024
)

func promptTail(s string) string {
	if len(s) <= promptTailBytes {
		return s
	}
	cut := s[len(s)-promptTailBytes:]
	if i := strings.IndexByte(cut, ' '); i >= 0 && i+1 < len(cut) {
		cut = cut[i+1:]
	}
	return cut
}

type Config struct {
	Endpoint string
	APIKey   string
	Model    string
	// Realtime streams the audio over the endpoint's /v1/realtime websocket
	// as it is captured, instead of posting a chunk every few seconds.
	Realtime bool

	LocalBin      string
	LocalModel    string
	LocalVADModel string
}

func (c Config) withDefaults() Config {
	if c.Model == "" {
		c.Model = "whisper"
	}
	if c.LocalBin == "" {
		c.LocalBin = "whisper-cli"
	}
	return c
}

func (c Config) usesLocal() bool {
	return c.Endpoint == "" && c.LocalModel != ""
}

func (c Config) configured() bool {
	return c.Endpoint != "" || c.LocalModel != ""
}

type State struct {
	Locked  string
	Pending string
	Fast    string
	Err     error

	PassText        string
	PassStartSample int
	PassEndSample   int

	// Speaker is who the STT endpoint heard in PassText: the registry name
	// when it has one, else the cluster id, empty when nothing matched or
	// the endpoint does not identify speakers.
	Speaker      string
	SpeakerScore float64
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

	interval time.Duration
	rtSent   int

	updates chan State
	emitMu  sync.Mutex
	closed  bool
	once    sync.Once

	passFailed     atomic.Bool
	promptRejected atomic.Bool

	tmpWav string
}

func New(parent context.Context, mic string, cfg Config) *Session {
	ctx, cancel := context.WithCancel(parent)
	return &Session{
		ctx:      ctx,
		cancel:   cancel,
		mic:      mic,
		cfg:      cfg.withDefaults(),
		interval: qualityInterval,
		updates:  make(chan State, 16),
	}
}

// NewFeed builds a session that transcribes PCM the caller pushes via Feed
// (16kHz mono s16le) instead of capturing a device itself.
func NewFeed(parent context.Context, cfg Config) *Session {
	return New(parent, "", cfg)
}

func (s *Session) StartFeed() error {
	if !s.cfg.configured() {
		return fmt.Errorf("no transcription backend configured -- set [transcription.remote] endpoint in the recgo config, or provide a local whisper model")
	}
	s.setupTmpWav()
	if s.cfg.usesRealtime() {
		go s.realtimeLoop()
	} else {
		go s.transcribeLoop()
	}
	return nil
}

// One temp WAV per session, rewritten each local pass, instead of a
// create/unlink cycle every 3s for the whole recording. transcribeLoop owns
// its removal; passes run synchronously inside the loop.
func (s *Session) setupTmpWav() {
	if !s.cfg.usesLocal() {
		return
	}
	if f, err := os.CreateTemp("", "recgo-live-*.wav"); err == nil {
		s.tmpWav = f.Name()
		f.Close()
	}
}

func (s *Session) Feed(pcm []byte) {
	s.pcmMu.Lock()
	s.pcm = append(s.pcm, pcm...)
	s.pcmMu.Unlock()
}

func (s *Session) Start() error {
	if !s.cfg.configured() {
		return fmt.Errorf("no transcription backend configured -- set [transcription.remote] endpoint in the recgo config, or provide a local whisper model")
	}
	s.setupTmpWav()
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
	if s.cfg.usesRealtime() {
		go s.realtimeLoop()
	}

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
	if !s.cfg.usesRealtime() {
		go s.transcribeLoop()
	}
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
		// Take emitMu so no in-flight emit is holding a send on the channel
		// when it closes; emit checks closed under the same lock.
		s.emitMu.Lock()
		s.closed = true
		close(s.updates)
		s.emitMu.Unlock()
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
	if s.tmpWav != "" {
		defer os.Remove(s.tmpWav)
	}
	ticker := time.NewTicker(s.interval)
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
	prompt := promptTail(strings.TrimSpace(s.locked))
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
	var result string
	var err error
	if s.cfg.usesLocal() {
		result, err = s.whisperLocal(chunk, prompt)
	} else {
		result, err = s.whisper(chunk, s.cfg.Model, prompt)
	}
	dur := time.Since(t0)
	sentSec := float64(len(chunk)) / float64(sampleRate*bytesPerSample)
	unreadSec := float64(fullSamples) / float64(sampleRate)
	if err != nil {
		logging.Log("transcribe: pass failed (%dms, sent=%.1fs unread=%.1fs): %v", dur.Milliseconds(), sentSec, unreadSec, err)
		// Surface the first failure to the Updates consumer; without this a
		// missing binary, bad model, or dead endpoint looks identical to the
		// user simply not speaking.
		if s.ctx.Err() == nil && s.passFailed.CompareAndSwap(false, true) {
			s.emit(State{Err: fmt.Errorf("live transcription pass failed: %w", err)})
		}
		return
	}
	s.passFailed.Store(false)
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
	snap.PassText = result
	snap.PassStartSample = skipSamples + dropped/bytesPerSample
	snap.PassEndSample = skipSamples + fullSamples
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
	s.emitMu.Lock()
	defer s.emitMu.Unlock()
	if s.closed {
		return
	}
	select {
	case s.updates <- st:
	default:
	}
}

// Some OpenAI-compatible endpoints (e.g. speaches-plus) reject any decoder
// prompt with a 400 rather than silently dropping it. Detect that once, retry
// the pass without the prompt, and omit it for the rest of the session.
func (s *Session) whisper(pcm []byte, model, prompt string) (string, error) {
	if s.promptRejected.Load() {
		prompt = ""
	}
	text, err := s.whisperOnce(pcm, model, prompt)
	if err != nil && prompt != "" && isPromptRejected(err) {
		s.promptRejected.Store(true)
		logging.Log("transcribe: endpoint rejects decoder prompt, omitting it from now on: %v", err)
		return s.whisperOnce(pcm, model, "")
	}
	return text, err
}

func isPromptRejected(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "whisper http 400") && strings.Contains(msg, "prompt")
}

func (s *Session) whisperOnce(pcm []byte, model, prompt string) (string, error) {
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

func (s *Session) whisperLocal(pcm []byte, prompt string) (string, error) {
	path := s.tmpWav
	if path == "" {
		f, err := os.CreateTemp("", "recgo-live-*.wav")
		if err != nil {
			return "", err
		}
		path = f.Name()
		f.Close()
		defer os.Remove(path)
	}
	if err := os.WriteFile(path, pcmToWAV(pcm, sampleRate), 0o600); err != nil {
		return "", err
	}

	ctx, cancel := context.WithTimeout(s.ctx, maxRequestWait)
	defer cancel()
	args := []string{"-m", s.cfg.LocalModel, "-f", path, "-np", "-nt"}
	if s.cfg.LocalVADModel != "" {
		args = append(args, "--vad", "--vad-model", s.cfg.LocalVADModel)
	}
	if prompt != "" {
		args = append(args, "--prompt", prompt)
	}
	cmd := exec.CommandContext(ctx, s.cfg.LocalBin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		tail := strings.TrimSpace(stderr.String())
		if len(tail) > 200 {
			tail = tail[len(tail)-200:]
		}
		return "", fmt.Errorf("%s: %v: %s", s.cfg.LocalBin, err, tail)
	}
	text := strings.Join(strings.Fields(stdout.String()), " ")
	if strings.HasPrefix(text, "[") && strings.HasSuffix(text, "]") {
		text = ""
	}
	if strings.HasPrefix(text, "(") && strings.HasSuffix(text, ")") {
		text = ""
	}
	return text, nil
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
