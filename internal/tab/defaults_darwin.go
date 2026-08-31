//go:build darwin

package tab

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

func DocumentsDir() string {
	if d := os.Getenv("XDG_DOCUMENTS_DIR"); d != "" {
		return d
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Documents")
}

// DefaultChromium resolves the chromium binary used when --chromium is left
// at its default. PATH wins; otherwise the usual app bundles are probed.
func DefaultChromium() string {
	if p, err := exec.LookPath("chromium"); err == nil {
		return p
	}
	home, _ := os.UserHomeDir()
	roots := []string{"/Applications"}
	if home != "" {
		roots = append(roots, filepath.Join(home, "Applications"))
	}
	bundles := []struct{ app, bin string }{
		{"Chromium.app", "Chromium"},
		{"Google Chrome.app", "Google Chrome"},
		{"Brave Browser.app", "Brave Browser"},
		{"Microsoft Edge.app", "Microsoft Edge"},
	}
	for _, b := range bundles {
		for _, root := range roots {
			p := filepath.Join(root, b.app, "Contents", "MacOS", b.bin)
			if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
				return p
			}
		}
	}
	return "chromium"
}

// DefaultOutRoot picks the output root when --out is not given. If
// ~/Documents is iCloud-synced (Desktop & Documents sync), sessions would be
// uploaded to Apple, so fall back to ~/walk-and-talk instead.
func DefaultOutRoot() string {
	docs := DocumentsDir()
	home, _ := os.UserHomeDir()
	if home != "" && icloudSynced(home, docs) {
		fmt.Fprintf(os.Stderr,
			"WARNING: %s is synced to iCloud (Desktop & Documents sync).\n"+
				"WARNING: sessions contain unredacted narration and screenshots;\n"+
				"WARNING: writing to %s instead. Use --out to override.\n",
			docs, filepath.Join(home, "walk-and-talk"))
		return filepath.Join(home, "walk-and-talk")
	}
	return filepath.Join(docs, "walk-and-talk")
}

// icloudSynced reports whether docs resolves to somewhere under
// home/Library/Mobile Documents (the iCloud Drive backing store).
func icloudSynced(home, docs string) bool {
	resolved, err := filepath.EvalSymlinks(docs)
	if err != nil {
		return false
	}
	mobile := filepath.Join(home, "Library", "Mobile Documents")
	if m, err := filepath.EvalSymlinks(mobile); err == nil {
		mobile = m
	}
	rel, err := filepath.Rel(mobile, resolved)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}
