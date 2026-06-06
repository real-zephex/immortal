package tui

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"immortal/utils"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
)

type responseMsg string
type thinkingMsg struct {
	thinking bool
	status   string
}
type logMsg string

type tuiModel struct {
	db         *sql.DB
	ctx        context.Context
	cancel     context.CancelFunc
	eventsCh   chan<- utils.Event
	responseCh <-chan string

	viewport   viewport.Model
	textinput  textinput.Model
	messages   []string
	width      int
	height     int

	thinking   bool
	statusText string
	history    []string
	historyIdx int
}

func (m tuiModel) Init() tea.Cmd {
	return textinput.Blink
}

func (m tuiModel) headerView() string {
	title := HeaderStyle.Render("immortal • interactive")
	meta := SubtleStyle.Render("commands: /help for slash commands")
	bar := SubtleStyle.Render(strings.Repeat("─", m.width))
	if len(bar) > 0 {
		return title + "\n" + meta + "\n" + bar
	}
	return title + "\n" + meta
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

			wrapLimit := m.viewport.Width - 4
			if wrapLimit < 20 {
				wrapLimit = 20
			}
			wrappedInput := wrapText(input, wrapLimit)
			lines := strings.Split(wrappedInput, "\n")
			var formattedUserMsg strings.Builder
			formattedUserMsg.WriteString("\n")
			for _, line := range lines {
				formattedUserMsg.WriteString(UserMsgStyle.Render(line) + "\n")
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
				switch input {
				case "/help":
					helpText := "\n  /help  - show this help\n  /clear - clear conversation history\n  /exit  - exit the program\n\n"
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
					m.messages = append(m.messages, fmt.Sprintf("Unknown command: %s\n", input))
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
			m.statusText = "processing..."
			return m, nil
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
		return m, nil

	case thinkingMsg:
		m.thinking = msg.thinking
		m.statusText = msg.status
		return m, nil

	case logMsg:
		m.messages = append(m.messages, string(msg))
		m.viewport.SetContent(m.renderContent())
		m.viewport.GotoBottom()
		return m, nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		headerHeight := lipgloss.Height(m.headerView())
		footerHeight := 3
		m.viewport.Width = msg.Width - 4
		m.viewport.Height = msg.Height - headerHeight - footerHeight - 2
		m.textinput.Width = msg.Width - promptWidth()
		m.viewport.SetContent(m.renderContent())
		m.viewport.GotoBottom()
	}

	// Don't forward mouse events to textinput — mouse wheel and clicks
	// can leak random characters into the input field.
	// Only textinput and viewport handle their own relevant messages.
	switch msg := msg.(type) {
	case tea.KeyMsg:
		m.textinput, cmd = m.textinput.Update(msg)
		cmds = append(cmds, cmd)
		// KeyMsg already handled above for scroll keys, history, etc.
		// Don't forward KeyMsg to viewport to prevent j/k scroll interference.
	case tea.MouseMsg:
		m.viewport, cmd = m.viewport.Update(msg)
		cmds = append(cmds, cmd)
	default:
		m.textinput, cmd = m.textinput.Update(msg)
		cmds = append(cmds, cmd)
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

	if m.thinking {
		s.WriteString(StatusStyle.Render("● "+m.statusText) + "\n")
	} else {
		s.WriteString("\n")
	}

	s.WriteString(PromptStyle.Render("immortal ❯") + " " + m.textinput.View())
	return s.String()
}

var activeProgram *tea.Program

func listenResponses(ctx context.Context, p *tea.Program, responseCh <-chan string) {
	for {
		select {
		case <-ctx.Done():
			return
		case resp, ok := <-responseCh:
			if !ok {
				return
			}
			p.Send(responseMsg(resp))
		}
	}
}

func RunTUI(ctx context.Context, db *sql.DB, eventsCh chan<- utils.Event, responseCh <-chan string) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	ti := textinput.New()
	ti.Placeholder = "Ask a question..."
	ti.Focus()
	ti.CharLimit = 2048

	// Detect terminal width for initial sizing
	termWidth, _, err := getTermSize()
	if err != nil || termWidth < 40 {
		termWidth = 80
	}
	ti.Width = termWidth - promptWidth()

	vp := viewport.New(termWidth-4, 20)

	m := &tuiModel{
		db:         db,
		ctx:        ctx,
		cancel:     cancel,
		eventsCh:   eventsCh,
		responseCh: responseCh,
		textinput:  ti,
		viewport:   vp,
	}

	// Pre-load conversation history from DB
	params := utils.LoadConversation(db, "default")
	if params != nil {
		for _, param := range params {
			role, content := extractRoleContent(param)
			if role == "" {
				continue
			}
			switch role {
			case "user":
				if content == "" {
					continue
				}
				wrapLimit := vp.Width - 4
				if wrapLimit < 20 {
					wrapLimit = 20
				}
				wrappedInput := wrapText(content, wrapLimit)
				lines := strings.Split(wrappedInput, "\n")
				var formattedUserMsg strings.Builder
				formattedUserMsg.WriteString("\n")
				for _, line := range lines {
					formattedUserMsg.WriteString(UserMsgStyle.Render(line) + "\n")
				}
				formattedUserMsg.WriteString("\n")
				m.messages = append(m.messages, formattedUserMsg.String())
			case "assistant":
				if content == "" {
					continue
				}
				m.messages = append(m.messages, renderToStringWithWidth(content, vp.Width)+"\n")
			}
		}
	}

	m.viewport.SetContent(m.renderContent())
	m.viewport.GotoBottom()

	utils.PrintHook = func(text string) {
		if activeProgram != nil {
			activeProgram.Send(logMsg(text))
		}
	}
	utils.ThinkingHook = func(thinking bool, status string) {
		if activeProgram != nil {
			activeProgram.Send(thinkingMsg{thinking: thinking, status: status})
		}
	}
	utils.DebugHook = func(string) {} // noop — suppress debug prints in TUI mode

	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())
	activeProgram = p
	defer func() {
		activeProgram = nil
		utils.PrintHook = nil
		utils.ThinkingHook = nil
		utils.DebugHook = nil
	}()

	go listenResponses(ctx, p, responseCh)

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
	if limit <= 0 {
		return text
	}
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
				if currentLine != "" {
					wrappedLines = append(wrappedLines, currentLine)
				}
				for len(word) > limit {
					wrappedLines = append(wrappedLines, word[:limit])
					word = word[limit:]
				}
				currentLine = word
			} else {
				if currentLine == "" {
					currentLine = word
				} else {
					currentLine += " " + word
				}
			}
		}
		if currentLine != "" {
			wrappedLines = append(wrappedLines, currentLine)
		}
	}

	return strings.Join(wrappedLines, "\n")
}

// extractRoleContent extracts the role and content from a ChatCompletionMessageParamUnion
// by marshaling to JSON and back to a map.
func extractRoleContent(param interface{}) (string, string) {
	data, err := json.Marshal(param)
	if err != nil {
		return "", ""
	}
	var msg map[string]any
	if err := json.Unmarshal(data, &msg); err != nil {
		return "", ""
	}

	role, _ := msg["role"].(string)

	// Content can be a string or nil (tool calls, etc.)
	content := ""
	if c, ok := msg["content"].(string); ok {
		content = c
	}

	return role, content
}

func getTermSize() (int, int, error) {
	return term.GetSize(int(os.Stdin.Fd()))
}

func promptWidth() int {
	return lipgloss.Width(PromptStyle.Render("immortal ❯")) + 1
}
