package tab

import "path/filepath"

// chromiumCandidates lists where Chromium-family browsers install on
// Windows, per-machine (Program Files) before per-user (LocalAppData),
// skipping roots that are unset.
func chromiumCandidates(programFiles, programFilesX86, localAppData string) []string {
	var out []string
	for _, root := range []string{programFiles, programFilesX86, localAppData} {
		if root == "" {
			continue
		}
		out = append(out,
			filepath.Join(root, "Chromium", "Application", "chrome.exe"),
			filepath.Join(root, "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(root, "BraveSoftware", "Brave-Browser", "Application", "brave.exe"),
			filepath.Join(root, "Microsoft", "Edge", "Application", "msedge.exe"),
		)
	}
	return out
}
