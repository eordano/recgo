package tabui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/eordano/recgo/internal/tab"
)

type Mode int

const (
	ModePick Mode = iota
	ModeFollow
)

type Model struct {
	Mode Mode

	Chosen    *tab.TabInfo
	Cancelled bool

	port     int
	follower *tab.Follower
	rec      *tab.Recording

	tabs   []tab.TabInfo
	cursor int
	err    error

	width, height int
	started       time.Time
	quitting      bool
	marks         int
	narration     []string
}

// Narration is sent by the caller when a live transcription pass decodes
// text; the follow view shows the last few lines so the operator can see
// that speech is being picked up.
type Narration struct {
	T    float64
	Text string
}

const narrationKeep = 5

type tabsMsg struct {
	tabs []tab.TabInfo
	err  error
}
type tickMsg time.Time
type updateMsg struct{}

func NewPicker(port int) *Model {
	return &Model{Mode: ModePick, port: port, started: time.Now()}
}

func NewFollow(port int, f *tab.Follower, rec *tab.Recording) *Model {
	return &Model{Mode: ModeFollow, port: port, follower: f, rec: rec, started: time.Now()}
}

func (m *Model) Init() tea.Cmd {
	return tea.Batch(m.refresh(), tick())
}

func tick() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m *Model) refresh() tea.Cmd {
	return func() tea.Msg {
		if m.Mode == ModeFollow && m.follower != nil {
			return tabsMsg{tabs: m.follower.Tabs()}
		}
		tabs, err := tab.ListRecordableTabs(m.port)
		return tabsMsg{tabs: tabs, err: err}
	}
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height

	case tabsMsg:
		m.tabs, m.err = msg.tabs, msg.err
		if m.cursor >= len(m.tabs) {
			m.cursor = max(0, len(m.tabs)-1)
		}

	case tickMsg:
		return m, tea.Batch(m.refresh(), tick())

	case updateMsg:
		return m, m.refresh()

	case Narration:
		line := strings.TrimSpace(msg.Text)
		if line != "" {
			m.narration = append(m.narration, fmt.Sprintf("%s  %s", tab.FormatClock(msg.T), line))
			if len(m.narration) > narrationKeep {
				m.narration = m.narration[len(m.narration)-narrationKeep:]
			}
		}

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			m.quitting = true
			if m.Mode == ModePick {
				m.Cancelled = true
			}
			return m, tea.Quit

		case "j", "down":
			if m.cursor < len(m.tabs)-1 {
				m.cursor++
			}
		case "k", "up":
			if m.cursor > 0 {
				m.cursor--
			}
		case "g":
			m.cursor = 0
		case "G":
			m.cursor = max(0, len(m.tabs)-1)

		case "enter":
			if m.Mode == ModePick && m.cursor < len(m.tabs) {
				t := m.tabs[m.cursor]
				m.Chosen = &t
				m.quitting = true
				return m, tea.Quit
			}

		case "m":
			// Through the follower when there is one, so the mark is
			// attributed to the tab that was in front when it was pressed.
			if m.Mode == ModeFollow && m.follower != nil {
				m.follower.Mark("")
				m.marks++
			} else if m.Mode == ModeFollow && m.rec != nil {
				m.rec.Push(tab.Event{T: m.rec.Clock.Now(), Kind: "mark"})
				m.marks++
			}

		case "r":
			return m, m.refresh()
		}
	}
	return m, nil
}

var (
	border   = lipgloss.Color("#5C7CFA")
	dim      = lipgloss.Color("#6C757D")
	good     = lipgloss.Color("#40C057")
	warn     = lipgloss.Color("#FAB005")
	bad      = lipgloss.Color("#FA5252")
	panel    = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(border).Padding(0, 1)
	titleSt  = lipgloss.NewStyle().Bold(true)
	dimSt    = lipgloss.NewStyle().Foreground(dim)
	activeSt = lipgloss.NewStyle().Foreground(good).Bold(true)
	cursorSt = lipgloss.NewStyle().Foreground(border).Bold(true)
	errSt    = lipgloss.NewStyle().Foreground(bad)
	warnSt   = lipgloss.NewStyle().Foreground(warn)
)

func (m *Model) View() string {
	if m.quitting {
		return ""
	}

	w := m.width
	if w < 40 {
		w = 80
	}
	inner := w - 6

	var b strings.Builder

	header := "recgo-tab — pick a tab to record"
	if m.Mode == ModeFollow {
		header = "recgo-browser — following you across tabs"
	}
	b.WriteString(titleSt.Render(header))
	b.WriteString("  ")
	b.WriteString(dimSt.Render(m.elapsed()))
	b.WriteString("\n\n")

	if m.err != nil {
		b.WriteString(errSt.Render(fmt.Sprintf("cannot reach the browser on port %d: %v", m.port, m.err)))
		b.WriteString("\n")
		b.WriteString(dimSt.Render("start it with --remote-debugging-port=" + fmt.Sprint(m.port)))
		b.WriteString("\n\n")
	}

	b.WriteString(panel.Width(w - 2).Render(m.tabList(inner)))
	b.WriteString("\n")

	if m.Mode == ModeFollow && m.rec != nil {
		b.WriteString(panel.Width(w - 2).Render(m.stats()))
		b.WriteString("\n")
	}

	if len(m.narration) > 0 {
		b.WriteString(panel.Width(w - 2).Render(
			titleSt.Render("narration") + "\n" + strings.Join(m.narration, "\n")))
		b.WriteString("\n")
	}

	b.WriteString(dimSt.Render(m.help()))
	return b.String()
}

func (m *Model) tabList(inner int) string {
	if len(m.tabs) == 0 {
		return dimSt.Render("no recordable tabs open")
	}

	var b strings.Builder
	for i, t := range m.tabs {
		cursor := "  "
		if i == m.cursor {
			cursor = cursorSt.Render("▸ ")
		}

		dot := dimSt.Render("○")
		if t.Visible {
			dot = activeSt.Render("●")
		}

		label := t.Short(inner - 24)
		line := fmt.Sprintf("%s%s %-*s", cursor, dot, max(1, inner-24), label)

		var suffix string
		switch {
		case t.Clicks > 0:
			suffix = fmt.Sprintf("%d clicks", t.Clicks)
		case t.Attached:
			suffix = "attached"
		}
		b.WriteString(line + dimSt.Render(suffix))
		if i < len(m.tabs)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

func (m *Model) stats() string {
	clicks, hmr, errs := m.rec.Counts()

	errStr := fmt.Sprintf("%d errors", errs)
	if errs > 0 {
		errStr = warnSt.Render(errStr)
	}
	parts := []string{
		fmt.Sprintf("%d clicks", clicks),
		fmt.Sprintf("%d HMR", hmr),
		errStr,
		fmt.Sprintf("%d marks", m.marks),
		fmt.Sprintf("%d tabs", m.follower.Count()),
	}
	return strings.Join(parts, dimSt.Render(" · "))
}

func (m *Model) elapsed() string {
	d := time.Since(m.started).Truncate(time.Second)
	return fmt.Sprintf("%02d:%02d", int(d.Minutes()), int(d.Seconds())%60)
}

func (m *Model) help() string {
	if m.Mode == ModePick {
		return "j/k move · enter record this tab · r refresh · q cancel"
	}
	return "j/k move · m mark this moment · r refresh · q stop and write the session"
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func Refresh() tea.Msg { return updateMsg{} }
