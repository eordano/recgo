package tab

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type SpeechRegion struct {
	StartMs float64 `json:"startMs"`
	EndMs   float64 `json:"endMs"`
}

var (
	durationRe   = regexp.MustCompile(`Duration:\s*(\d+):(\d+):(\d+\.\d+)`)
	silenceStart = regexp.MustCompile(`silence_start:\s*(-?[\d.]+)`)
	silenceEnd   = regexp.MustCompile(`silence_end:\s*([\d.]+)`)
)

func DetectSpeechRegions(ffmpegBin, wavPath string, noiseDb, minSilenceSec float64) ([]SpeechRegion, float64, error) {
	if ffmpegBin == "" {
		ffmpegBin = "ffmpeg"
	}
	if noiseDb == 0 {
		noiseDb = -40
	}
	if minSilenceSec == 0 {
		minSilenceSec = 0.35
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, ffmpegBin,
		"-hide_banner", "-i", wavPath,
		"-af", fmt.Sprintf("silencedetect=n=%gdB:d=%g", noiseDb, minSilenceSec),
		"-f", "null", "-")
	out, _ := cmd.CombinedOutput()
	text := string(out)

	m := durationRe.FindStringSubmatch(text)
	if m == nil {
		return nil, 0, fmt.Errorf("could not read duration from ffmpeg output")
	}
	h, _ := strconv.ParseFloat(m[1], 64)
	mins, _ := strconv.ParseFloat(m[2], 64)
	secs, _ := strconv.ParseFloat(m[3], 64)
	durationMs := (h*3600 + mins*60 + secs) * 1000

	type silence struct{ start, end float64 }
	var silences []silence
	open := -1.0
	for _, line := range strings.Split(text, "\n") {
		if s := silenceStart.FindStringSubmatch(line); s != nil {
			v, _ := strconv.ParseFloat(s[1], 64)
			open = v * 1000
			if open < 0 {
				open = 0
			}
		}
		if e := silenceEnd.FindStringSubmatch(line); e != nil && open >= 0 {
			v, _ := strconv.ParseFloat(e[1], 64)
			silences = append(silences, silence{open, v * 1000})
			open = -1
		}
	}
	if open >= 0 {
		silences = append(silences, silence{open, durationMs})
	}

	var regions []SpeechRegion
	cursor := 0.0
	for _, s := range silences {
		if s.start > cursor {
			regions = append(regions, SpeechRegion{StartMs: cursor, EndMs: s.start})
		}
		if s.end > cursor {
			cursor = s.end
		}
	}
	if cursor < durationMs {
		regions = append(regions, SpeechRegion{StartMs: cursor, EndMs: durationMs})
	}

	filtered := regions[:0]
	for _, r := range regions {
		if r.EndMs-r.StartMs > 50 {
			filtered = append(filtered, r)
		}
	}
	return filtered, durationMs, nil
}

func AlignGroupsToRegions(groups [][]Segment, regions []SpeechRegion) ([][]Segment, error) {
	if len(groups) == 0 || len(regions) == 0 {
		return groups, fmt.Errorf("nothing to align")
	}
	if len(groups) != len(regions) {
		return groups, fmt.Errorf(
			"transcript has %d utterance(s) but audio has %d speech region(s); "+
				"leaving timings untouched rather than guessing a mapping",
			len(groups), len(regions))
	}

	out := make([][]Segment, len(groups))
	for i, words := range groups {
		if len(words) == 0 {
			out[i] = words
			continue
		}
		region := regions[i]
		from := words[0].AudioStartMs
		to := words[len(words)-1].AudioEndMs
		span := to - from
		target := region.EndMs - region.StartMs

		mapMs := func(ms float64) float64 {
			if span <= 0 {
				return region.StartMs
			}
			return region.StartMs + ((ms-from)/span)*target
		}

		mapped := make([]Segment, len(words))
		for j, w := range words {
			mapped[j] = w
			mapped[j].AudioStartMs = mapMs(w.AudioStartMs)
			mapped[j].AudioEndMs = mapMs(w.AudioEndMs)
		}
		out[i] = mapped
	}
	return out, nil
}

func VADCorrect(t Transcript, wavPath, ffmpegBin string, clock *Clock) Transcript {
	if !t.OK || t.Granularity != "word" || len(t.RawSegments) == 0 {
		t.VADReason = "needs word timings and server segments"
		return t
	}

	regions, _, err := DetectSpeechRegions(ffmpegBin, wavPath, 0, 0)
	if err != nil {
		t.VADReason = err.Error()
		return t
	}

	var groups [][]Segment
	for _, seg := range t.RawSegments {
		lo, hi := seg.Start*1000-1, seg.End*1000
		var g []Segment
		for _, w := range t.Segments {
			if w.AudioStartMs >= lo && w.AudioStartMs < hi {
				g = append(g, w)
			}
		}
		if len(g) > 0 {
			groups = append(groups, g)
		}
	}

	aligned, err := AlignGroupsToRegions(groups, regions)
	if err != nil {
		t.VADReason = err.Error()
		return t
	}

	var segs []Segment
	for _, g := range aligned {
		for _, w := range g {
			st, _ := clock.FromAudioTime(w.AudioStartMs / 1000)
			en, _ := clock.FromAudioTime(w.AudioEndMs / 1000)
			w.T, w.EndT = st, en
			segs = append(segs, w)
		}
	}

	t.Segments = segs
	t.VADCorrected = true
	t.AccuracyNote = "Word timings corrected against ffmpeg silencedetect: utterance onsets " +
		"are aligned to real speech, word positions within an utterance are interpolated."
	return t
}
