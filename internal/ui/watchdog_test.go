package ui

import (
	"testing"
	"time"
)

func TestShouldRecalibrate(t *testing.T) {
	const (
		window      = 30 * time.Second
		cooldown    = 60 * time.Second
		maxAttempts = 3
	)

	cases := []struct {
		name      string
		silent    time.Duration
		sinceLast time.Duration
		attempts  int
		want      bool
	}{
		{"flatline too short", 10 * time.Second, time.Hour, 0, false},
		{"silent, first attempt fires", 31 * time.Second, time.Hour, 0, true},
		{"silent but within cooldown", 31 * time.Second, 5 * time.Second, 1, false},
		{"silent, cooldown elapsed", 31 * time.Second, 61 * time.Second, 1, true},
		{"attempts exhausted", 31 * time.Second, time.Hour, maxAttempts, false},
		{"exactly at window and cooldown", window, cooldown, 0, true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := shouldRecalibrate(c.silent, window, c.sinceLast, cooldown, c.attempts, maxAttempts)
			if got != c.want {
				t.Errorf("shouldRecalibrate(silent=%v sinceLast=%v attempts=%d) = %v, want %v",
					c.silent, c.sinceLast, c.attempts, got, c.want)
			}
		})
	}
}

func TestShouldRecalibrateNoCap(t *testing.T) {
	if !shouldRecalibrate(time.Minute, 30*time.Second, time.Hour, time.Minute, 99, 0) {
		t.Error("with maxAttempts=0 (no cap), a long flatline past cooldown should fire")
	}
}

func TestSinkChurnActive(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)

	cases := []struct {
		name  string
		until time.Time
		want  bool
	}{
		{"zero value (never armed) is inactive", time.Time{}, false},
		{"window still open", now.Add(time.Second), true},
		{"window just closed", now, false},
		{"window long expired", now.Add(-time.Hour), false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := sinkChurnActive(now, c.until); got != c.want {
				t.Errorf("sinkChurnActive(now, until=%v) = %v, want %v", c.until, got, c.want)
			}
		})
	}
}
