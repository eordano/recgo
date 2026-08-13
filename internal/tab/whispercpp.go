package tab

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

func ModelSearchPaths() []string {
	var candidates []string
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		candidates = append(candidates, filepath.Join(d, "recgo", "models"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates,
			filepath.Join(home, ".local", "share", "recgo", "models"),
			filepath.Join(home, ".local", "share", "whisper"),
			filepath.Join(home, "models"),
		)
	}

	seen := map[string]bool{}
	dirs := make([]string, 0, len(candidates))
	for _, d := range candidates {
		if seen[d] {
			continue
		}
		seen[d] = true
		dirs = append(dirs, d)
	}
	return dirs
}

var modelPreference = []string{
	"ggml-large-v3-turbo-q8_0.bin",
	"ggml-large-v3-turbo.bin",
	"ggml-large-v3.bin",
	"ggml-medium.en.bin",
	"ggml-small.en.bin",
	"ggml-base.en.bin",
	"ggml-tiny.en.bin",
}

func DiscoverWhisperModel() (model, vad string) {
	for _, dir := range ModelSearchPaths() {
		for _, name := range modelPreference {
			p := filepath.Join(dir, name)
			if st, err := os.Stat(p); err == nil && !st.IsDir() && model == "" {
				model = p
			}
		}
		if vad == "" {
			matches, _ := filepath.Glob(filepath.Join(dir, "ggml-silero*.bin"))
			if len(matches) > 0 {
				vad = matches[0]
			}
		}
	}
	return model, vad
}

func whisperArgs(model, wavPath, outBase, vadModel string) []string {
	args := []string{
		"-m", model,
		"-f", wavPath,
		"-oj",
		"-of", outBase,
		"-ml", "1",
		"-sow",
		"-np",
	}
	if vadModel != "" {
		args = append(args, "--vad", "--vad-model", vadModel)
	}
	return args
}

func TranscribeLocal(clock *Clock, wavPath, bin, model, vadModel string) Transcript {
	if bin == "" {
		bin = "whisper-cli"
	}
	if model == "" {
		return Transcript{Reason: "no local whisper model configured or discovered " +
			"(--whisper-model, or drop a ggml-*.bin in ~/.local/share/recgo/models)"}
	}
	if !clock.AudioAnchored() {
		return Transcript{Reason: "no audio was captured, so nothing can be aligned"}
	}

	outBase := filepath.Join(filepath.Dir(wavPath), "transcript")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	cmd := exec.CommandContext(ctx, bin, whisperArgs(model, wavPath, outBase, vadModel)...)
	var stderr strings.Builder
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		msg := fmt.Sprintf("whisper.cpp failed: %v", err)
		if s := strings.TrimSpace(stderr.String()); s != "" {
			msg += ": " + tail(s, 400)
		}
		return Transcript{Reason: msg}
	}

	raw, err := os.ReadFile(outBase + ".json")
	if err != nil {
		return Transcript{Reason: fmt.Sprintf("whisper produced no JSON: %v", err)}
	}

	var payload struct {
		Result struct {
			Language string `json:"language"`
		} `json:"result"`
		Transcription []struct {
			Offsets struct {
				From float64 `json:"from"`
				To   float64 `json:"to"`
			} `json:"offsets"`
			Text string `json:"text"`
		} `json:"transcription"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return Transcript{Reason: fmt.Sprintf("whisper JSON did not parse: %v", err)}
	}

	out := Transcript{
		OK: true, Backend: "whisper.cpp", Model: model,
		Granularity: "word", Language: payload.Result.Language,
	}
	for _, s := range payload.Transcription {
		text := strings.TrimSpace(s.Text)
		if text == "" {
			continue
		}
		t, _ := clock.FromAudioTime(s.Offsets.From / 1000)
		e, _ := clock.FromAudioTime(s.Offsets.To / 1000)
		out.Segments = append(out.Segments, Segment{
			AudioStartMs: s.Offsets.From, AudioEndMs: s.Offsets.To,
			T: t, EndT: e, Text: text,
		})
	}

	if vadModel != "" {
		out.VADCorrected = true
		out.AccuracyNote = "Local whisper.cpp with silero VAD; word onsets measured " +
			"within ~10ms of ground truth on the test fixture. Audio never left this machine."
	} else {
		out.AccuracyNote = "NO VAD MODEL — whisper anchors the first segment at 0 regardless " +
			"of leading silence, which can put every narration timestamp seconds early. " +
			"Pass --whisper-vad-model."
	}
	return out
}
