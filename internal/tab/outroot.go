package tab

import (
	"os"
	"path/filepath"

	"github.com/eordano/recgo/internal/config"
)

func ConfiguredOutRoot() string {
	cfg, err := config.Load()
	if err != nil || cfg.Recording.SessionsDir == "" {
		return ""
	}
	dir := cfg.Recording.SessionsDir
	if len(dir) > 1 && dir[:2] == "~/" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, dir[2:])
	}
	return dir
}
