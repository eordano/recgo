package audio

import (
	"fmt"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestParseProgressLineSpeedPadding(t *testing.T) {
	cases := map[string]float64{
		"size=     240KiB time=00:00:09.00 bitrate= 205.8kbits/s speed=1.01x": 9,
		"size=   42496KiB time=00:15:00.12 bitrate= 205.8kbits/s speed=   1x": 900.12,
	}
	for line, want := range cases {
		s := parseProgressLine(line)
		if s == nil {
			t.Fatalf("no match: %q", line)
		}
		if got := s.Duration.Seconds(); got < want-0.01 || got > want+0.01 {
			t.Errorf("%q: duration %v, want %v", line, got, want)
		}
	}
}

func TestBuildArgsFillsMonitorGaps(t *testing.T) {
	mon := ffmpegInputArgs("out.monitor")
	mic := ffmpegInputArgs("mic")
	enc := []string{"-c:v", "copy", "-c:a", "libopus", "-ac", "2", "-ar", "48000", "-b:a", "64k", "-y", "out.ogg"}
	cases := []struct {
		name, mic, mon string
		want           [][]string
	}{
		{"mixed", "mic", "out.monitor", [][]string{mon, mic, {
			"-filter_complex", "[0:a:0]aresample=async=1,asplit=2[sys][sysmix];[sysmix][1:a:0]amix=inputs=2:duration=longest:normalize=0[a]",
			"-map", "[a]", "-map", "[sys]", "-map", "1:a"}, enc}},
		{"monitor only", "", "out.monitor", [][]string{mon, {"-af", "aresample=async=1"}, enc}},
		{"mic only", "mic", "", [][]string{mic, enc}},
	}
	for _, c := range cases {
		got := NewRecorder("out.ogg", c.mic, c.mon, "libopus", "64k").buildArgs()
		if want := slices.Concat(c.want...); !slices.Equal(got, want) {
			t.Errorf("%s:\n got %s\nwant %s", c.name, strings.Join(got, " "), strings.Join(want, " "))
		}
	}
}

func TestMonitorGapFillKeepsWallClockTime(t *testing.T) {
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not on PATH")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command(ff, append([]string{"-hide_banner", "-loglevel", "error", "-nostdin", "-y"}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("ffmpeg %v: %v\n%s", args, err, out)
		}
	}
	gap := filepath.Join(dir, "gap.mka")
	steady := filepath.Join(dir, "steady.mka")
	mic := filepath.Join(dir, "mic.mka")
	run("-f", "lavfi", "-i", "sine=f=440:r=48000:d=2", "-af", "asetpts='if(gte(T,1),PTS+0.7/TB,PTS)'", "-c:a", "pcm_s16le", gap)
	run("-f", "lavfi", "-i", "sine=f=440:r=48000:d=2", "-c:a", "pcm_s16le", steady)
	run("-f", "lavfi", "-i", "sine=f=1000:r=48000:d=2", "-c:a", "pcm_s16le", mic)

	record := func(name, monitor, micFile string) []float64 {
		out := filepath.Join(dir, name+".mka")
		micDev, inputs := "", []string{"-i", monitor}
		if micFile != "" {
			micDev, inputs = "mic", append(inputs, "-i", micFile)
		}
		args := NewRecorder(out, micDev, "monitor", "pcm_s16le", "64k").buildArgs()
		skip := len(ffmpegInputArgs("monitor"))
		if micDev != "" {
			skip += len(ffmpegInputArgs(micDev))
		}
		run(append(inputs, args[skip:]...)...)

		var secs []float64
		for i := 0; ; i++ {
			pcm := filepath.Join(dir, fmt.Sprintf("%s.%d.pcm", name, i))
			cmd := exec.Command(ff, "-hide_banner", "-loglevel", "error", "-nostdin", "-y", "-i", out,
				"-map", fmt.Sprintf("0:a:%d", i), "-f", "s16le", "-ac", "1", "-ar", "48000", pcm)
			if cmd.Run() != nil {
				return secs
			}
			fi, err := os.Stat(pcm)
			if err != nil {
				t.Fatal(err)
			}
			secs = append(secs, float64(fi.Size())/(2*48000))
		}
	}

	for _, c := range []struct {
		name, monitor, mic string
		want               []float64
	}{
		{"gap-mixed", gap, mic, []float64{2.7, 2.7, 2.0}},
		{"steady-mixed", steady, mic, []float64{2.0, 2.0, 2.0}},
		{"gap-monitor-only", gap, "", []float64{2.7}},
	} {
		got := record(c.name, c.monitor, c.mic)
		if len(got) != len(c.want) {
			t.Fatalf("%s: %d tracks %v, want %d", c.name, len(got), got, len(c.want))
		}
		for i := range got {
			if math.Abs(got[i]-c.want[i]) > 0.01 {
				t.Errorf("%s: track %d is %.3fs, want %.1fs (tracks %v)", c.name, i, got[i], c.want[i], got)
			}
		}
	}
}
