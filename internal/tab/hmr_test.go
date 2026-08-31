package tab

import (
	"reflect"
	"testing"
)

func TestClassifyHMRVite(t *testing.T) {
	h := ClassifyHMR(`{"type":"update","updates":[
		{"type":"js-update","path":"/src/App.tsx","acceptedPath":"/src/App.tsx"},
		{"type":"css-update","path":"/src/index.css","acceptedPath":"/src/index.css"}]}`)
	if h == nil {
		t.Fatal("expected a vite update")
	}
	if h.Flavor != "vite" || h.Type != "update" {
		t.Errorf("got %s/%s", h.Flavor, h.Type)
	}
	if want := []string{"/src/App.tsx", "/src/index.css"}; !reflect.DeepEqual(h.Files, want) {
		t.Errorf("files = %v, want %v", h.Files, want)
	}
}

func TestClassifyHMRViteVariants(t *testing.T) {
	if h := ClassifyHMR(`{"type":"full-reload","path":"/src/main.tsx"}`); h == nil ||
		len(h.Files) != 1 || h.Files[0] != "/src/main.tsx" {
		t.Errorf("full-reload: %+v", h)
	}
	if h := ClassifyHMR(`{"type":"connected"}`); h == nil || h.Type != "connected" {
		t.Errorf("connected: %+v", h)
	}
	if h := ClassifyHMR(`{"type":"prune","paths":["/a.ts","/b.ts"]}`); h == nil || len(h.Files) != 2 {
		t.Errorf("prune: %+v", h)
	}
	if h := ClassifyHMR(`{"type":"error","err":{"id":"/src/Broken.tsx"}}`); h == nil ||
		len(h.Files) != 1 || h.Files[0] != "/src/Broken.tsx" {
		t.Errorf("error: %+v", h)
	}
}

func TestClassifyHMRWebpackAndNonHMR(t *testing.T) {
	if h := ClassifyHMR(`{"type":"hash","data":[]}`); h == nil || h.Flavor != "webpack" {
		t.Errorf("webpack hash: %+v", h)
	}
	for _, payload := range []string{
		`{"type":"chat-message","text":"hi"}`,
		`not json at all`,
		`["array"]`,
		`{"noTypeField":1}`,
		`{"type":123}`,
	} {
		if h := ClassifyHMR(payload); h != nil {
			t.Errorf("ClassifyHMR(%q) = %+v, want nil", payload, h)
		}
	}
}

func TestClassifyHMRPhoenix(t *testing.T) {
	h := ClassifyHMR(`["241","242","phoenix:live_reload","assets_change",{"asset_type":"css"}]`)
	if h == nil || h.Flavor != "phoenix" || h.Type != "assets_change" {
		t.Fatalf("assets_change: %+v", h)
	}
	if !reflect.DeepEqual(h.Files, []string{"css"}) {
		t.Errorf("files = %v", h.Files)
	}

	h = ClassifyHMR(`["240","240","phoenix:live_reload","phx_reply",` +
		`{"status":"error","response":{"message":"live reload backend not running"}}]`)
	if h == nil || h.Flavor != "phoenix" || h.Type != "error" {
		t.Fatalf("error reply: %+v", h)
	}
	if !reflect.DeepEqual(h.Files, []string{"live reload backend not running"}) {
		t.Errorf("files = %v", h.Files)
	}
}

func TestClassifyHMRPhoenixIgnoresAppTraffic(t *testing.T) {
	for _, payload := range []string{
		`[null,"241","phoenix","heartbeat",{}]`,
		`["240","240","phoenix:live_reload","phx_join",{}]`,
		`["240","240","phoenix:live_reload","phx_reply",{"status":"ok","response":{}}]`,
		`["4","8","lv:phx-GMxlQt1_uQMargAJ","event",{"type":"click","event":"save","value":{}}]`,
		`["4","9","lv:phx-GMxlQt1_uQMargAJ","phx_reply",{"status":"ok","response":{"diff":{}}}]`,
		`["array"]`,
	} {
		if h := ClassifyHMR(payload); h != nil {
			t.Errorf("ClassifyHMR(%q) = %+v, want nil", payload, h)
		}
	}
}

func TestToUtterancesSplitsOnGap(t *testing.T) {
	segs := []Segment{
		{T: 0, EndT: 200, Text: "the"},
		{T: 250, EndT: 400, Text: "save"},
		{T: 420, EndT: 600, Text: "button"},
		{T: 2000, EndT: 2200, Text: "anyway"},
	}
	u := ToUtterances(segs, 350, 700)
	if len(u) != 2 {
		t.Fatalf("got %d utterances: %+v", len(u), u)
	}
	if u[0].Text != "the save button" {
		t.Errorf("first = %q", u[0].Text)
	}
	if u[1].Text != "anyway" {
		t.Errorf("second = %q", u[1].Text)
	}
}

func TestToUtterancesClampsInflatedWordDuration(t *testing.T) {
	segs := []Segment{
		{T: 3892, EndT: 4400, Text: "save"},
		{T: 4400, EndT: 6300, Text: "now"},
		{T: 6300, EndT: 6500, Text: "and"},
		{T: 6500, EndT: 6700, Text: "that"},
	}

	if u := ToUtterances(segs, 350, 700); len(u) != 2 {
		t.Fatalf("got %d utterances, want 2: %+v", len(u), u)
	} else if u[1].Text != "and that" {
		t.Errorf("second utterance = %q, want \"and that\"", u[1].Text)
	}

	if u := ToUtterances(segs, 350, 1e9); len(u) != 1 {
		t.Errorf("with no clamp expected a single merged utterance, got %d", len(u))
	}
}

func TestRenderArgsAndTopFrame(t *testing.T) {
	args := []remoteObject{
		{Type: "string", Value: []byte(`"save failed"`)},
		{Type: "number", Value: []byte(`500`)},
		{Type: "object", Description: "TypeError: nope"},
	}
	if got := renderArgs(args); got != "save failed 500 TypeError: nope" {
		t.Errorf("renderArgs = %q", got)
	}

	if got := topFrame(nil); got != "" {
		t.Errorf("topFrame(nil) = %q", got)
	}
	st := &cdpStackTrace{}
	st.CallFrames = append(st.CallFrames, struct {
		FunctionName string `json:"functionName"`
		URL          string `json:"url"`
		LineNumber   int    `json:"lineNumber"`
		ColumnNumber int    `json:"columnNumber"`
	}{FunctionName: "onClick", URL: "http://x/app.js", LineNumber: 29, ColumnNumber: 12})
	if got := topFrame(st); got != "onClick (http://x/app.js:30:13)" {
		t.Errorf("topFrame = %q", got)
	}
}
