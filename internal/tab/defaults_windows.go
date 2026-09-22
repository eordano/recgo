//go:build windows

package tab

import (
	"os"
	"os/exec"
	"path/filepath"
)

func DocumentsDir() string {
	if d := os.Getenv("XDG_DOCUMENTS_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Documents")
}

// DefaultChromium resolves the browser used when --chromium is left at its
// default: PATH first, then the usual install locations.
func DefaultChromium() string {
	for _, name := range []string{"chromium", "chrome", "msedge", "brave"} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	for _, p := range chromiumCandidates(os.Getenv("ProgramFiles"),
		os.Getenv("ProgramFiles(x86)"), os.Getenv("LOCALAPPDATA")) {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	return "chromium"
}

// DefaultOutRoot is %USERPROFILE%\walk-and-talk unless config.toml says
// otherwise; Documents may be OneDrive-redirected, which would upload
// unredacted sessions.
func DefaultOutRoot() string {
	if dir := ConfiguredOutRoot(); dir != "" {
		return dir
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "walk-and-talk")
}
