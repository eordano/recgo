package tab

import (
	"fmt"
	"sync"
	"time"
)

type Clock struct {
	origin     time.Time
	WallOrigin time.Time

	mu sync.Mutex

	BrowserWallOffset  float64
	BrowserWallErrorMs float64
	browserCalibrated  bool

	MonoToWallMs float64
	monoLearned  bool

	AudioStartMs   float64
	audioAnchored  bool
	AudioStartNote string
}

func NewClock() *Clock {
	return &Clock{origin: time.Now(), WallOrigin: time.Now()}
}

func (c *Clock) Now() float64 {
	return float64(time.Since(c.origin).Nanoseconds()) / 1e6
}

type calibrationSample struct {
	W float64 `json:"w"`
	P float64 `json:"p"`
	O float64 `json:"o"`
}

type Evaluator interface {
	Send(method string, params any, result any) error
}

func (c *Clock) CalibrateToBrowser(cdp Evaluator, samples int) (offsetMs, errorMs float64, err error) {
	if samples <= 0 {
		samples = 12
	}

	bestRTT := -1.0
	bestOffset := 0.0

	for i := 0; i < samples; i++ {
		tSend := c.Now()
		var res struct {
			Result struct {
				Value calibrationSample `json:"value"`
			} `json:"result"`
		}
		err = cdp.Send("Runtime.evaluate", map[string]any{
			"expression":    "({w: Date.now(), p: performance.now(), o: performance.timeOrigin})",
			"returnByValue": true,
		}, &res)
		if err != nil {
			return 0, 0, err
		}
		tRecv := c.Now()

		rtt := tRecv - tSend
		browserWall := res.Result.Value.O + res.Result.Value.P
		mid := (tSend + tRecv) / 2

		if bestRTT < 0 || rtt < bestRTT {
			bestRTT = rtt
			bestOffset = browserWall - mid
		}
	}

	c.mu.Lock()
	c.BrowserWallOffset = bestOffset
	c.BrowserWallErrorMs = bestRTT / 2
	c.browserCalibrated = true
	c.mu.Unlock()

	return bestOffset, bestRTT / 2, nil
}

func (c *Clock) Calibrated() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.browserCalibrated
}

func (c *Clock) Offsets() (offsetMs, errorMs float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.BrowserWallOffset, c.BrowserWallErrorMs
}

func (c *Clock) LearnMonotonicEpoch(timestamp, wallTime float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.monoLearned || timestamp == 0 || wallTime == 0 {
		return
	}
	c.MonoToWallMs = wallTime*1000 - timestamp*1000
	c.monoLearned = true
}

func (c *Clock) FromBrowserWall(wallMs float64) (float64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.browserCalibrated {
		return 0, false
	}
	return wallMs - c.BrowserWallOffset, true
}

func (c *Clock) FromPageTime(timeOrigin, perfNow float64) (float64, bool) {
	return c.FromBrowserWall(timeOrigin + perfNow)
}

func (c *Clock) FromCDPMonotonic(seconds float64) (float64, bool) {
	c.mu.Lock()
	learned := c.monoLearned
	mono := c.MonoToWallMs
	c.mu.Unlock()
	if !learned {
		return 0, false
	}
	return c.FromBrowserWall(seconds*1000 + mono)
}

func (c *Clock) FromCDPEpochSeconds(seconds float64) (float64, bool) {
	return c.FromBrowserWall(seconds * 1000)
}

func (c *Clock) FromAudioTime(seconds float64) (float64, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.audioAnchored {
		return 0, false
	}
	return c.AudioStartMs + seconds*1000, true
}

func (c *Clock) SetAudioStart(ms float64, note string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.AudioStartMs = ms
	c.AudioStartNote = note
	c.audioAnchored = true
}

func (c *Clock) AudioAnchored() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.audioAnchored
}

type ClockReport struct {
	WallOriginISO        string            `json:"wallOriginIso"`
	BrowserWallOffsetMs  *float64          `json:"browserWallOffsetMs"`
	BrowserWallErrorMs   *float64          `json:"browserWallErrorMs"`
	CDPMonotonicToWallMs *float64          `json:"cdpMonotonicToWallMs"`
	AudioStartMs         *float64          `json:"audioStartMs"`
	AudioStartNote       string            `json:"audioStartNote,omitempty"`
	Accuracy             map[string]string `json:"accuracy"`
}

func (c *Clock) Report() ClockReport {
	c.mu.Lock()
	defer c.mu.Unlock()

	r := ClockReport{
		WallOriginISO: c.WallOrigin.UTC().Format(time.RFC3339Nano),
		Accuracy:      map[string]string{},
	}
	if c.browserCalibrated {
		o, e := c.BrowserWallOffset, c.BrowserWallErrorMs
		r.BrowserWallOffsetMs, r.BrowserWallErrorMs = &o, &e
	}
	if c.monoLearned {
		m := c.MonoToWallMs
		r.CDPMonotonicToWallMs = &m
	}
	if c.audioAnchored {
		a := c.AudioStartMs
		r.AudioStartMs = &a
		r.AudioStartNote = c.AudioStartNote
	}

	r.Accuracy["clickToScreenshot"] = "Exact. Both derive from browser-reported timestamps on " +
		"the same clock; conversion to session time applies the same offset to both, so it cancels."
	r.Accuracy["clickToConsole"] = "Exact, same reason — Runtime and the injected listener share the page clock."
	if c.monoLearned {
		r.Accuracy["clickToNetwork"] = fmt.Sprintf("±%.1fms", c.BrowserWallErrorMs)
	} else {
		r.Accuracy["clickToNetwork"] = "UNKNOWN — no Network.requestWillBeSent was seen, so the monotonic epoch is unlearned."
	}
	if c.AudioStartNote != "" {
		r.Accuracy["clickToTranscript"] = c.AudioStartNote
	} else {
		r.Accuracy["clickToTranscript"] = "No audio anchor; narration timestamps are unavailable."
	}
	return r
}
