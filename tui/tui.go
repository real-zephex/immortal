package tui

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"immortal/utils"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
)

type responseMsg string
type statusMsg string
type logMsg string

type tuiModel struct {
	db         *sql.DB
	ctx        context.Context
	cancel     context.CancelFunc
	eventsCh   chan<- utils.Event
	responseCh <-chan string

	viewport  viewport.Model
	textinput textinput.Model
	spinner   spinner.Model
	messages  []string
	width     int
	height    int

	thinking   bool
	statusText string
	history    []string
	historyIdx int
}

func (m tuiModel) Init() tea.Cmd {
	return tea.Batch(textinput.Blink, m.spinner.Tick, waitForResponse(m.ctx, m.responseCh))
}

func (m tuiModel) headerView() string {
	title := HeaderStyle.Render(" 🤖 IMMORTAL AGENT ")
	meta := SubtleStyle.Render(" /help | /clear | pgup/pgdn ")
	
	barWidth := m.width - lipgloss.Width(title) - lipgloss.Width(meta) - 2
	if barWidth < 0 {
		barWidth = 0
	}
	bar := SubtleStyle.Render(strings.Repeat("─", barWidth))
	
	return lipgloss.JoinHorizontal(lipgloss.Center, title, " ", meta, " ", bar)
}

func (m tuiModel) renderContent() string {
	var sb strings.Builder
	for _, text := range m.messages {
		sb.WriteString(text)
	}
	return sb.String()
}

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			m.cancel()
			return m, tea.Quit
		case "pgup":
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		case "pgdown":
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd
		case "up":
			if len(m.history) > 0 && m.historyIdx > 0 {
				m.historyIdx--
				m.textinput.SetValue(m.history[m.historyIdx])
				m.textinput.SetCursor(len(m.history[m.historyIdx]))
			}
			return m, nil
		case "down":
			if len(m.history) > 0 && m.historyIdx < len(m.history)-1 {
				m.historyIdx++
				m.textinput.SetValue(m.history[m.historyIdx])
				m.textinput.SetCursor(len(m.history[m.historyIdx]))
			} else {
				m.historyIdx = len(m.history)
				m.textinput.SetValue("")
			}
			return m, nil
		case "enter":
			input := strings.TrimSpace(m.textinput.Value())
			if input == "" {
				return m, nil
			}

			m.textinput.SetValue("")

			wrapLimit := m.viewport.Width - 6
			if wrapLimit < 20 {
				wrapLimit = 20
			}
			wrappedInput := wrapText(input, wrapLimit)
			lines := strings.Split(wrappedInput, "\n")
			var formattedUserMsg strings.Builder
			formattedUserMsg.WriteString("\n")
			for _, line := range lines {
				// Right-align simulation by adding padding based on viewport width
				padding := m.viewport.Width - lipgloss.Width(line) - 5
				if padding < 0 { padding = 0 }
				formattedUserMsg.WriteString(strings.Repeat(" ", padding) + UserMsgStyle.Render(line) + "\n")
			}
			formattedUserMsg.WriteString("\n")
			m.messages = append(m.messages, formattedUserMsg.String())

			m.viewport.SetContent(m.renderContent())
			m.viewport.GotoBottom()

			m.history = append(m.history, input)
			m.historyIdx = len(m.history)

			if input == "/exit" || input == "/quit" {
				m.cancel()
				return m, tea.Quit
			}

			if strings.HasPrefix(input, "/") {
				mauveStyle := lipgloss.NewStyle().Foreground(MochaMauve)
				pinkStyle := lipgloss.NewStyle().Foreground(MochaPink)
				
				switch input {
				case "/help":
					helpText := "\n  " + mauveStyle.Render("/help") + SubtleStyle.Render("  - show this help") +
								"\n  " + mauveStyle.Render("/clear") + SubtleStyle.Render(" - clear conversation history") +
								"\n  " + mauveStyle.Render("/exit") + SubtleStyle.Render("  - exit the program\n\n")
					m.messages = append(m.messages, helpText)
					m.viewport.SetContent(m.renderContent())
					m.viewport.GotoBottom()
					return m, nil
				case "/clear":
					utils.ClearConversations(m.db)
					m.messages = nil
					m.viewport.SetContent("")
					m.viewport.GotoBottom()
					return m, nil
				default:
					m.messages = append(m.messages, fmt.Sprintf("\n%s\n", pinkStyle.Render("Unknown command: "+input)))
					m.viewport.SetContent(m.renderContent())
					m.viewport.GotoBottom()
					return m, nil
				}
			}

			select {
			case m.eventsCh <- utils.Event{Type: utils.EventTypeUserMessage, Payload: input}:
			default:
			}

			m.thinking = true
			m.statusText = "Processing..."
			return m, m.spinner.Tick
		default:
			if !isTextInputKey(msg) {
				return m, nil
			}
			m.textinput, cmd = m.textinput.Update(msg)
			cmds = append(cmds, cmd)
		}

	case responseMsg:
		m.thinking = false
		m.statusText = ""
		responseText := string(msg)
		if responseText != "" {
			m.messages = append(m.messages, renderToStringWithWidth(responseText, m.viewport.Width)+"\n")
		}
		m.viewport.SetContent(m.renderContent())
		m.viewport.GotoBottom()
		return m, waitForResponse(m.ctx, m.responseCh)

	case statusMsg:
		m.statusText = string(msg)
		return m, nil

	case logMsg:
		m.messages = append(m.messages, ToolCallStyle.Render("✦ "+string(msg))+"\n")
		m.viewport.SetContent(m.renderContent())
		m.viewport.GotoBottom()
		return m, nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		headerHeight := lipgloss.Height(m.headerView())
		footerHeight := 3
		// Subtract 4 for ViewportStyle border (2) + padding (2)
		m.viewport.Width = max(1, msg.Width-4)
		m.viewport.Height = max(1, msg.Height-headerHeight-footerHeight-1)
		
		promptPrefix := PromptStyle.Render("❯") + " "
		m.textinput.Width = max(1, msg.Width-lipgloss.Width(promptPrefix))
		
		m.viewport.SetContent(m.renderContent())
		m.viewport.GotoBottom()
	
	case spinner.TickMsg:
		if m.thinking {
			m.spinner, cmd = m.spinner.Update(msg)
			cmds = append(cmds, cmd)
		}
	}

	switch msg := msg.(type) {
	case tea.MouseMsg:
		m.viewport, cmd = m.viewport.Update(msg)
		cmds = append(cmds, cmd)
	default:
		m.viewport, cmd = m.viewport.Update(msg)
		cmds = append(cmds, cmd)
	}

	return m, tea.Batch(cmds...)
}

func (m tuiModel) View() string {
	if m.width == 0 || m.height == 0 {
		return "Initializing..."
	}

	var s strings.Builder
	s.WriteString(m.headerView() + "\n")
	s.WriteString(ViewportStyle.Render(m.viewport.View()) + "\n")

	// Status Line / Input Area
	if m.thinking {
		s.WriteString(StatusStyle.Render(m.spinner.View()+" "+m.statusText) + "\n")
	} else {
		s.WriteString("\n")
	}

	textStyle := lipgloss.NewStyle().Foreground(MochaText)
	s.WriteString(PromptStyle.Render("❯") + " " + textStyle.Render(m.textinput.View()))
	return s.String()
}

func waitForResponse(ctx context.Context, responseCh <-chan string) tea.Cmd {
	if responseCh == nil {
		return nil
	}
	return func() tea.Msg {
		select {
		case <-ctx.Done():
			return nil
		case resp, ok := <-responseCh:
			if !ok {
				return nil
			}
			return responseMsg(resp)
		}
	}
}

func isTextInputKey(msg tea.KeyMsg) bool {
	switch msg.Type {
	case tea.KeyRunes:
		if msg.Paste {
			return areSafePasteRunes(msg.Runes)
		}
		return !msg.Alt && arePrintableRunes(msg.Runes) && !looksLikeTerminalControlFragment(msg.Runes)
	case tea.KeySpace, tea.KeyBackspace, tea.KeyDelete, tea.KeyLeft, tea.KeyRight,
		tea.KeyHome, tea.KeyEnd, tea.KeyCtrlA, tea.KeyCtrlB, tea.KeyCtrlD,
		tea.KeyCtrlE, tea.KeyCtrlF, tea.KeyCtrlH, tea.KeyCtrlK, tea.KeyCtrlU,
		tea.KeyCtrlV, tea.KeyCtrlW:
		return true
	default:
		return false
	}
}

func arePrintableRunes(runes []rune) bool {
	if len(runes) == 0 {
		return false
	}
	for _, r := range runes {
		if r < ' ' || r == 0x7f {
			return false
		}
	}
	return true
}

func areSafePasteRunes(runes []rune) bool {
	if len(runes) == 0 {
		return false
	}
	for _, r := range runes {
		switch r {
		case '\t', '\n', '\r':
			continue
		case 0x1b, 0x7f:
			return false
		default:
			if r < ' ' {
				return false
			}
		}
	}
	return true
}

func looksLikeTerminalControlFragment(runes []rune) bool {
	fragment := string(runes)
	if strings.HasPrefix(fragment, "[<") && strings.Contains(fragment, ";") {
		return true
	}
	if strings.HasPrefix(fragment, "[M") {
		return true
	}
	return false
}

func RunTUI(ctx context.Context, cancel context.CancelFunc, db *sql.DB, eventsCh chan<- utils.Event, responseCh <-chan string) {
	defer cancel()

	ti := textinput.New()
	ti.Placeholder = "Message immortal-agent..."
	ti.Prompt = ""
	ti.Focus()
	ti.CharLimit = 2048
	
	ti.TextStyle = lipgloss.NewStyle().Foreground(MochaText)
	ti.PlaceholderStyle = lipgloss.NewStyle().Foreground(MochaOverlay)
	ti.Cursor.Style = lipgloss.NewStyle().Foreground(MochaPink)

	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(MochaPink)

	termWidth, _, err := getTermSize()
	if err != nil || termWidth < 40 {
		termWidth = 80
	}
	promptPrefix := PromptStyle.Render("❯") + " "
	ti.Width = max(1, termWidth-lipgloss.Width(promptPrefix))

	vp := viewport.New(max(1, termWidth-4), 20)

	m := &tuiModel{
		db:         db,
		ctx:        ctx,
		cancel:     cancel,
		eventsCh:   eventsCh,
		responseCh: responseCh,
		textinput:  ti,
		spinner:    s,
		viewport:   vp,
	}

	params := utils.LoadConversation(db, "default")
	if params != nil {
		for _, param := range params {
			role, content := extractRoleContent(param)
			if role == "" || content == "" {
				continue
			}
			switch role {
			case "user":
				wrapLimit := vp.Width - 6
				if wrapLimit < 20 { wrapLimit = 20 }
				wrappedInput := wrapText(content, wrapLimit)
				lines := strings.Split(wrappedInput, "\n")
				var formattedUserMsg strings.Builder
				formattedUserMsg.WriteString("\n")
				for _, line := range lines {
					padding := vp.Width - lipgloss.Width(line) - 5
					if padding < 0 { padding = 0 }
					formattedUserMsg.WriteString(strings.Repeat(" ", padding) + UserMsgStyle.Render(line) + "\n")
				}
				formattedUserMsg.WriteString("\n")
				m.messages = append(m.messages, formattedUserMsg.String())
			case "assistant":
				m.messages = append(m.messages, renderToStringWithWidth(content, vp.Width)+"\n")
			}
		}
	}

	m.viewport.SetContent(m.renderContent())
	m.viewport.GotoBottom()

	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())

	utils.PrintHook = func(text string) {
		p.Send(logMsg(text))
	}
	utils.StatusHook = func(status string) {
		p.Send(statusMsg(status))
	}
	utils.DebugHook = func(string) {}

	defer func() {
		utils.PrintHook = nil
		utils.DebugHook = nil
	}()

	if _, err := p.Run(); err != nil {
		fmt.Printf("TUI error: %v\n", err)
	}
}

func renderToStringWithWidth(text string, width int) string {
	if width < 20 {
		width = 20
	}

	r, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return text
	}
	out, err := r.Render(text)
	if err != nil {
		return text
	}
	return out
}

func wrapText(text string, limit int) string {
	if limit <= 0 { return text }
	lines := strings.Split(text, "\n")
	var wrappedLines []string

	for _, line := range lines {
		if len(line) <= limit {
			wrappedLines = append(wrappedLines, line)
			continue
		}
		words := strings.Fields(line)
		if len(words) == 0 {
			wrappedLines = append(wrappedLines, "")
			continue
		}
		var currentLine string
		for _, word := range words {
			if len(currentLine)+len(word)+1 > limit {
				if currentLine != "" { wrappedLines = append(wrappedLines, currentLine) }
				for len(word) > limit {
					wrappedLines = append(wrappedLines, word[:limit])
					word = word[limit:]
				}
				currentLine = word
			} else {
				if currentLine == "" { currentLine = word } else { currentLine += " " + word }
			}
		}
		if currentLine != "" { wrappedLines = append(wrappedLines, currentLine) }
	}
	return strings.Join(wrappedLines, "\n")
}

func extractRoleContent(param interface{}) (string, string) {
	data, err := json.Marshal(param)
	if err != nil { return "", "" }
	var msg map[string]any
	if err := json.Unmarshal(data, &msg); err != nil { return "", "" }

	role, _ := msg["role"].(string)
	content := ""
	if c, ok := msg["content"].(string); ok { content = c }
	return role, content
}

func getTermSize() (int, int, error) {
	return term.GetSize(int(os.Stdin.Fd()))
}

func stringPtr(s string) *string { return &s }
func boolPtr(b bool) *bool       { return &b }
func uintPtr(u uint) *uint       { return &u }
