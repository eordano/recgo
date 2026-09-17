package audio

import "testing"

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
