package android

import (
	"fmt"
	"github.com/eordano/recgo/internal/tab"
	"html"
	"regexp"
	"strings"
	"time"
	"unicode"
)

const HelperPackage = "dev.eordano.recgo.android"

var packageRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_]*(\.[A-Za-z][A-Za-z0-9_]*)+$`)
var serialRE = regexp.MustCompile(`^(?:[A-Za-z0-9][A-Za-z0-9_.:-]*|\[[0-9A-Fa-f:]+\]:[0-9]+)$`)

type Options struct {
	Serial, Package, ExtraPackages, ADB, Scrcpy, APK, Out               string
	Setup, Devices, Screenshots, Video, Mirror, Logs, Audio, Transcribe bool
	Mic, FFmpeg, Whisper, Model                                         string
	Duration                                                            time.Duration
}

func (o Options) validate() ([]string, error) {
	if !serialRE.MatchString(o.Serial) {
		return nil, fmt.Errorf("--serial must name one explicit ADB device; use --devices")
	}
	if o.Duration < 0 {
		return nil, fmt.Errorf("--duration cannot be negative")
	}
	if o.Transcribe && !o.Audio {
		return nil, fmt.Errorf("--transcribe requires --audio")
	}
	if o.Setup {
		return nil, nil
	}
	names := []string{o.Package}
	if o.ExtraPackages != "" {
		names = append(names, strings.Split(o.ExtraPackages, ",")...)
	}
	if len(names) > 16 {
		return nil, fmt.Errorf("at most 16 packages may be inspected")
	}
	for _, n := range names {
		if !packageRE.MatchString(n) {
			return nil, fmt.Errorf("invalid application package %q", n)
		}
	}
	return names, nil
}

type Node struct {
	ResourceID  string `json:"resourceId"`
	Class       string `json:"class"`
	Text        string `json:"text,omitempty"`
	Description string `json:"description,omitempty"`
	Bounds      [4]int `json:"bounds"`
	Clickable   bool   `json:"clickable"`
	Enabled     bool   `json:"enabled"`
	Checked     bool   `json:"checked"`
	Redacted    bool   `json:"redacted"`
}
type Shot struct {
	File    string  `json:"file,omitempty"`
	StartMs float64 `json:"startMs"`
	EndMs   float64 `json:"endMs"`
	Error   string  `json:"error,omitempty"`
}
type Event struct {
	Kind         string  `json:"kind"`
	ElapsedMs    float64 `json:"elapsedMs,omitempty"`
	Version      int     `json:"version,omitempty"`
	Dropped      int     `json:"dropped,omitempty"`
	Package      string  `json:"package,omitempty"`
	WindowID     int     `json:"windowId,omitempty"`
	TargetStatus string  `json:"targetStatus,omitempty"`
	Node         *Node   `json:"node,omitempty"`
	Seq          int     `json:"seq,omitempty"`
	T            float64 `json:"t"`
	ReceivedMs   float64 `json:"receivedMs"`
	Screenshot   *Shot   `json:"screenshot,omitempty"`
	Text         string  `json:"text,omitempty"`
}

func (e *Event) sanitize() {
	if e.Node == nil {
		e.TargetStatus = "unknown"
		return
	}
	if e.Node.Redacted {
		e.Node.Text = ""
		e.Node.Description = ""
	}
}

type Session struct {
	Version int `json:"version"`
	Meta    struct {
		Tool       string   `json:"tool"`
		StartedISO string   `json:"startedISO"`
		DurationMs float64  `json:"durationMs"`
		Serial     string   `json:"serial"`
		TargetURL  string   `json:"targetURL"`
		Packages   []string `json:"packages"`
	} `json:"meta"`
	Clock struct {
		DeviceToSessionMs float64 `json:"deviceToSessionMs"`
		HandshakeErrorMs  float64 `json:"handshakeErrorMs"`
		Note              string  `json:"note"`
	} `json:"clock"`
	Events     []Event         `json:"events"`
	Warnings   []string        `json:"warnings"`
	Video      string          `json:"video,omitempty"`
	Audio      string          `json:"audio,omitempty"`
	Transcript *tab.Transcript `json:"transcript,omitempty"`
}

// Escape device-controlled labels; never let an app inject report headings/links.
func markdown(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	s = html.EscapeString(s)
	return strings.NewReplacer("\\", "\\\\", "[", "\\[", "]", "\\]", "*", "\\*", "_", "\\_", "`", "\\`", "#", "\\#", "|", "\\|").Replace(s)
}
func label(e Event) string {
	if e.Node == nil {
		return "unknown target (Android did not expose a node)"
	}
	n := e.Node
	parts := []string{}
	if n.ResourceID != "" {
		parts = append(parts, n.ResourceID)
	}
	if n.Text != "" {
		parts = append(parts, n.Text)
	} else if n.Description != "" {
		parts = append(parts, n.Description)
	}
	if len(parts) == 0 {
		parts = append(parts, n.Class)
	}
	if n.Redacted {
		parts = append(parts, "[editable/password value redacted]")
	}
	return strings.Join(parts, " · ")
}
func Report(s Session, live bool) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Session: Android %s\n\nStart: %s\n\nPage: android://%s\n\nRecorded %.0fs by recgo-android\n\n", markdown(s.Meta.TargetURL), s.Meta.StartedISO, markdown(s.Meta.TargetURL), s.Meta.DurationMs/1000)
	if live {
		b.WriteString("Recording in progress.\n\n")
	}
	b.WriteString("Accessibility actions, not raw touch coordinates. Bounds describe an element, not the finger location. Missing events/targets are possible for custom views, WebViews and system UI.\n\nScreenshots are captured **after receipt**, not at the tap or before it. Video starts independently; frame/audio synchronization is approximate.\n\n")
	fmt.Fprintf(&b, "Clock: %s (initial handshake ±%.1f ms).\n\n", markdown(s.Clock.Note), s.Clock.HandshakeErrorMs)
	if s.Video != "" {
		fmt.Fprintf(&b, "Video: [%s](%s)\n\n", s.Video, s.Video)
	}
	if s.Audio != "" {
		fmt.Fprintf(&b, "Narration: [%s](%s)\n\n", s.Audio, s.Audio)
	}
	for _, w := range s.Warnings {
		fmt.Fprintf(&b, "Warning: %s\n\n", markdown(w))
	}
	for _, e := range s.Events {
		text := e.Text
		if e.Kind == "click" || e.Kind == "long-click" || e.Kind == "scroll" || e.Kind == "window" {
			text = label(e)
		}
		kind := map[string]string{"click": "Click", "long-click": "Click", "window": "Window", "scroll": "Scroll", "warning": "Error"}[e.Kind]
		if kind == "" {
			kind = "Event"
		}
		if e.Kind == "long-click" {
			text = "long press: " + text
		}
		fmt.Fprintf(&b, "%s  %s: %s", tab.FormatClock(e.T), kind, markdown(text))
		if e.Screenshot != nil {
			if e.Screenshot.File != "" {
				fmt.Fprintf(&b, " (capture started %.0f ms after event) → %s", e.Screenshot.StartMs-e.T, e.Screenshot.File)
			}
			if e.Screenshot.Error != "" {
				fmt.Fprintf(&b, " (screenshot unavailable: %s)", markdown(e.Screenshot.Error))
			}
		}
		b.WriteString("\n\n")
	}
	if s.Transcript != nil {
		if !s.Transcript.OK {
			fmt.Fprintf(&b, "Transcription unavailable: %s\n", markdown(s.Transcript.Reason))
		}
		for _, seg := range s.Transcript.Segments {
			fmt.Fprintf(&b, "%s  **user narration**: %s\n\n", tab.FormatClock(seg.T), markdown(seg.Text))
		}
	}
	return b.String()
}
