package ui

import (
	"fmt"
	"math"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/eordano/recgo/internal/audio"
)

var osUserHomeDir = os.UserHomeDir

const version = "0.1.0"

func (m Model) View() string {
	if m.quitting {
		if len(m.segments) > 1 {
			return dimStyle.Render("merging "+fmt.Sprint(len(m.segments))+" segments…") + "\n"
		}
		return dimStyle.Render("stopping recording…") + "\n"
	}

	if m.width == 0 {
		return "Loading..."
	}

	if m.mode == ModeSettings {
		return m.renderSettingsView()
	}

	if m.height > 0 && m.height < 10 {
		return m.viewMinimal()
	}

	return m.viewFull()
}

func (m Model) viewFull() string {
	top := strings.Join([]string{
		m.renderHeader(),
		"",
		m.renderDevices(),
		"",
		m.renderTranscript(),
	}, "\n")

	bottom := m.renderHelpBar()

	if m.err != nil {
		errStyle := lipgloss.NewStyle().Foreground(dangerColor).Bold(true)
		bottom = "  " + errStyle.Render("error: "+m.err.Error()) + "\n" + bottom
	}

	topLines := strings.Count(top, "\n") + 1
	bottomLines := strings.Count(bottom, "\n") + 1
	pad := m.height - topLines - bottomLines - 1
	if pad < 1 {
		pad = 1
	}
	return top + strings.Repeat("\n", pad) + bottom
}

func (m Model) viewMinimal() string {
	var status string
	if m.recording {
		status = recordingStyle.Render("● REC") + " " + valueStyle.Render(formatDuration(m.stats.Duration))
	} else {
		status = stoppedStyle.Render("○ IDLE")
	}
	if n := len(m.segments); n > 1 {
		status += "  " + dimStyle.Render("seg") + " " + valueStyle.Render(fmt.Sprintf("%d", n))
	}
	if m.transcribing {
		status += "  " + recordingStyle.Render("● live")
	}

	micName := "—"
	if m.selectedMic < len(m.mics) {
		micName = truncate(m.mics[m.selectedMic].Description, 24)
	}
	monName := "—"
	if m.selectedMon < len(m.monitors) {
		monName = truncate(m.monitors[m.selectedMon].Description, 24)
	}
	devices := badgeStyle.Render(" M ") + " " + deviceStyle.Render(micName) +
		"   " + badgeStyle.Render(" S ") + " " + deviceStyle.Render(monName)

	tail := ""
	maxWidth := m.width - 4
	if maxWidth < 10 {
		maxWidth = 10
	}
	if m.transcriptMic != "" {
		if lines := wrapLines(m.transcriptMic, maxWidth); len(lines) > 0 {
			tail = "M " + lines[len(lines)-1]
		}
	}
	if m.transcriptSys != "" {
		if lines := wrapLines(m.transcriptSys, maxWidth); len(lines) > 0 {
			cand := "S " + lines[len(lines)-1]
			if tail == "" {
				tail = cand
			} else {
				tail = tail + "  " + cand
			}
		}
	}
	if tail == "" && m.transcribing {
		tail = dimStyle.Render("listening…")
	}

	hint := dimStyle.Render("m·mic  s·sys  t·transcribe  q·quit")

	rows := []string{
		"  " + status,
		"  " + devices,
		"  " + tail,
		"  " + hint,
	}
	return strings.Join(rows, "\n")
}

func (m Model) renderHeader() string {
	left := headerStyle.Render("recgo") + " " + versionStyle.Render(version)

	var status string
	if m.recording {
		status = recordingStyle.Render("● REC") + " " + valueStyle.Render(formatDuration(m.stats.Duration))
	} else if m.err != nil {
		status = stoppedStyle.Render("⨯ STOPPED")
	} else {
		status = stoppedStyle.Render("○ IDLE")
	}

	if n := len(m.segments); n > 1 {
		status += "   " + dimStyle.Render("segments") + " " + valueStyle.Render(fmt.Sprintf("%d", n))
	}

	bar := padBar(left, status, m.width)
	pathLine := ""
	if m.sessionFinalPath != "" {
		path := abbreviatePath(m.sessionFinalPath)
		extra := ""
		if m.stats.Size > 0 {
			extra = "  " + sepStyle.Render("·") + "  " + dimStyle.Render(formatBytes(m.stats.Size))
		}
		pathLine = "\n  " + dimStyle.Render("→ ") + pathStyle.Render(path) + extra
	}
	return bar + pathLine
}

func (m Model) renderDevices() string {
	if m.mode == ModeSelectMic {
		return m.renderDeviceList("Select Microphone", m.mics, m.selectedMic, m.deviceCursor)
	}
	if m.mode == ModeSelectMonitor {
		return m.renderDeviceList("Select System Audio", m.monitors, m.selectedMon, m.deviceCursor)
	}

	header := sectionLabel("Inputs", [][2]string{
		{"m", "switch mic"},
		{"s", "switch system"},
	})
	var rows []string
	rows = append(rows, m.renderDeviceRow("M", "Mic", m.mics, m.selectedMic, m.micLevel, m.mode == ModeNormal && m.focus == FocusMic))
	rows = append(rows, m.renderDeviceRow("S", "Sys", m.monitors, m.selectedMon, m.monitorLevel, m.mode == ModeNormal && m.focus == FocusSys))
	return header + "\n\n" + strings.Join(rows, "\n\n")
}

func sectionLabel(name string, hints [][2]string) string {
	parts := []string{subtitleStyle.Render(name)}
	for _, h := range hints {
		parts = append(parts, keyChipStyle.Render(" "+h[0]+" ")+" "+dimStyle.Render(h[1]))
	}
	return "  " + strings.Join(parts, sepStyle.Render("   "))
}

func (m Model) renderDeviceRow(badge, label string, devices []audio.Device, selected int, level float64, focused bool) string {
	badgeStyled := badgeStyle.Render(" " + badge + " ")

	var name string
	if selected < len(devices) {
		name = deviceStyle.Render(truncate(devices[selected].Description, m.width-14))
	} else if runtime.GOOS == "darwin" && label == "Sys" && len(devices) == 0 {
		name = dimStyle.Render(truncate("system audio needs BlackHole + a Multi-Output Device", m.width-14))
	} else {
		name = dimStyle.Render(fmt.Sprintf("(no %s)", label))
	}

	meterWidth := m.width - 14
	if meterWidth < 20 {
		meterWidth = 20
	}
	meter := renderVUMeter(level, meterWidth-8)
	db := dimStyle.Render(fmt.Sprintf("%6s", formatDB(level)))

	cursor := "  "
	if focused {
		cursor = accentStyle.Render("▶ ")
	}
	first := cursor + badgeStyled + " " + name
	second := "    " + meter + "  " + db
	return first + "\n" + second
}

func (m Model) renderDeviceList(title string, devices []audio.Device, selected, cursor int) string {
	header := sectionLabel(title, [][2]string{
		{"↑/k", "up"}, {"↓/j", "down"}, {"⏎", "select"}, {"esc", "cancel"},
	})

	var b strings.Builder
	b.WriteString(header + "\n\n")
	if len(devices) == 0 {
		b.WriteString("  " + dimStyle.Render("(no devices)") + "\n")
	}
	limit := len(devices)
	if limit > 9 {
		limit = 9
	}
	for i := 0; i < limit; i++ {
		num := keyChipStyle.Render(fmt.Sprintf(" %d ", i+1))
		desc := truncate(devices[i].Description, m.width-14)
		var marker string
		switch {
		case i == selected && i == cursor:
			marker = selectedDeviceStyle.Render("▶ ● ")
		case i == cursor:
			marker = accentStyle.Render("▶   ")
		case i == selected:
			marker = selectedDeviceStyle.Render("  ● ")
		default:
			marker = "    "
		}
		line := marker + desc
		switch {
		case i == cursor:
			b.WriteString("  " + num + "  " + accentStyle.Render(line) + "\n")
		case i == selected:
			b.WriteString("  " + num + "  " + selectedDeviceStyle.Render(line) + "\n")
		default:
			b.WriteString("  " + num + "  " + deviceStyle.Render(line) + "\n")
		}
	}
	return b.String()
}

func (m Model) renderTranscript() string {
	var b strings.Builder
	b.WriteString(divider(m.width))
	b.WriteString("\n")

	header := sectionLabel("Transcript", [][2]string{{"t", "toggle"}})
	if m.mode == ModeNormal && m.focus == FocusTranscribe {
		header = "  " + accentStyle.Render("▶ ") + strings.TrimPrefix(header, "  ")
	}
	var indicator string
	if m.transcribing {
		indicator = recordingStyle.Render("● live")
	} else {
		indicator = dimStyle.Render("○ off")
	}
	headerLen := lipgloss.Width(header)
	indicatorLen := lipgloss.Width(indicator)
	gap := m.width - headerLen - indicatorLen - 2
	if gap < 1 {
		gap = 1
	}
	b.WriteString(header + strings.Repeat(" ", gap) + indicator + "\n\n")

	if !m.transcribing && m.transcriptMic == "" && m.transcriptSys == "" {
		b.WriteString("  " + dimStyle.Render("(no transcript yet)") + "\n")
		return b.String()
	}

	width := m.width - 8
	if width < 20 {
		width = 20
	}

	b.WriteString(m.renderTranscriptBlock("M", m.transcriptMic, width))
	b.WriteString("\n")
	b.WriteString(m.renderTranscriptBlock("S", m.transcriptSys, width))
	return b.String()
}

const transcriptMaxLines = 3

func (m Model) renderTranscriptBlock(badge, text string, width int) string {
	var b strings.Builder
	tag := badgeStyle.Render(" " + badge + " ")
	if text == "" {
		if m.transcribing {
			b.WriteString("  " + tag + "  " + dimStyle.Render("listening…") + "\n")
		} else {
			b.WriteString("  " + tag + "  " + dimStyle.Render("(empty)") + "\n")
		}
		return b.String()
	}
	lines := wrapLines(text, width)
	if len(lines) > transcriptMaxLines {
		lines = lines[len(lines)-transcriptMaxLines:]
	}
	for i, line := range lines {
		prefix := "      "
		if i == 0 {
			prefix = "  " + tag + "  "
		}
		b.WriteString(prefix + transcriptStyle.Render(line) + "\n")
	}
	return b.String()
}

func (m Model) renderHelpBar() string {
	if m.help.ShowAll {
		return "\n" + m.help.View(m.keyMap)
	}
	chips := []string{
		chip("c", "settings"),
		chip("q", "quit"),
		chip("?", "help"),
	}
	return divider(m.width) + "\n  " + strings.Join(chips, "   ")
}

func chip(k, label string) string {
	return keyChipStyle.Render(" "+k+" ") + " " + helpStyle.Render(label)
}

func divider(width int) string {
	if width <= 0 {
		return ""
	}
	return dividerStyle.Render(strings.Repeat("─", width))
}

func padBar(left, right string, width int) string {
	lw := lipgloss.Width(left)
	rw := lipgloss.Width(right)
	gap := width - lw - rw - 2
	if gap < 1 {
		gap = 1
	}
	return "  " + left + strings.Repeat(" ", gap) + right
}

func padTo(n int) string {
	if n < 1 {
		return " "
	}
	return strings.Repeat(" ", n)
}

func wrapLines(text string, width int) []string {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	words := strings.Fields(text)
	var lines []string
	var cur strings.Builder
	for _, w := range words {
		if cur.Len() == 0 {
			cur.WriteString(w)
			continue
		}
		if cur.Len()+1+len(w) > width {
			lines = append(lines, cur.String())
			cur.Reset()
			cur.WriteString(w)
		} else {
			cur.WriteByte(' ')
			cur.WriteString(w)
		}
	}
	if cur.Len() > 0 {
		lines = append(lines, cur.String())
	}
	return lines
}

func abbreviatePath(path string) string {
	if home := homeDir(); home != "" && strings.HasPrefix(path, home) {
		return "~" + path[len(home):]
	}
	return path
}

func homeDir() string {
	if h, err := osUserHomeDir(); err == nil {
		return h
	}
	return ""
}

func formatDB(level float64) string {
	if level <= 0 {
		return "−∞ dB"
	}
	db := 20 * math.Log10(level)
	if db < -60 {
		return "−∞ dB"
	}
	if db > 0 {
		db = 0
	}
	return fmt.Sprintf("%+.0f dB", db)
}

func formatDuration(d time.Duration) string {
	hours := int(d.Hours())
	minutes := int(d.Minutes()) % 60
	seconds := int(d.Seconds()) % 60
	return fmt.Sprintf("%02d:%02d:%02d", hours, minutes, seconds)
}

func formatBytes(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}

func truncate(s string, maxLen int) string {
	if maxLen <= 0 {
		return ""
	}
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-1] + "…"
}

func (m Model) renderSettingsView() string {
	settingsContent := m.settingsPanel.View(m.width, m.height)
	settingsHeight := strings.Count(settingsContent, "\n") + 1
	topPadding := (m.height - settingsHeight) / 2
	if topPadding < 0 {
		topPadding = 0
	}
	var output strings.Builder
	for i := 0; i < topPadding; i++ {
		output.WriteString("\n")
	}
	centered := lipgloss.Place(m.width, settingsHeight, lipgloss.Center, lipgloss.Top, settingsContent)
	output.WriteString(centered)
	return output.String()
}
