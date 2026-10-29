package ui

import "github.com/charmbracelet/lipgloss"

var (
	primaryColor = lipgloss.Color("#5C7CFA")
	accentColor  = lipgloss.Color("#91A7FF")
	successColor = lipgloss.Color("#51CF66")
	warningColor = lipgloss.Color("#FFD43B")
	dangerColor  = lipgloss.Color("#FF6B6B")
	mutedColor   = lipgloss.Color("#868E96")
	textColor    = lipgloss.Color("#F8F9FA")
	dimTextColor = lipgloss.Color("#ADB5BD")
	panelBgColor = lipgloss.Color("#2B3035")

	vuGreen  = lipgloss.Color("#51CF66")
	vuYellow = lipgloss.Color("#FFD43B")
	vuRed    = lipgloss.Color("#FF6B6B")

	subtitleStyle = lipgloss.NewStyle().
			Foreground(dimTextColor)

	recordingStyle = lipgloss.NewStyle().
			Foreground(dangerColor).
			Bold(true)

	stoppedStyle = lipgloss.NewStyle().
			Foreground(mutedColor)

	selectedDeviceStyle = lipgloss.NewStyle().
				Foreground(successColor).
				Bold(true)

	deviceStyle = lipgloss.NewStyle().
			Foreground(textColor)

	dimStyle = lipgloss.NewStyle().
			Foreground(mutedColor)

	valueStyle = lipgloss.NewStyle().
			Foreground(textColor).
			Bold(true)

	helpStyle = lipgloss.NewStyle().
			Foreground(dimTextColor)

	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(textColor).
			Padding(0, 1)

	versionStyle = lipgloss.NewStyle().
			Foreground(mutedColor)

	badgeStyle = lipgloss.NewStyle().
			Foreground(textColor).
			Background(primaryColor).
			Bold(true)

	keyChipStyle = lipgloss.NewStyle().
			Foreground(textColor).
			Background(panelBgColor).
			Bold(true)

	pathStyle = lipgloss.NewStyle().
			Foreground(accentColor)

	dividerStyle = lipgloss.NewStyle().
			Foreground(panelBgColor)

	sepStyle = lipgloss.NewStyle().
			Foreground(mutedColor)

	accentStyle = lipgloss.NewStyle().
			Foreground(accentColor).
			Bold(true)

	transcriptStyle = lipgloss.NewStyle().
			Foreground(textColor)
)

var vuBlocks = []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

func getVUColor(level float64) lipgloss.Color {
	if level < 0.5 {
		return vuGreen
	} else if level < 0.8 {
		return vuYellow
	}
	return vuRed
}

func renderVUMeter(level float64, width int) string {
	if width <= 0 {
		return ""
	}

	if level < 0 {
		level = 0
	}
	if level > 1 {
		level = 1
	}

	filled := int(float64(width) * level)
	result := make([]byte, 0, width*4)

	for i := 0; i < width; i++ {
		var block rune
		var color lipgloss.Color

		if i < filled {
			posRatio := float64(i) / float64(width)
			color = getVUColor(posRatio)
			block = vuBlocks[7]
		} else if i == filled && level > 0 {
			fraction := (float64(width)*level - float64(filled))
			blockIdx := int(fraction * 8)
			if blockIdx >= len(vuBlocks) {
				blockIdx = len(vuBlocks) - 1
			}
			block = vuBlocks[blockIdx]
			color = getVUColor(float64(i) / float64(width))
		} else {
			block = '▁'
			color = mutedColor
		}

		style := lipgloss.NewStyle().Foreground(color)
		result = append(result, []byte(style.Render(string(block)))...)
	}

	return string(result)
}
