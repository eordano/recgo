package ui

import (
	"errors"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/eordano/recgo/internal/audio"
)

func cmdName(c tea.Cmd) string {
	return runtime.FuncForPC(reflect.ValueOf(c).Pointer()).Name()
}

// batchCmdNames names the commands a Batch carries without running any of
// them: only the Batch wrapper itself is invoked.
func batchCmdNames(t *testing.T, c tea.Cmd) []string {
	t.Helper()
	if c == nil {
		return nil
	}
	if !strings.Contains(cmdName(c), ".compactCmds[") {
		return []string{cmdName(c)}
	}
	var names []string
	for _, sub := range c().(tea.BatchMsg) {
		names = append(names, batchCmdNames(t, sub)...)
	}
	return names
}

func isCmd(c tea.Cmd, fn string) bool {
	return c != nil && strings.Contains(cmdName(c), fn)
}

const (
	restartCmd = ".restartRecording-fm"
	reloadCmd  = ".loadDevices-fm"
	settleCmd  = ".settleDefaultSink.func"
	watchCmd   = ".waitForSinkChange.func"
)

func recordingSinkModel(t *testing.T) Model {
	t.Helper()
	m, _ := headlessModel(t, Options{})
	m.recording = true
	m.selectedMon = 0
	return m
}

func fakeDefaultSink(t *testing.T, sink string) {
	t.Helper()
	prev := readDefaultSink
	readDefaultSink = func() (string, error) { return sink, nil }
	t.Cleanup(func() { readDefaultSink = prev })
}

func TestDefaultSinkChangeOnlySchedulesASettleCheck(t *testing.T) {
	if runtime.GOOS == "darwin" {
		t.Skip("darwin re-asserts the output device instead")
	}
	m := recordingSinkModel(t)
	m.sinkWatcher = &audio.DefaultSinkWatcher{}
	next, cmd := m.Update(defaultSinkChangedMsg{newSink: "out-y"})
	got := next.(Model)
	if got.selectedMon != 0 || !got.suppressSinkUntil.IsZero() {
		t.Fatalf("a change must not act before it settles: selectedMon=%d suppressUntil=%v", got.selectedMon, got.suppressSinkUntil)
	}
	names := batchCmdNames(t, cmd)
	var watch, settle int
	for _, n := range names {
		switch {
		case strings.Contains(n, restartCmd), strings.Contains(n, reloadCmd):
			t.Fatalf("a change must not restart or reload immediately: %v", names)
		case strings.Contains(n, watchCmd):
			watch++
		case strings.Contains(n, settleCmd):
			settle++
		}
	}
	if watch != 1 || settle != 1 {
		t.Fatalf("want the watcher re-armed and one settle check, got %v", names)
	}
}

func TestSettledOnTheSelectedSinkDoesNothing(t *testing.T) {
	m := recordingSinkModel(t)
	next, cmd := m.Update(sinkSettledMsg{sink: "out-x"})
	got := next.(Model)
	if cmd != nil || got.selectedMon != 0 || !got.suppressSinkUntil.IsZero() {
		t.Fatalf("settling back on the recorded sink must be a no-op: cmd=%v selectedMon=%d suppressUntil=%v",
			cmd != nil, got.selectedMon, got.suppressSinkUntil)
	}
}

func TestSettledOnAnotherKnownSinkFollowsIt(t *testing.T) {
	m := recordingSinkModel(t)
	next, cmd := m.Update(sinkSettledMsg{sink: "out-y"})
	got := next.(Model)
	if got.selectedMon != 1 {
		t.Fatalf("selectedMon = %d, want 1", got.selectedMon)
	}
	if !isCmd(cmd, restartCmd) {
		t.Fatalf("recording must restart onto the new monitor, got %v", batchCmdNames(t, cmd))
	}
	if !sinkChurnActive(time.Now(), got.suppressSinkUntil) {
		t.Fatal("the self-induced restart must arm the churn window")
	}

	m.recording = false
	next, cmd = m.Update(sinkSettledMsg{sink: "out-y"})
	if next.(Model).selectedMon != 1 || cmd != nil {
		t.Fatalf("idle: select without restarting, got selectedMon=%d cmd=%v", next.(Model).selectedMon, batchCmdNames(t, cmd))
	}
}

func TestSettledOnAnUnknownSinkReloadsDevices(t *testing.T) {
	m := recordingSinkModel(t)
	next, cmd := m.Update(sinkSettledMsg{sink: "bluez_output.new"})
	if !isCmd(cmd, reloadCmd) || next.(Model).selectedMon != 0 {
		t.Fatalf("unknown sink: want a device reload and no switch, got %v selectedMon=%d", batchCmdNames(t, cmd), next.(Model).selectedMon)
	}
}

func TestSettledWithoutAnAnswerDoesNothing(t *testing.T) {
	m := recordingSinkModel(t)
	for _, msg := range []sinkSettledMsg{{err: errors.New("pactl gone")}, {sink: ""}} {
		next, cmd := m.Update(msg)
		if cmd != nil || next.(Model).selectedMon != 0 {
			t.Fatalf("%+v: want no action, got %v", msg, batchCmdNames(t, cmd))
		}
	}
}

func TestSettledDuringChurnWindowRechecksAfterIt(t *testing.T) {
	m := recordingSinkModel(t)
	until := time.Now().Add(80 * time.Millisecond)
	m.suppressSinkUntil = until
	next, cmd := m.Update(sinkSettledMsg{sink: "out-y"})
	got := next.(Model)
	if got.selectedMon != 0 || !got.suppressSinkUntil.Equal(until) {
		t.Fatalf("must not act inside the churn window: selectedMon=%d", got.selectedMon)
	}
	if !isCmd(cmd, settleCmd) {
		t.Fatalf("must reschedule the check, not drop it: got %v", batchCmdNames(t, cmd))
	}

	fakeDefaultSink(t, "out-y")
	settled, ok := cmd().(sinkSettledMsg)
	if !ok {
		t.Fatal("the rescheduled check must deliver a sinkSettledMsg")
	}
	if time.Now().Before(until) {
		t.Fatal("the rescheduled check fired inside the churn window")
	}
	next, cmd = got.Update(settled)
	if next.(Model).selectedMon != 1 || !isCmd(cmd, restartCmd) {
		t.Fatalf("after the window the settled sink must be followed, got selectedMon=%d cmd=%v", next.(Model).selectedMon, batchCmdNames(t, cmd))
	}
}
