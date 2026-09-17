package tab

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

type Meta struct {
	Slug        string  `json:"slug"`
	Title       string  `json:"title,omitempty"`
	StartedISO  string  `json:"startedIso"`
	StartedWall string  `json:"startedWall,omitempty"`
	DurationMs  float64 `json:"durationMs"`
	Cwd         string  `json:"cwd,omitempty"`
	TargetURL   string  `json:"targetUrl"`
	TargetTitle string  `json:"targetTitle,omitempty"`
	Tool        string  `json:"tool"`
	AudioNote   string  `json:"audioNote,omitempty"`
	SystemAudio string  `json:"systemAudio,omitempty"`
	TitleNote   string  `json:"titleNote,omitempty"`

	System *SystemInfo `json:"system,omitempty"`
}

type PackOptions struct {
	JSON bool
}

type Session struct {
	Version    int         `json:"version"`
	Meta       Meta        `json:"meta"`
	Clock      ClockReport `json:"clock"`
	Counts     Counts      `json:"counts"`
	Transcript *Transcript `json:"transcript"`
	Events     []Event     `json:"events"`
}

type Counts struct {
	Clicks     int `json:"clicks"`
	HMR        int `json:"hmr"`
	Errors     int `json:"errors"`
	Utterances int `json:"utterances"`
	Events     int `json:"events"`
}

func FormatT(ms float64) string {
	if math.IsNaN(ms) {
		return "??:??"
	}
	sign := ""
	if ms < 0 {
		sign = "-"
		ms = -ms
	}
	m := int(ms / 60000)
	s := int(math.Mod(ms, 60000) / 1000)
	milli := int(math.Mod(ms, 1000))
	return fmt.Sprintf("%s%d:%02d.%03d", sign, m, s, milli)
}

func FormatClock(ms float64) string {
	if math.IsNaN(ms) || ms < 0 {
		ms = 0
	}
	total := int(ms / 1000)
	return fmt.Sprintf("%02d.%02d.%02d", total/3600, (total%3600)/60, total%60)
}

var wsRe = regexp.MustCompile(`\s+`)

func oneLine(s string) string { return strings.TrimSpace(wsRe.ReplaceAllString(s, " ")) }

func clip(s string, n int) string {
	s = oneLine(s)
	if len(s) <= n {
		return s
	}
	// Cut on a rune boundary: a byte slice through a multibyte character
	// writes invalid UTF-8 into the session document.
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return strings.TrimSpace(s[:n]) + "..."
}

func isNoise(e Event) bool {
	if e.Kind != "network-error" && e.Kind != "browser-log" {
		return false
	}
	return strings.Contains(e.URL, "/favicon.ico") || strings.Contains(e.Text, "/favicon.ico")
}

func isError(e Event) bool {
	if isNoise(e) {
		return false
	}
	switch e.Kind {
	case "exception", "network-error":
		return true
	case "console", "browser-log":
		return e.Level == "error"
	}
	return false
}

var slugStrip = regexp.MustCompile(`[^a-z0-9]+`)

func Slugify(s string) string {
	s = strings.Trim(slugStrip.ReplaceAllString(strings.ToLower(s), "-"), "-")
	if len(s) > 48 {
		s = strings.Trim(s[:48], "-")
	}
	return s
}

func DeterministicSlug(events []Event) string {
	counts := map[string]int{}
	order := []string{}
	for _, e := range events {
		if e.Kind != "click" || e.Elem == nil {
			continue
		}
		label := FirstNonEmpty(e.Elem.TestID, e.Elem.Text, e.Elem.AriaLabel, e.Elem.ID)
		if label == "" {
			continue
		}
		if _, seen := counts[label]; !seen {
			order = append(order, label)
		}
		counts[label]++
	}

	best := ""
	bestN := 0
	for _, k := range order {
		if counts[k] > bestN {
			best, bestN = k, counts[k]
		}
	}
	if best == "" {
		for _, e := range events {
			if e.Kind == "click" && e.Title != "" {
				best = e.Title
				break
			}
		}
	}
	if best == "" {
		best = "session"
	}

	errs := 0
	for _, e := range events {
		if !isNoise(e) && (e.Kind == "exception" || e.Kind == "network-error") {
			errs++
		}
	}
	if errs > 0 {
		best = fmt.Sprintf("%s-%d-errors", best, errs)
	}
	s := Slugify(best)
	if s == "" {
		s = "session"
	}
	return s
}

func FirstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func elementRef(el *Element) string {
	if el == nil {
		return "<unknown>"
	}
	if el.Selector != "" {
		return strings.TrimPrefix(el.Selector, "html > body > ")
	}
	if el.ID != "" {
		return el.Tag + "#" + el.ID
	}
	return el.Tag
}

func elementLabel(el *Element) string {
	if el == nil {
		return ""
	}
	if el.Text != "" {
		return "text: " + clip(el.Text, 60)
	}
	if el.AriaLabel != "" {
		return "label: " + clip(el.AriaLabel, 60)
	}
	return ""
}

func imageRef(e Event) string {
	for _, label := range []string{"at", "after", "before"} {
		for _, s := range e.Shots {
			if s.Label == label && s.File != "" {
				return s.File
			}
		}
	}
	return e.FullShot
}

func staticThrough(e Event) bool {
	distinct := map[float64]bool{}
	captured := 0
	for _, s := range e.Shots {
		if s.FrameT != nil {
			distinct[*s.FrameT] = true
			captured++
		}
	}
	return captured > 1 && len(distinct) == 1
}

func clickLine(e Event) string {
	var b strings.Builder
	b.WriteString("Click: ")
	hasPos := e.X != 0 || e.Y != 0
	if hasPos {
		fmt.Fprintf(&b, "%.0f,%.0f", e.X, e.Y)
	}
	// A desktop click carries a position but no element -- there is no DOM
	// to name the control under the cursor -- so "on <unknown>" is noise.
	if e.Elem != nil || !hasPos {
		if hasPos {
			b.WriteString(" on ")
		}
		b.WriteString(elementRef(e.Elem))
		if label := elementLabel(e.Elem); label != "" {
			b.WriteString(" " + label)
		}
	}
	if img := imageRef(e); img != "" {
		b.WriteString(" → " + img)
	}
	if staticThrough(e) {
		b.WriteString(" — screen did not repaint")
	}
	return b.String()
}

func systemLine(s *SystemInfo) string {
	if s == nil {
		return ""
	}
	who := s.Hostname
	if s.User != "" && who != "" {
		who = s.User + "@" + who
	} else if who == "" {
		who = s.User
	}
	var facts []string
	for _, f := range []string{s.OS, s.Arch, s.Model, s.Kernel} {
		if f != "" {
			facts = append(facts, f)
		}
	}
	switch {
	case who == "" && len(facts) == 0:
		return ""
	case len(facts) == 0:
		return who
	case who == "":
		return strings.Join(facts, ", ")
	}
	return fmt.Sprintf("%s (%s)", who, strings.Join(facts, ", "))
}

func displaysLine(s *SystemInfo) string {
	if s == nil || len(s.Displays) == 0 {
		return ""
	}
	var parts []string
	for _, d := range s.Displays {
		p := fmt.Sprintf("%dx%d", d.W, d.H)
		if d.Scale > 1 {
			p += fmt.Sprintf("@%gx", d.Scale)
		}
		if d.Main && len(s.Displays) > 1 {
			p += " (main)"
		}
		parts = append(parts, p)
	}
	return strings.Join(parts, ", ")
}

func errorLine(e Event) string {
	switch e.Kind {
	case "network-error":
		what := e.ErrorText
		if e.Status > 0 {
			what = fmt.Sprintf("%d", e.Status)
		}
		return oneLine(fmt.Sprintf("Error: network %s %s", what, e.URL))
	case "exception":
		return "Error: " + clip(e.Text, 200)
	default:
		return "Error: " + clip(e.Text, 200)
	}
}

func consoleLevel(e Event) string {
	switch e.Level {
	case "", "log":
		return "log"
	case "warning":
		return "warn"
	}
	return e.Level
}

func collapseRepeats(lines []streamLine) []streamLine {
	out := lines[:0]
	for _, l := range lines {
		if n := len(out); n > 0 && sameLineText(out[n-1].text, l.text) {
			out[n-1].repeats++
			continue
		}
		out = append(out, l)
	}
	return out
}

func sameLineText(a, b string) bool {
	ai, bi := strings.Index(a, "  "), strings.Index(b, "  ")
	return ai > 0 && ai == bi && a[ai:] == b[bi:]
}

func echoesANetworkError(e Event, events []Event) bool {
	if e.Kind != "browser-log" || !strings.Contains(e.Text, "Failed to load resource") {
		return false
	}
	for _, o := range events {
		if o.Kind == "network-error" && o.URL == e.URL && math.Abs(o.T-e.T) < 1000 {
			return true
		}
	}
	return false
}

func pageTitle(meta Meta, events []Event) string {
	title := meta.TargetTitle
	for _, e := range events {
		if e.Title != "" && e.Title != "about:blank" {
			title = e.Title
		}
	}
	if title == "about:blank" {
		return ""
	}
	return title
}

type streamLine struct {
	t       float64
	rank    int
	text    string
	repeats int
}

func (l streamLine) render() string {
	if l.repeats > 0 {
		return fmt.Sprintf("%s (×%d)", l.text, l.repeats+1)
	}
	return l.text
}

const (
	narrationMergeGapMs  = 1500
	narrationMergeMaxLen = 400
)

// mergeUtterances joins consecutive transcript segments into readable
// narration lines: segments separated by less than a breath, with nothing
// else happening between them, were spoken as one sentence and should read
// as one. A merged line keeps the first segment's stamp. Any event between
// two segments breaks the run, so a click stays between the words said
// before it and the words said after — that adjacency is what lets a reader
// resolve "this" and "these".
func mergeUtterances(utterances []Utterance, events []Event) []Utterance {
	var eventTimes []float64
	for _, e := range events {
		if e.Kind == "record-start" || e.Kind == "record-stop" || isNoise(e) {
			continue
		}
		eventTimes = append(eventTimes, e.T)
	}
	sort.Float64s(eventTimes)
	eventBetween := func(a, b float64) bool {
		i := sort.SearchFloat64s(eventTimes, a)
		for ; i < len(eventTimes) && eventTimes[i] <= b; i++ {
			if eventTimes[i] > a {
				return true
			}
		}
		return false
	}

	var out []Utterance
	for _, u := range utterances {
		if n := len(out); n > 0 {
			prev := &out[n-1]
			if u.T-prev.EndT <= narrationMergeGapMs &&
				len(prev.Text)+len(u.Text) < narrationMergeMaxLen &&
				!eventBetween(prev.T, u.T) {
				prev.Text = strings.TrimRight(prev.Text, " ") + " " +
					strings.TrimLeft(u.Text, " ")
				prev.EndT = u.EndT
				continue
			}
		}
		out = append(out, u)
	}
	return out
}

func buildStream(events []Event, utterances []Utterance) []streamLine {
	var lines []streamLine
	add := func(t float64, rank int, text string) {
		lines = append(lines, streamLine{t: t, rank: rank, text: text})
	}

	for _, u := range mergeUtterances(utterances, events) {
		add(u.T, 1, fmt.Sprintf("%s  **user narration**: %s", FormatClock(u.T), oneLine(u.Text)))
	}

	for _, e := range events {
		stamp := FormatClock(e.T)
		switch {
		case e.Kind == "click":
			add(e.T, 0, fmt.Sprintf("%s  %s", stamp, clickLine(e)))
		case e.Kind == "mark":
			line := fmt.Sprintf("%s  Mark: %d", stamp, e.Seq)
			if img := imageRef(e); img != "" {
				line += " → " + img
			}
			add(e.T, 0, line)
		case e.Kind == "focus", e.Kind == "window-appear":
			label := "Focus"
			if e.Kind == "window-appear" {
				label = "Window"
			}
			line := fmt.Sprintf("%s  %s: %s", stamp, label, oneLine(e.Title))
			if img := imageRef(e); img != "" {
				line += " → " + img
			}
			add(e.T, 2, line)
		case isError(e):
			if echoesANetworkError(e, events) {
				continue
			}
			add(e.T, 2, fmt.Sprintf("%s  %s", stamp, errorLine(e)))
		case e.Kind == "console":
			add(e.T, 2, fmt.Sprintf("%s  console.%s: %s", stamp, consoleLevel(e), clip(e.Text, 200)))
		case e.Kind == "navigation":
			add(e.T, 2, fmt.Sprintf("%s  Navigate: %s", stamp, e.URL))
		case e.Kind == "note":
			add(e.T, 2, fmt.Sprintf("%s  Note: %s", stamp, oneLine(e.Text)))
		case e.Kind == "tab-switch":
			add(e.T, 2, fmt.Sprintf("%s  Tab: %s",
				stamp, oneLine(FirstNonEmpty(e.Title, e.URL))))
		case e.Kind == "hmr":
			files := strings.Join(e.Files, ", ")
			add(e.T, 2, oneLine(fmt.Sprintf("%s  HMR: %s %s %s", stamp, e.Flavor, e.Type, files)))
		}
	}

	sort.SliceStable(lines, func(i, j int) bool {
		if lines[i].t != lines[j].t {
			return lines[i].t < lines[j].t
		}
		return lines[i].rank < lines[j].rank
	})
	return collapseRepeats(lines)
}

func hmrLogPath(outDir string) string {
	rel := filepath.Join("logs", "websocket-frames.jsonl")
	if st, err := os.Stat(filepath.Join(outDir, rel)); err == nil && st.Size() > 0 {
		return rel
	}
	return ""
}

var extraFileRoles = []struct{ Name, Role string }{
	{"system.wav", "system audio"},
	{"audio.wav", "microphone/narration audio"},
	{"audio.pcm", "raw microphone capture"},
	{"session.json", "machine-readable timeline"},
	{"logs", "raw dev-server websocket frames"},
}

func describeExtras(outDir string, hmrListed bool) string {
	entries, err := os.ReadDir(outDir)
	if err != nil {
		return ""
	}

	shots := 0
	seen := map[string]bool{}
	var others []string
	for _, e := range entries {
		name := e.Name()
		if strings.HasSuffix(name, ".png") {
			shots++
			continue
		}
		if name == "SESSION.md" || (name == "logs" && hmrListed) {
			continue
		}
		seen[name] = true
	}

	var parts []string
	if shots > 0 {
		parts = append(parts, fmt.Sprintf("Screenshots taken: %d", shots))
	}
	for _, f := range extraFileRoles {
		if !seen[f.Name] {
			continue
		}
		delete(seen, f.Name)
		name := f.Name
		if !strings.Contains(name, ".") {
			name += "/"
		}
		parts = append(parts, fmt.Sprintf("%s: `%s`", f.Role, name))
	}
	for name := range seen {
		others = append(others, "`"+name+"`")
	}
	sort.Strings(others)
	return strings.Join(append(parts, others...), ", ")
}

func initialShot(events []Event) string {
	for _, e := range events {
		if e.Kind == "record-start" && e.FullShot != "" {
			return e.FullShot
		}
	}
	return ""
}

func Pack(outDir string, events []Event, clock *Clock, tr *Transcript, meta Meta, opts PackOptions) (*Session, error) {
	var clicks, hmr, errs, marks int
	for _, e := range events {
		switch e.Kind {
		case "click":
			clicks++
		case "hmr":
			hmr++
		case "mark":
			marks++
		}
		if isError(e) && !echoesANetworkError(e, events) {
			errs++
		}
	}

	var utterances []Utterance
	if tr != nil {
		utterances = ToUtterances(tr.Segments, 0, 0)
	}

	session := &Session{
		Version: 2, Meta: meta, Clock: clock.Report(), Transcript: tr, Events: events,
		Counts: Counts{clicks, hmr, errs, len(utterances), len(events)},
	}

	if opts.JSON {
		blob, err := json.MarshalIndent(session, "", "  ")
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(outDir, "session.json"), blob, sessionFileMode); err != nil {
			return nil, err
		}
	}

	var b strings.Builder
	w := func(format string, args ...any) { fmt.Fprintf(&b, format+"\n", args...) }

	title := meta.Title
	if title == "" {
		title = meta.Slug
	}
	w("# Session: %s", title)
	w("")
	w("Start: %s", FirstNonEmpty(meta.StartedWall, meta.StartedISO))
	if meta.Cwd != "" {
		w("Folder: %s", meta.Cwd)
	}
	if line := systemLine(meta.System); line != "" {
		w("Host: %s", line)
	}
	if line := displaysLine(meta.System); line != "" {
		w("Displays: %s", line)
	}
	page := meta.TargetURL
	if title := pageTitle(meta, events); title != "" {
		page = fmt.Sprintf("%s — %s", page, oneLine(title))
	}
	label, subject := "Page", fmt.Sprintf("%d clicks", clicks)
	switch meta.Tool {
	case "recgo-desktop", "recgo-window":
		label, subject = "Capture", fmt.Sprintf("%d clicks · %d marks", clicks, marks)
	case "recgo-audio":
		label, subject = "Capture", fmt.Sprintf("%d marks", marks)
	}
	w("%s: %s", label, page)
	w("Recorded %.0fs by %s · %s · %d errors · %d utterances",
		meta.DurationMs/1000, meta.Tool, subject, errs, len(utterances))

	if tr == nil || !tr.OK {
		reason := "transcription was not run"
		if tr != nil && tr.Reason != "" {
			reason = tr.Reason
		}
		w("No narration: %s", oneLine(reason))
	}
	w("")

	if shot := initialShot(events); shot != "" {
		w("Initial screenshot: %s", shot)
	}
	lines := buildStream(events, utterances)
	for _, l := range lines {
		w("%s", l.render())
	}
	if len(lines) == 0 {
		w("_Nothing was recorded._")
	}
	w("")

	w("## How this was captured")
	w("")
	if meta.Tool == "recgo-audio" {
		w("- Audio-only capture: the microphone and its narration, no screen recording,")
		w("  so there are no frames or screenshots. Marks are the moments you flagged")
		w("  with `m`.")
		w("- All timestamps come from one clock, so order and spacing are exact.")
	} else if meta.Tool == "recgo-desktop" || meta.Tool == "recgo-window" {
		if meta.Tool == "recgo-window" {
			w("- Only the screen (or window) picked when the recording started was")
			w("  captured: every frame and screenshot shows that one source. Clicks")
			w("  elsewhere still appear, as position-only lines.")
		}
		w("- Clicks come from a system-wide listener: position only, since outside a")
		w("  browser nothing names the control under the cursor. Marks are the moments")
		w("  you flagged with `m`. Focus lines are the moment another window, app or")
		w("  browser tab came to the front; Window lines are a new window or dialog")
		w("  appearing. `0002.png` is the screen at that instant;")
		w("  `0002-before.png` and `0002-after.png` are -100ms and +100ms,")
		w("  `0002-full.png` a full-resolution capture.")
		w("- Frames come from the desktop screencast, which emits one only when the")
		w("  screen changes, so identical frames mean nothing moved — not a miss.")
		w("- All timestamps come from one clock, so order and spacing are exact.")
	} else {
		w("- Clicks are reported by a listener injected into the page, so each names the")
		w("  element it hit. `0002.png` is the screen at that click; `0002-before.png`")
		w("  and `0002-after.png` are -100ms and +100ms, `0002-full.png` full-resolution.")
		w("- A frame is emitted only when the page repaints, so three identical frames are")
		w("  evidence the screen did not change — not a missed capture.")
		w("- All timestamps come from one browser clock, so order and spacing are exact.")
	}
	if meta.AudioNote != "" {
		w("- Audio anchor: %s", oneLine(meta.AudioNote))
	}
	if meta.SystemAudio != "" {
		w("- System audio (%s) is mixed into audio.wav with the microphone; a", meta.SystemAudio)
		w("  `Note: system audio off/on` line marks where it was toggled.")
	}
	hmrLog := hmrLogPath(outDir)
	if hmrLog != "" {
		w("- Raw dev-server websocket frames, including every HMR payload: `%s`.", hmrLog)
	}
	if extras := describeExtras(outDir, hmrLog != ""); extras != "" {
		w("- %s.", extras)
	}

	if err := os.WriteFile(filepath.Join(outDir, "SESSION.md"), []byte(b.String()), sessionFileMode); err != nil {
		return nil, err
	}
	return session, nil
}
