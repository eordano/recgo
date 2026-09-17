package main

import (
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/eordano/recgo/internal/tab"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

//go:embed template.html
var tmpl string

type event struct {
	Kind      string   `json:"kind"`
	T         int      `json:"t"`
	Text      string   `json:"text,omitempty"`
	Sel       string   `json:"sel,omitempty"`
	X         *int     `json:"x,omitempty"`
	Y         *int     `json:"y,omitempty"`
	Img       string   `json:"img,omitempty"`
	NoRepaint bool     `json:"norepaint,omitempty"`
	Fx        *float64 `json:"fx,omitempty"`
	Fy        *float64 `json:"fy,omitempty"`
	Start     bool     `json:"start,omitempty"`
}

type session struct {
	Dir      string   `json:"dir"`
	Title    string   `json:"title"`
	Start    string   `json:"start"`
	Page     string   `json:"page"`
	Folder   string   `json:"folder"`
	Recorded string   `json:"recorded"`
	Duration int      `json:"duration"`
	Notes    []string `json:"notes"`
	Audio    bool     `json:"audio"`
	Events   []*event `json:"events"`
	Frames   []*event `json:"frames"`
}

var (
	clickRE     = regexp.MustCompile(`^(\d\d\.\d\d\.\d\d)  Click: (.*?) → (\d{4}\.png)( — screen did not repaint)?\s*$`)
	shotRE      = regexp.MustCompile(`^(\d\d\.\d\d\.\d\d)  (Mark|Focus|Window): (.*?)(?: → (\d{4}\.png))?\s*$`)
	narrBoldRE  = regexp.MustCompile(`^(\d\d\.\d\d\.\d\d)  \*\*user narration\*\*: (.*)$`)
	narrPlainRE = regexp.MustCompile(`^(\d\d\.\d\d\.\d\d): (.*)$`)
	otherRE     = regexp.MustCompile(`^(\d\d\.\d\d\.\d\d)  ([A-Za-z._]+): (.*)$`)
	coordRE     = regexp.MustCompile(`(?s)^(\d+),(\d+)(?: on (.*))?$`)
	durationRE  = regexp.MustCompile(`Recorded (\d+)s`)
)

func main() {
	dir := flag.String("dir", "", "sessions root (default: [recording] output_dir in config.toml, else ~/walk-and-talk; ~/Documents/walk-and-talk on macOS)")
	out := flag.String("out", "", "output file (default <dir>/index.html)")
	flag.Parse()

	root := *dir
	if root == "" {
		root = defaultRoot()
	}
	sessions, err := scan(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, "recgo-sessions:", err)
		os.Exit(1)
	}
	if len(sessions) == 0 {
		fmt.Fprintf(os.Stderr, "recgo-sessions: no session folders (SESSION.md + 0001.png) under %s\n", root)
		os.Exit(1)
	}

	data, err := json.Marshal(sessions)
	if err != nil {
		fmt.Fprintln(os.Stderr, "recgo-sessions:", err)
		os.Exit(1)
	}
	outPath := *out
	if outPath == "" {
		outPath = filepath.Join(root, "index.html")
	}
	html := strings.Replace(tmpl, "__DATA__", string(data), 1)
	if err := os.WriteFile(outPath, []byte(html), 0o600); err != nil {
		fmt.Fprintln(os.Stderr, "recgo-sessions:", err)
		os.Exit(1)
	}
	fmt.Printf("%s (%d sessions)\n", outPath, len(sessions))
}

// defaultRoot mirrors recgo-tab's output root, including its iCloud fallback
// location, so the viewer finds sessions wherever the recorder put them.
func defaultRoot() string {
	root := tab.DefaultOutRoot()
	if _, err := os.Stat(root); err == nil {
		return root
	}
	home, _ := os.UserHomeDir()
	for _, alt := range []string{filepath.Join(home, "walk-and-talk"), filepath.Join(tab.DocumentsDir(), "walk-and-talk")} {
		if _, err := os.Stat(alt); err == nil {
			return alt
		}
	}
	return root
}

func scan(root string) ([]*session, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(root, e.Name())
		if fileExists(filepath.Join(dir, "SESSION.md")) && fileExists(filepath.Join(dir, "0001.png")) {
			names = append(names, e.Name())
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(names)))
	sessions := make([]*session, 0, len(names))
	for _, name := range names {
		s, err := parseSession(filepath.Join(root, name))
		if err != nil {
			fmt.Fprintf(os.Stderr, "recgo-sessions: skipping %s: %v\n", name, err)
			continue
		}
		sessions = append(sessions, s)
	}
	return sessions, nil
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

func parseSession(dir string) (*session, error) {
	raw, err := os.ReadFile(filepath.Join(dir, "SESSION.md"))
	if err != nil {
		return nil, err
	}
	s := &session{
		Dir:    filepath.Base(dir),
		Title:  filepath.Base(dir),
		Notes:  []string{},
		Events: []*event{},
		Audio:  fileExists(filepath.Join(dir, "audio.wav")),
	}
	var events []*event
	for _, line := range strings.Split(strings.ToValidUTF8(string(raw), "�"), "\n") {
		if strings.HasPrefix(line, "## ") {
			break
		}
		if v, ok := strings.CutPrefix(line, "# Session: "); ok {
			s.Title = strings.TrimSpace(v)
			continue
		}
		if v, ok := strings.CutPrefix(line, "Start: "); ok {
			s.Start = strings.TrimSpace(v)
			continue
		}
		if v, ok := strings.CutPrefix(line, "Folder: "); ok {
			s.Folder = strings.TrimSpace(v)
			continue
		}
		if v, ok := strings.CutPrefix(line, "Page: "); ok {
			s.Page = strings.TrimSpace(v)
			continue
		}
		if v, ok := strings.CutPrefix(line, "Capture: "); ok {
			s.Page = strings.TrimSpace(v)
			continue
		}
		if strings.HasPrefix(line, "Recorded ") {
			s.Recorded = strings.TrimSpace(line)
			if m := durationRE.FindStringSubmatch(line); m != nil {
				s.Duration, _ = strconv.Atoi(m[1])
			}
			continue
		}
		if strings.HasPrefix(line, "Initial screenshot: ") {
			continue
		}
		if m := clickRE.FindStringSubmatch(line); m != nil {
			ev := &event{Kind: "click", T: toSeconds(m[1]), Img: m[3], NoRepaint: m[4] != ""}
			if c := coordRE.FindStringSubmatch(m[2]); c != nil {
				x, _ := strconv.Atoi(c[1])
				y, _ := strconv.Atoi(c[2])
				ev.X, ev.Y = &x, &y
				ev.Sel, ev.Text, _ = strings.Cut(c[3], " text: ")
				ev.Sel = strings.TrimSpace(ev.Sel)
				ev.Text = strings.TrimSpace(ev.Text)
			} else {
				ev.Sel = strings.TrimSpace(m[2])
			}
			events = append(events, ev)
			continue
		}
		if m := shotRE.FindStringSubmatch(line); m != nil {
			events = append(events, &event{
				Kind: strings.ToLower(m[2]), T: toSeconds(m[1]),
				Text: strings.TrimSpace(m[3]), Img: m[4],
			})
			continue
		}
		if m := narrBoldRE.FindStringSubmatch(line); m != nil {
			events = append(events, &event{Kind: "narration", T: toSeconds(m[1]), Text: strings.TrimSpace(m[2])})
			continue
		}
		if m := narrPlainRE.FindStringSubmatch(line); m != nil {
			events = append(events, &event{Kind: "narration", T: toSeconds(m[1]), Text: strings.TrimSpace(m[2])})
			continue
		}
		if m := otherRE.FindStringSubmatch(line); m != nil {
			events = append(events, &event{Kind: strings.ToLower(m[2]), T: toSeconds(m[1]), Text: strings.TrimSpace(m[3])})
			continue
		}
		if t := strings.TrimSpace(line); t != "" {
			s.Notes = append(s.Notes, t)
		}
	}

	for _, ev := range events {
		if n := len(s.Events); n > 0 && ev.Kind == "narration" &&
			s.Events[n-1].Kind == "narration" && s.Events[n-1].T == ev.T {
			s.Events[n-1].Text += " " + ev.Text
			continue
		}
		s.Events = append(s.Events, ev)
	}

	var clicks, shots []*event
	for _, ev := range s.Events {
		if ev.Img == "" {
			continue
		}
		shots = append(shots, ev)
		if ev.Kind == "click" {
			clicks = append(clicks, ev)
		}
	}
	annotate(dir, clicks)
	s.Frames = append([]*event{{Kind: "click", T: 0, Img: "0001.png", Text: "session start", Start: true}}, shots...)
	return s, nil
}

// annotate turns each click's viewport coordinates into fractions of its
// screenshot, using the -full capture (viewport x devicePixelRatio) as the
// reference. The ratio is assumed 2 (every capture so far is Retina) unless
// some click would land outside the frame, which proves a 1x display.
func annotate(dir string, clicks []*event) {
	type wh struct{ w, h int }
	dims := map[string]wh{}
	for _, ev := range clicks {
		p := filepath.Join(dir, strings.TrimSuffix(ev.Img, ".png")+"-full.png")
		w, h, err := pngDims(p)
		if err != nil {
			w, h, err = pngDims(filepath.Join(dir, ev.Img))
		}
		if err == nil {
			dims[ev.Img] = wh{w, h}
		}
	}
	dpr := 2
	for _, ev := range clicks {
		d, ok := dims[ev.Img]
		if ok && ev.X != nil && (*ev.X*2 > d.w+8 || *ev.Y*2 > d.h+8) {
			dpr = 1
			break
		}
	}
	for _, ev := range clicks {
		d, ok := dims[ev.Img]
		if !ok || ev.X == nil || d.w == 0 || d.h == 0 {
			continue
		}
		fx := float64(*ev.X*dpr) / float64(d.w)
		fy := float64(*ev.Y*dpr) / float64(d.h)
		ev.Fx, ev.Fy = &fx, &fy
	}
}

func pngDims(path string) (int, int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, err
	}
	defer f.Close()
	cfg, err := png.DecodeConfig(f)
	if err != nil {
		return 0, 0, err
	}
	return cfg.Width, cfg.Height, nil
}

func toSeconds(stamp string) int {
	parts := strings.SplitN(stamp, ".", 3)
	h, _ := strconv.Atoi(parts[0])
	m, _ := strconv.Atoi(parts[1])
	sec, _ := strconv.Atoi(parts[2])
	return h*3600 + m*60 + sec
}
