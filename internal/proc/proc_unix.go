//go:build !windows

package proc

import (
	"os"
	"os/exec"
	"syscall"
)

// Detach puts cmd in its own process group so the terminal's SIGINT reaches
// the recorder alone, which then stops the child itself.
func Detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// Interrupt asks the child to finish what it is writing and exit.
func Interrupt(p *os.Process) error { return p.Signal(syscall.SIGINT) }

// Terminate asks the child to exit.
func Terminate(p *os.Process) error { return p.Signal(syscall.SIGTERM) }

// KillGroup kills the child and everything it spawned.
func KillGroup(p *os.Process) error { return syscall.Kill(-p.Pid, syscall.SIGKILL) }
