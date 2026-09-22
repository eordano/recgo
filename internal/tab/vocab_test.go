package tab

import (
	"strings"
	"testing"
)

const q4Title = "Acme · Q4 2026 Plan — Worth coming back"

func click(seq int, text string) Event {
	return Event{T: float64(seq) * 1000, Kind: "click", Seq: seq, Title: q4Title,
		URL:  "file:///home/user/slides/q4-plan.html",
		Elem: &Element{Selector: "section > p", Tag: "p", Text: text}}
}

func vocabTerms(s string) []string {
	if s == "" {
		return nil
	}
	return strings.Split(s, ", ")
}

func TestVocabularyPhrasesFromRealSession(t *testing.T) {
	events := []Event{
		{T: 0, Kind: "record-start", FullShot: InitialShot},
		click(1, "Worth coming back."),
		click(2, "Creators making a living thanks to our platform."),
		click(3, "$50/week Leading indicator Carried over from Q3 · already tracked"),
		click(4, "Creators earning ≥ $X/month"),
		click(5, "From an idea to something people play, without fighting the tools"),
		click(6, "Social games with a clear goal ≈2×"),
		click(7, "$200/month"),
		click(8, "$200"),
		click(12, "2026"),
		click(9, "…"),
		click(10, "Q4"),
		{T: 11000, Kind: "click", Seq: 11, Title: q4Title,
			Elem: &Element{Tag: "button", AriaLabel: "Open example.app", TestID: "hero-cta"}},
		{T: 12000, Kind: "console", Level: "log", Text: "render pass"},
	}
	got := Vocabulary(events, q4Title)
	terms := vocabTerms(got)

	for _, want := range []string{
		"Worth coming back",
		"Creators making a living thanks to",
		"our platform",
		"$50/week Leading indicator Carried over from",
		"already tracked",
		"$200/month",
		"Creators earning $X/month",
		"From an idea to something people",
		"without fighting the tools",
		"Social games with a clear goal",
		"Open example.app",
		"hero-cta",
		"Acme",
		"Q4 2026 Plan",
	} {
		if !contains(terms, want) {
			t.Errorf("missing %q in %q", want, got)
		}
	}
	for _, reject := range []string{"$200", "2026", "…", "Q4", "Q3", "render pass", "≥", "≈2×"} {
		if contains(terms, reject) {
			t.Errorf("%q should have been dropped: %q", reject, got)
		}
	}
	for _, term := range terms {
		if n := len(strings.Fields(term)); n > vocabMaxWords {
			t.Errorf("%q has %d words", term, n)
		}
	}
}

func TestVocabularyDedupesCaseInsensitivelyKeepingFirstSpelling(t *testing.T) {
	events := []Event{
		click(1, "NORTH STAR METRIC"),
		click(2, "North Star Metric"),
		click(3, "Worth coming back"),
	}
	terms := vocabTerms(Vocabulary(events))
	if !contains(terms, "NORTH STAR METRIC") || contains(terms, "North Star Metric") {
		t.Errorf("terms = %q", terms)
	}
	if n := count(terms, "north star metric"); n != 1 {
		t.Errorf("north star metric appears %d times in %q", n, terms)
	}
}

func TestVocabularyMostFrequentTermsComeLast(t *testing.T) {
	events := []Event{
		click(1, "Threshold to confirm Sep 22"),
		click(2, "Worth coming back."),
		click(3, "Worth coming back."),
		click(4, "Worth coming back."),
		click(5, "$50/week"),
		click(6, "$50/week"),
	}
	terms := vocabTerms(Vocabulary(events, q4Title))
	// The title rides on every click event plus the extra, so it is the most
	// frequent and must close the prompt.
	if last := terms[len(terms)-1]; last != "Worth coming back" {
		t.Errorf("last = %q, want the title's tail; terms = %q", last, terms)
	}
	if idx(terms, "Threshold to confirm Sep 22") > idx(terms, "$50/week") {
		t.Errorf("rare term after frequent one: %q", terms)
	}
	if idx(terms, "$50/week") > idx(terms, "Acme") {
		t.Errorf("$50/week (2) should precede Acme (7): %q", terms)
	}
}

func TestVocabularyCutsFromTheFrontToFitBudget(t *testing.T) {
	var events []Event
	for i := 0; i < 80; i++ {
		events = append(events, click(i+1, "Unique clause number "+strings.Repeat("x", 20)+string(rune('a'+i%26))+string(rune('a'+i/26))))
	}
	for i := 0; i < 5; i++ {
		events = append(events, click(100+i, "Creators paid from the players who come back"))
	}
	got := Vocabulary(events, q4Title)
	if len(got) > vocabMaxBytes {
		t.Fatalf("prompt is %d bytes", len(got))
	}
	terms := vocabTerms(got)
	if !contains(terms, "Creators paid from the players who") || !contains(terms, "come back") {
		t.Errorf("frequent term cut: %q", got)
	}
	if terms[len(terms)-1] != "Worth coming back" {
		t.Errorf("last = %q", terms[len(terms)-1])
	}
	if len(terms) >= 85 {
		t.Errorf("nothing was cut: %d terms", len(terms))
	}
	if strings.HasPrefix(got, "Unique clause number "+strings.Repeat("x", 20)+"aa") {
		t.Error("the front should have been cut first")
	}
}

func TestVocabularyEmptyWhenNothingQualifies(t *testing.T) {
	events := []Event{
		{T: 0, Kind: "record-start"},
		{T: 1000, Kind: "click", Seq: 1, Elem: &Element{Tag: "p", Text: "42"}},
		{T: 2000, Kind: "click", Seq: 2, Elem: &Element{Tag: "p", Text: "—"}},
		{T: 3000, Kind: "click", Seq: 3, Elem: &Element{Tag: "p", Text: "ok", AriaLabel: "$5"}},
		{T: 4000, Kind: "navigation", URL: "http://localhost:5173/"},
	}
	if got := Vocabulary(events, "", "  ", "12"); got != "" {
		t.Errorf("got %q, want empty", got)
	}
	if got := Vocabulary(nil); got != "" {
		t.Errorf("got %q, want empty", got)
	}
}

func TestVocabularyExtraAloneQualifies(t *testing.T) {
	if got := Vocabulary(nil, "Acme"); got != "Acme" {
		t.Errorf("got %q", got)
	}
}

func contains(xs []string, s string) bool { return idx(xs, s) >= 0 }

func idx(xs []string, s string) int {
	for i, x := range xs {
		if x == s {
			return i
		}
	}
	return -1
}

func count(xs []string, lower string) int {
	n := 0
	for _, x := range xs {
		if strings.ToLower(x) == lower {
			n++
		}
	}
	return n
}
