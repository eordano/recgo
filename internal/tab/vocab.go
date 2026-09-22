package tab

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	vocabMaxWords = 6
	vocabMaxBytes = 900
)

// Vocabulary distills the page text the recorder saw (click targets, page
// titles, plus extra strings such as the target title) into a decoder prompt
// for the batch STT pass, so domain words visible on screen are spelled the
// way the page spells them. Whisper reads only the tail of a prompt, so the
// most frequent terms go last and the cut is taken from the front.
func Vocabulary(events []Event, extra ...string) string {
	type term struct {
		text  string
		count int
		first int
	}
	var terms []*term
	index := map[string]*term{}
	add := func(s string) {
		for _, phrase := range vocabPhrases(s) {
			key := strings.ToLower(phrase)
			if t, ok := index[key]; ok {
				t.count++
				continue
			}
			t := &term{text: phrase, count: 1, first: len(terms)}
			index[key] = t
			terms = append(terms, t)
		}
	}

	for _, ev := range events {
		add(ev.Title)
		if ev.Elem != nil {
			add(ev.Elem.Text)
			add(ev.Elem.AriaLabel)
			add(ev.Elem.TestID)
		}
	}
	for _, s := range extra {
		add(s)
	}
	if len(terms) == 0 {
		return ""
	}

	sort.SliceStable(terms, func(i, j int) bool {
		if terms[i].count != terms[j].count {
			return terms[i].count < terms[j].count
		}
		return terms[i].first < terms[j].first
	})

	total := 2 * (len(terms) - 1)
	for _, t := range terms {
		total += len(t.text)
	}
	start := 0
	for total > vocabMaxBytes && start < len(terms)-1 {
		total -= len(terms[start].text) + 2
		start++
	}

	out := make([]string, 0, len(terms)-start)
	for _, t := range terms[start:] {
		out = append(out, t.text)
	}
	return strings.Join(out, ", ")
}

// vocabPhrases splits a page string at clause boundaries (separator glyphs
// and sentence punctuation), chunks each clause into runs of at most
// vocabMaxWords words and drops fragments too short or too numeric to help
// a decoder.
func vocabPhrases(s string) []string {
	var phrases []string
	var clause []string
	flush := func() {
		if len(clause) > 0 {
			if p := strings.Join(clause, " "); vocabKeep(p) {
				phrases = append(phrases, p)
			}
			clause = clause[:0]
		}
	}

	for _, word := range strings.Fields(s) {
		if isSeparatorWord(word) {
			flush()
			continue
		}
		word, ends := trimWordPunct(word)
		if word != "" {
			clause = append(clause, word)
		}
		if ends || len(clause) == vocabMaxWords {
			flush()
		}
	}
	flush()
	return phrases
}

func isSeparatorWord(w string) bool {
	switch w {
	case "·", "•", "—", "–", "-", "|", "/", "→", "←", "»", "«", ">", "<":
		return true
	}
	return false
}

// trimWordPunct strips wrapping quotes and brackets and reports whether the
// word closed a clause (trailing comma, period, colon, ...). Inner
// punctuation such as "example.app" or "$50/week" is kept.
func trimWordPunct(w string) (string, bool) {
	w = strings.TrimLeftFunc(w, func(r rune) bool {
		return r != '$' && r != '#' && r != '@' && (unicode.IsPunct(r) || unicode.IsSymbol(r))
	})
	ends := false
	for {
		r, size := utf8.DecodeLastRuneInString(w)
		if size == 0 {
			break
		}
		switch r {
		case ',', '.', ';', ':', '!', '?', '…':
			ends = true
		case '"', '\'', ')', ']', '}', '“', '”', '’', '‘', '»':
		default:
			return w, ends
		}
		w = w[:len(w)-size]
	}
	return w, ends
}

func vocabKeep(p string) bool {
	if utf8.RuneCountInString(p) < 3 {
		return false
	}
	return strings.ContainsFunc(p, unicode.IsLetter)
}
