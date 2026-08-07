package utils

import "fmt"

var (
	PrintHook        func(string)
	StatusHook       func(string)
	DebugHook        func(string)
	IntermediateHook func(string) // Called when model returns content + tool calls
	ResponseHook     func(string) // Called with the final assistant reply
	WebHook          *WebServer    // Set to WebServer to broadcast to WebSocket clients
)

// DebugPrint routes debug output through DebugHook when set (TUI mode),
// otherwise falls back to stdout for non-TUI/Telegram use.
func DebugPrint(format string, args ...any) {
	if DebugHook != nil {
		DebugHook(fmt.Sprintf(format, args...))
	} else {
		fmt.Printf(format, args...)
	}
}
