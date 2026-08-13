//go:build darwin

package audio

import (
	"context"
	"time"
)

var defaultOutputPollInterval = 2 * time.Second

type DefaultSinkWatcher struct {
	cancel  context.CancelFunc
	changes chan string
	done    chan struct{}
}

func NewDefaultSinkWatcher(parent context.Context) (*DefaultSinkWatcher, error) {
	ctx, cancel := context.WithCancel(parent)

	current, err := GetDefaultOutput()
	if err != nil {
		cancel()
		return nil, err
	}

	w := &DefaultSinkWatcher{
		cancel:  cancel,
		changes: make(chan string, 4),
		done:    make(chan struct{}),
	}

	go func() {
		defer close(w.done)
		defer close(w.changes)

		seen := current
		ticker := time.NewTicker(defaultOutputPollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			next, err := GetDefaultOutput()
			if err != nil || next == "" || next == seen {
				continue
			}
			seen = next
			select {
			case w.changes <- next:
			case <-ctx.Done():
				return
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
	<-w.done
}
