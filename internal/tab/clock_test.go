package tab

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"
)

type fakeBrowser struct {
	clock     *Clock
	offset    float64
	latencies []time.Duration
	n         int
}

func (f *fakeBrowser) Send(method string, params any, result any) error {
	lat := f.latencies[f.n%len(f.latencies)]
	f.n++

	mid := f.clock.Now() + float64(lat.Milliseconds())/2
	time.Sleep(lat)

	blob, _ := json.Marshal(map[string]any{
		"result": map[string]any{
			"value": map[string]any{"w": 0, "p": mid + f.offset, "o": 0},
		},
	})
	return json.Unmarshal(blob, result)
}

func TestNowIsMonotonicAndStartsNearZero(t *testing.T) {
	c := NewClock()
	a := c.Now()
	b := c.Now()
	if a < 0 || a > 50 {
		t.Fatalf("first reading %v should be near zero", a)
	}
	if b < a {
		t.Fatalf("clock went backwards: %v then %v", a, b)
	}
}

func TestCalibrationPicksLowestRTTSample(t *testing.T) {
	c := NewClock()
	fb := &fakeBrowser{
		clock:     c,
		offset:    1_000_000,
		latencies: []time.Duration{40 * time.Millisecond, 25 * time.Millisecond, 4 * time.Millisecond, 30 * time.Millisecond, 60 * time.Millisecond},
	}

	offset, errMs, err := c.CalibrateToBrowser(fb, 5)
	if err != nil {
		t.Fatal(err)
	}
	if math.Abs(offset-1_000_000) > 30 {
		t.Errorf("offset %v should be ~1e6", offset)
	}
	if errMs > 20 {
		t.Errorf("error bound %v should come from the fast probe", errMs)
	}
}

func TestBrowserWallConversion(t *testing.T) {
	c := NewClock()
	c.BrowserWallOffset = 500
	c.browserCalibrated = true

	if got, ok := c.FromBrowserWall(1500); !ok || got != 1000 {
		t.Errorf("FromBrowserWall(1500) = %v, %v; want 1000, true", got, ok)
	}
	if got, ok := c.FromPageTime(1000, 500); !ok || got != 1000 {
		t.Errorf("FromPageTime(1000,500) = %v, %v; want 1000, true", got, ok)
	}
}

func TestMonotonicEpochLearnedAndApplied(t *testing.T) {
	c := NewClock()
	c.browserCalibrated = true

	c.LearnMonotonicEpoch(12.5, 1_000_000_003)
	if want := 1_000_000_003_000 - 12_500.0; c.MonoToWallMs != want {
		t.Fatalf("MonoToWallMs = %v, want %v", c.MonoToWallMs, want)
	}

	got, ok := c.FromCDPMonotonic(14.5)
	if !ok || got != 1_000_000_005_000 {
		t.Errorf("FromCDPMonotonic(14.5) = %v, %v", got, ok)
	}
}

func TestMonotonicEpochLearnedOnlyOnce(t *testing.T) {
	c := NewClock()
	c.LearnMonotonicEpoch(1, 100)
	first := c.MonoToWallMs
	c.LearnMonotonicEpoch(999, 999)
	if c.MonoToWallMs != first {
		t.Error("later requests must not move the epoch")
	}
}

func TestUnlearnedClocksReportNotOK(t *testing.T) {
	c := NewClock()
	if _, ok := c.FromBrowserWall(123); ok {
		t.Error("FromBrowserWall should not be ok before calibration")
	}
	if _, ok := c.FromCDPMonotonic(1); ok {
		t.Error("FromCDPMonotonic should not be ok before calibration")
	}
	if _, ok := c.FromAudioTime(1); ok {
		t.Error("FromAudioTime should not be ok without an anchor")
	}

	c.browserCalibrated = true
	if _, ok := c.FromCDPMonotonic(1); ok {
		t.Error("still not ok without the monotonic epoch")
	}
}

func TestAudioTimeMapsViaFirstSampleAnchor(t *testing.T) {
	c := NewClock()
	c.SetAudioStart(2400, "test")

	if got, _ := c.FromAudioTime(3.5); got != 5900 {
		t.Errorf("FromAudioTime(3.5) = %v, want 5900", got)
	}
	if got, _ := c.FromAudioTime(0); got != 2400 {
		t.Errorf("FromAudioTime(0) = %v, want 2400", got)
	}
}

func TestAccuracyReportDegradesHonestly(t *testing.T) {
	c := NewClock()
	c.browserCalibrated = true
	c.BrowserWallErrorMs = 2

	if got := c.Report().Accuracy["clickToNetwork"]; !strings.Contains(got, "UNKNOWN") {
		t.Errorf("without the epoch, got %q; want UNKNOWN", got)
	}

	c.LearnMonotonicEpoch(1, 2)
	if got := c.Report().Accuracy["clickToNetwork"]; !strings.Contains(got, "±2.0ms") {
		t.Errorf("with the epoch, got %q; want ±2.0ms", got)
	}
}

func TestReportOmitsUnmeasuredFields(t *testing.T) {
	c := NewClock()
	r := c.Report()
	if r.BrowserWallOffsetMs != nil || r.CDPMonotonicToWallMs != nil || r.AudioStartMs != nil {
		t.Error("unmeasured fields must serialise as null, not 0")
	}
}
