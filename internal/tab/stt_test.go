package tab

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

const wordResponse = `{
  "task":"transcribe","language":"en","duration":8.2,
  "text":"Okay so I am going to hit save now. And that just did nothing.",
  "words":[
    {"word":"Okay","start":2.0,"end":2.4},{"word":"so","start":2.4,"end":2.6},
    {"word":"I","start":2.6,"end":2.7},{"word":"am","start":2.7,"end":2.9},
    {"word":"going","start":2.9,"end":3.2},{"word":"to","start":3.2,"end":3.3},
    {"word":"hit","start":3.3,"end":3.6},{"word":"save","start":3.6,"end":4.0},
    {"word":"now","start":4.0,"end":4.4},{"word":"And","start":6.3,"end":6.5},
    {"word":"that","start":6.5,"end":6.7},{"word":"just","start":6.7,"end":6.9},
    {"word":"did","start":6.9,"end":7.1},{"word":"nothing","start":7.1,"end":7.6}],
  "segments":[
    {"id":0,"start":2.0,"end":4.4,"text":" Okay so I am going to hit save now."},
    {"id":1,"start":6.3,"end":7.6,"text":" And that just did nothing."}]
}`

const audioStart = 4000

type mockSTT struct {
	mu       sync.Mutex
	calls    int
	lastBody string
	lastAuth string
	lastPath string
	respond  func(call int) (int, string)
	server   *httptest.Server
}

func newMockSTT(t *testing.T) *mockSTT {
	t.Helper()
	m := &mockSTT{respond: func(int) (int, string) { return 200, wordResponse }}
	m.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		m.mu.Lock()
		m.calls++
		call := m.calls
		m.lastBody = string(body)
		m.lastAuth = r.Header.Get("Authorization")
		m.lastPath = r.URL.Path
		respond := m.respond
		m.mu.Unlock()

		status, payload := respond(call)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		io.WriteString(w, payload)
	}))
	t.Cleanup(m.server.Close)
	return m
}

func (m *mockSTT) base() string { return m.server.URL + "/v1" }

func testWav(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "audio.wav")
	if err := os.WriteFile(p, make([]byte, 2048), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func anchoredClock() *Clock {
	c := NewClock()
	c.SetAudioStart(audioStart, "test")
	return c
}

func TestPostsMultipartWithRightFields(t *testing.T) {
	m := newMockSTT(t)
	c := anchoredClock()

	tr := TranscribeFallback(c, testWav(t), STTOptions{
		Endpoints: []Endpoint{{URL: m.base(), Model: "whisper-1"}},
		APIKey:    "test-key", Language: "en",
	})
	if !tr.OK {
		t.Fatalf("not ok: %s", tr.Reason)
	}

	if m.lastPath != "/v1/audio/transcriptions" {
		t.Errorf("path = %q", m.lastPath)
	}
	if m.lastAuth != "Bearer test-key" {
		t.Errorf("auth = %q", m.lastAuth)
	}
	for _, want := range []string{
		`name="file"`, `filename="audio.wav"`,
		`name="model"`, "whisper-1",
		`name="response_format"`, "verbose_json",
		`name="timestamp_granularities[]"`, "word",
		`name="language"`,
	} {
		if !strings.Contains(m.lastBody, want) {
			t.Errorf("request body missing %q", want)
		}
	}
}

func TestTrailingSlashesDoNotDoubleUp(t *testing.T) {
	m := newMockSTT(t)
	TranscribeFallback(anchoredClock(), testWav(t), STTOptions{
		Endpoints: []Endpoint{{URL: m.base() + "///", Model: "whisper"}},
	})
	if m.lastPath != "/v1/audio/transcriptions" {
		t.Errorf("path = %q", m.lastPath)
	}
}

func TestOmitsAuthHeaderWithoutKey(t *testing.T) {
	m := newMockSTT(t)
	TranscribeFallback(anchoredClock(), testWav(t), STTOptions{
		Endpoints: []Endpoint{{URL: m.base(), Model: "whisper"}},
	})
	if m.lastAuth != "" {
		t.Errorf("auth = %q, want empty", m.lastAuth)
	}
}

func TestWordTimestampsLandOnSessionClock(t *testing.T) {
	m := newMockSTT(t)
	tr := TranscribeFallback(anchoredClock(), testWav(t), STTOptions{
		Endpoints: []Endpoint{{URL: m.base(), Model: "whisper"}},
	})

	if tr.Granularity != "word" {
		t.Fatalf("granularity = %q", tr.Granularity)
	}
	if len(tr.Segments) != 14 {
		t.Fatalf("got %d segments", len(tr.Segments))
	}
	if tr.Segments[0].Text != "Okay" || tr.Segments[0].T != audioStart+2000 {
		t.Errorf("first = %+v", tr.Segments[0])
	}
	if tr.Segments[0].EndT != audioStart+2400 {
		t.Errorf("first endT = %v", tr.Segments[0].EndT)
	}
	for _, s := range tr.Segments {
		if s.T != audioStart+s.AudioStartMs {
			t.Fatalf("conversion must be a pure offset: %+v", s)
		}
	}
}

func TestFallsBackToSegmentGranularity(t *testing.T) {
	m := newMockSTT(t)
	m.respond = func(int) (int, string) {
		return 200, `{"language":"en","segments":[{"start":2.0,"end":4.4,"text":"Okay"},{"start":6.3,"end":7.6,"text":"And"}]}`
	}
	tr := TranscribeFallback(anchoredClock(), testWav(t), STTOptions{
		Endpoints: []Endpoint{{URL: m.base(), Model: "whisper"}},
	})
	if tr.Granularity != "segment" || len(tr.Segments) != 2 {
		t.Fatalf("got %q with %d segments", tr.Granularity, len(tr.Segments))
	}
	if !strings.Contains(tr.AccuracyNote, "segment-level") {
		t.Errorf("note = %q", tr.AccuracyNote)
	}
}

func TestFlatTextIsPinnedAndFlagged(t *testing.T) {
	m := newMockSTT(t)
	m.respond = func(int) (int, string) { return 200, `{"text":"okay so I am going to hit save now"}` }
	tr := TranscribeFallback(anchoredClock(), testWav(t), STTOptions{
		Endpoints: []Endpoint{{URL: m.base(), Model: "whisper"}},
	})
	if tr.Granularity != "none" || len(tr.Segments) != 1 || tr.Segments[0].T != audioStart {
		t.Fatalf("got %q %+v", tr.Granularity, tr.Segments)
	}
	if !strings.Contains(tr.AccuracyNote, "do NOT trust") {
		t.Errorf("note = %q", tr.AccuracyNote)
	}
}

func TestAuthFailureExplainsTheFix(t *testing.T) {
	m := newMockSTT(t)
	m.respond = func(int) (int, string) { return 401, `{"error":"no key"}` }
	tr := TranscribeFallback(anchoredClock(), testWav(t), STTOptions{
		Endpoints: []Endpoint{{URL: m.base(), Model: "whisper"}},
	})
	if tr.OK || !strings.Contains(tr.Reason, "401") || !strings.Contains(tr.Reason, "OPENAI_API_KEY") {
		t.Errorf("reason = %q", tr.Reason)
	}
}

func TestServerErrorSurfacesBody(t *testing.T) {
	m := newMockSTT(t)
	m.respond = func(int) (int, string) { return 500, "model not loaded" }
	tr := TranscribeFallback(anchoredClock(), testWav(t), STTOptions{
		Endpoints: []Endpoint{{URL: m.base(), Model: "whisper"}},
	})
	if tr.OK || !strings.Contains(tr.Reason, "500") || !strings.Contains(tr.Reason, "model not loaded") {
		t.Errorf("reason = %q", tr.Reason)
	}
}

func TestNonJSONSuccessIsReported(t *testing.T) {
	m := newMockSTT(t)
	m.respond = func(int) (int, string) { return 200, "<html>gateway</html>" }
	tr := TranscribeFallback(anchoredClock(), testWav(t), STTOptions{
		Endpoints: []Endpoint{{URL: m.base(), Model: "whisper"}},
	})
	if tr.OK || !strings.Contains(tr.Reason, "not JSON") {
		t.Errorf("reason = %q", tr.Reason)
	}
}

func TestUnreachableEndpointIsReported(t *testing.T) {
	tr := TranscribeFallback(anchoredClock(), testWav(t), STTOptions{
		Endpoints: []Endpoint{{URL: "http://127.0.0.1:1/v1", Model: "whisper"}},
	})
	if tr.OK || !strings.Contains(tr.Reason, "could not reach") {
		t.Errorf("reason = %q", tr.Reason)
	}
}

func TestRefusesWithoutAudioAnchor(t *testing.T) {
	m := newMockSTT(t)
	tr := TranscribeFallback(NewClock(), testWav(t), STTOptions{
		Endpoints: []Endpoint{{URL: m.base(), Model: "whisper"}},
	})
	if tr.OK || !strings.Contains(tr.Reason, "no audio") {
		t.Errorf("reason = %q", tr.Reason)
	}
}

func TestFallsThroughDeadHostToLiveOne(t *testing.T) {
	m := newMockSTT(t)
	tr := TranscribeFallback(anchoredClock(), testWav(t), STTOptions{
		Endpoints: []Endpoint{
			{URL: "http://127.0.0.1:1/v1", Model: "whisper"},
			{URL: m.base(), Model: "whisper-ct2"},
		},
	})
	if !tr.OK {
		t.Fatalf("not ok: %s", tr.Reason)
	}
	if len(tr.Attempts) != 1 || !strings.Contains(tr.Attempts[0].Reason, "could not reach") {
		t.Errorf("attempts = %+v", tr.Attempts)
	}
	if !strings.Contains(m.lastBody, "whisper-ct2") {
		t.Error("expected the second endpoint's model in the request")
	}
}

func TestFallsThroughForbidden(t *testing.T) {
	m := newMockSTT(t)
	m.respond = func(call int) (int, string) {
		if call == 1 {
			return 403, "Forbidden"
		}
		return 200, wordResponse
	}
	tr := TranscribeFallback(anchoredClock(), testWav(t), STTOptions{
		Endpoints: []Endpoint{{URL: m.base(), Model: "a"}, {URL: m.base(), Model: "b"}},
	})
	if !tr.OK {
		t.Fatalf("not ok: %s", tr.Reason)
	}
	if len(tr.Attempts) != 1 || !strings.Contains(tr.Attempts[0].Reason, "403") {
		t.Errorf("attempts = %+v", tr.Attempts)
	}
}

func TestBadRequestStopsTheChain(t *testing.T) {
	m := newMockSTT(t)
	m.respond = func(int) (int, string) { return 400, "unsupported file format" }
	tr := TranscribeFallback(anchoredClock(), testWav(t), STTOptions{
		Endpoints: []Endpoint{{URL: m.base(), Model: "a"}, {URL: m.base(), Model: "b"}},
	})
	if tr.OK {
		t.Fatal("should have failed")
	}
	if m.calls != 1 {
		t.Errorf("made %d calls, want 1", m.calls)
	}
}

func TestExplicitModelOverridesEndpointDefaults(t *testing.T) {
	m := newMockSTT(t)
	TranscribeFallback(anchoredClock(), testWav(t), STTOptions{
		Endpoints: []Endpoint{{URL: m.base(), Model: "ignored"}},
		Model:     "my-model",
	})
	if !strings.Contains(m.lastBody, "my-model") || strings.Contains(m.lastBody, "ignored") {
		t.Error("explicit --stt-model must win")
	}
}

func TestAllFailReportsEveryAttempt(t *testing.T) {
	m := newMockSTT(t)
	m.respond = func(int) (int, string) { return 502, "bad gateway" }
	tr := TranscribeFallback(anchoredClock(), testWav(t), STTOptions{
		Endpoints: []Endpoint{{URL: m.base(), Model: "a"}, {URL: m.base(), Model: "b"}},
	})
	if tr.OK || len(tr.Attempts) != 2 || !strings.Contains(tr.Reason, "all 2 STT endpoints failed") {
		t.Errorf("reason = %q attempts = %+v", tr.Reason, tr.Attempts)
	}
}

func TestNoEndpointsShipInTheBinary(t *testing.T) {
	if len(DefaultEndpoints) != 0 {
		t.Errorf("DefaultEndpoints must stay empty (endpoints come from the config file): %+v", DefaultEndpoints)
	}
	if len(DefaultTitleEndpoints) != 0 {
		t.Errorf("DefaultTitleEndpoints must stay empty (endpoints come from the config file): %+v", DefaultTitleEndpoints)
	}
}

func TestPromptFieldIsSent(t *testing.T) {
	m := newMockSTT(t)
	tr := TranscribeFallback(anchoredClock(), testWav(t), STTOptions{
		Endpoints: []Endpoint{{URL: m.base(), Model: "whisper"}},
		Prompt:    "Worth coming back, example.app, without fighting the tools",
	})
	if !tr.OK {
		t.Fatalf("not ok: %s", tr.Reason)
	}
	if !strings.Contains(m.lastBody, `name="prompt"`) || !strings.Contains(m.lastBody, "without fighting the tools") {
		t.Errorf("request body missing the prompt field:\n%s", m.lastBody)
	}
	if tr.PromptRejected || strings.Contains(tr.AccuracyNote, "rejected") {
		t.Errorf("prompt was accepted but flagged: %+v", tr)
	}
}

func TestNoPromptFieldWhenEmpty(t *testing.T) {
	m := newMockSTT(t)
	TranscribeFallback(anchoredClock(), testWav(t), STTOptions{
		Endpoints: []Endpoint{{URL: m.base(), Model: "whisper"}},
	})
	if strings.Contains(m.lastBody, `name="prompt"`) {
		t.Errorf("empty prompt must not produce a field:\n%s", m.lastBody)
	}
}

func TestRejectedPromptRetriesOnceWithoutIt(t *testing.T) {
	m := newMockSTT(t)
	m.respond = func(call int) (int, string) {
		if call == 1 {
			return 400, `{"detail":[{"loc":["body","prompt"],"msg":"extra fields not permitted"}]}`
		}
		return 200, wordResponse
	}
	tr := TranscribeFallback(anchoredClock(), testWav(t), STTOptions{
		Endpoints: []Endpoint{{URL: m.base(), Model: "whisper"}},
		Prompt:    "Worth coming back, example.app",
	})
	if !tr.OK {
		t.Fatalf("not ok: %s", tr.Reason)
	}
	if m.calls != 2 {
		t.Errorf("made %d calls, want 2", m.calls)
	}
	if strings.Contains(m.lastBody, `name="prompt"`) {
		t.Error("retry still carried the prompt")
	}
	if !tr.PromptRejected || !strings.Contains(tr.AccuracyNote, "rejected the vocabulary prompt") {
		t.Errorf("rejection not recorded: PromptRejected=%v note=%q", tr.PromptRejected, tr.AccuracyNote)
	}
	if len(tr.Attempts) != 0 {
		t.Errorf("a rejected prompt is not an endpoint failure: %+v", tr.Attempts)
	}
	if len(tr.Segments) != 14 {
		t.Errorf("got %d segments from the retry", len(tr.Segments))
	}
}

// A long recording goes up in pieces; the prompt is rejected once and the
// remaining pieces skip it instead of eating a 400 each.
func TestRejectedPromptIsDroppedForTheRemainingPieces(t *testing.T) {
	m := newMockSTT(t)
	promptCalls := 0
	m.respond = func(int) (int, string) {
		m.mu.Lock()
		withPrompt := strings.Contains(m.lastBody, `name="prompt"`)
		m.mu.Unlock()
		if withPrompt {
			promptCalls++
			return 400, `{"detail":[{"loc":["body","prompt"],"msg":"extra fields not permitted"}]}`
		}
		return 200, wordResponse
	}

	wav := pcmWav((sttMaxPieceSec-sttCutSearchSec)*2 - 100)
	pieces := len(splitWAV(wav, sttMaxPieceSec, sttCutSearchSec))
	if pieces < 2 {
		t.Fatalf("fixture splits into %d piece(s), need at least 2", pieces)
	}
	path := filepath.Join(t.TempDir(), "audio.wav")
	if err := os.WriteFile(path, wav, 0o644); err != nil {
		t.Fatal(err)
	}

	tr := TranscribeFallback(anchoredClock(), path, STTOptions{
		Endpoints: []Endpoint{{URL: m.base(), Model: "whisper"}},
		Prompt:    "Worth coming back, example.app",
	})
	if !tr.OK {
		t.Fatalf("not ok: %s", tr.Reason)
	}
	if promptCalls != 1 {
		t.Errorf("the prompt was sent %d times, want once", promptCalls)
	}
	if m.calls != pieces+1 {
		t.Errorf("made %d calls for %d pieces, want %d", m.calls, pieces, pieces+1)
	}
	if !tr.PromptRejected {
		t.Error("rejection not recorded")
	}
}

func TestUnrelatedBadRequestDoesNotRetryWithoutPrompt(t *testing.T) {
	m := newMockSTT(t)
	m.respond = func(int) (int, string) { return 400, "unsupported file format" }
	tr := TranscribeFallback(anchoredClock(), testWav(t), STTOptions{
		Endpoints: []Endpoint{{URL: m.base(), Model: "whisper"}},
		Prompt:    "Worth coming back",
	})
	if tr.OK || m.calls != 1 {
		t.Errorf("ok=%v calls=%d, want a single failed call", tr.OK, m.calls)
	}
}

func TestPromptRejectionOnlyRetriesOnce(t *testing.T) {
	m := newMockSTT(t)
	m.respond = func(int) (int, string) { return 400, `{"error":"prompt not supported"}` }
	tr := TranscribeFallback(anchoredClock(), testWav(t), STTOptions{
		Endpoints: []Endpoint{{URL: m.base(), Model: "whisper"}},
		Prompt:    "Worth coming back",
	})
	if tr.OK || m.calls != 2 {
		t.Errorf("ok=%v calls=%d, want exactly one retry then failure", tr.OK, m.calls)
	}
}
