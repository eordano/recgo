package tab

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSessionsAndRecordingsAreSeparateDestinations(t *testing.T) {
	cfgHome := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfgHome)
	if got := ConfiguredOutRoot(); got != "" {
		t.Errorf("no config.toml: sessions root = %q, want the walk-and-talk default", got)
	}

	path := filepath.Join(cfgHome, "recgo", "config.toml")
	write := func(body string) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write("[recording]\noutput_dir = \"/r/archive/recordings\"\n")
	if got := ConfiguredOutRoot(); got != "" {
		t.Errorf("output_dir alone moved the sessions root to %q; it is the audio recordings folder", got)
	}

	write("[recording]\noutput_dir = \"/r/archive/recordings\"\nsessions_dir = \"/r/walk-and-talk\"\n")
	if got := ConfiguredOutRoot(); got != "/r/walk-and-talk" {
		t.Errorf("sessions root = %q, want /r/walk-and-talk", got)
	}
}
