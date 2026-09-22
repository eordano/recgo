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
	// FrameStream says the desktop capture pumped frames continuously (the
	// Linux portal), so an unchanged frame is repaint evidence. Windows and
	// macOS take one screenshot per event and have none.
	FrameStream bool   `json:"frameStream,omitempty"`
	Tool        string `json:"tool"`
	AudioNote   string `json:"audioNote,omitempty"`
	SystemAudio string `json:"systemAudio,omitempty"`
	TitleNote   string `json:"titleNote,omitempty"`

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

const slugMaxLen = 48

func Slugify(s string) string {
	return clipSlug(strings.Trim(slugStrip.ReplaceAllString(strings.ToLower(s), "-"), "-"), slugMaxLen)
}

func clipSlug(s string, max int) string {
	if len(s) > max {
		s = strings.Trim(s[:max], "-")
	}
	return s
}

// siteSeparators split "Page title — Site name" the way browsers and apps
// conventionally do; the site half may lead ("GitHub · issue") or trail.
var siteSeparators = []string{" — ", " · ", " | ", " - "}

// stripSiteSuffix drops the site-name half of a page title, choosing the
// shorter end because site names are short and page names are not. A bare
// " - " also splits ordinary phrases, so the strip only happens when what
// remains still reads like a name: at least three words.
func stripSiteSuffix(title string) string {
	first, last := -1, -1
	firstSep, lastSep := "", ""
	for _, sep := range siteSeparators {
		if i := strings.Index(title, sep); i >= 0 && (first < 0 || i < first) {
			first, firstSep = i, sep
		}
		if i := strings.LastIndex(title, sep); i >= 0 && i > last {
			last, lastSep = i, sep
		}
	}
	if first < 0 {
		return title
	}
	leading, withoutLeading := title[:first], title[first+len(firstSep):]
	trailing, withoutTrailing := title[last+len(lastSep):], title[:last]
	candidates := []string{withoutTrailing, withoutLeading}
	if len(strings.Fields(leading)) < len(strings.Fields(trailing)) {
		candidates = []string{withoutLeading, withoutTrailing}
	}
	for _, c := range candidates {
		if len(strings.Fields(c)) >= 3 {
			return strings.TrimSpace(c)
		}
	}
	return title
}

// DeterministicSlug names a session without an LLM: the page title first,
// because a whole-deck review is about the deck and not about whichever
// element happened to be clicked most; the most-clicked element when the
// page had no title. A --launch session starts at about:blank and only the
// navigation into the page carries its title, and a deck flipped with the
// keyboard has no clicks at all, so navigations and tab switches count too.
func DeterministicSlug(events []Event) string {
	best := ""
	for _, e := range events {
		switch e.Kind {
		case "record-start", "click", "navigation", "tab-switch":
		default:
			continue
		}
		if e.Title != "" {
			best = stripSiteSuffix(e.Title)
			break
		}
	}

	if best == "" {
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
		bestN := 0
		for _, k := range order {
			if counts[k] > bestN {
				best, bestN = k, counts[k]
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
	s := Slugify(best)
	if s == "" {
		s = "session"
	}
	// The error count is the point of the suffix, so the title half gives
	// way to it rather than the other way round.
	if errs > 0 {
		suffix := fmt.Sprintf("-%d-errors", errs)
		s = clipSlug(s, slugMaxLen-len(suffix)) + suffix
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
	if e.Note != "" {
		b.WriteString(" — " + oneLine(e.Note))
	}
	if staticThrough(e) {
		b.WriteString(noRepaintNote)
	}
	return b.String()
}

const noRepaintNote = " — screen did not repaint"

// clickKey names what a click hit, without the position or the image name,
// so repeated clicks on one control can be told apart from clicks elsewhere.
func clickKey(e Event) string {
	if e.Elem == nil {
		return fmt.Sprintf("%.0f,%.0f", e.X, e.Y)
	}
	return elementRef(e.Elem) + " " + elementLabel(e.Elem)
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
		if n := len(out); n > 0 && sameLineText(out[n-1], l) {
			out[n-1].repeats++
			out[n-1].through = l.image
			continue
		}
		out = append(out, l)
	}
	return out
}

// sameLineText decides whether a line is a repeat of the one before it.
// Click lines repeat when they hit the same control and none of them
// repainted the screen: a burst of clicks that changed nothing reads as one
// fact. Any other line repeats only when its text after the stamp matches.
func sameLineText(a, b streamLine) bool {
	if a.clickKey != "" || b.clickKey != "" {
		return a.clickKey == b.clickKey && a.static && b.static
	}
	ai, bi := strings.Index(a.text, "  "), strings.Index(b.text, "  ")
	return ai > 0 && ai == bi && a.text[ai:] == b.text[bi:]
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

	clickKey string
	image    string
	static   bool
	through  string
}

func (l streamLine) render() string {
	if l.repeats == 0 {
		return l.text
	}
	if l.clickKey != "" {
		text := strings.TrimSuffix(l.text, noRepaintNote)
		return fmt.Sprintf("%s (×%d, through %s)%s", text, l.repeats+1, l.through, noRepaintNote)
	}
	return fmt.Sprintf("%s (×%d)", l.text, l.repeats+1)
}

const (
	narrationMergeGapMs  = 1500
	narrationMergeMaxLen = 400
)

// isAnchor reports the events that are pointed at from inside a narration
// line rather than breaking it: a click (or a mark, or a window coming to
// the front) during a sentence is what the words around it refer to.
func isAnchor(e Event) bool {
	switch e.Kind {
	case "click", "mark", "focus", "window-appear":
		return true
	}
	return false
}

func anchorText(e Event) string {
	if img := imageRef(e); img != "" {
		return "[→" + img + "]"
	}
	switch e.Kind {
	case "mark":
		return fmt.Sprintf("[mark %d]", e.Seq)
	case "focus":
		return fmt.Sprintf("[focus %d]", e.Seq)
	case "window-appear":
		return fmt.Sprintf("[window %d]", e.Seq)
	}
	return fmt.Sprintf("[click %d]", e.Seq)
}

// mergeUtterances joins consecutive transcript segments into readable
// narration lines: segments separated by less than a breath were spoken as
// one sentence and should read as one. A merged line keeps the first
// segment's stamp. Clicks between two segments do not break the run — they
// are anchored inline by narrationText, at the word they fell on, which is
// what lets a reader resolve "this" and "these". Anything else happening
// between two segments (an error, a navigation, a note) still splits them.
func mergeUtterances(utterances []Utterance, events []Event) []Utterance {
	var eventTimes []float64
	for _, e := range events {
		if e.Kind == "record-start" || e.Kind == "record-stop" || isNoise(e) || isAnchor(e) {
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
		if len(u.Words) == 0 {
			u.Words = []Segment{{T: u.T, EndT: u.EndT, Text: u.Text}}
		}
		if n := len(out); n > 0 {
			prev := &out[n-1]
			if u.T-prev.EndT <= narrationMergeGapMs &&
				len(prev.Text)+len(u.Text) < narrationMergeMaxLen &&
				!eventBetween(prev.T, u.T) {
				prev.Text = strings.TrimRight(prev.Text, " ") + " " +
					strings.TrimLeft(u.Text, " ")
				prev.EndT = u.EndT
				prev.Words = append(prev.Words, u.Words...)
				continue
			}
		}
		out = append(out, u)
	}
	return out
}

// narrationText renders a merged utterance with each anchor placed before
// the first word spoken at or after it. anchors must be sorted by T.
func narrationText(u Utterance, anchors []Event) string {
	words := u.Words
	if len(words) == 0 {
		words = []Segment{{T: u.T, EndT: u.EndT, Text: u.Text}}
	}
	var parts []string
	i := 0
	for _, w := range words {
		for ; i < len(anchors) && anchors[i].T <= w.T; i++ {
			parts = append(parts, anchorText(anchors[i]))
		}
		if w.Text != "" {
			parts = append(parts, w.Text)
		}
	}
	for ; i < len(anchors); i++ {
		parts = append(parts, anchorText(anchors[i]))
	}
	return oneLine(strings.Join(parts, " "))
}

var (
	approvalPhrases = []string{
		"this is great", "looks great", "looks good", "love it", "i love",
		"perfect", "this is good", "keep this", "amazing", "great",
	}
	changeWords = map[string]bool{
		"drop": true, "remove": true, "change": true, "make": true, "replace": true,
		"let's": true, "lets": true, "add": true, "move": true, "fix": true,
		"instead": true, "reduce": true, "less": true, "more": true, "but": true,
		"rename": true, "highlight": true, "center": true, "update": true,
	}
)

// isApproval tells a short verdict ("this is great", "I love it") from an
// instruction that happens to open with praise ("this is great, make sure
// that..."): a change word anywhere means work is being asked for.
func isApproval(text string) bool {
	text = strings.ToLower(oneLine(text))
	words := strings.Fields(text)
	if len(words) == 0 || len(words) > 12 {
		return false
	}
	for _, w := range words {
		if changeWords[strings.Trim(w, ".,;:!?\"'()")] {
			return false
		}
	}
	for _, p := range approvalPhrases {
		if strings.Contains(text, p) {
			return true
		}
	}
	return false
}

func buildStream(events []Event, utterances []Utterance) []streamLine {
	var lines []streamLine
	add := func(t float64, rank int, text string) *streamLine {
		lines = append(lines, streamLine{t: t, rank: rank, text: text})
		return &lines[len(lines)-1]
	}

	var anchors []Event
	for _, e := range events {
		if isAnchor(e) {
			anchors = append(anchors, e)
		}
	}
	sort.SliceStable(anchors, func(i, j int) bool { return anchors[i].T < anchors[j].T })

	for _, u := range mergeUtterances(utterances, events) {
		var inside []Event
		for _, a := range anchors {
			if a.T > u.T && a.T <= u.EndT {
				inside = append(inside, a)
			}
		}
		tag := "user narration"
		if isApproval(u.Text) {
			tag = "user approves"
		}
		add(u.T, 1, fmt.Sprintf("%s  **%s**: %s", FormatClock(u.T), tag, narrationText(u, inside)))
	}

	for _, e := range events {
		stamp := FormatClock(e.T)
		switch {
		case e.Kind == "click":
			l := add(e.T, 0, fmt.Sprintf("%s  %s", stamp, clickLine(e)))
			l.clickKey, l.image, l.static = clickKey(e), imageRef(e), staticThrough(e)
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
			add(e.T, 2, fmt.Sprintf("%s  %s", stamp, navigationLine(e)))
		case e.Kind == "note":
			add(e.T, 2, fmt.Sprintf("%s  Note: %s", stamp, oneLine(e.Text)))
		case e.Kind == "tab-switch":
			add(e.T, 2, fmt.Sprintf("%s  Tab: %s",
				stamp, oneLine(FirstNonEmpty(e.Title, e.URL))))
		case e.Kind == "hmr":
			files := strings.Join(e.Files, ", ")
			add(e.T, 2, oneLine(fmt.Sprintf("%s  HMR: %s %s %s", stamp, e.Flavor, e.Type, files)))
		case e.Kind == "record-stop":
			add(e.T, 3, fmt.Sprintf("%s  Stop: recording ended", stamp))
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
	// A browser Page is the URL and the page's title. A desktop Capture is
	// the capture source alone: its focus events carry titles too, but the
	// last window to come to the front says nothing about what was
	// recorded (on the Windows run, a browser opened at the end of a Unity
	// session took over the line).
	page := meta.TargetURL
	label, subject := "Page", fmt.Sprintf("%d clicks", clicks)
	switch meta.Tool {
	case "recgo-desktop", "recgo-window", "recgo-alttester":
		label, subject = "Capture", fmt.Sprintf("%d clicks · %d marks", clicks, marks)
	case "recgo-audio":
		label, subject = "Capture", fmt.Sprintf("%d marks", marks)
	}
	if title := pageTitle(meta, events); title != "" && label == "Page" {
		page = fmt.Sprintf("%s — %s", page, oneLine(title))
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
	// What a repeated screenshot proves depends on where it came from: a
	// frame stream (the Linux portal) emits only on change, so an identical
	// frame is evidence; windows-gdi and macOS screencapture shoot once per
	// event and prove nothing about the interval between two.
	frameNote := func() {
		if meta.FrameStream {
			w("- Frames come from the desktop screencast, which emits one only when the")
			w("  screen changes, so identical frames mean nothing moved — not a miss.")
			return
		}
		w("- There is no frame stream on this platform: a screenshot is taken at each")
		w("  click, mark and focus event, and nothing is captured in between. Two")
		w("  identical screenshots are not evidence that the screen did not repaint,")
		w("  so no line carries a `screen did not repaint` verdict and there are no")
		w("  `-before` / `-after` frames.")
	}
	if meta.Tool == "recgo-audio" {
		w("- Audio-only capture: the microphone and its narration, no screen recording,")
		w("  so there are no frames or screenshots. Marks are the moments you flagged")
		w("  with `m`; one pressed mid-sentence is anchored inline as `[mark 3]` at the")
		w("  word it fell on.")
		w("- A short verdict with no change asked for (\"looks good\", \"I love it\") is")
		w("  tagged **user approves** instead of **user narration**.")
		w("- All timestamps come from one clock, so order and spacing are exact.")
	} else if meta.Tool == "recgo-alttester" {
		w("- Clicks come from a system-wide listener and are resolved to the Unity UI")
		w("  element under them by asking the game through the AltTester SDK inside the")
		w("  dev build, so a click line names the control it hit. A `/Root/Panel/Button`")
		w("  path is a UGUI transform path (scene root to the hit object); a `uitk:` path")
		w("  is a UI Toolkit element path from its document root, with the element's USS")
		w("  classes. A click with no element means the probe found nothing under it, or")
		w("  the click was outside the game window (`— outside <window>` names which).")
		w("  Marks are the moments you flagged with `m`. Focus lines are the moment")
		w("  another window, app or browser tab came to the front; Window lines are a")
		w("  new window or dialog appearing. `0002.png` is the screen at that instant;")
		w("  `0002-before.png` and `0002-after.png` are -100ms and +100ms,")
		w("  `0002-full.png` a full-resolution capture.")
		w("- A click, mark, focus or new window that fell mid-sentence is anchored")
		w("  inline in the narration as `[→0002.png]` (`[mark 3]`, `[focus 3]`,")
		w("  `[window 3]` without a screenshot) at the word it landed on; its own line")
		w("  follows the sentence.")
		frameNote()
		if meta.FrameStream {
			w("- Repeated clicks on one element (or at one position, when none was named)")
			w("  that never changed the screen collapse to one line with `(×N, through")
			w("  0008.png)` naming the last screenshot.")
		}
		w("- A short verdict with no change asked for (\"looks good\", \"I love it\") is")
		w("  tagged **user approves** instead of **user narration**.")
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
		w("- A click, mark, focus or new window that fell mid-sentence is anchored")
		w("  inline in the narration as `[→0002.png]` (`[mark 3]`, `[focus 3]`,")
		w("  `[window 3]` without a screenshot) at the word it landed on; its own line")
		w("  follows the sentence.")
		frameNote()
		if meta.FrameStream {
			w("- Repeated clicks at one position that never changed the screen collapse to")
			w("  one line with `(×N, through 0008.png)` naming the last screenshot.")
		}
		w("- A short verdict with no change asked for (\"looks good\", \"I love it\") is")
		w("  tagged **user approves** instead of **user narration**.")
		w("- All timestamps come from one clock, so order and spacing are exact.")
	} else {
		w("- Clicks are reported by a listener injected into the page, so each names the")
		w("  element it hit. `0002.png` is the screen at that click; `0002-before.png`")
		w("  and `0002-after.png` are -100ms and +100ms, `0002-full.png` full-resolution.")
		w("- A click that fell mid-sentence is anchored inline in the narration as")
		w("  `[→0002.png]` (or `[click 3]` without a screenshot) at the word it landed")
		w("  on, so \"this\" points at the element under the cursor; the click's own")
		w("  line follows the sentence.")
		w("- A frame is emitted only when the page repaints, so three identical frames are")
		w("  evidence the screen did not change — not a missed capture.")
		w("- Repeated clicks on one element that never repainted collapse to one line")
		w("  with `(×N, through 0008.png)` naming the last of the screenshots.")
		w("- A short verdict with no change asked for (\"looks good\", \"I love it\") is")
		w("  tagged **user approves** instead of **user narration**.")
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
