package tui

import "github.com/charmbracelet/lipgloss"

// Modern Color Palette (Catppuccin Mocha / Tokyo Night inspired)
var (
	ColorMauve    = lipgloss.Color("#cba6f7")
	ColorBlue     = lipgloss.Color("#89b4fa")
	ColorCyan     = lipgloss.Color("#89dceb")
	ColorTeal     = lipgloss.Color("#94e2d5")
	ColorGreen    = lipgloss.Color("#a6e3a1")
	ColorYellow   = lipgloss.Color("#f9e2af")
	ColorPeach    = lipgloss.Color("#fab387")
	ColorRed      = lipgloss.Color("#f38ba8")
	ColorLavender = lipgloss.Color("#b4befe")
	ColorText     = lipgloss.Color("#cdd6f4")
	ColorSubtext  = lipgloss.Color("#a6adc8")
	ColorOverlay  = lipgloss.Color("#6c7086")
	ColorSurface  = lipgloss.Color("#313244")
	ColorMantle   = lipgloss.Color("#181825")
	ColorBase     = lipgloss.Color("#1e1e2e")
	ColorBorder   = lipgloss.Color("#45475a")
)

var (
	// Backward compatibility references for existing code/tests
	MochaMauve   = ColorMauve
	MochaBlue    = ColorBlue
	MochaPink    = ColorRed
	MochaGreen   = ColorGreen
	MochaYellow  = ColorYellow
	MochaText    = ColorText
	MochaBase    = ColorBase
	MochaSurface = ColorSurface
	MochaOverlay = ColorOverlay
)

var (
	// Top Header Elements
	LogoBadgeStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorBase).
			Background(ColorMauve).
			Padding(0, 1)

	ModelBadgeStyle = lipgloss.NewStyle().
			Foreground(ColorLavender).
			Background(ColorSurface).
			Padding(0, 1).
			Bold(true)

	StatusIdleStyle = lipgloss.NewStyle().
			Foreground(ColorGreen).
			Bold(true)

	StatusActiveStyle = lipgloss.NewStyle().
			Foreground(ColorYellow).
			Bold(true)

	StatusErrorStyle = lipgloss.NewStyle().
			Foreground(ColorRed).
			Bold(true)

	HeaderStyle = lipgloss.NewStyle().
			Background(ColorMantle).
			Border(lipgloss.NormalBorder(), false, false, true, false).
			BorderForeground(ColorBorder)

	// User Messages
	UserHeaderStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorBlue)

	UserMsgStyle = lipgloss.NewStyle().
			Foreground(ColorText).
			Border(lipgloss.NormalBorder(), false, false, false, true).
			BorderForeground(ColorBlue).
			Padding(0, 0, 0, 1)

	// Assistant Messages
	AssistantHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(ColorMauve)

	AssistantMsgStyle = lipgloss.NewStyle().
			Foreground(ColorText).
			Border(lipgloss.NormalBorder(), false, false, false, true).
			BorderForeground(ColorMauve).
			Padding(0, 0, 0, 1)

	// Tool Executions & Logs
	ToolTagStyle = lipgloss.NewStyle().
			Foreground(ColorTeal).
			Bold(true)

	ToolCallStyle = lipgloss.NewStyle().
			Foreground(ColorTeal).
			Italic(true)

	ToolBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.NormalBorder(), false, false, false, true).
			BorderForeground(ColorTeal).
			Padding(0, 0, 0, 1)

	// System / Error / General Text
	SubtleStyle = lipgloss.NewStyle().
			Foreground(ColorOverlay).
			Italic(true)

	StatusStyle = lipgloss.NewStyle().
			Foreground(ColorYellow).
			Bold(true)

	ErrorMsgStyle = lipgloss.NewStyle().
			Foreground(ColorRed).
			Bold(true)

	// Input Container & Prompt
	PromptStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(ColorMauve)

	InputBoxStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(ColorBorder).
			Padding(0, 1)

	InputBoxActiveStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(ColorMauve).
				Padding(0, 1)

	// Viewport Container
	ViewportStyle = lipgloss.NewStyle().
			Padding(0, 0)

	// Footer / Keybindings bar
	FooterStyle = lipgloss.NewStyle().
			Foreground(ColorSubtext).
			Background(ColorMantle).
			Padding(0, 1)

	KeyBadgeStyle = lipgloss.NewStyle().
			Foreground(ColorText).
			Background(ColorSurface).
			Bold(true).
			Padding(0, 1)
)
