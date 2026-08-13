package logging

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"
)

const maxLogBytes = 5 * 1024 * 1024

var (
	logger  *log.Logger
	logFile *os.File
	verbose atomic.Bool
)

func Init(debug bool) error {
	verbose.Store(debug)

	dir, err := stateDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	logPath := filepath.Join(dir, "recgo.log")
	if info, err := os.Stat(logPath); err == nil && info.Size() > maxLogBytes {
		_ = os.Rename(logPath, logPath+".1")
	}

	logFile, err = os.OpenFile(
		logPath,
		os.O_CREATE|os.O_WRONLY|os.O_APPEND,
		0644,
	)
	if err != nil {
		return err
	}

	logger = log.New(logFile, "", 0)
	Log("=== recgo started pid=%d at %s debug=%v ===", os.Getpid(), time.Now().Format(time.RFC3339), debug)
	return nil
}

func stateDir() (string, error) {
	stateHome := os.Getenv("XDG_STATE_HOME")
	if stateHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("home dir: %w", err)
		}
		stateHome = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(stateHome, "recgo"), nil
}

func LogPath() string {
	dir, err := stateDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "recgo.log")
}

func Log(format string, args ...any) {
	if logger == nil {
		return
	}
	ts := time.Now().Format("15:04:05.000")
	logger.Output(2, fmt.Sprintf(ts+" "+format, args...))
}

func Verbose(format string, args ...any) {
	if logger == nil || !verbose.Load() {
		return
	}
	ts := time.Now().Format("15:04:05.000")
	logger.Output(2, fmt.Sprintf(ts+" [v] "+format, args...))
}

func IsVerbose() bool {
	return verbose.Load()
}

func Close() {
	if logger != nil {
		Log("=== recgo exiting pid=%d ===", os.Getpid())
	}
	if logFile != nil {
		logFile.Close()
	}
}
