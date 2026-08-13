package tab

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func fakeChatServer(t *testing.T, reply string, status int) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/models", func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"data": []map[string]string{
			{"id": "whisper-large"}, {"id": "test-chat-model"},
		}})
	})
	mux.HandleFunc("/v1/chat/completions", func(w http.ResponseWriter, r *http.Request) {
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		var body struct {
			Model    string `json:"model"`
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		if body.Model != "test-chat-model" {
			t.Errorf("chat used model %q, want the first non-audio model the endpoint serves", body.Model)
		}
		if len(body.Messages) != 2 || !strings.Contains(body.Messages[1].Content, "popup") {
			t.Errorf("narration did not reach the model: %+v", body.Messages)
		}
		json.NewEncoder(w).Encode(map[string]any{"choices": []map[string]any{
			{"message": map[string]string{"content": reply}},
		}})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func narratedTranscript() *Transcript {
	return &Transcript{OK: true, Segments: []Segment{
		{T: 1000, EndT: 2000, Text: "the popup closes with no feedback"},
	}}
}

func TestGenerateTitleUsesTheFirstEndpointThatAnswers(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer dead.Close()
	live := fakeChatServer(t, "\"Login popup dismisses itself with no feedback.\"\n", http.StatusOK)

	res := GenerateTitle(TitleOptions{
		Enabled: true,
		Endpoints: []Endpoint{
			{URL: dead.URL + "/v1"},
			{URL: live.URL + "/v1"},
		},
	}, narratedTranscript(), nil)

	if res.Title != "Login popup dismisses itself with no feedback" {
		t.Errorf("title = %q — quotes/trailing period should be stripped", res.Title)
	}
	if res.Slug != "login-popup-dismisses-itself-with-no-feedback" {
		t.Errorf("slug = %q", res.Slug)
	}
}

func TestGenerateTitleFallsBackToTheClickDerivedName(t *testing.T) {
	dead := fakeChatServer(t, "", http.StatusInternalServerError)
	events := []Event{{T: 1, Kind: "click", Elem: &Element{Text: "Save", Tag: "button"}}}

	res := GenerateTitle(TitleOptions{
		Enabled: true, Endpoints: []Endpoint{{URL: dead.URL + "/v1"}},
	}, narratedTranscript(), events)

	if res.Title != "" {
		t.Errorf("title = %q, want none when every endpoint fails", res.Title)
	}
	if res.Slug != "save" {
		t.Errorf("slug = %q, want the deterministic click-derived slug", res.Slug)
	}
	if !strings.Contains(res.Note, "title generation failed") {
		t.Errorf("note = %q — the failure should be reported in the document", res.Note)
	}
}

func TestGenerateTitleStaysOfflineWithoutNarration(t *testing.T) {
	srv := fakeChatServer(t, "should never be asked", http.StatusOK)
	srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("called %s with no transcript to summarise", r.URL.Path)
	})

	res := GenerateTitle(TitleOptions{Enabled: true, Endpoints: []Endpoint{{URL: srv.URL + "/v1"}}},
		&Transcript{OK: false, Reason: "no audio"}, nil)
	if res.Title != "" {
		t.Errorf("title = %q, want none", res.Title)
	}
}

func TestGenerateTitleAgainstLiveEndpoint(t *testing.T) {
	if os.Getenv("RECGO_TITLE_E2E") != "1" {
		t.Skip("set RECGO_TITLE_E2E=1 to call the real endpoints")
	}
	res := GenerateTitle(TitleOptions{
		Enabled: true,
		APIKey:  firstNonEmpty(os.Getenv("OPENAI_API_KEY"), os.Getenv("LLM_API_KEY")),
	}, &Transcript{OK: true, Segments: []Segment{
		{T: 1000, EndT: 3000, Text: "ok so when i click log in here"},
		{T: 4000, EndT: 7000, Text: "the popup just disappears and nothing happens, no error, nothing"},
	}}, nil)
	if res.Title == "" {
		t.Fatalf("no endpoint produced a title: %s", res.Note)
	}
	t.Logf("title = %q (%s)", res.Title, res.Note)
}
