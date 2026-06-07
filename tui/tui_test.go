package tui

import (
	"testing"

	tea "github.com/charmbracelet/bubbletea"
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
