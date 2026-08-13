package ui

import (
	"testing"
	"time"
)

func TestShouldReassertOutput(t *testing.T) {
	const target = "Multi-Output Device"
	now := time.Unix(1_700_000_000, 0)

	cases := []struct {
		name          string
		recording     bool
		savedOutput   string
		target        string
		newOutput     string
		suppressUntil time.Time
		want          bool
	}{
		{"drift while recording fires", true, "Px8", target, "Px8", time.Time{}, true},
		{"not recording", false, "Px8", target, "Px8", time.Time{}, false},
		{"no saved output (switching never engaged)", true, "", target, "Px8", time.Time{}, false},
		{"no target configured", true, "Px8", "", "Px8", time.Time{}, false},
		{"already on target", true, "Px8", target, target, time.Time{}, false},
		{"within churn window user override wins", true, "Px8", target, "Px8", now.Add(time.Second), false},
		{"churn window expired", true, "Px8", target, "Px8", now.Add(-time.Second), true},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := shouldReassertOutput(c.recording, c.savedOutput, c.target, c.newOutput, now, c.suppressUntil)
			if got != c.want {
				t.Errorf("shouldReassertOutput(recording=%v saved=%q target=%q new=%q until=%v) = %v, want %v",
					c.recording, c.savedOutput, c.target, c.newOutput, c.suppressUntil, got, c.want)
			}
		})
	}
}
