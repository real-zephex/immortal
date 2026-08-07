package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
)

// TestFullToolFlow simulates what the TUI sees during a real tool-calling turn:
// 1. intermediateMsg  - model says "Sure, let me check..." + tool call
// 2. logMsg           - tool execution log
// 3. responseMsg      - final response (thinking turns off)
func TestFullToolFlow(t *testing.T) {
	modelName := "test-model"
	model := &tuiModel{
		ctx:      context.Background(),
		textarea: textarea.New(),
		viewport: viewport.New(80, 20),
		width:    80,
		height:   24,
		pending:  1,
		thinking: true,
		model:    &modelName,
	}

	// 1. Intermediate content
	updated, _ := model.Update(intermediateMsg("Sure, let me check the system info!"))
	got := updated.(*tuiModel)
	if !got.thinking {
		t.Fatal("FAIL: thinking turned OFF on intermediate message (should stay ON)")
	}
	t.Log("PASS: intermediate msg shown, thinking still ON")

	// 2. Tool log
	updated, _ = got.Update(logMsg("TOOL:bash_tool|||bash: uname -a|||Command: uname -a"))
	got = updated.(*tuiModel)
	if !got.thinking {
		t.Fatal("FAIL: thinking turned OFF on tool log (should stay ON)")
	}
	t.Log("PASS: tool log shown, thinking still ON")

	// 3. Final response
	updated, _ = got.Update(responseMsg("Here's what I found: Linux fedora..."))
	got = updated.(*tuiModel)
	if got.thinking {
		t.Fatal("FAIL: thinking still ON after final response (should turn OFF)")
	}
	if got.pending != 0 {
		t.Fatalf("FAIL: pending should be 0, got %d", got.pending)
	}
	t.Log("PASS: final response shown, thinking OFF, pending 0")

	// Verify all three blocks are visible in order
	content := got.renderContent()
	intermediateIdx := strings.Index(content, "Sure, let me check the system info!")
	toolIdx := strings.Index(content, "uname -a")
	finalIdx := strings.Index(content, "Here's what I found")
	if intermediateIdx == -1 || toolIdx == -1 || finalIdx == -1 {
		t.Fatalf("FAIL: missing blocks. intermediate=%d tool=%d final=%d", intermediateIdx, toolIdx, finalIdx)
	}
	if !(intermediateIdx < toolIdx && toolIdx < finalIdx) {
		t.Fatal("FAIL: blocks out of order")
	}
	t.Log("PASS: all blocks displayed in correct order")
}
