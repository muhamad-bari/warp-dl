package ui

import (
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"warp-dl/internal/downloader"
)

type tickMsg time.Time

type Model struct {
	stats    *downloader.Stats
	progress progress.Model
	quitting bool
	err      error
}

func NewModel(stats *downloader.Stats) Model {
	return Model{
		stats:    stats,
		progress: progress.New(progress.WithDefaultGradient()),
	}
}

func (m Model) Init() tea.Cmd {
	return tickCmd()
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			m.quitting = true
			return m, tea.Quit
		}
		return m, nil

	case tea.WindowSizeMsg:
		m.progress.Width = msg.Width - 4
		if m.progress.Width > 80 {
			m.progress.Width = 80
		}
		return m, nil

	case tickMsg:
		if m.stats == nil {
			return m, tickCmd()
		}

		// Calculate progress
		var percent float64
		if m.stats.TotalBytes > 0 {
			percent = float64(m.stats.GetDownloaded()) / float64(m.stats.TotalBytes)
		}

		cmd := m.progress.SetPercent(percent)
		
		if percent >= 1.0 {
			m.quitting = true
			return m, tea.Batch(cmd, tea.Quit)
		}

		return m, tea.Batch(cmd, tickCmd())

	default:
		return m, nil
	}
}

func (m Model) View() string {
	if m.err != nil {
		return fmt.Sprintf("Error: %v\n", m.err)
	}

	if m.stats == nil {
		return "Initializing...\n"
	}

	pad := lipgloss.NewStyle().Padding(1).Render

	info := fmt.Sprintf("Downloaded: %.2f MB / %.2f MB", 
		float64(m.stats.GetDownloaded())/1024/1024, 
		float64(m.stats.TotalBytes)/1024/1024)

	return pad(fmt.Sprintf("\n%s\n%s\n", info, m.progress.View()))
}

func tickCmd() tea.Cmd {
	return tea.Tick(time.Millisecond*100, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}
