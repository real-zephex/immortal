package utils

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"time"
)

const maxOutputSize = 100 * 1024

func ExecuteTool(toolName string, args map[string]any) (string, error) {
	fmt.Printf(
		"Executing tool: %s with args: %v\n", toolName, args,
	)
	switch toolName {
	case "bash_tool":
		command, _ := args["command"].(string)
		reason, _ := args["reason"].(string)

		if reason == "" || command == "" {
			return "", fmt.Errorf("[ERROR] Please make sure you are passing both the reason and command for this tool")
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		cmd := exec.CommandContext(ctx, "bash", "-c", command)

		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		err := cmd.Run()

		output := stdout.String()
		if stderr.Len() > 0 {
			if output != "" {
				output += "\n"
			}
			output += "--- stderr ---\n" + stderr.String()
		}

		if err != nil {
			if output != "" {
				output += "\n"
			}
			output += fmt.Sprintf("--- error: %v", err)
		}

		if len(output) > maxOutputSize {
			output = output[:maxOutputSize] + "\n... (truncated)"
		}

		return output, nil
	default:
		return "", fmt.Errorf("unknown tool: %s", toolName)
	}
}
