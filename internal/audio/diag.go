package audio

import (
	"fmt"
	"strings"
	"time"

	"github.com/eordano/recgo/internal/logging"
)

type Snapshot struct {
	SelectedMic     string
	SelectedMonitor string
	DefaultSink     string
	DefaultSource   string
	MicStats        *LevelStats
	MonStats        *LevelStats
	Recording       bool
	RecorderRunning bool
	OutputPath      string
	Duration        time.Duration
	SizeBytes       int64
	Bitrate         float64
	Speed           float64
}

func LogSnapshot(s Snapshot) {
	logging.Log("diag: --- snapshot ---")

	if def, err := GetDefaultSource(); err == nil {
		logging.Log("diag: default-source=%s", def)
	} else {
		logging.Log("diag: default-source query failed: %v", err)
	}
	if def, err := GetDefaultSink(); err == nil {
		logging.Log("diag: default-sink=%s", def)
	} else {
		logging.Log("diag: default-sink query failed: %v", err)
	}

	logging.Log("diag: selected mic=%q monitor=%q", s.SelectedMic, s.SelectedMonitor)

	if mics, monitors, err := ListAllDevices(); err == nil {
		logging.Log("diag: %d mics %d monitors visible", len(mics), len(monitors))
		for _, d := range mics {
			star := " "
			if d.IsDefault {
				star = "*"
			}
			logging.Log("diag:   mic[%d]%s state=%s name=%s desc=%q", d.Index, star, d.State, d.Name, d.Description)
		}
		for _, d := range monitors {
			star := " "
			if d.IsDefault {
				star = "*"
			}
			logging.Log("diag:   mon[%d]%s state=%s name=%s desc=%q", d.Index, star, d.State, d.Name, d.Description)
		}
	} else {
		logging.Log("diag: device list query failed: %v", err)
	}

	logLevelStats("mic", s.MicStats)
	logLevelStats("sys", s.MonStats)

	if s.Recording {
		logging.Log("diag: recording=true running=%v dur=%s size=%dB bitrate=%.1fkbps speed=%.3fx path=%s",
			s.RecorderRunning,
			s.Duration.Truncate(time.Second),
			s.SizeBytes,
			s.Bitrate,
			s.Speed,
			s.OutputPath,
		)
	} else {
		logging.Log("diag: recording=false")
	}
}

func logLevelStats(label string, st *LevelStats) {
	if st == nil {
		logging.Log("diag: %s level monitor: not running", label)
		return
	}
	flag := ""
	if st.Silent && st.Count > 0 {
		flag = " SILENT"
	}
	logging.Log("diag: %s level src=%s pid=%d window=%s n=%d avg=%.3f peak=%.3f min=%.3f%s",
		label,
		shortenSource(st.Source),
		st.PID,
		st.Window.Truncate(time.Second),
		st.Count,
		st.Avg,
		st.Peak,
		st.Min,
		flag,
	)
}

func shortenSource(s string) string {
	const maxLen = 60
	if len(s) <= maxLen {
		return s
	}
	return "..." + s[len(s)-maxLen+3:]
}

func FormatStatsLine(r RecordingStats) string {
	return fmt.Sprintf("dur=%s size=%dB bitrate=%.1fkbps speed=%.3fx",
		r.Duration.Truncate(time.Second),
		r.Size,
		r.Bitrate,
		r.Speed,
	)
}

func JoinDeviceNames(devs []Device) string {
	names := make([]string, 0, len(devs))
	for _, d := range devs {
		names = append(names, d.Name)
	}
	return strings.Join(names, ",")
}
