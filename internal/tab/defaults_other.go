//go:build !darwin && !windows

package tab

import (
	"os"
	"path/filepath"
)

func DocumentsDir() string {
	if d := os.Getenv("XDG_DOCUMENTS_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Documents")
}

func DefaultChromium() string {
	return "chromium"
}

func DefaultOutRoot() string {
	if dir := ConfiguredOutRoot(); dir != "" {
		return dir
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "walk-and-talk")
}
