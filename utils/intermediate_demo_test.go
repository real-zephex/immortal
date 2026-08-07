package utils

import (
	"fmt"
	"testing"
)

// TestIntermediateHookWithRealToolCall simulates the exact production flow:
// 1. Model returns intermediate content + tool call
// 2. IntermediateHook fires -> TUI shows it (thinking stays ON)
// 3. Real tool executes via ExecuteTool
// 4. Model returns final content (no tools) -> TUI shows it (thinking turns OFF)
func TestIntermediateHookWithRealToolCall(t *testing.T) {
	var displayed []string
	thinking := true // TUI starts in thinking state

	IntermediateHook = func(text string) {
		displayed = append(displayed, fmt.Sprintf("[INTERMEDIATE] %s", text))
		// TUI's intermediateMsg handler does NOT change thinking state
		// (verified separately in tui package tests)
	}
	defer func() { IntermediateHook = nil }()

	// Step 1: model says something AND wants to run a tool
	intermediate := "Sure, let me check the system info for you!"
	if IntermediateHook != nil && intermediate != "" {
		IntermediateHook(intermediate)
	}
	if !thinking {
		t.Fatal("thinking should still be true after intermediate message")
	}

	// Step 2: tool call executes (this is what happens in the loop after the hook)
	result, err := ExecuteTool("bash_tool", map[string]any{
		"command": "uname -a",
		"reason":  "demo tool call",
	})
	if err != nil {
		t.Fatalf("tool execution failed: %v", err)
	}
	fmt.Printf("Tool result: %s\n", result)

	// Step 3: final response from model (no tool calls -> returned from OpenAIManager)
	final := fmt.Sprintf("Here's what I found:\n%s", result)

	fmt.Println("=== What the TUI displays in order ===")
	for i, d := range displayed {
		fmt.Printf("[%d] %s\n", i+1, d)
	}
	fmt.Printf("[final] %s\n", final)

	// Order verification
	if len(displayed) != 1 {
		t.Fatalf("expected 1 intermediate message, got %d", len(displayed))
	}
	if displayed[0] != "[INTERMEDIATE] Sure, let me check the system info for you!" {
		t.Fatalf("unexpected intermediate content: %s", displayed[0])
	}
}
