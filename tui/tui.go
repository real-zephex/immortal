package tui

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"immortal/utils"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
	"golang.org/x/term"
)

type responseMsg string
type statusMsg string
type logMsg string
type sendErrorMsg string

const (
	defaultViewportHeight = 20
	minMessageWidth       = 20
	maxHistoryEntries     = 200
	maxInputLines         = 5
)

type tuiModel struct {
	db         *sql.DB
	ctx        context.Context
	cancel     context.CancelFunc
	eventsCh   chan<- utils.Event
	responseCh <-chan string

	viewport viewport.Model
	textarea textarea.Model
	spinner  spinner.Model
	messages []string
	width    int
	height   int

	thinking   bool
	pending    int
	statusText string
	history    []string
	historyIdx int
	aborted    bool

	model *string
}

// Init implements tea.Model.
func (m *tuiModel) Init() tea.Cmd {
	return tea.Batch(textarea.Blink, m.spinner.Tick, waitForResponse(m.ctx, m.responseCh))
}

func (m *tuiModel) headerView() string {
	logo := LogoBadgeStyle.Render(" 🤖 IMMORTAL AGENT ")
	model := ModelBadgeStyle.Render(*m.model)

	var status string
	if m.thinking {
		status = StatusActiveStyle.Render(" ◐ THINKING ")
	} else if m.aborted {
		status = StatusErrorStyle.Render(" ⏹ ABORTED ")
	} else {
		status = StatusIdleStyle.Render(" ● READY ")
	}

	left := lipgloss.JoinHorizontal(lipgloss.Center, logo, " ", model, " ", status)
	right := SubtleStyle.Render("Alt+Enter: Newline │ Esc: Abort │ /help: Commands │ Ctrl+C: Exit")

	gapWidth := max(m.width-lipgloss.Width(left)-lipgloss.Width(right), 1)
	// if gapWidth < 1 {
	// 		gapWidth = 1
	// 	}
	spacer := strings.Repeat(" ", gapWidth)

	topBar := lipgloss.JoinHorizontal(lipgloss.Center, left, spacer, right)
	return HeaderStyle.Width(m.width).Render(topBar)
}

func (m *tuiModel) renderContent() string {
	var sb strings.Builder
	for _, text := range m.messages {
		sb.WriteString(text)
	}
	return sb.String()
}

func (m *tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c":
			m.cancel()
			return m, tea.Quit

		case "up":
			if m.textarea.Line() == 0 || m.textarea.LineCount() <= 1 {
				if len(m.history) > 0 && m.historyIdx > 0 {
					m.historyIdx--
					m.textarea.SetValue(m.history[m.historyIdx])
					m.adjustInputHeight()
					m.resize(m.width, m.height)
				}
				return m, nil
			}
			m.textarea, cmd = m.textarea.Update(msg)
			return m, cmd

		case "down":
			if m.textarea.Line() == m.textarea.LineCount()-1 || m.textarea.LineCount() <= 1 {
				if len(m.history) > 0 && m.historyIdx < len(m.history)-1 {
					m.historyIdx++
					m.textarea.SetValue(m.history[m.historyIdx])
					m.adjustInputHeight()
					m.resize(m.width, m.height)
				} else {
					m.historyIdx = len(m.history)
					m.textarea.Reset()
					m.adjustInputHeight()
					m.resize(m.width, m.height)
				}
				return m, nil
			}
			m.textarea, cmd = m.textarea.Update(msg)
			return m, cmd

		case "pgup", "pgdown":
			m.viewport, cmd = m.viewport.Update(msg)
			return m, cmd

		case "esc":
			if m.thinking {
				m.aborted = true
				m.pending = 0
				m.thinking = false
				m.resize(m.width, m.height)
				m.statusText = ""
				select {
				case utils.AbortCh <- struct{}{}:
				default:
				}
			}
			return m, nil

		case "alt+enter", "ctrl+j", "shift+enter":
			m.textarea.InsertString("\n")
			m.adjustInputHeight()
			m.resize(m.width, m.height)
			return m, nil

		case "enter":
			if msg.Alt {
				m.textarea.InsertString("\n")
				m.adjustInputHeight()
				m.resize(m.width, m.height)
				return m, nil
			}

			input := strings.TrimSpace(m.textarea.Value())
			if input == "" {
				return m, nil
			}

			m.textarea.Reset()
			m.adjustInputHeight()
			m.addHistory(input)

			if input == "/exit" || input == "/quit" {
				m.cancel()
				return m, tea.Quit
			}

			if strings.HasPrefix(input, "/") {
				switch input {
				case "/help":
					m.messages = append(m.messages, formatHelpMenu())
					m.viewport.SetContent(m.renderContent())
					m.viewport.GotoBottom()
					m.resize(m.width, m.height)
					return m, nil
				case "/clear":
					utils.ClearConversations(m.db)
					m.messages = nil
					m.viewport.SetContent("")
					m.viewport.GotoBottom()
					m.statusText = "Conversation cleared"
					m.resize(m.width, m.height)
					return m, nil
				case "/memories":
					records, err := utils.ListMemories(m.ctx, m.db)
					var content string
					if err != nil {
						content = ErrorMsgStyle.Render("Failed to load memories: " + err.Error())
					} else if len(records) == 0 {
						content = SubtleStyle.Render("No stored memories found.")
					} else {
						var sb strings.Builder
						sb.WriteString("\n" + AssistantHeaderStyle.Render("✦ STORED MEMORIES") + "\n")
						sb.WriteString(SubtleStyle.Render("  Use full ID with memory_delete tool\n") + "\n")
						for _, r := range records {
							sb.WriteString("  " + KeyBadgeStyle.Render(r.ID) + " " + r.Content + "\n")
						}
						content = sb.String()
					}
					m.messages = append(m.messages, content+"\n")
					m.viewport.SetContent(m.renderContent())
					m.viewport.GotoBottom()
					m.resize(m.width, m.height)
					return m, nil
				case "/tasks":
					tasks := utils.ListLocalTasks()
					var content string
					if len(tasks) == 0 {
						content = SubtleStyle.Render("No active scheduled tasks.")
					} else {
						var sb strings.Builder
						sb.WriteString("\n" + AssistantHeaderStyle.Render("✦ SCHEDULED TASKS") + "\n\n")
						for _, t := range tasks {
							repeatStr := "one-shot"
							if t.Repeat {
								repeatStr = "repeating"
							}
							fmt.Fprintf(&sb, "  %s %s (every %s, %s)\n",
								KeyBadgeStyle.Render(t.ID),
								t.Task,
								t.Interval.String(),
								repeatStr)
						}
						content = sb.String()
					}
					m.messages = append(m.messages, content+"\n")
					m.viewport.SetContent(m.renderContent())
					m.viewport.GotoBottom()
					m.resize(m.width, m.height)
					return m, nil
				case "/status":
					var sb strings.Builder
					sb.WriteString("\n" + AssistantHeaderStyle.Render("✦ SYSTEM STATUS") + "\n\n")
					sb.WriteString("  " + KeyBadgeStyle.Render("Model") + "        deepseek-v4-flash\n")
					sb.WriteString("  " + KeyBadgeStyle.Render("Base URL") + "     https://openrouter.ai/api/v1\n")
					fmt.Fprintf(&sb, "  %s     %d messages in current session\n", KeyBadgeStyle.Render("Messages"), len(m.messages))
					tasks := utils.ListLocalTasks()
					fmt.Fprintf(&sb, "  %s        %d active background tasks\n", KeyBadgeStyle.Render("Tasks"), len(tasks))
					m.messages = append(m.messages, sb.String()+"\n")
					m.viewport.SetContent(m.renderContent())
					m.viewport.GotoBottom()
					m.resize(m.width, m.height)
					return m, nil
				default:
					m.messages = append(m.messages, fmt.Sprintf("\n%s\n", ErrorMsgStyle.Render("Unknown command: "+input)))
					m.viewport.SetContent(m.renderContent())
					m.viewport.GotoBottom()
					m.resize(m.width, m.height)
					return m, nil
				}
			}

			m.messages = append(m.messages, formatUserMessage(input, m.viewport.Width))
			m.viewport.SetContent(m.renderContent())
			m.viewport.GotoBottom()

			m.thinking = true
			m.resize(m.width, m.height)
			m.pending++
			m.statusText = pendingStatus(m.pending)
			return m, tea.Batch(m.spinner.Tick, sendUserMessage(m.ctx, m.eventsCh, input))

		default:
			if !isTextInputKey(msg) {
				return m, nil
			}
			m.textarea, cmd = m.textarea.Update(msg)
			m.adjustInputHeight()
			m.resize(m.width, m.height)
			cmds = append(cmds, cmd)
		}

	case responseMsg:
		if m.aborted {
			m.aborted = false
			m.messages = append(m.messages, SubtleStyle.Render("\n⏹ Task aborted.\n"))
			m.viewport.SetContent(m.renderContent())
			m.viewport.GotoBottom()
			return m, waitForResponse(m.ctx, m.responseCh)
		}
		if m.pending > 0 {
			m.pending--
		}
		m.thinking = m.pending > 0
		m.resize(m.width, m.height)
		m.statusText = pendingStatus(m.pending)
		responseText := string(msg)
		if responseText != "" {
			m.messages = append(m.messages, formatAssistantMessage(responseText, m.viewport.Width))
		} else {
			m.messages = append(m.messages, SubtleStyle.Render("\nNo response returned.\n"))
		}
		m.viewport.SetContent(m.renderContent())
		m.viewport.GotoBottom()
		return m, waitForResponse(m.ctx, m.responseCh)

	case statusMsg:
		if m.thinking {
			m.statusText = string(msg)
		}
		return m, nil

	case logMsg:
		text := string(msg)
		m.messages = append(m.messages, formatToolLog(text))
		m.viewport.SetContent(m.renderContent())
		m.viewport.GotoBottom()
		return m, nil

	case sendErrorMsg:
		if m.pending > 0 {
			m.pending--
		}
		m.thinking = m.pending > 0
		m.resize(m.width, m.height)
		m.statusText = pendingStatus(m.pending)
		m.messages = append(m.messages, fmt.Sprintf("\n%s\n", ErrorMsgStyle.Render(string(msg))))
		m.viewport.SetContent(m.renderContent())
		m.viewport.GotoBottom()
		return m, nil

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.resize(msg.Width, msg.Height)
		m.viewport.SetContent(m.renderContent())
		m.viewport.GotoBottom()
		return m, nil

	case spinner.TickMsg:
		if m.thinking {
			m.spinner, cmd = m.spinner.Update(msg)
			cmds = append(cmds, cmd)
		}
		return m, tea.Batch(cmds...)
	}

	m.viewport, cmd = m.viewport.Update(msg)
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

func (m *tuiModel) addHistory(input string) {
	if input == "" {
		return
	}
	if len(m.history) == 0 || m.history[len(m.history)-1] != input {
		m.history = append(m.history, input)
	}
	if len(m.history) > maxHistoryEntries {
		m.history = append([]string(nil), m.history[len(m.history)-maxHistoryEntries:]...)
	}
	m.historyIdx = len(m.history)
}

func (m *tuiModel) adjustInputHeight() {
	lines := strings.Split(m.textarea.Value(), "\n")
	numLines := min(max(len(lines), 1), maxInputLines)
	// if numLines < 1 {
	// 	numLines = 1
	// }
	// if numLines > maxInputLines {
	// 	numLines = maxInputLines
	// }
	if m.textarea.Height() != numLines {
		m.textarea.SetHeight(numLines)
	}
}

func (m *tuiModel) resize(width, height int) {
	headerHeight := lipgloss.Height(m.headerView())
	statusLines := 0
	if m.thinking {
		statusLines = 1
	}

	promptPrefix := PromptStyle.Render("❯") + " "
	promptWidth := lipgloss.Width(promptPrefix)

	inputWidth := max(1, width-promptWidth-6)
	m.textarea.SetWidth(inputWidth)

	m.adjustInputHeight()
	inputBoxHeight := m.textarea.Height() + 2

	m.viewport.Width = max(1, width)
	m.viewport.Height = max(1, height-headerHeight-statusLines-inputBoxHeight-1)
}

func (m *tuiModel) View() string {
	if m.width == 0 || m.height == 0 {
		return "Initializing..."
	}

	var s strings.Builder
	s.WriteString(m.headerView() + "\n")
	s.WriteString(ViewportStyle.Render(m.viewport.View()))

	if m.thinking {
		s.WriteString("\n " + m.spinner.View() + " " + StatusStyle.Render(m.statusText))
	}

	s.WriteString("\n")
	promptPrefix := PromptStyle.Render("❯") + " "
	inputView := promptPrefix + m.textarea.View()

	boxStyle := InputBoxStyle
	if m.thinking {
		boxStyle = InputBoxActiveStyle
	}
	s.WriteString(boxStyle.Width(max(10, m.width-4)).Render(inputView))

	return s.String()
}

func waitForResponse(ctx context.Context, responseCh <-chan string) tea.Cmd {
	if responseCh == nil {
		return nil
	}
	return func() tea.Msg {
		select {
		case <-ctx.Done():
			return responseMsg("")
		case resp, ok := <-responseCh:
			if !ok {
				return responseMsg("")
			}
			return responseMsg(resp)
		}
	}
}

func sendUserMessage(ctx context.Context, eventsCh chan<- utils.Event, input string) tea.Cmd {
	return func() tea.Msg {
		if eventsCh == nil {
			return sendErrorMsg("Unable to send message: event channel is not available.")
		}
		select {
		case eventsCh <- utils.Event{Type: utils.EventTypeUserMessage, Payload: input}:
			return nil
		case <-ctx.Done():
			err := ctx.Err()
			if err == nil {
				err = errors.New("context cancelled")
			}
			return sendErrorMsg("Unable to send message: " + err.Error())
		}
	}
}

func pendingStatus(pending int) string {
	switch {
	case pending <= 0:
		return ""
	case pending == 1:
		return "Processing..."
	default:
		return fmt.Sprintf("Processing %d messages...", pending)
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
		tea.KeyCtrlV, tea.KeyCtrlW, tea.KeyEnter:
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
	return strings.HasPrefix(fragment, "[M")
}

func RunTUI(ctx context.Context, cancel context.CancelFunc, db *sql.DB, eventsCh chan<- utils.Event, responseCh <-chan string, model *string) {
	defer cancel()

	ta := textarea.New()
	ta.Placeholder = fmt.Sprintf("Ask immortal agent - %s something... (Alt+Enter for new line)", *model)
	ta.Prompt = ""
	ta.ShowLineNumbers = false
	ta.CharLimit = 4096
	ta.SetHeight(1)
	ta.Focus()
	ta.FocusedStyle.Base = lipgloss.NewStyle()
	ta.BlurredStyle.Base = lipgloss.NewStyle()
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.FocusedStyle.Placeholder = lipgloss.NewStyle().Foreground(ColorOverlay)
	ta.FocusedStyle.Text = lipgloss.NewStyle().Foreground(ColorText)

	s := spinner.New()
	s.Spinner = spinner.Dot
	s.Style = lipgloss.NewStyle().Foreground(ColorMauve)

	termWidth, _, err := getTermSize()
	if err != nil || termWidth < 40 {
		termWidth = 80
	}
	promptPrefix := PromptStyle.Render("❯") + " "
	ta.SetWidth(max(1, termWidth-lipgloss.Width(promptPrefix)-6))

	vp := viewport.New(max(1, termWidth-4), defaultViewportHeight)
	vp.KeyMap.Up = key.NewBinding()
	vp.KeyMap.Down = key.NewBinding()
	vp.KeyMap.HalfPageUp = key.NewBinding()
	vp.KeyMap.HalfPageDown = key.NewBinding()
	vp.KeyMap.PageUp = key.NewBinding(key.WithKeys("pgup"))
	vp.KeyMap.PageDown = key.NewBinding(key.WithKeys("pgdown"))

	m := &tuiModel{
		db:         db,
		ctx:        ctx,
		cancel:     cancel,
		eventsCh:   eventsCh,
		responseCh: responseCh,
		textarea:   ta,
		spinner:    s,
		viewport:   vp,
		model:      model,
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
				m.messages = append(m.messages, formatUserMessage(content, vp.Width))
				m.addHistory(content)
			case "assistant":
				m.messages = append(m.messages, formatAssistantMessage(content, vp.Width))
			}
		}
	}

	m.viewport.SetContent(m.renderContent())
	m.viewport.GotoBottom()

	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseCellMotion())

	prevPrintHook := utils.PrintHook
	prevStatusHook := utils.StatusHook
	prevDebugHook := utils.DebugHook

	utils.PrintHook = func(text string) { p.Send(logMsg(text)) }
	utils.StatusHook = func(status string) { p.Send(statusMsg(status)) }
	utils.DebugHook = func(string) {}

	defer func() {
		utils.PrintHook = prevPrintHook
		utils.StatusHook = prevStatusHook
		utils.DebugHook = prevDebugHook
	}()

	if _, err := p.Run(); err != nil {
		fmt.Printf("TUI error: %v\n", err)
	}
}

func formatHelpMenu() string {
	var sb strings.Builder
	sb.WriteString("\n" + AssistantHeaderStyle.Render("✦ IMMORTAL AGENT COMMANDS") + "\n\n")
	sb.WriteString("  " + KeyBadgeStyle.Render("/help") + "        " + SubtleStyle.Render("Show this help menu") + "\n")
	sb.WriteString("  " + KeyBadgeStyle.Render("/clear") + "       " + SubtleStyle.Render("Clear conversation history & context") + "\n")
	sb.WriteString("  " + KeyBadgeStyle.Render("/memories") + "    " + SubtleStyle.Render("List long-term user memories stored in DB") + "\n")
	sb.WriteString("  " + KeyBadgeStyle.Render("/tasks") + "       " + SubtleStyle.Render("List active background scheduled tasks") + "\n")
	sb.WriteString("  " + KeyBadgeStyle.Render("/status") + "      " + SubtleStyle.Render("Show system status & session stats") + "\n")
	sb.WriteString("  " + KeyBadgeStyle.Render("/exit") + "        " + SubtleStyle.Render("Exit the application") + "\n\n")
	sb.WriteString("  " + KeyBadgeStyle.Render("Alt+Enter") + "   " + SubtleStyle.Render("Insert line break in input box") + "\n")
	sb.WriteString("  " + KeyBadgeStyle.Render("Esc") + "          " + SubtleStyle.Render("Abort active task or tool execution") + "\n")
	sb.WriteString("  " + KeyBadgeStyle.Render("↑ / ↓") + "        " + SubtleStyle.Render("Navigate prompt history / multiline cursor") + "\n")
	sb.WriteString("  " + KeyBadgeStyle.Render("PgUp/Dn") + "      " + SubtleStyle.Render("Scroll conversation window") + "\n\n")
	return sb.String()
}

func formatUserMessage(text string, width int) string {
	header := UserHeaderStyle.Render("❯ YOU")
	wrapLimit := max(width-6, minMessageWidth)
	// if wrapLimit < minMessageWidth {
	// 	wrapLimit = minMessageWidth
	// }
	wrappedInput := wrapText(text, wrapLimit)
	lines := strings.Split(wrappedInput, "\n")
	var formatted strings.Builder
	formatted.WriteString("\n" + header + "\n")
	for _, line := range lines {
		formatted.WriteString(UserMsgStyle.Render(line) + "\n")
	}
	return formatted.String()
}

func formatAssistantMessage(text string, width int) string {
	header := AssistantHeaderStyle.Render("✦ ASSISTANT")
	rendered := renderToStringWithWidth(text, width-2)
	lines := strings.Split(rendered, "\n")
	var formatted strings.Builder
	formatted.WriteString("\n" + header + "\n")
	for _, line := range lines {
		formatted.WriteString(AssistantMsgStyle.Render(line) + "\n")
	}
	return formatted.String()
}

func formatToolLog(text string) string {
	cleanText := text
	cleanText = strings.TrimPrefix(cleanText, "🔧 ")
	cleanText = strings.TrimPrefix(cleanText, "✦ ")
	cleanText = strings.TrimSpace(cleanText)

	if strings.HasPrefix(cleanText, "[ERROR]") {
		return "\n" + ToolBoxStyle.BorderForeground(ColorRed).Render(
			ToolTagStyle.Foreground(ColorRed).Render("✕ ERROR")+" "+ErrorMsgStyle.Render(cleanText),
		) + "\n"
	}

	if strings.HasPrefix(cleanText, "TOOL:") {
		parts := strings.SplitN(cleanText, "|||", 3)
		if len(parts) == 3 {
			toolName := strings.TrimPrefix(parts[0], "TOOL:")
			summary := parts[1]
			details := parts[2]
			return formatStructuredToolLog(toolName, summary, details)
		}
	}

	return "\n" + ToolBoxStyle.Render(
		ToolTagStyle.Render("⚡ TOOL")+" "+ToolCallStyle.Render(cleanText),
	) + "\n"
}

func formatStructuredToolLog(toolName, summary, details string) string {
	icon := "⚡"
	toolColor := ColorTeal

	switch toolName {
	case "bash_tool":
		icon = "⟩"
		toolColor = ColorBlue
	case "web_search":
		icon = "🔍"
		toolColor = ColorCyan
	case "url_fetch":
		icon = "📄"
		toolColor = ColorCyan
	case "mail":
		icon = "✉"
		toolColor = ColorPeach
	case "memory_add", "memory_view", "memory_update", "memory_delete":
		icon = "🧠"
		toolColor = ColorLavender
	case "spawn_agents":
		icon = "⧉"
		toolColor = ColorGreen
	case "schedule_task", "local_schedule_task", "cancel_task", "local_cancel_task":
		icon = "⏰"
		toolColor = ColorYellow
	case "send_document_over_telegram", "send_image_over_telegram":
		icon = "📎"
		toolColor = ColorMauve
	case "list_scheduled_tasks", "local_list_scheduled_tasks":
		icon = "📋"
		toolColor = ColorYellow
	}

	nameStyle := lipgloss.NewStyle().Foreground(toolColor).Bold(true)
	summaryStyle := lipgloss.NewStyle().Foreground(ColorSubtext)

	var sb strings.Builder
	sb.WriteString("\n")
	sb.WriteString(ToolTagStyle.Render(icon) + " " + nameStyle.Render(toolName) + " " + summaryStyle.Render(summary) + "\n")

	if details != "" {
		for line := range strings.SplitSeq(details, "\n") {
			if strings.TrimSpace(line) == "" {
				continue
			}
			sb.WriteString("  " + SubtleStyle.Render(line) + "\n")
		}
	}

	return sb.String() + "\n"
}

func renderToStringWithWidth(text string, width int) string {
	if width < minMessageWidth {
		width = minMessageWidth
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
	return strings.Trim(out, " \n\t")
}

func wrapText(text string, limit int) string {
	if limit <= 0 {
		return text
	}
	lines := strings.Split(text, "\n")
	var wrappedLines []string

	for _, line := range lines {
		if lipgloss.Width(line) <= limit {
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
			nextWidth := lipgloss.Width(word)
			if currentLine != "" {
				nextWidth += lipgloss.Width(currentLine) + 1
			}
			if nextWidth > limit {
				if currentLine != "" {
					wrappedLines = append(wrappedLines, currentLine)
				}
				for lipgloss.Width(word) > limit {
					part, rest := splitByDisplayWidth(word, limit)
					wrappedLines = append(wrappedLines, part)
					word = rest
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

func splitByDisplayWidth(text string, limit int) (string, string) {
	if limit <= 0 {
		return "", text
	}
	width := 0
	for idx, r := range text {
		rw := lipgloss.Width(string(r))
		if width+rw > limit {
			if idx == 0 {
				return text[:idx+len(string(r))], text[idx+len(string(r)):]
			}
			return text[:idx], text[idx:]
		}
		width += rw
	}
	return text, ""
}

func extractRoleContent(param any) (string, string) {
	data, err := json.Marshal(param)
	if err != nil {
		return "", ""
	}
	var msg map[string]any
	if err := json.Unmarshal(data, &msg); err != nil {
		return "", ""
	}
	role, _ := msg["role"].(string)
	content := ""
	if c, ok := msg["content"].(string); ok {
		content = c
	}
	return role, content
}

func getTermSize() (int, int, error) {
	return term.GetSize(int(os.Stdin.Fd()))
}
