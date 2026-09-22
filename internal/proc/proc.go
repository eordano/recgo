// Package proc holds the per-platform process plumbing the recorders share:
// putting a child (ffmpeg, gst-launch, chromium) out of the terminal's
// Ctrl-C reach and stopping it gracefully. Unix uses process groups and
// signals; Windows has neither, so the same verbs map to console control
// events and Kill.
package proc
