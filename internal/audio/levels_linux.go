//go:build linux

package audio

import (
	"context"
	"os/exec"
)

func levelCmd(ctx context.Context, sourceName string) *exec.Cmd {
	return exec.CommandContext(ctx, "parec",
		"--raw", "--format=s16le", "--rate=16000", "--channels=1",
		"--latency-msec=50", "-d", sourceName)
}
