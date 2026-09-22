//go:build windows

package proc

import (
	"os"
	"os/exec"
	"syscall"
)

var (
	kernel32                  = syscall.NewLazyDLL("kernel32.dll")
	procGenerateConsoleCtrlEv = kernel32.NewProc("GenerateConsoleCtrlEvent")
)

const ctrlBreakEvent = 1

// Detach gives the child its own console process group: the console's
// Ctrl-C then reaches the recorder alone, and the group id doubles as the
// address Interrupt sends the break event to.
func Detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

// Interrupt sends Ctrl-Break to the child's process group, which ffmpeg
// treats like Ctrl-C (it finishes the output and exits). Without a console
// the event cannot be delivered, so the child is killed instead; the mic
// path streams raw PCM, so nothing is lost either way.
func Interrupt(p *os.Process) error {
	r, _, _ := procGenerateConsoleCtrlEv.Call(ctrlBreakEvent, uintptr(p.Pid))
	if r != 0 {
		return nil
	}
	return p.Kill()
}

// Terminate has no graceful form on Windows; the child is killed.
func Terminate(p *os.Process) error { return p.Kill() }

// KillGroup kills the child; Windows has no signal that reaches its
// descendants, which the callers only spawn for chromium in tests.
func KillGroup(p *os.Process) error { return p.Kill() }
