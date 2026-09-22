//go:build !linux && !windows

package screencast

// WindowAt is the window under a point. KWin (tap_linux.go) and Windows
// (winat_windows.go) answer it; other platforms cannot yet.
func WindowAt(x, y float64) (WindowRect, bool) { return WindowRect{}, false }
