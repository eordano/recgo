package tab

import (
	"math"
	"os"
	"strings"
	"testing"
)

const (
	truthA = 2001.0
	truthB = 6308.0
)

var liveWords = []struct {
	text       string
	start, end float64
}{
	{"Okay", 0, 0.677}, {"so", 0.677, 1.015}, {"I", 1.015, 1.185}, {"am", 1.185, 1.523},
	{"going", 1.523, 2.369}, {"to", 2.369, 2.708}, {"hit", 2.708, 3.215},
	{"save", 3.215, 3.892}, {"now", 3.892, 4.4}, {"And", 4.4, 4.889},
	{"that", 4.889, 5.54}, {"just", 5.54, 6.191}, {"did", 6.191, 6.68},
	{"nothing", 6.68, 7.82},
}

func liveTranscript(c *Clock) Transcript {
	tr := Transcript{
		OK: true, Granularity: "word",
		RawSegments: []rawSegment{
			{Start: 0, End: 4.4, Text: "Okay so I am going to hit save now"},
			{Start: 4.4, End: 7.82, Text: "And that just did nothing"},
		},
	}
	for _, w := range liveWords {
		st, _ := c.FromAudioTime(w.start)
		en, _ := c.FromAudioTime(w.end)
		tr.Segments = append(tr.Segments, Segment{
			Text: w.text, AudioStartMs: w.start * 1000, AudioEndMs: w.end * 1000, T: st, EndT: en,
		})
	}
	return tr
}

func TestUncorrectedLiveResponseIsWrong(t *testing.T) {
	c := NewClock()
	c.SetAudioStart(0, "test")
	tr := liveTranscript(c)

	if tr.Segments[0].T != 0 {
		t.Fatalf("expected the server to put the first word at 0, got %v", tr.Segments[0].T)
	}
	if math.Abs(tr.Segments[0].T-truthA) < 1900 {
		t.Error("expected the uncorrected error to be ~2s")
	}
	if u := ToUtterances(tr.Segments, 0, 0); len(u) != 1 {
		t.Errorf("expected 1 merged utterance before correction, got %d", len(u))
	}
}

func TestAlignGroupsStretchesOntoRegions(t *testing.T) {
	groups := [][]Segment{
		{{AudioStartMs: 0, AudioEndMs: 2000}, {AudioStartMs: 2000, AudioEndMs: 4400}},
		{{AudioStartMs: 4400, AudioEndMs: 7820}},
	}
	regions := []SpeechRegion{{StartMs: 2001, EndMs: 4419}, {StartMs: 6308, EndMs: 8201}}

	out, err := AlignGroupsToRegions(groups, regions)
	if err != nil {
		t.Fatal(err)
	}
	if out[0][0].AudioStartMs != 2001 {
		t.Errorf("first word should snap to speech onset, got %v", out[0][0].AudioStartMs)
	}
	if want := 2001 + (2000.0/4400.0)*2418; math.Abs(out[0][0].AudioEndMs-want) > 0.001 {
		t.Errorf("interpolated end = %v, want %v", out[0][0].AudioEndMs, want)
	}
	if out[1][0].AudioStartMs != 6308 {
		t.Errorf("second utterance should snap too, got %v", out[1][0].AudioStartMs)
	}
}

func TestAlignRefusesOnCountMismatch(t *testing.T) {
	_, err := AlignGroupsToRegions(
		[][]Segment{{{AudioStartMs: 0, AudioEndMs: 100}}},
		[]SpeechRegion{{StartMs: 0, EndMs: 100}, {StartMs: 200, EndMs: 300}},
	)
	if err == nil {
		t.Fatal("expected a refusal")
	}
	if !strings.Contains(err.Error(), "rather than guessing") {
		t.Errorf("err = %v", err)
	}
}

func TestAlignHandlesZeroLengthSpan(t *testing.T) {
	out, err := AlignGroupsToRegions(
		[][]Segment{{{AudioStartMs: 500, AudioEndMs: 500}}},
		[]SpeechRegion{{StartMs: 1000, EndMs: 2000}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if out[0][0].AudioStartMs != 1000 || math.IsNaN(out[0][0].AudioEndMs) {
		t.Errorf("got %+v", out[0][0])
	}
}

func fixtureWav(t *testing.T) (string, string) {
	t.Helper()
	wav := os.Getenv("SPEECH_WAV")
	ff := os.Getenv("FFMPEG")
	if wav == "" || ff == "" {
		t.Skip("set FFMPEG and SPEECH_WAV to run the detection tests")
	}
	return wav, ff
}

func TestDetectSpeechRegionsFindsBothBursts(t *testing.T) {
	wav, ff := fixtureWav(t)
	regions, _, err := DetectSpeechRegions(ff, wav, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(regions) != 2 {
		t.Fatalf("got %d regions: %+v", len(regions), regions)
	}
	if math.Abs(regions[0].StartMs-truthA) > 50 {
		t.Errorf("region 0 starts at %v, want ~%v", regions[0].StartMs, truthA)
	}
	if math.Abs(regions[1].StartMs-truthB) > 50 {
		t.Errorf("region 1 starts at %v, want ~%v", regions[1].StartMs, truthB)
	}
}

func TestVADCorrectFixesTheLiveResponse(t *testing.T) {
	wav, ff := fixtureWav(t)
	c := NewClock()
	c.SetAudioStart(0, "test")

	corrected := VADCorrect(liveTranscript(c), wav, ff, c)
	if !corrected.VADCorrected {
		t.Fatalf("not corrected: %s", corrected.VADReason)
	}

	if err := math.Abs(corrected.Segments[0].T - truthA); err > 50 {
		t.Errorf("first word off by %.0fms", err)
	}
	var and *Segment
	for i := range corrected.Segments {
		if corrected.Segments[i].Text == "And" {
			and = &corrected.Segments[i]
		}
	}
	if and == nil {
		t.Fatal(`no "And" segment`)
	}
	if err := math.Abs(and.T - truthB); err > 50 {
		t.Errorf("second utterance off by %.0fms", err)
	}

	u := ToUtterances(corrected.Segments, 0, 0)
	if len(u) != 2 {
		t.Fatalf("got %d utterances after correction", len(u))
	}
	if !strings.HasPrefix(u[1].Text, "And") {
		t.Errorf("second utterance = %q", u[1].Text)
	}
}
