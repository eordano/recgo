package tab

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/eordano/recgo/internal/transcribe"
)

type Endpoint struct {
	URL   string
	Model string
}

var DefaultEndpoints = []Endpoint{}

func Endpoints(flagURL, flagModel, cfgURL, cfgModel string) []Endpoint {
	if flagURL != "" {
		return []Endpoint{{URL: flagURL, Model: FirstNonEmpty(flagModel, "whisper")}}
	}
	if cfgURL != "" {
		return []Endpoint{{URL: cfgURL, Model: FirstNonEmpty(cfgModel, "whisper")}}
	}
	return DefaultEndpoints
}

func APIKey(flagKey, cfgKey string) string {
	return FirstNonEmpty(flagKey, os.Getenv("OPENAI_API_KEY"), os.Getenv("LLM_API_KEY"), cfgKey)
}

type Segment struct {
	AudioStartMs float64 `json:"audioStartMs"`
	AudioEndMs   float64 `json:"audioEndMs"`
	T            float64 `json:"t"`
	EndT         float64 `json:"endT"`
	Text         string  `json:"text"`
}

type rawSegment struct {
	Start float64 `json:"start"`
	End   float64 `json:"end"`
	Text  string  `json:"text"`
}

type Transcript struct {
	OK           bool         `json:"ok"`
	Reason       string       `json:"reason,omitempty"`
	Backend      string       `json:"backend,omitempty"`
	Endpoint     string       `json:"endpoint,omitempty"`
	Model        string       `json:"model,omitempty"`
	Granularity  string       `json:"granularity,omitempty"`
	Language     string       `json:"language,omitempty"`
	Segments     []Segment    `json:"segments"`
	RawSegments  []rawSegment `json:"-"`
	AccuracyNote string       `json:"accuracyNote,omitempty"`
	// PromptRejected: the endpoint answered 400 to the vocabulary prompt and
	// the pass was redone without it, so on-screen terms were not seeded.
	PromptRejected bool      `json:"promptRejected,omitempty"`
	VADCorrected   bool      `json:"vadCorrected"`
	VADReason      string    `json:"vadReason,omitempty"`
	Attempts       []Attempt `json:"attempts,omitempty"`

	retryable bool
	status    int
	body      string
}

type Attempt struct {
	URL    string `json:"url"`
	Model  string `json:"model"`
	Reason string `json:"reason"`
}

type STTOptions struct {
	Endpoints []Endpoint
	Model     string
	APIKey    string
	Language  string
	Timeout   time.Duration
	// Prompt seeds the decoder with vocabulary (see Vocabulary); some
	// endpoints reject it with a 400, in which case the pass retries without.
	Prompt string
}

func TranscribeFallback(clock *Clock, wavPath string, opts STTOptions) Transcript {
	eps := opts.Endpoints
	if len(eps) == 0 {
		eps = DefaultEndpoints
	}

	var attempts []Attempt
	for _, ep := range eps {
		model := ep.Model
		if opts.Model != "" {
			model = opts.Model
		}
		t := transcribeOne(clock, wavPath, ep.URL, model, opts)
		if t.OK {
			t.Attempts = attempts
			return t
		}
		attempts = append(attempts, Attempt{URL: ep.URL, Model: model, Reason: t.Reason})
		if !t.retryable {
			break
		}
	}

	reason := "no remote STT endpoint configured (--stt-url, or [transcription.remote] endpoint in ~/.config/recgo/config.toml)"
	if len(attempts) == 1 {
		reason = attempts[0].Reason
	} else if len(attempts) > 1 {
		var b strings.Builder
		fmt.Fprintf(&b, "all %d STT endpoints failed:", len(attempts))
		for _, a := range attempts {
			fmt.Fprintf(&b, "\n  - %s", a.Reason)
		}
		reason = b.String()
	}
	return Transcript{Reason: reason, Attempts: attempts}
}

type sttPayload struct {
	Text     string `json:"text"`
	Language string `json:"language"`
	Words    []struct {
		Word  string  `json:"word"`
		Start float64 `json:"start"`
		End   float64 `json:"end"`
	} `json:"words"`
	Segments []rawSegment `json:"segments"`
}

func transcribeOne(clock *Clock, wavPath, baseURL, model string, opts STTOptions) Transcript {
	if !clock.AudioAnchored() {
		return Transcript{Reason: "no audio was captured, so nothing can be aligned"}
	}

	endpoint := strings.TrimRight(baseURL, "/") + "/audio/transcriptions"

	wav, err := os.ReadFile(wavPath)
	if err != nil {
		return Transcript{Reason: fmt.Sprintf("open %s: %v", wavPath, err), retryable: false}
	}

	pieces := splitWAV(wav, sttMaxPieceSec, sttCutSearchSec)
	var payload sttPayload
	promptRejected := false
	for i, piece := range pieces {
		if len(pieces) > 1 {
			fmt.Fprintf(os.Stderr, "stt: piece %d/%d (%.0fs from %.0fs)\n",
				i+1, len(pieces), piece.DurationSec, piece.OffsetSec)
		}
		part, failure := postWAV(endpoint, filepath.Base(wavPath), piece, model, opts)
		if failure != nil && opts.Prompt != "" && isPromptRejection(failure.status, failure.body) {
			fmt.Fprintf(os.Stderr, "stt: %s rejects the vocabulary prompt, retrying without it\n", endpoint)
			promptRejected = true
			opts.Prompt = ""
			part, failure = postWAV(endpoint, filepath.Base(wavPath), piece, model, opts)
		}
		if failure != nil {
			if len(pieces) > 1 {
				failure.Reason = fmt.Sprintf("piece %d/%d: %s", i+1, len(pieces), failure.Reason)
			}
			return *failure
		}
		payload.merge(part, piece.OffsetSec)
	}

	mk := func(startSec, endSec float64, text string) Segment {
		t, _ := clock.FromAudioTime(startSec)
		e, _ := clock.FromAudioTime(endSec)
		return Segment{
			AudioStartMs: startSec * 1000, AudioEndMs: endSec * 1000,
			T: t, EndT: e, Text: strings.TrimSpace(text),
		}
	}

	out := Transcript{OK: true, Backend: "openai", Endpoint: endpoint, Model: model,
		Language: payload.Language, RawSegments: payload.Segments}

	switch {
	case len(payload.Words) > 0:
		out.Granularity = "word"
		for _, w := range payload.Words {
			out.Segments = append(out.Segments, mk(w.Start, w.End, w.Word))
		}
		out.AccuracyNote = "Word-level timestamps from the STT server; alignment is as good as the model."
	case len(payload.Segments) > 0:
		out.Granularity = "segment"
		for _, s := range payload.Segments {
			out.Segments = append(out.Segments, mk(s.Start, s.End, s.Text))
		}
		out.AccuracyNote = "Server returned segment-level timestamps only — narration attributes " +
			"to the right click, but sub-second alignment within a sentence is not available."
	case strings.TrimSpace(payload.Text) != "":
		out.Granularity = "none"
		out.Segments = []Segment{mk(0, 0, payload.Text)}
		out.AccuracyNote = "Server returned untimed text. Every line is pinned to the start of " +
			"the recording; do NOT trust narration timestamps."
	}

	filtered := out.Segments[:0]
	for _, s := range out.Segments {
		if s.Text != "" {
			filtered = append(filtered, s)
		}
	}
	out.Segments = filtered
	if promptRejected {
		out.PromptRejected = true
		out.AccuracyNote = strings.TrimSpace(out.AccuracyNote + " The endpoint rejected the " +
			"vocabulary prompt, so on-screen terms were not seeded into the decoder.")
	}
	return out
}

// Same test as the live feed's isPromptRejected (internal/transcribe): a 400
// whose body names the prompt field is the endpoint refusing the prompt, not
// the audio.
func isPromptRejection(status int, body string) bool {
	return status == http.StatusBadRequest && strings.Contains(strings.ToLower(body), "prompt")
}

func (p *sttPayload) merge(part sttPayload, offsetSec float64) {
	if p.Language == "" {
		p.Language = part.Language
	}
	if text := strings.TrimSpace(part.Text); text != "" {
		p.Text = strings.TrimSpace(p.Text + " " + text)
	}
	for _, w := range part.Words {
		w.Start += offsetSec
		w.End += offsetSec
		p.Words = append(p.Words, w)
	}
	for _, s := range part.Segments {
		s.Start += offsetSec
		s.End += offsetSec
		p.Segments = append(p.Segments, s)
	}
}

func postWAV(endpoint, name string, piece wavPiece, model string, opts STTOptions) (sttPayload, *Transcript) {
	var payload sttPayload

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	part, err := mw.CreateFormFile("file", name)
	if err != nil {
		return payload, &Transcript{Reason: err.Error()}
	}
	if _, err := part.Write(piece.Data); err != nil {
		return payload, &Transcript{Reason: err.Error()}
	}
	mw.WriteField("model", model)
	mw.WriteField("response_format", "verbose_json")
	mw.WriteField("timestamp_granularities[]", "word")
	mw.WriteField("timestamp_granularities[]", "segment")
	if opts.Language != "" {
		mw.WriteField("language", opts.Language)
	}
	if opts.Prompt != "" {
		mw.WriteField("prompt", opts.Prompt)
	}
	mw.Close()

	timeout := opts.Timeout
	if timeout == 0 {
		timeout = 2*time.Minute + time.Duration(piece.DurationSec/3*float64(time.Second))
	}

	req, err := http.NewRequest(http.MethodPost, endpoint, &body)
	if err != nil {
		return payload, &Transcript{Reason: err.Error()}
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if opts.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+opts.APIKey)
	}

	res, err := (&http.Client{Timeout: timeout}).Do(req)
	if err != nil {
		return payload, &Transcript{Reason: fmt.Sprintf("could not reach %s: %v", endpoint, err), retryable: true}
	}
	defer res.Body.Close()

	if res.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(res.Body, 400))
		msg := fmt.Sprintf("%s returned %d", endpoint, res.StatusCode)
		if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden {
			msg += " — set --stt-api-key or OPENAI_API_KEY"
		}
		if len(snippet) > 0 {
			msg += ": " + strings.TrimSpace(string(snippet))
		}
		retry := res.StatusCode != http.StatusBadRequest && res.StatusCode != http.StatusUnprocessableEntity
		return payload, &Transcript{Reason: msg, retryable: retry, status: res.StatusCode, body: string(snippet)}
	}

	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return payload, &Transcript{Reason: err.Error(), retryable: true}
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return payload, &Transcript{Reason: fmt.Sprintf("response was not JSON: %v", err), retryable: true}
	}
	return payload, nil
}

// ResolveWhisperModel merges explicit flag values with discovery: a non-empty
// override wins per field, the rest comes from DiscoverWhisperModel.
func ResolveWhisperModel(model, vad string) (string, string) {
	if model == "" || vad == "" {
		dm, dv := DiscoverWhisperModel()
		if model == "" {
			model = dm
		}
		if vad == "" {
			vad = dv
		}
	}
	return model, vad
}

// LocalLiveFeed resolves a whisper model and builds a local live-transcription
// session. When no model is available it returns nil and a human-readable
// reason so the CLI can say why live narration is off instead of going silent.
func LocalLiveFeed(ctx context.Context, bin, model, vad string) (*transcribe.Session, string) {
	model, vad = ResolveWhisperModel(model, vad)
	if model == "" {
		return nil, "no local whisper model found " +
			"(pass --whisper-model, or drop a ggml-*.bin in ~/.local/share/recgo/models)"
	}
	return transcribe.NewFeed(ctx, transcribe.Config{
		LocalBin: bin, LocalModel: model, LocalVADModel: vad,
	}), ""
}

// RemoteLiveFeed builds a live-transcription session against an
// OpenAI-compatible endpoint. This is the one live path that uploads audio
// while recording; callers announce it on stderr.
func RemoteLiveFeed(ctx context.Context, ep Endpoint, key, modelOverride string) *transcribe.Session {
	return transcribe.NewFeed(ctx, transcribe.Config{
		Endpoint: ep.URL, APIKey: key, Model: FirstNonEmpty(modelOverride, ep.Model),
	})
}

// RealtimeLiveFeed builds a live-transcription session that streams the audio
// over the endpoint's /v1/realtime websocket as it is spoken, so narration
// lands within a second instead of a pass every few seconds. Uploads audio
// while recording, like RemoteLiveFeed; callers announce it on stderr.
func RealtimeLiveFeed(ctx context.Context, ep Endpoint, key, modelOverride string) *transcribe.Session {
	return transcribe.NewFeed(ctx, transcribe.Config{
		Endpoint: ep.URL, APIKey: key, Model: FirstNonEmpty(modelOverride, ep.Model), Realtime: true,
	})
}

// ConsumeLive drains a live feed in a goroutine: decoded lines reach emit
// stamped with their session time, pass failures reach fail. The goroutine
// ends when the feed is stopped.
func ConsumeLive(feed *transcribe.Session, clock *Clock, emit func(t float64, text string), fail func(error)) {
	ConsumeLiveWith(feed, clock, emit, nil, fail)
}

// ConsumeLiveWith is ConsumeLive plus partial: the words heard so far of an
// utterance that has not ended, as the realtime lane streams them. Nothing
// partial reaches the session document; it is for a HUD to show.
func ConsumeLiveWith(feed *transcribe.Session, clock *Clock, emit func(t float64, text string),
	partial func(text string), fail func(error)) {
	go func() {
		for st := range feed.Updates() {
			if st.Err != nil {
				fail(st.Err)
				continue
			}
			text := strings.TrimSpace(st.PassText)
			if text == "" {
				if partial != nil && strings.TrimSpace(st.Fast) != "" {
					partial(strings.TrimSpace(st.Fast))
				}
				continue
			}
			t, ok := clock.FromAudioTime(float64(st.PassStartSample) / transcribe.SampleRate)
			if !ok {
				t = clock.Now()
			}
			emit(t, text)
		}
	}()
}

type Utterance struct {
	T    float64
	EndT float64
	Text string
	// Words are the segments the utterance was built from, in order, so a
	// click can be placed at the word it landed on. One entry per utterance
	// when the transcript has no word granularity.
	Words []Segment
}

func ToUtterances(segments []Segment, gapMs, maxWordMs float64) []Utterance {
	if gapMs <= 0 {
		gapMs = 350
	}
	if maxWordMs <= 0 {
		maxWordMs = 700
	}

	var out []Utterance
	var lastEffectiveEnd float64
	for _, s := range segments {
		effEnd := s.EndT
		if s.T+maxWordMs < effEnd {
			effEnd = s.T + maxWordMs
		}
		if len(out) > 0 && s.T-lastEffectiveEnd <= gapMs {
			u := &out[len(out)-1]
			u.Text = strings.TrimSpace(u.Text + " " + s.Text)
			u.EndT = s.EndT
			u.Words = append(u.Words, s)
		} else {
			out = append(out, Utterance{T: s.T, EndT: s.EndT, Text: s.Text, Words: []Segment{s}})
		}
		lastEffectiveEnd = effEnd
	}
	return out
}
