package screencast

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/eordano/recgo/internal/proc"
)

type Frame struct {
	T    float64
	Path string
}

type Pump struct {
	dir    string
	cmd    *exec.Cmd
	now    func() float64
	window float64
	stderr strings.Builder

	mu     sync.Mutex
	frames []Frame
	seen   map[string]bool
	done   chan struct{}
	closed bool
}

type PumpOptions struct {
	Now          func() float64
	Dir          string
	WindowMs     float64
	FPS          int
	GstLaunch    string
	PollInterval time.Duration
}

func StartPump(sess *Session, stream Stream, o PumpOptions) (*Pump, error) {
	if o.Now == nil {
		return nil, fmt.Errorf("PumpOptions.Now is required")
	}
	if o.Dir == "" {
		return nil, fmt.Errorf("PumpOptions.Dir is required")
	}
	if o.GstLaunch == "" {
		o.GstLaunch = "gst-launch-1.0"
	}
	if o.WindowMs == 0 {
		o.WindowMs = 20_000
	}
	if o.PollInterval == 0 {
		o.PollInterval = 25 * time.Millisecond
	}
	if err := os.MkdirAll(o.Dir, 0o700); err != nil {
		return nil, err
	}

	fd, err := sess.OpenPipeWireRemote()
	if err != nil {
		return nil, err
	}
	defer fd.Close()

	const childFD = 3

	rate := ""
	if o.FPS > 0 {
		rate = fmt.Sprintf("videorate ! video/x-raw,framerate=%d/1 ! ", o.FPS)
	}
	pipeline := fmt.Sprintf(
		"pipewiresrc fd=%d path=%d do-timestamp=true ! %svideoconvert ! pngenc ! "+
			"multifilesink location=%s/f%%06d.png",
		childFD, stream.NodeID, rate, o.Dir)

	p := &Pump{
		dir: o.Dir, now: o.Now, window: o.WindowMs,
		seen: map[string]bool{}, done: make(chan struct{}),
	}

	p.cmd = exec.Command(o.GstLaunch, strings.Fields(pipeline)...)
	proc.Detach(p.cmd)
	p.cmd.Stderr = &p.stderr
	p.cmd.ExtraFiles = []*os.File{fd}

	if err := p.cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", o.GstLaunch, err)
	}

	go p.watch(o.PollInterval)
	return p, nil
}

func (p *Pump) watch(interval time.Duration) {
	defer close(p.done)
	tick := time.NewTicker(interval)
	defer tick.Stop()

	for {
		<-tick.C

		p.mu.Lock()
		if p.closed {
			p.mu.Unlock()
			return
		}
		p.mu.Unlock()

		entries, err := os.ReadDir(p.dir)
		if err != nil {
			continue
		}
		now := p.now()

		var fresh []string
		p.mu.Lock()
		for _, e := range entries {
			name := e.Name()
			if e.IsDir() || !strings.HasSuffix(name, ".png") || p.seen[name] {
				continue
			}
			p.seen[name] = true
			fresh = append(fresh, name)
		}
		p.mu.Unlock()

		sort.Strings(fresh)
		for _, name := range fresh {
			full := filepath.Join(p.dir, name)
			if st, err := os.Stat(full); err != nil || st.Size() == 0 {
				continue
			}
			p.mu.Lock()
			p.frames = append(p.frames, Frame{T: now, Path: full})
			p.mu.Unlock()
		}
		p.trim(now)
	}
}

func (p *Pump) trim(now float64) {
	cutoff := now - p.window
	p.mu.Lock()
	i := 0
	for i < len(p.frames) && p.frames[i].T < cutoff {
		os.Remove(p.frames[i].Path)
		delete(p.seen, filepath.Base(p.frames[i].Path))
		i++
	}
	p.frames = p.frames[i:]
	p.mu.Unlock()
}

func (p *Pump) FrameAt(t float64) (Frame, bool, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	var chosen Frame
	var found, later bool
	for _, f := range p.frames {
		if f.T <= t && (!found || f.T > chosen.T) {
			chosen, found = f, true
		}
		if f.T > t {
			later = true
		}
	}
	return chosen, found, later
}

func (p *Pump) Count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.frames)
}

func (p *Pump) Stop() error {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return nil
	}
	p.closed = true
	p.mu.Unlock()

	if p.cmd != nil && p.cmd.Process != nil {
		proc.Interrupt(p.cmd.Process)
		exited := make(chan struct{})
		go func() { p.cmd.Wait(); close(exited) }()
		select {
		case <-exited:
		case <-time.After(5 * time.Second):
			proc.KillGroup(p.cmd.Process)
			<-exited
		}
	}
	<-p.done

	if s := strings.TrimSpace(p.stderr.String()); p.Count() == 0 && s != "" {
		return fmt.Errorf("no frames captured: %s", tailStr(s, 400))
	}
	return nil
}

func tailStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
