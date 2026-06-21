package tui

import "github.com/charmbracelet/lipgloss"

var (
	styleTitle    = lipgloss.NewStyle().Bold(true)
	styleDivider  = lipgloss.NewStyle().Foreground(lipgloss.Color("240"))
	styleColHead  = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("245"))
	styleUp       = lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Bold(true)
	styleDown     = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true)
	styleAuthErr  = lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Bold(true)
	stylePending  = lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Italic(true)
	styleSelected = lipgloss.NewStyle().Background(lipgloss.Color("236"))
	styleHelp     = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
)

// statusStyle returns the lipgloss style for a given checker status string.
func statusStyle(s string) lipgloss.Style {
	switch s {
	case "UP":
		return styleUp
	case "DOWN":
		return styleDown
	case "AUTH ERR":
		return styleAuthErr
	default:
		return stylePending
	}
}
