//go:build linux

package audio

import (
	"bufio"
	"context"
	"os/exec"
	"strings"
)

type DefaultSinkWatcher struct {
	ctx     context.Context
	cancel  context.CancelFunc
	cmd     *exec.Cmd
	changes chan string
}

func NewDefaultSinkWatcher(parent context.Context) (*DefaultSinkWatcher, error) {
	ctx, cancel := context.WithCancel(parent)
	w := &DefaultSinkWatcher{
		ctx:     ctx,
		cancel:  cancel,
		changes: make(chan string, 4),
	}

	current, err := GetDefaultSink()
	if err != nil {
		cancel()
		return nil, err
	}

	cmd := exec.CommandContext(ctx, "pactl", "subscribe")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, err
	}
	w.cmd = cmd

	go func() {
		defer close(w.changes)
		defer cmd.Wait()

		seen := current
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			if !strings.Contains(line, "on server") && !strings.Contains(line, "on sink") {
				continue
			}
			if next, err := GetDefaultSink(); err == nil && next != "" && next != seen {
				seen = next
				select {
				case w.changes <- next:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return w, nil
}

func (w *DefaultSinkWatcher) Changes() <-chan string {
	return w.changes
}

func (w *DefaultSinkWatcher) Stop() {
	w.cancel()
	if w.cmd != nil && w.cmd.Process != nil {
		w.cmd.Process.Kill()
	}
}
