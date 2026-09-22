package android

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type device struct{ bin, serial string }

func (d device) command(ctx context.Context, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, d.bin, append([]string{"-s", d.serial}, args...)...)
}
func (d device) run(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cmd := d.command(ctx, args...)
	b, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("adb %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(b)))
	}
	return strings.TrimSpace(string(b)), nil
}
func parseUID(output, pkg string) (string, error) {
	re := regexp.MustCompile(`^package:` + regexp.QuoteMeta(pkg) + ` uid:([0-9]+)$`)
	for _, line := range strings.Split(output, "\n") {
		if m := re.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
			return m[1], nil
		}
	}
	return "", fmt.Errorf("package %s not installed for the current user", pkg)
}
func (d device) uid(ctx context.Context, pkg string) (string, error) {
	user, err := d.run(ctx, "shell", "am", "get-current-user")
	if err != nil {
		return "", err
	}
	if _, err = strconv.Atoi(user); err != nil {
		return "", fmt.Errorf("unexpected Android user %q", user)
	}
	output, err := d.run(ctx, "shell", "cmd", "package", "list", "packages", "-U", "--user", user, pkg)
	if err != nil {
		return "", err
	}
	return parseUID(output, pkg)
}

type child struct {
	cmd  *exec.Cmd
	done chan error
	file *os.File
}

func startChild(cmd *exec.Cmd, path string) (*child, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return nil, err
	}
	cmd.Stdout = f
	cmd.Stderr = f
	if err = cmd.Start(); err != nil {
		f.Close()
		return nil, err
	}
	c := &child{cmd: cmd, done: make(chan error, 1), file: f}
	go func() { c.done <- cmd.Wait(); close(c.done) }()
	return c, nil
}
func (c *child) stop() {
	if c == nil {
		return
	}
	_ = c.cmd.Process.Signal(os.Interrupt)
	select {
	case <-c.done:
	case <-time.After(4 * time.Second):
		_ = c.cmd.Process.Kill()
		<-c.done
	}
	c.file.Close()
}

// Screencap happens after receiving the accessibility event. Never label it a tap-time frame.
func (d device) screenshot(ctx context.Context, path string) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := d.command(ctx, "exec-out", "screencap", "-p")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	cmd.Stdout = f
	var stderr strings.Builder
	cmd.Stderr = &stderr
	err = cmd.Run()
	closeErr := f.Close()
	if err != nil {
		_ = os.Remove(path)
		return fmt.Errorf("screencap failed: %w: %s", err, stderr.String())
	}
	if closeErr != nil {
		return closeErr
	}
	f, err = os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	sig := make([]byte, 8)
	_, err = io.ReadFull(f, sig)
	if err != nil || string(sig) != "\x89PNG\r\n\x1a\n" {
		_ = os.Remove(path)
		return fmt.Errorf("device did not return a PNG")
	}
	return nil
}
