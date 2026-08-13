package ui

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/eordano/recgo/internal/config"
)

func isValidBitrate(v string) bool {
	v = strings.TrimSpace(v)
	if len(v) == 0 {
		return false
	}
	suffix := v[len(v)-1]
	if suffix == 'k' || suffix == 'K' || suffix == 'M' || suffix == 'm' {
		_, err := strconv.Atoi(v[:len(v)-1])
		return err == nil
	}
	_, err := strconv.Atoi(v)
	return err == nil
}

type SettingsMode int

const (
	SettingsModeNav SettingsMode = iota
	SettingsModeEdit
)

type SettingsField int

const (
	FieldOutputDir SettingsField = iota
	FieldFormat
	FieldCodec
	FieldBitrate
	FieldShowVUMeters
	FieldRefreshRate
	FieldCount
)

type SettingsPanel struct {
	config       *config.Config
	mode         SettingsMode
	selectedIdx  int
	textInput    textinput.Model
	originalText string
	modified     bool
}

func NewSettingsPanel(cfg *config.Config) SettingsPanel {
	ti := textinput.New()
	ti.Prompt = ""
	ti.CharLimit = 256
	ti.Width = 50

	return SettingsPanel{
		config:      cfg,
		mode:        SettingsModeNav,
		selectedIdx: 0,
		textInput:   ti,
		modified:    false,
	}
}

func (s *SettingsPanel) Update(msg tea.Msg) (SettingsPanel, tea.Cmd) {
	switch s.mode {
	case SettingsModeEdit:
		return s.handleEditMode(msg)
	case SettingsModeNav:
		return s.handleNavMode(msg)
	}
	return *s, nil
}

func (s *SettingsPanel) handleNavMode(msg tea.Msg) (SettingsPanel, tea.Cmd) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		return *s, nil
	}

	switch keyMsg.String() {
	case "j", "down":
		s.selectedIdx = (s.selectedIdx + 1) % int(FieldCount)
	case "k", "up":
		s.selectedIdx = (s.selectedIdx - 1 + int(FieldCount)) % int(FieldCount)
	case "g":
		s.selectedIdx = 0
	case "G":
		s.selectedIdx = int(FieldCount) - 1
	case "enter":
		return s.startEdit()
	}

	return *s, nil
}

func (s *SettingsPanel) handleEditMode(msg tea.Msg) (SettingsPanel, tea.Cmd) {
	keyMsg, ok := msg.(tea.KeyMsg)
	if !ok {
		var cmd tea.Cmd
		s.textInput, cmd = s.textInput.Update(msg)
		return *s, cmd
	}

	switch keyMsg.String() {
	case "esc":
		s.mode = SettingsModeNav
		s.textInput.Blur()
		return *s, nil

	case "enter":
		s.saveCurrentField()
		s.mode = SettingsModeNav
		s.textInput.Blur()
		return *s, nil

	default:
		var cmd tea.Cmd
		s.textInput, cmd = s.textInput.Update(msg)
		return *s, cmd
	}
}

func (s *SettingsPanel) startEdit() (SettingsPanel, tea.Cmd) {
	s.mode = SettingsModeEdit
	s.originalText = s.getFieldValue(SettingsField(s.selectedIdx))
	s.textInput.SetValue(s.originalText)
	s.textInput.Focus()
	s.textInput.CursorEnd()
	return *s, textinput.Blink
}

func (s *SettingsPanel) getFieldValue(field SettingsField) string {
	switch field {
	case FieldOutputDir:
		return s.config.Recording.OutputDir
	case FieldFormat:
		return s.config.Recording.Format
	case FieldCodec:
		return s.config.Recording.AudioCodec
	case FieldBitrate:
		return s.config.Recording.Bitrate
	case FieldShowVUMeters:
		if s.config.UI.ShowVUMeters {
			return "true"
		}
		return "false"
	case FieldRefreshRate:
		return strconv.Itoa(s.config.UI.RefreshRate)
	}
	return ""
}

func (s *SettingsPanel) getFieldLabel(field SettingsField) string {
	switch field {
	case FieldOutputDir:
		return "Output Directory"
	case FieldFormat:
		return "Format"
	case FieldCodec:
		return "Audio Codec"
	case FieldBitrate:
		return "Bitrate"
	case FieldShowVUMeters:
		return "Show VU Meters"
	case FieldRefreshRate:
		return "Refresh Rate (FPS)"
	}
	return ""
}

func (s *SettingsPanel) getFieldCategory(field SettingsField) string {
	switch field {
	case FieldOutputDir, FieldFormat, FieldCodec, FieldBitrate:
		return "Recording"
	case FieldShowVUMeters, FieldRefreshRate:
		return "UI"
	}
	return ""
}

func (s *SettingsPanel) getFieldHelp(field SettingsField) string {
	switch field {
	case FieldOutputDir:
		return "Directory where recordings are saved (must exist)"
	case FieldFormat:
		return "File format: mkv, opus, wav"
	case FieldCodec:
		return "Audio codec: aac, opus, flac"
	case FieldBitrate:
		return "Bitrate: number + suffix k/M (e.g., 128k, 192k, 1M)"
	case FieldShowVUMeters:
		return "Show/hide VU meters: true, false"
	case FieldRefreshRate:
		return "UI refresh rate in FPS (1-60)"
	}
	return ""
}

func (s *SettingsPanel) saveCurrentField() {
	value := s.textInput.Value()
	if value == s.originalText {
		return
	}

	field := SettingsField(s.selectedIdx)
	switch field {
	case FieldOutputDir:
		expanded := value
		if strings.HasPrefix(expanded, "~/") {
			if home, err := os.UserHomeDir(); err == nil {
				expanded = home + expanded[1:]
			}
		}
		if info, err := os.Stat(expanded); err != nil || !info.IsDir() {
			return
		}
		s.config.Recording.OutputDir = value
		s.modified = true
	case FieldFormat:
		if value == "mkv" || value == "opus" || value == "wav" {
			s.config.Recording.Format = value
			s.modified = true
		}
	case FieldCodec:
		if value == "aac" || value == "opus" || value == "flac" {
			s.config.Recording.AudioCodec = value
			s.modified = true
		}
	case FieldBitrate:
		if isValidBitrate(value) {
			s.config.Recording.Bitrate = value
			s.modified = true
		}
	case FieldShowVUMeters:
		if value == "true" {
			s.config.UI.ShowVUMeters = true
			s.modified = true
		} else if value == "false" {
			s.config.UI.ShowVUMeters = false
			s.modified = true
		}
	case FieldRefreshRate:
		if rate, err := strconv.Atoi(value); err == nil && rate >= 1 && rate <= 60 {
			s.config.UI.RefreshRate = rate
			s.modified = true
		}
	}
}

func (s *SettingsPanel) View(width, height int) string {
	var content strings.Builder

	titleText := "Settings"
	if s.modified {
		titleText += " (modified)"
	}
	content.WriteString(settingsTitleStyle.Render(titleText))
	content.WriteString("\n\n")

	if s.mode == SettingsModeEdit {
		content.WriteString(settingsHelpStyle.Render("Enter to save • Esc to cancel"))
	} else {
		content.WriteString(settingsHelpStyle.Render("j/k: navigate • Enter: edit • c/Esc: close • Ctrl+S: save config"))
	}
	content.WriteString("\n\n")

	currentCategory := ""
	for i := 0; i < int(FieldCount); i++ {
		field := SettingsField(i)
		category := s.getFieldCategory(field)

		if category != currentCategory {
			if currentCategory != "" {
				content.WriteString("\n")
			}
			content.WriteString(settingsCategoryStyle.Render("─ " + category + " "))
			content.WriteString(settingsCategoryStyle.Render(strings.Repeat("─", 40)))
			content.WriteString("\n")
			currentCategory = category
		}

		isSelected := i == s.selectedIdx
		isEditing := isSelected && s.mode == SettingsModeEdit

		label := s.getFieldLabel(field)
		value := s.getFieldValue(field)

		var fieldLine string
		if isSelected {
			cursor := settingsCursorStyle.Render("▶")
			labelStyled := settingsSelectedLabelStyle.Render(label)

			if isEditing {
				valueStyled := s.textInput.View()
				fieldLine = fmt.Sprintf("%s %s: %s", cursor, labelStyled, valueStyled)
			} else {
				valueStyled := settingsSelectedValueStyle.Render(value)
				fieldLine = fmt.Sprintf("%s %s: %s", cursor, labelStyled, valueStyled)
			}
		} else {
			labelStyled := settingsLabelStyle.Render(label)
			valueStyled := settingsValueStyle.Render(value)
			fieldLine = fmt.Sprintf("  %s: %s", labelStyled, valueStyled)
		}

		content.WriteString(fieldLine)
		content.WriteString("\n")

		if isSelected && s.mode == SettingsModeNav {
			helpText := s.getFieldHelp(field)
			content.WriteString("  ")
			content.WriteString(settingsHelpTextStyle.Render(helpText))
			content.WriteString("\n")
		}
	}

	if s.modified {
		content.WriteString("\n")
		content.WriteString(settingsModifiedStyle.Render("⚠ Unsaved changes - press Ctrl+S to save to config file"))
	}

	panelContent := content.String()

	panelWidth := width - 4
	if panelWidth < 60 {
		panelWidth = 60
	}
	if panelWidth > 100 {
		panelWidth = 100
	}

	return settingsPanelStyle.Width(panelWidth).Render(panelContent)
}

func (s *SettingsPanel) IsModified() bool {
	return s.modified
}

func (s *SettingsPanel) SaveConfig() error {
	err := s.config.Save()
	if err == nil {
		s.modified = false
	}
	return err
}

var (
	settingsPanelStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(primaryColor).
				Padding(1, 2)

	settingsTitleStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(textColor).
				Background(primaryColor).
				Padding(0, 1)

	settingsCategoryStyle = lipgloss.NewStyle().
				Foreground(accentColor).
				Bold(true)

	settingsLabelStyle = lipgloss.NewStyle().
				Foreground(dimTextColor).
				Width(25)

	settingsValueStyle = lipgloss.NewStyle().
				Foreground(textColor)

	settingsSelectedLabelStyle = lipgloss.NewStyle().
					Foreground(textColor).
					Bold(true).
					Width(25)

	settingsSelectedValueStyle = lipgloss.NewStyle().
					Foreground(accentColor).
					Bold(true)

	settingsCursorStyle = lipgloss.NewStyle().
				Foreground(successColor)

	settingsHelpStyle = lipgloss.NewStyle().
				Foreground(mutedColor).
				Italic(true)

	settingsHelpTextStyle = lipgloss.NewStyle().
				Foreground(dimTextColor).
				Italic(true).
				Faint(true)

	settingsModifiedStyle = lipgloss.NewStyle().
				Foreground(warningColor).
				Bold(true)
)
