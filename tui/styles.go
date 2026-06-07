package tui

import "github.com/charmbracelet/lipgloss"

// Catppuccin Mocha Palette
var (
	MochaMauve   = lipgloss.Color("#cba6f7")
	MochaBlue    = lipgloss.Color("#89b4fa")
	MochaPink    = lipgloss.Color("#f5c2e7")
	MochaGreen   = lipgloss.Color("#a6e3a1")
	MochaYellow  = lipgloss.Color("#f9e2af")
	MochaText    = lipgloss.Color("#cdd6f4")
	MochaBase    = lipgloss.Color("#1e1e2e")
	MochaSurface = lipgloss.Color("#313244")
	MochaOverlay = lipgloss.Color("#6c7086") // Used for subtle/faint text
)

var (
	// User messages align right/distinct
	UserMsgStyle = lipgloss.NewStyle().
			Foreground(MochaBase).
			Background(MochaBlue).
			Padding(0, 1).
			Bold(true).
			MarginLeft(2)

	// AI Assistant messages
	AssistantMsgStyle = lipgloss.NewStyle().
			Foreground(MochaText)

	// Main scrolling window
	ViewportStyle = lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(MochaMauve).
			Padding(0, 1)

	// Top banner
	HeaderStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(MochaBase).
			Background(MochaMauve).
			Padding(0, 2)

	// Faint/meta text
	SubtleStyle = lipgloss.NewStyle().
			Foreground(MochaOverlay).
			Italic(true)

	// Status line when processing
	StatusStyle = lipgloss.NewStyle().
			Foreground(MochaYellow).
			Bold(true)

	// Input prompt ❯
	PromptStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(MochaPink)

	// System logs/Tool calls
	ToolCallStyle = lipgloss.NewStyle().
			Foreground(MochaGreen).
			Italic(true)
)
