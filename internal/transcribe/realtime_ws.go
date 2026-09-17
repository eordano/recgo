package transcribe

import (
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"github.com/eordano/recgo/internal/logging"
)

const (
	realtimeRate     = 24000
	realtimeTick     = 100 * time.Millisecond
	realtimeMaxRetry = 10 * time.Second
)

// RealtimeURL turns an OpenAI-compatible base URL into the websocket of its
// realtime lane: https://host/v1 -> wss://host/v1/realtime?intent=transcription.
func RealtimeURL(endpoint string) (string, error) {
	u, err := url.Parse(strings.TrimRight(endpoint, "/"))
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	case "http":
		u.Scheme = "ws"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("realtime: unsupported endpoint scheme %q", u.Scheme)
	}
	if !strings.HasSuffix(u.Path, "/realtime") {
		u.Path = strings.TrimRight(u.Path, "/") + "/realtime"
	}
	q := u.Query()
	if q.Get("intent") == "" {
		q.Set("intent", "transcription")
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func envInt(name string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(name)); err == nil && v > 0 {
		return v
	}
	return def
}

func (c Config) usesRealtime() bool {
	return c.Realtime && c.Endpoint != ""
}

func (s *Session) realtimeLoop() {
	backoff := time.Second
	for s.ctx.Err() == nil {
		err := s.realtimeOnce()
		if s.ctx.Err() != nil {
			return
		}
		if err != nil {
			logging.Log("transcribe: realtime: %v", err)
			if s.passFailed.CompareAndSwap(false, true) {
				s.emit(State{Err: fmt.Errorf("live transcription (realtime) failed: %w", err)})
			}
		}
		select {
		case <-s.ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < realtimeMaxRetry {
			backoff *= 2
		}
	}
}

type realtimeEvent struct {
	Type         string           `json:"type"`
	ItemID       string           `json:"item_id"`
	Delta        string           `json:"delta"`
	Transcript   string           `json:"transcript"`
	Text         string           `json:"text"`
	AudioStartMs *float64         `json:"audio_start_ms"`
	AudioEndMs   *float64         `json:"audio_end_ms"`
	Error        json.RawMessage  `json:"error"`
	Speaker      *realtimeSpeaker `json:"speaker"`
}

// realtimeSpeaker is speaches' per-utterance speaker match against its voices
// workspace registry (cosine of the WeSpeaker embedding to a cluster centroid,
// only reported at or above the registry's link threshold).
type realtimeSpeaker struct {
	ID    string  `json:"id"`
	Name  *string `json:"name"`
	Score float64 `json:"score"`
}

func (r *realtimeSpeaker) label() string {
	if r == nil {
		return ""
	}
	if r.Name != nil && strings.TrimSpace(*r.Name) != "" {
		return strings.TrimSpace(*r.Name)
	}
	return r.ID
}

func (s *Session) realtimeOnce() error {
	target, err := RealtimeURL(s.cfg.Endpoint)
	if err != nil {
		return err
	}
	hdr := http.Header{}
	if s.cfg.APIKey != "" {
		hdr.Set("Authorization", "Bearer "+s.cfg.APIKey)
	}
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}
	conn, resp, err := dialer.DialContext(s.ctx, target, hdr)
	if err != nil {
		if resp != nil {
			return fmt.Errorf("dial %s: %v (http %d)", target, err, resp.StatusCode)
		}
		return fmt.Errorf("dial %s: %w", target, err)
	}
	defer conn.Close()

	setup := map[string]any{
		"type": "session.update",
		"session": map[string]any{
			// Narration pauses mid-sentence; the upstream default (about half
			// a second of silence) chops it into fragments, so the turn waits
			// longer before it closes. RECGO_REALTIME_SILENCE_MS overrides.
			"turn_detection": map[string]any{
				"type": "server_vad", "create_response": false, "interrupt_response": false,
				"threshold": 0.5, "prefix_padding_ms": 300,
				"silence_duration_ms": envInt("RECGO_REALTIME_SILENCE_MS", 900),
			},
			"input_audio_format":  "pcm16",
			"output_audio_format": "pcm16",
		},
	}
	if err := conn.WriteJSON(setup); err != nil {
		return fmt.Errorf("session.update: %w", err)
	}
	logging.Log("transcribe: realtime connected to %s", target)

	// Audio recorded before this connection is not replayed: the server's
	// clock starts at this connection's first sample, so base maps its
	// millisecond offsets back onto the session's sample count.
	s.pcmMu.Lock()
	base := len(s.pcm) / bytesPerSample
	s.pcmMu.Unlock()

	ctx, cancel := context.WithCancel(s.ctx)
	defer cancel()
	sendErr := make(chan error, 1)
	go func() {
		sendErr <- s.realtimeSend(ctx, conn, base)
	}()

	partials := map[string]string{}
	var utterStart, utterEnd int
	utterStart = -1
	for {
		select {
		case err := <-sendErr:
			return err
		default:
		}
		_ = conn.SetReadDeadline(time.Now().Add(realtimeMaxRetry * 6))
		_, data, err := conn.ReadMessage()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("read: %w", err)
		}
		var ev realtimeEvent
		if json.Unmarshal(data, &ev) != nil {
			continue
		}
		switch ev.Type {
		case "error":
			return fmt.Errorf("server error: %s", strings.TrimSpace(string(ev.Error)))
		case "input_audio_buffer.speech_started":
			utterStart = s.realtimePos(base, ev.AudioStartMs)
			utterEnd = -1
		case "input_audio_buffer.speech_stopped":
			utterEnd = s.realtimePos(base, ev.AudioEndMs)
		case "input_audio_buffer.partial_transcription",
			"conversation.item.input_audio_transcription.delta":
			said := ev.Transcript
			if said == "" {
				said = ev.Text
			}
			if strings.HasSuffix(ev.Type, ".delta") {
				said = partials[ev.ItemID] + ev.Delta
			}
			partials[ev.ItemID] = said
			if strings.TrimSpace(said) != "" {
				s.stateMu.RLock()
				st := State{Locked: s.locked, Fast: said}
				s.stateMu.RUnlock()
				s.emit(st)
			}
		case "conversation.item.input_audio_transcription.completed":
			delete(partials, ev.ItemID)
			said := strings.TrimSpace(ev.Transcript)
			if said == "" {
				continue
			}
			start := utterStart
			if start < 0 {
				start = s.realtimePos(base, nil)
			}
			end := utterEnd
			if end < 0 {
				end = s.realtimePos(base, nil)
			}
			utterStart, utterEnd = -1, -1
			s.stateMu.Lock()
			if s.locked != "" {
				s.locked += " "
			}
			s.locked += said
			st := State{Locked: s.locked, PassText: said, PassStartSample: start, PassEndSample: end,
				Speaker: ev.Speaker.label()}
			if ev.Speaker != nil {
				st.SpeakerScore = ev.Speaker.Score
			}
			s.stateMu.Unlock()
			s.passFailed.Store(false)
			logging.Log("transcribe: realtime heard %q [%d..%d] speaker=%q", said, start, end, st.Speaker)
			s.emit(st)
		}
	}
}

// realtimePos maps a server millisecond offset (relative to this connection's
// audio) onto the session's 16 kHz sample index; without one it is the send
// position.
func (s *Session) realtimePos(base int, ms *float64) int {
	if ms != nil {
		return base + int(*ms*float64(sampleRate)/1000)
	}
	s.pcmMu.Lock()
	defer s.pcmMu.Unlock()
	return s.rtSent
}

func (s *Session) realtimeSend(ctx context.Context, conn *websocket.Conn, base int) error {
	ticker := time.NewTicker(realtimeTick)
	defer ticker.Stop()
	s.pcmMu.Lock()
	s.rtSent = base
	s.pcmMu.Unlock()
	var carry int16
	haveCarry := false
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		s.pcmMu.Lock()
		from := s.rtSent * bytesPerSample
		var chunk []byte
		if from < len(s.pcm) {
			chunk = make([]byte, len(s.pcm)-from)
			copy(chunk, s.pcm[from:])
			s.rtSent = len(s.pcm) / bytesPerSample
		}
		s.pcmMu.Unlock()
		if len(chunk) < bytesPerSample {
			continue
		}
		out := upsample16to24(chunk, &carry, &haveCarry)
		msg := map[string]string{
			"type":  "input_audio_buffer.append",
			"audio": base64.StdEncoding.EncodeToString(out),
		}
		_ = conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
		if err := conn.WriteJSON(msg); err != nil {
			return fmt.Errorf("append: %w", err)
		}
	}
}

// upsample16to24 linearly interpolates 16 kHz s16le mono to 24 kHz. carry
// keeps the previous chunk's last sample so chunk boundaries stay smooth.
func upsample16to24(in []byte, carry *int16, haveCarry *bool) []byte {
	n := len(in) / bytesPerSample
	samples := make([]int16, 0, n+1)
	if *haveCarry {
		samples = append(samples, *carry)
	}
	for i := 0; i < n; i++ {
		samples = append(samples, int16(binary.LittleEndian.Uint16(in[i*2:])))
	}
	if len(samples) < 2 {
		if n > 0 {
			*carry = samples[len(samples)-1]
			*haveCarry = true
		}
		return nil
	}
	// Output sample j sits at input position j*2/3 within this window.
	total := (len(samples) - 1) * realtimeRate / sampleRate
	out := make([]byte, total*bytesPerSample)
	for j := 0; j < total; j++ {
		pos := float64(j) * float64(sampleRate) / float64(realtimeRate)
		i := int(pos)
		frac := pos - float64(i)
		a := float64(samples[i])
		b := a
		if i+1 < len(samples) {
			b = float64(samples[i+1])
		}
		v := a + (b-a)*frac
		binary.LittleEndian.PutUint16(out[j*2:], uint16(int16(v)))
	}
	*carry = samples[len(samples)-1]
	*haveCarry = true
	return out
}
