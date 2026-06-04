package utils

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/openai/openai-go/v3"
)

const maxOutputSize = 100 * 1024

func ExecuteTool(toolName string, args map[string]any) (string, error) {
	fmt.Printf(
		"Executing tool: %s with args: %v\n", toolName, args,
	)
	switch toolName {
	case "bash_tool":
		return executeBash(args)
	case "spawn_agents":
		return executeSpawnAgents(args)
	case "send_document_over_telegram":
		return executeSendDocument(args)
	case "send_image_over_telegram":
		return executeSendImage(args)
	default:
		return "", fmt.Errorf("unknown tool: %s", toolName)
	}
}

func executeBash(args map[string]any) (string, error) {
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
}

func executeSpawnAgents(args map[string]any) (string, error) {
	subAgentsRaw, ok := args["sub_agents"].([]any)
	if !ok {
		return "", fmt.Errorf("[ERROR] sub_agents must be an array")
	}

	type subAgentTask struct {
		ID   int
		Task string
	}

	var tasks []subAgentTask
	for _, raw := range subAgentsRaw {
		agent, ok := raw.(map[string]any)
		if !ok {
			return "", fmt.Errorf("[ERROR] each sub_agent must be an object")
		}
		idFloat, ok := agent["id"].(float64)
		if !ok {
			return "", fmt.Errorf("[ERROR] each sub_agent must have a numeric id")
		}
		task, ok := agent["task"].(string)
		if !ok {
			return "", fmt.Errorf("[ERROR] each sub_agent must have a string task")
		}
		tasks = append(tasks, subAgentTask{ID: int(idFloat), Task: task})
	}

	if len(tasks) == 0 {
		return "", fmt.Errorf("[ERROR] must provide at least one sub-agent")
	}

	results := make(map[int]string)
	var mu sync.Mutex
	var wg sync.WaitGroup

	fmt.Printf("[spawn_agents] launching %d sub-agents\n", len(tasks))

	for _, t := range tasks {
		wg.Add(1)
		go func(t subAgentTask) {
			defer wg.Done()

			fmt.Printf("[spawn_agents] agent %d starting: %s\n", t.ID, t.Task)

			subMessages := []openai.ChatCompletionMessageParamUnion{
				openai.SystemMessage(SubAgentSystemPrompt),
				openai.UserMessage(t.Task),
			}

			output := openAIManagerWithTools(context.Background(), &subMessages, subAgentTools)

			mu.Lock()
			if output == "" {
				results[t.ID] = fmt.Sprintf("[Agent %d FAILED]: sub-agent returned empty response", t.ID)
				fmt.Printf("[spawn_agents] agent %d finished: FAILED (empty response)\n", t.ID)
			} else {
				results[t.ID] = fmt.Sprintf("[Agent %d]: %s", t.ID, output)
				fmt.Printf("[spawn_agents] agent %d finished: OK (%d chars)\n", t.ID, len(output))
			}
			mu.Unlock()
		}(t)
	}

	wg.Wait()

	fmt.Printf("[spawn_agents] all %d sub-agents completed\n", len(tasks))

	out := make([]string, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, results[t.ID])
	}
	return strings.Join(out, "\n"), nil
}

func executeSendDocument(args map[string]any) (string, error) {
	filepath, ok := args["filepath"].(string)
	if !ok || strings.TrimSpace(filepath) == "" {
		return "", fmt.Errorf("filepath must be a non-empty string")
	}

	if CurrentTelegramChatID <= 0 {
		return "", fmt.Errorf("telegram chat id is not set - this tool only works when communicating over Telegram")
	}

	if err := sendDocument(CurrentTelegramChatID, filepath); err != nil {
		return "", fmt.Errorf("failed to send document: %v", err)
	}

	return fmt.Sprintf("Successfully sent document to Telegram: %s", filepath), nil
}

func executeSendImage(args map[string]any) (string, error) {
	filepath, ok := args["filepath"].(string)
	if !ok || strings.TrimSpace(filepath) == "" {
		return "", fmt.Errorf("filepath must be a non-empty string")
	}

	if CurrentTelegramChatID <= 0 {
		return "", fmt.Errorf("telegram chat id is not set - this tool only works when communicating over Telegram")
	}

	if err := sendImage(CurrentTelegramChatID, filepath); err != nil {
		return "", fmt.Errorf("failed to send image: %v", err)
	}

	return fmt.Sprintf("Successfully sent image to Telegram: %s", filepath), nil
}
