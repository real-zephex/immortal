package tui

import (
	"context"
	"strings"
	"testing"

	"immortal/utils"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func TestIsTextInputKeyAllowsPrintableInput(t *testing.T) {
	tests := []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune("hello")},
		{Type: tea.KeyRunes, Runes: []rune("[];")},
		{Type: tea.KeyRunes, Runes: []rune("hello\nworld"), Paste: true},
		{Type: tea.KeySpace},
		{Type: tea.KeyBackspace},
		{Type: tea.KeyLeft},
		{Type: tea.KeyCtrlW},
	}

	for _, msg := range tests {
		if !isTextInputKey(msg) {
			t.Fatalf("expected %q to be allowed", msg.String())
		}
	}
}

func TestIsTextInputKeyRejectsControlAndMouseSequenceFragments(t *testing.T) {
	tests := []tea.KeyMsg{
		{Type: tea.KeyEsc},
		{Type: tea.KeyRunes, Runes: []rune("\x1b[<64;12;4M")},
		{Type: tea.KeyRunes, Runes: []rune("[<64;12;4M")},
		{Type: tea.KeyRunes, Runes: []rune("[M!!")},
		{Type: tea.KeyRunes, Runes: []rune("x"), Alt: true},
		{Type: tea.KeyRunes, Runes: []rune("\x1b[<64;12;4M"), Paste: true},
	}

	for _, msg := range tests {
		if isTextInputKey(msg) {
			t.Fatalf("expected %q to be rejected", msg.String())
		}
	}
}

func TestCommandInputIsNotSentToAgent(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events := make(chan utils.Event, 1)
	model := testModel(ctx, cancel)
	model.eventsCh = events
	model.textinput.SetValue("/help")

	updated, cmd := model.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("expected /help to be handled synchronously")
	}

	got := updated.(tuiModel)
	if got.pending != 0 || got.thinking {
		t.Fatalf("expected no pending agent work, got pending=%d thinking=%v", got.pending, got.thinking)
	}
	if len(events) != 0 {
		t.Fatal("expected command input not to be sent to agent")
	}
	if !strings.Contains(got.renderContent(), "/clear") {
		t.Fatal("expected help content to be rendered")
	}
}

func TestPendingStateTracksMultipleResponses(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	model := testModel(ctx, cancel)
	model.pending = 2
	model.thinking = true
	model.statusText = pendingStatus(model.pending)

	updated, _ := model.Update(responseMsg("first"))
	got := updated.(tuiModel)
	if !got.thinking || got.pending != 1 || got.statusText != "Processing..." {
		t.Fatalf("expected one pending response, got pending=%d thinking=%v status=%q", got.pending, got.thinking, got.statusText)
	}

	updated, _ = got.Update(responseMsg("second"))
	got = updated.(tuiModel)
	if got.thinking || got.pending != 0 || got.statusText != "" {
		t.Fatalf("expected pending state to clear, got pending=%d thinking=%v status=%q", got.pending, got.thinking, got.statusText)
	}
}

func TestSendUserMessageReportsUnavailableChannel(t *testing.T) {
	msg := sendUserMessage(context.Background(), nil, "hello")()
	if _, ok := msg.(sendErrorMsg); !ok {
		t.Fatalf("expected sendErrorMsg, got %T", msg)
	}
}

func TestWrapTextUsesDisplayWidth(t *testing.T) {
	wrapped := wrapText("hello 世界 hello", 8)
	for _, line := range strings.Split(wrapped, "\n") {
		if width := lipgloss.Width(line); width > 8 {
			t.Fatalf("line %q width=%d exceeds limit", line, width)
		}
	}
}

func TestAddHistoryDeduplicatesAndCapsEntries(t *testing.T) {
	model := tuiModel{}
	model.addHistory("same")
	model.addHistory("same")
	if len(model.history) != 1 {
		t.Fatalf("expected duplicate consecutive history entry to collapse, got %d", len(model.history))
	}

	for i := 0; i < maxHistoryEntries+10; i++ {
		model.addHistory(string(rune('a' + i%26)))
	}
	if len(model.history) > maxHistoryEntries {
		t.Fatalf("expected history capped at %d, got %d", maxHistoryEntries, len(model.history))
	}
	if model.historyIdx != len(model.history) {
		t.Fatalf("expected history index at end, got idx=%d len=%d", model.historyIdx, len(model.history))
	}
}

func testModel(ctx context.Context, cancel context.CancelFunc) tuiModel {
	ti := textinput.New()
	vp := viewport.New(80, 20)
	return tuiModel{
		ctx:       ctx,
		cancel:    cancel,
		textinput: ti,
		viewport:  vp,
		width:     80,
		height:    24,
	}
}
