package tui

import (
	"context"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
)

func TestIntermediateMsgDoesNotClearThinking(t *testing.T) {
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

	updated, cmd := model.Update(intermediateMsg("Sure, let me check that for you!"))

	got := updated.(*tuiModel)
	if !got.thinking {
		t.Fatal("expected thinking to STAY true after intermediateMsg")
	}
	if got.pending != 1 {
		t.Fatalf("expected pending to stay 1, got %d", got.pending)
	}
	if cmd != nil {
		t.Fatalf("expected no cmd from intermediateMsg, got %v", cmd)
	}
	if !strings.Contains(got.renderContent(), "Sure, let me check that for you!") {
		t.Fatal("expected intermediate content to be displayed")
	}
}

func TestIntermediateThenFinalResponse(t *testing.T) {
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

	// Intermediate message keeps thinking on
	updated, _ := model.Update(intermediateMsg("Checking the filesystem..."))
	got := updated.(*tuiModel)
	if !got.thinking {
		t.Fatal("thinking should still be true after intermediate")
	}

	// Final response clears thinking
	updated, _ = got.Update(responseMsg("Done! Found 3 files."))
	got = updated.(*tuiModel)
	if got.thinking {
		t.Fatal("expected thinking to be false after final response")
	}
	if got.pending != 0 {
		t.Fatalf("expected pending 0, got %d", got.pending)
	}
	if !strings.Contains(got.renderContent(), "Done! Found 3 files.") {
		t.Fatal("expected final response to be displayed")
	}
}
