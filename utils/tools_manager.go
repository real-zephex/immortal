package utils

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"net/http"
	"net/url"

	"github.com/openai/openai-go/v3"
)

const (
	maxOutputSize = 100 * 1024
	WEB_BASE      = "https://s.jina.ai"
	URL_BASE      = "https://r.jina.ai"
)

var httpClient = &http.Client{
	Timeout: 30 * time.Second,
}

func ExecuteTool(toolName string, args map[string]any) (string, error) {
	DebugPrint(
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
	case "web_search":
		return webSearch(args)
	case "url_fetch":
		return urlSearch(args)
	case "mail":
		return executeMailHandler(args)
	case "schedule_task":
		return executeScheduleTask(args)
	case "cancel_task":
		return executeCancelTask(args)
	case "list_scheduled_tasks":
		return executeListTasks(args)
	case "local_schedule_task":
		return executeLocalScheduleTask(args)
	case "local_cancel_task":
		return executeLocalCancelTask(args)
	case "local_list_scheduled_tasks":
		return executeLocalListTasks(args)
	default:
		return "", fmt.Errorf("unknown tool: %s", toolName)
	}
}

func getJinaKey() (string, error) {
	key, exists := os.LookupEnv("JINA_API_KEY")
	if !exists {
		return "", fmt.Errorf("[ERROR] JINA_API_KEY not set")
	}
	return key, nil
}

func webSearch(args map[string]any) (string, error) {
	topic, _ := args["topic"].(string)
	if topic == "" {
		return "", fmt.Errorf("[ERROR] Please provide a topic for the web search")
	}

	TOKEN, err := getJinaKey()
	if err != nil {
		return "", fmt.Errorf("[ERROR] Failed to get Jina API key: %w", err)
	}

	parseUrl, err := url.Parse(WEB_BASE)
	if err != nil {
		return "", fmt.Errorf("[ERROR] Failed to parse web base URL: %w", err)
	}

	query := parseUrl.Query()
	query.Set("q", topic)
	parseUrl.RawQuery = query.Encode()

	req, err := http.NewRequest("GET", parseUrl.String(), nil)
	if err != nil {
		return "", fmt.Errorf("[ERROR] Failed to create request: %w", err)
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", TOKEN))

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("[ERROR] Failed to execute web search: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("[ERROR] Failed to read response body: %w", err)
	}

	return string(body), nil
}

func urlSearch(args map[string]any) (string, error) {
	url, _ := args["url"].(string)
	if url == "" {
		return "", fmt.Errorf("[ERROR] Please provide a URL for the search")
	}

	TOKEN, err := getJinaKey()
	if err != nil {
		return "", fmt.Errorf("[ERROR] Failed to get Jina API key: %w", err)
	}

	finalUrl := fmt.Sprintf("%s/%s", URL_BASE, url)

	req, err := http.NewRequest("GET", finalUrl, nil)
	if err != nil {
		return "", fmt.Errorf("[ERROR] Failed to create request: %w", err)
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", TOKEN))
	req.Header.Set("X-Retain-Images", "none")
	req.Header.Set("X-Return-Format", "text")

	resp, err := httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("[ERROR] Failed to execute URL search: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("[ERROR] Failed to read response body: %w", err)
	}

	return string(body), nil
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

	DebugPrint("[spawn_agents] launching %d sub-agents\n", len(tasks))

	for _, t := range tasks {
		wg.Add(1)
		go func(t subAgentTask) {
			defer wg.Done()

			DebugPrint("[spawn_agents] agent %d starting: %s\n", t.ID, t.Task)

			subMessages := []openai.ChatCompletionMessageParamUnion{
				openai.SystemMessage(SubAgentSystemPrompt),
				openai.UserMessage(t.Task),
			}

			output := openAIManagerWithTools(context.Background(), &subMessages, subAgentTools)

			mu.Lock()
			if output == "" {
				results[t.ID] = fmt.Sprintf("[Agent %d FAILED]: sub-agent returned empty response", t.ID)
				DebugPrint("[spawn_agents] agent %d finished: FAILED (empty response)\n", t.ID)
			} else {
				results[t.ID] = fmt.Sprintf("[Agent %d]: %s", t.ID, output)
				DebugPrint("[spawn_agents] agent %d finished: OK (%d chars)\n", t.ID, len(output))
			}
			mu.Unlock()
		}(t)
	}

	wg.Wait()

	DebugPrint("[spawn_agents] all %d sub-agents completed\n", len(tasks))

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

func executeMailHandler(args map[string]any) (string, error) {
	req, err := parseMailRequest(args)
	if err != nil {
		return "", err
	}

	res := executeMail(req)
	response := res.toToolResponse()

	data, err := json.Marshal(response)
	if err != nil {
		return "", fmt.Errorf("failed to marshal mail response: %v", err)
	}

	return string(data), nil
}

func executeScheduleTask(args map[string]any) (string, error) {
	task, _ := args["task"].(string)
	intervalStr, _ := args["interval"].(string)
	repeat, _ := args["repeat"].(bool)

	if task == "" || intervalStr == "" {
		return "", fmt.Errorf("task and interval are required")
	}

	interval, err := parseInterval(intervalStr)
	if err != nil {
		return "", fmt.Errorf("invalid interval '%s': %v", intervalStr, err)
	}

	channelKey := TelegramChannel(CurrentTelegramChatID)
	id := AddTask(channelKey, CurrentTelegramChatID, task, interval, repeat)
	if id == "" {
		return "", fmt.Errorf("scheduler not initialized")
	}

	repeatStr := "one-shot"
	if repeat {
		repeatStr = "repeating every " + interval.String()
	}

	return fmt.Sprintf("Task scheduled [%s]: %s (%s)", id, task, repeatStr), nil
}

func executeCancelTask(args map[string]any) (string, error) {
	taskID, _ := args["task_id"].(string)
	if taskID == "" {
		return "", fmt.Errorf("task_id is required")
	}

	if err := CancelTask(taskID); err != nil {
		return "", err
	}

	return fmt.Sprintf("Task %s cancelled.", taskID), nil
}

func executeListTasks(_ map[string]any) (string, error) {
	channelKey := TelegramChannel(CurrentTelegramChatID)
	tasks := ListTasks(channelKey)
	return formatTaskList(tasks), nil
}

func executeLocalScheduleTask(args map[string]any) (string, error) {
	task, _ := args["task"].(string)
	intervalStr, _ := args["interval"].(string)
	repeat, _ := args["repeat"].(bool)

	if task == "" || intervalStr == "" {
		return "", fmt.Errorf("task and interval are required")
	}

	interval, err := parseInterval(intervalStr)
	if err != nil {
		return "", fmt.Errorf("invalid interval '%s': %v", intervalStr, err)
	}

	id := AddLocalTask(task, interval, repeat)
	if id == "" {
		return "", fmt.Errorf("local scheduler not initialized")
	}

	repeatStr := "one-shot"
	if repeat {
		repeatStr = "repeating every " + interval.String()
	}

	return fmt.Sprintf("Task scheduled [%s]: %s (%s)", id, task, repeatStr), nil
}

func executeLocalCancelTask(args map[string]any) (string, error) {
	taskID, _ := args["task_id"].(string)
	if taskID == "" {
		return "", fmt.Errorf("task_id is required")
	}

	if err := CancelLocalTask(taskID); err != nil {
		return "", err
	}

	return fmt.Sprintf("Task %s cancelled.", taskID), nil
}

func executeLocalListTasks(_ map[string]any) (string, error) {
	tasks := ListLocalTasks()
	return formatLocalTaskList(tasks), nil
}
