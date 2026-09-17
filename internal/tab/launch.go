package tab

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type LaunchOptions struct {
	Chromium string
	Port     int
	Headless bool
	// Profile names the throwaway user-data-dir; empty picks one under
	// TempDir keyed on the pid.
	Profile string
}

// LaunchChromium starts a browser with remote debugging on Port and waits for
// it to expose a page target. It lands on about:blank rather than the session
// URL so a caller can instrument before navigating; the returned profile
// directory is the caller's to remove.
func LaunchChromium(o LaunchOptions) (*exec.Cmd, string, error) {
	bin := o.Chromium
	if bin == "" {
		bin = DefaultChromium()
	}
	profile := o.Profile
	if profile == "" {
		profile = filepath.Join(os.TempDir(), fmt.Sprintf("recgo-profile-%d", os.Getpid()))
	}
	// A browser already answering on the port would win the attach that
	// follows: the session would record -- and navigate -- one of its tabs
	// instead of the throwaway instance launched here.
	if _, err := ListTargets(o.Port); err == nil {
		return nil, profile, fmt.Errorf("port %d already has a browser listening; --launch/--headless "+
			"need a free port (pass --port, or drop --launch to attach to that browser)", o.Port)
	}

	args := []string{
		fmt.Sprintf("--remote-debugging-port=%d", o.Port),
		"--user-data-dir=" + profile,
		"--no-first-run",
		"--no-default-browser-check",
		"--window-size=1280,800",
	}
	if o.Headless {
		args = append(args, "--headless=new", "--disable-gpu", "--no-sandbox")
	}
	args = append(args, "about:blank")

	cmd := exec.Command(bin, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, profile, fmt.Errorf("launch chromium: %w", err)
	}

	for i := 0; i < 100; i++ {
		if targets, err := ListTargets(o.Port); err == nil {
			for _, t := range targets {
				if t.Type == "page" {
					return cmd, profile, nil
				}
			}
		}
		if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
			return nil, profile, fmt.Errorf("chromium exited: %s", tailString(stderr.String(), 800))
		}
		time.Sleep(100 * time.Millisecond)
	}
	cmd.Process.Kill()
	return nil, profile, fmt.Errorf("chromium exposed no page target on port %d: %s",
		o.Port, tailString(stderr.String(), 800))
}

// StopChromium tears down what LaunchChromium started, profile included.
func StopChromium(cmd *exec.Cmd, profile string) {
	if cmd != nil && cmd.Process != nil {
		cmd.Process.Signal(syscall.SIGTERM)
		cmd.Wait()
	}
	if profile != "" {
		os.RemoveAll(profile)
	}
}

func tailString(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
