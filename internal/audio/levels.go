package audio

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"sync"
	"time"

	"github.com/eordano/recgo/internal/logging"
	"github.com/eordano/recgo/internal/proc"
)

func NewLevelMonitor(ctx context.Context, sourceName string) (*LevelMonitor, error) {
	ctx, cancel := context.WithCancel(ctx)
	cmd := levelCmd(ctx, sourceName)
	proc.Detach(cmd)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start level monitor for %s: %w", sourceName, err)
	}

	lm := newLevelMonitor(sourceName, cmd.Process.Pid, cancel, stdout, func() { cmd.Wait() })
	logging.Log("level monitor started for %s, pid=%d", sourceName, cmd.Process.Pid)
	return lm, nil
}

type LevelMonitor struct {
	source  string
	pid     int
	cancel  context.CancelFunc
	done    chan struct{}
	level   float64
	mu      sync.Mutex
	stopped bool
	stdout  io.ReadCloser

	silenceThreshDb float64
	lastSignal      time.Time

	statsMu     sync.Mutex
	sum         float64
	peak        float64
	min         float64
	count       int
	windowStart time.Time
}

type LevelStats struct {
	Source string
	PID    int
	Window time.Duration
	Avg    float64
	Peak   float64
	Min    float64
	Count  int
	Silent bool
}

const silenceFloor = 0.02

const defaultSilenceThreshDb = -90.0

func newLevelMonitor(source string, pid int, cancel context.CancelFunc, stdout io.ReadCloser, reap func()) *LevelMonitor {
	lm := &LevelMonitor{
		source:          source,
		pid:             pid,
		cancel:          cancel,
		done:            make(chan struct{}),
		stdout:          stdout,
		min:             1.0,
		windowStart:     time.Now(),
		silenceThreshDb: defaultSilenceThreshDb,
		lastSignal:      time.Now(),
	}
	go lm.run(reap)
	return lm
}

func (lm *LevelMonitor) run(reap func()) {
	defer close(lm.done)
	defer reap()

	const (
		dbFloor     = -60.0
		attackCoeff = 0.6
		decayCoeff  = 0.05
	)

	var smoothed float64
	buf := make([]byte, 1600)

	for {
		n, err := lm.stdout.Read(buf)
		if err != nil {
			lm.mu.Lock()
			lm.level = 0
			lm.mu.Unlock()
			return
		}
		if n < 2 {
			continue
		}

		var sumSquares float64
		samples := n / 2
		for i := 0; i < samples; i++ {
			sample := float64(int16(binary.LittleEndian.Uint16(buf[i*2:i*2+2]))) / 32768.0
			sumSquares += sample * sample
		}
		rms := math.Sqrt(sumSquares / float64(samples))

		dbLevel := dbFloor
		if rms > 0 {
			dbLevel = 20.0 * math.Log10(rms)
		}
		normalized := (dbLevel - dbFloor) / -dbFloor
		normalized = max(0, min(1, normalized))

		if normalized > smoothed {
			smoothed += attackCoeff * (normalized - smoothed)
		} else {
			smoothed += decayCoeff * (normalized - smoothed)
		}

		lm.mu.Lock()
		lm.level = smoothed
		if rms > 0 && dbLevel > lm.silenceThreshDb {
			lm.lastSignal = time.Now()
		}
		lm.mu.Unlock()

		lm.statsMu.Lock()
		lm.sum += smoothed
		lm.count++
		if smoothed > lm.peak {
			lm.peak = smoothed
		}
		if smoothed < lm.min {
			lm.min = smoothed
		}
		lm.statsMu.Unlock()
	}
}

func (lm *LevelMonitor) Level() float64 {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	return lm.level
}

func (lm *LevelMonitor) SetSilenceThreshold(db float64) {
	lm.mu.Lock()
	lm.silenceThreshDb = db
	lm.lastSignal = time.Now()
	lm.mu.Unlock()
}

func (lm *LevelMonitor) SilentFor() time.Duration {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	if lm.stopped {
		return 0
	}
	return time.Since(lm.lastSignal)
}

func (lm *LevelMonitor) StatsSinceLast() LevelStats {
	lm.statsMu.Lock()
	defer lm.statsMu.Unlock()

	now := time.Now()
	var avg float64
	if lm.count > 0 {
		avg = lm.sum / float64(lm.count)
	}
	mn := lm.min
	if lm.count == 0 {
		mn = 0
	}
	st := LevelStats{
		Source: lm.source,
		PID:    lm.pid,
		Window: now.Sub(lm.windowStart),
		Avg:    avg,
		Peak:   lm.peak,
		Min:    mn,
		Count:  lm.count,
		Silent: lm.peak < silenceFloor,
	}

	lm.sum = 0
	lm.peak = 0
	lm.min = 1.0
	lm.count = 0
	lm.windowStart = now

	return st
}

func (lm *LevelMonitor) Source() string { return lm.source }

func (lm *LevelMonitor) PID() int { return lm.pid }

func (lm *LevelMonitor) Stop() {
	lm.mu.Lock()
	if lm.stopped {
		lm.mu.Unlock()
		return
	}
	lm.stopped = true
	lm.level = 0
	lm.mu.Unlock()

	lm.stdout.Close()
	lm.cancel()

	select {
	case <-lm.done:
	case <-time.After(time.Second):
		logging.Log("level monitor stop timed out")
	}
}
