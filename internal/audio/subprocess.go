package audio

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/eordano/recgo/internal/logging"
)

const toolTimeout = 5 * time.Second

func runTool(name string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), toolTimeout)
	defer cancel()
	return runCmd(ctx, exec.CommandContext(ctx, name, args...))
}

func runCmd(ctx context.Context, cmd *exec.Cmd) (string, error) {
	t0 := time.Now()
	out, err := cmd.CombinedOutput()
	trimmed := strings.TrimSpace(string(out))
	label := strings.Join(cmd.Args, " ")

	switch {
	case ctx.Err() == context.DeadlineExceeded:
		logging.Log("%s TIMEOUT after %v", label, toolTimeout)
		return "", fmt.Errorf("%s timed out after %v", label, toolTimeout)
	case err != nil:
		logging.Log("%s failed in %dms: %v (output: %s)", label, time.Since(t0).Milliseconds(), err, trimmed)
		return "", fmt.Errorf("%s: %w (output: %s)", label, err, trimmed)
	}
	logging.Verbose("%s ok in %dms (%d bytes)", label, time.Since(t0).Milliseconds(), len(trimmed))
	return trimmed, nil
}
