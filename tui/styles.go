package tui

import "github.com/charmbracelet/lipgloss"

var (
	UserMsgStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("255")).
			Background(lipgloss.Color("24")).
			Padding(0, 1)

	ViewportStyle = lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("62")).
			Padding(0, 1)

	HeaderStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("212")).
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("63")).
			Padding(0, 1)

	SubtleStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))

	StatusStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("220"))

	PromptStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("230")).
			Background(lipgloss.Color("24")).
			Padding(0, 1)

	ToolCallStyle = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("204"))
)
