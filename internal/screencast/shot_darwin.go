//go:build darwin

package screencast

import (
	"context"
	"fmt"
	"image"
	"image/png"
	"os"
	"os/exec"
	"strings"
	"time"
)

func Available() (bool, string) {
	if _, err := exec.LookPath("screencapture"); err != nil {
		return false, "screencapture(1) not found; it ships with macOS"
	}
	return true, ""
}

func shotArgs(window bool, includeCursor bool) ([]string, error) {
	return shotArgsDisplay(window, includeCursor, 0)
}

// display is the 1-based screencapture -D number; 0 captures every display.
func shotArgsDisplay(window bool, includeCursor bool, display int) ([]string, error) {
	if window {
		return nil, fmt.Errorf("window capture unsupported on darwin")
	}
	args := []string{"-x", "-t", "png"}
	if includeCursor {
		args = append(args, "-C")
	}
	if display > 0 {
		args = append(args, "-D", fmt.Sprint(display))
	}
	return args, nil
}

func Shot(window bool, includeCursor bool, timeout time.Duration) (image.Image, error) {
	return ShotDisplay(0, includeCursor, timeout)
}

func ShotDisplay(display int, includeCursor bool, timeout time.Duration) (image.Image, error) {
	if timeout == 0 {
		timeout = 10 * time.Second
	}

	args, err := shotArgsDisplay(false, includeCursor, display)
	if err != nil {
		return nil, err
	}

	tmp, err := os.CreateTemp("", "recgo-shot-*.png")
	if err != nil {
		return nil, err
	}
	path := tmp.Name()
	tmp.Close()
	defer os.Remove(path)

	args = append(args, path)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	out, err := exec.CommandContext(ctx, "screencapture", args...).CombinedOutput()
	if ctx.Err() == context.DeadlineExceeded {
		return nil, fmt.Errorf("screencapture timed out after %s", timeout)
	}
	if err != nil {
		return nil, fmt.Errorf("screencapture: %w (%s)", err, strings.TrimSpace(string(out)))
	}

	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("screencapture produced no file — is Screen Recording "+
			"allowed for this binary in System Settings > Privacy & Security? (%w)", err)
	}
	defer f.Close()

	img, err := png.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("decode screenshot: %w", err)
	}
	return img, nil
}

func CheckPermission() error {
	img, err := Shot(false, false, 10*time.Second)
	if err != nil {
		return err
	}
	b := img.Bounds()
	if b.Dx() == 0 || b.Dy() == 0 {
		return fmt.Errorf("screencapture returned an empty image; Screen Recording is likely denied")
	}
	return nil
}
