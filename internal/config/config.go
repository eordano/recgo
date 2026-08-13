package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"time"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Recording     RecordingConfig     `toml:"recording"`
	Audio         AudioConfig         `toml:"audio"`
	UI            UIConfig            `toml:"ui"`
	Transcription TranscriptionConfig `toml:"transcription"`
	Upload        UploadConfig        `toml:"upload"`
	Watchdog      WatchdogConfig      `toml:"watchdog"`

	RecordOutputDevice string `toml:"record_output_device"`
}

type WatchdogConfig struct {
	Enabled       bool    `toml:"enabled"`
	SilenceDB     float64 `toml:"silence_db"`
	SilenceWindow string  `toml:"silence_window"`
	Cooldown      string  `toml:"cooldown"`
	MaxAttempts   int     `toml:"max_attempts"`
}

type UploadConfig struct {
	Enabled         bool   `toml:"enabled"`
	URL             string `toml:"url"`
	Target          string `toml:"target"`
	SSHKey          string `toml:"ssh_key"`
	OrganizeByMonth bool   `toml:"organize_by_month"`
}

type TranscriptionConfig struct {
	Remote RemoteTranscriptionConfig `toml:"remote"`
}

type RemoteTranscriptionConfig struct {
	Endpoint string `toml:"endpoint"`
	APIKey   string `toml:"api_key"`
	Model    string `toml:"model"`
}

type RecordingConfig struct {
	OutputDir   string `toml:"output_dir"`
	Format      string `toml:"format"`
	AudioCodec  string `toml:"audio_codec"`
	Bitrate     string `toml:"bitrate"`
	MaxDuration string `toml:"max_duration"`
}

type AudioConfig struct {
	PreferPipeWire bool   `toml:"prefer_pipewire"`
	DefaultMic     string `toml:"default_mic"`
	DefaultOutput  string `toml:"default_output"`
}

type UIConfig struct {
	ShowVUMeters bool   `toml:"show_vu_meters"`
	RefreshRate  int    `toml:"refresh_rate"`
	Theme        string `toml:"theme"`
}

func DefaultConfig() *Config {
	homeDir, _ := os.UserHomeDir()
	return &Config{
		Recording: RecordingConfig{
			OutputDir:   filepath.Join(homeDir, "archive", "recordings"),
			Format:      "mkv",
			AudioCodec:  "aac",
			Bitrate:     "128k",
			MaxDuration: "60m",
		},
		Audio: AudioConfig{
			PreferPipeWire: true,
			DefaultMic:     "",
			DefaultOutput:  "",
		},
		UI: UIConfig{
			ShowVUMeters: true,
			RefreshRate:  10,
			Theme:        "dark",
		},
		Transcription: TranscriptionConfig{
			Remote: RemoteTranscriptionConfig{
				Endpoint: "",
				APIKey:   "",
				Model:    "whisper",
			},
		},
		Upload: UploadConfig{
			Enabled:         false,
			Target:          "",
			SSHKey:          "",
			OrganizeByMonth: true,
		},
		Watchdog: WatchdogConfig{
			Enabled:       true,
			SilenceDB:     -90,
			SilenceWindow: "30s",
			Cooldown:      "60s",
			MaxAttempts:   3,
		},
		RecordOutputDevice: func() string {
			if runtime.GOOS == "darwin" {
				return "Multi-Output Device"
			}
			return ""
		}(),
	}
}

func ConfigPath() string {
	configDir := os.Getenv("XDG_CONFIG_HOME")
	if configDir == "" {
		homeDir, _ := os.UserHomeDir()
		configDir = filepath.Join(homeDir, ".config")
	}
	return filepath.Join(configDir, "recgo", "config.toml")
}

func Load() (*Config, error) {
	return LoadFromPath(ConfigPath())
}

func LoadFromPath(configPath string) (*Config, error) {
	cfg := DefaultConfig()

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		return cfg, nil
	}

	if _, err := toml.DecodeFile(configPath, cfg); err != nil {
		return nil, err
	}

	return cfg, nil
}

func (c *Config) Save() error {
	configPath := ConfigPath()
	dir := filepath.Dir(configPath)

	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	f, err := os.Create(configPath)
	if err != nil {
		return err
	}
	defer f.Close()

	encoder := toml.NewEncoder(f)
	return encoder.Encode(c)
}

func (c *Config) MaxDurationParsed() time.Duration {
	d, err := time.ParseDuration(c.Recording.MaxDuration)
	if err != nil {
		return 60 * time.Minute
	}
	return d
}

func (c *Config) WatchdogSilenceWindow() time.Duration {
	if d, err := time.ParseDuration(c.Watchdog.SilenceWindow); err == nil && d > 0 {
		return d
	}
	return 30 * time.Second
}

func (c *Config) WatchdogCooldown() time.Duration {
	if d, err := time.ParseDuration(c.Watchdog.Cooldown); err == nil && d > 0 {
		return d
	}
	return 60 * time.Second
}

func (c *Config) OutputPath(name string, startTime time.Time) string {
	timestamp := startTime.Format("2006.01.02-15.04")
	filename := timestamp + "-" + name + "." + c.Recording.Format
	path := filepath.Join(c.Recording.OutputDir, filename)
	return avoidOverwrite(path)
}

func avoidOverwrite(path string) string {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return path
	}
	ext := filepath.Ext(path)
	base := path[:len(path)-len(ext)]
	for i := 2; i < 1000; i++ {
		candidate := fmt.Sprintf("%s_%d%s", base, i, ext)
		if _, err := os.Stat(candidate); os.IsNotExist(err) {
			return candidate
		}
	}
	return path
}
