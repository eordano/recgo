//go:build windows

package audio

import (
	"context"
	"os/exec"
)

func levelCmd(ctx context.Context, sourceName string) *exec.Cmd {
	return exec.CommandContext(ctx, "ffmpeg",
		"-hide_banner", "-loglevel", "quiet",
		"-f", "dshow", "-i", "audio="+sourceName,
		"-ac", "1", "-ar", "16000", "-f", "s16le", "-")
}
