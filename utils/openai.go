package utils

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

var openaiClient openai.Client
var currentModel string

const SystemPrompt = `You are an autonomous agent operating in a persistent Go runtime. You have access to the following tools and capabilities.

Core tools available across all contexts:
- bash_tool: Run bash commands (30s timeout, 100KB output cap). Include a reason for every command.
- spawn_agents: Spawn multiple sub-agents to work on sub-tasks in parallel. Each sub-agent gets a fresh conversation with bash, web search, and URL fetch tools. Responses from all sub-agents are returned together.
- web_search: Search the web for information on a given topic (backed by Jina).
- url_fetch: Fetch and read the content of a given URL (backed by Jina Reader).
- mail: Manage AgentMail inbox — list threads, fetch a thread, send email, reply to a message, forward a message, or delete a thread.
- memory_add: Store a fact or preference you learn about the user for long-term recall across conversations. Use this when the user tells you something you should remember. The tool automatically deduplicates by content — use memory_update when your understanding evolves.
- memory_view: List all stored memories. Use this to recall what you know about the user and their preferences.
- memory_update: Update an existing memory with new content. Use this when the user corrects a stored fact or your understanding of their preferences changes.
- memory_delete: Remove a memory by its ID. Use this when a stored fact is no longer relevant or the user asks you to forget something.

Telegram-specific tools (only available when communicating over Telegram):
- schedule_task: Schedule a one-shot or repeating task. The task will fire after the specified interval (e.g. "10m", "1h", "daily", "hourly", "weekly") and be presented to you as a new user message prefixed with "⏰". For non-repeating tasks, supply repeat: false; for repeating, supply repeat: true.
- cancel_task: Cancel a previously scheduled task by its task ID.
- list_scheduled_tasks: List all currently scheduled tasks for this chat.
- send_document_over_telegram: Send a file as a document back to the Telegram chat.
- send_image_over_telegram: Send an image file back to the Telegram chat.

Behavior and constraints:
- Your conversation history is persisted in SQLite and restored on restart. Treat it as continuous across sessions.
- When a scheduled task fires, you see it as "⏰ <task description>". Process it like any user request, but the response and tool calls from scheduled runs are ephemeral — they are not saved to conversation history.
- When replying, your response goes back to the user through the same channel (file output or Telegram chat).
- Files you create are written to the working directory on the server.
- You can create files with bash (e.g. echo, cat, python) and then send them over Telegram if requested.
- You do not have internet access except through web_search and url_fetch tools.
- Keep responses concise and direct. Use sub-agents for parallelizable work.`

const SubAgentSystemPrompt = `You are a sub-agent focused on completing the assigned task.
You have access to bash tools. Respond concisely with only your findings.
Do not ask follow-up questions or request additional tasks.`

func GetAPIKey() (string, error) {
	key, exists := os.LookupEnv("IMMORTAL_API_KEY")
	if !exists {
		return "", fmt.Errorf("IMMORTAL_API_KEY environment variable not set")
	}
	return key, nil
}

func InitOpenAIClient(baseURL, model string) error {
	key, err := GetAPIKey()
	if err != nil {
		return err
	}
	client := openai.NewClient(
		option.WithAPIKey(key),
		option.WithBaseURL(baseURL),
	)
	openaiClient = client
	currentModel = model
	return nil
}

var (
	bashTool = openai.ChatCompletionToolUnionParam{
		OfFunction: &openai.ChatCompletionFunctionToolParam{
			Function: openai.FunctionDefinitionParam{
				Name:        "bash_tool",
				Description: openai.String("Run bash commands using this tool"),
				Parameters: openai.FunctionParameters{
					"type": "object",
					"properties": map[string]any{
						"command": map[string]any{
							"type":        "string",
							"description": "The bash command to run",
						},
						"reason": map[string]any{
							"type":        "string",
							"description": "The reason for running the bash command",
						},
					},
					"required": []string{"command", "reason"},
				},
			},
		},
	}

	spawnAgentsTool = openai.ChatCompletionToolUnionParam{
		OfFunction: &openai.ChatCompletionFunctionToolParam{
			Function: openai.FunctionDefinitionParam{
				Name:        "spawn_agents",
				Description: openai.String("Spawn multiple sub-agents to work on sub-tasks in parallel"),
				Parameters: openai.FunctionParameters{
					"type": "object",
					"properties": map[string]any{
						"sub_agents": map[string]any{
							"type": "array",
							"items": map[string]any{
								"type": "object",
								"properties": map[string]any{
									"id": map[string]any{
										"type":        "integer",
										"description": "Unique identifier for this sub-agent",
									},
									"task": map[string]any{
										"type":        "string",
										"description": "The task for this sub-agent to complete",
									},
								},
								"required": []string{"id", "task"},
							},
						},
						"reason": map[string]any{
							"type":        "string",
							"description": "Why you are spawning sub-agents",
						},
					},
					"required": []string{"sub_agents", "reason"},
				},
			},
		},
	}

	webSearchTool = openai.ChatCompletionToolUnionParam{
		OfFunction: &openai.ChatCompletionFunctionToolParam{
			Function: openai.FunctionDefinitionParam{
				Name:        "web_search",
				Description: openai.String("Search the web for information on a given topic"),
				Parameters: openai.FunctionParameters{
					"type": "object",
					"properties": map[string]any{
						"topic": map[string]any{
							"type":        "string",
							"description": "The topic to search for",
						},
					},
					"required": []string{"topic"},
				},
			},
		},
	}

	urlFetchTool = openai.ChatCompletionToolUnionParam{
		OfFunction: &openai.ChatCompletionFunctionToolParam{
			Function: openai.FunctionDefinitionParam{
				Name:        "url_fetch",
				Description: openai.String("Fetch the content of a given URL"),
				Parameters: openai.FunctionParameters{
					"type": "object",
					"properties": map[string]any{
						"url": map[string]any{
							"type":        "string",
							"description": "The URL to fetch",
						},
					},
					"required": []string{"url"},
				},
			},
		},
	}

	mailTool = openai.ChatCompletionToolUnionParam{
		OfFunction: &openai.ChatCompletionFunctionToolParam{
			Function: openai.FunctionDefinitionParam{
				Name:        "mail",
				Description: openai.String("Manage AgentMail inbox threads and messages: list threads, fetch a thread, send, reply, forward, or delete a thread."),
				Parameters: openai.FunctionParameters{
					"type": "object",
					"properties": map[string]any{
						"action": map[string]any{
							"type":        "string",
							"description": "Action to perform: get_threads, get_thread, send_email, reply_to_message, forward_message, delete_thread",
							"enum":        []string{"get_threads", "get_thread", "send_email", "reply_to_message", "forward_message", "delete_thread"},
						},
						"thread_id": map[string]any{
							"type":        "string",
							"description": "Thread ID for get_thread or delete_thread",
						},
						"message_id": map[string]any{
							"type":        "string",
							"description": "Message ID for reply_to_message or forward_message",
						},
						"to": map[string]any{
							"type":        "string",
							"description": "Recipient email address",
						},
						"subject": map[string]any{
							"type":        "string",
							"description": "Subject for send_email",
						},
						"text": map[string]any{
							"type":        "string",
							"description": "Plain text body",
						},
						"html": map[string]any{
							"type":        "string",
							"description": "HTML body. You must always include this whenever sending an email. Without this, email clients won't be able to see the contents of the email.",
						},
						"reply_to": map[string]any{
							"type":        "string",
							"description": "Reply-to message id for reply_to_message",
						},
					},
					"required": []string{"action"},
				},
			},
		},
	}

	sendDocumentTool = openai.ChatCompletionToolUnionParam{
		OfFunction: &openai.ChatCompletionFunctionToolParam{
			Function: openai.FunctionDefinitionParam{
				Name:        "send_document_over_telegram",
				Description: openai.String("Send a file as a document to the Telegram chat. Use this when the user needs a file delivered over Telegram."),
				Parameters: openai.FunctionParameters{
					"type": "object",
					"properties": map[string]any{
						"filepath": map[string]any{
							"type":        "string",
							"description": "Complete file path of the file to send",
						},
					},
					"required": []string{"filepath"},
				},
			},
		},
	}

	sendImageTool = openai.ChatCompletionToolUnionParam{
		OfFunction: &openai.ChatCompletionFunctionToolParam{
			Function: openai.FunctionDefinitionParam{
				Name:        "send_image_over_telegram",
				Description: openai.String("Send an image file to the Telegram chat. Use this when the user needs an image delivered over Telegram."),
				Parameters: openai.FunctionParameters{
					"type": "object",
					"properties": map[string]any{
						"filepath": map[string]any{
							"type":        "string",
							"description": "Complete file path of the image to send",
						},
					},
					"required": []string{"filepath"},
				},
			},
		},
	}

	scheduleTaskTool = openai.ChatCompletionToolUnionParam{
		OfFunction: &openai.ChatCompletionFunctionToolParam{
			Function: openai.FunctionDefinitionParam{
				Name:        "schedule_task",
				Description: openai.String("Schedule a task to run after a given interval. The task will be executed and the result sent to this chat."),
				Parameters: openai.FunctionParameters{
					"type": "object",
					"properties": map[string]any{
						"task": map[string]any{
							"type":        "string",
							"description": "The task description that will be executed when the timer fires",
						},
						"interval": map[string]any{
							"type":        "string",
							"description": "Time interval (e.g. '10m', '1h', 'daily', 'hourly', 'weekly')",
						},
						"repeat": map[string]any{
							"type":        "boolean",
							"description": "Whether the task should repeat after each interval",
						},
						"reason": map[string]any{
							"type":        "string",
							"description": "Why you are scheduling this task",
						},
					},
					"required": []string{"task", "interval", "repeat"},
				},
			},
		},
	}

	cancelTaskTool = openai.ChatCompletionToolUnionParam{
		OfFunction: &openai.ChatCompletionFunctionToolParam{
			Function: openai.FunctionDefinitionParam{
				Name:        "cancel_task",
				Description: openai.String("Cancel a previously scheduled task by its task ID."),
				Parameters: openai.FunctionParameters{
					"type": "object",
					"properties": map[string]any{
						"task_id": map[string]any{
							"type":        "string",
							"description": "The ID of the task to cancel (e.g. 'task_1')",
						},
					},
					"required": []string{"task_id"},
				},
			},
		},
	}

	listTasksTool = openai.ChatCompletionToolUnionParam{
		OfFunction: &openai.ChatCompletionFunctionToolParam{
			Function: openai.FunctionDefinitionParam{
				Name:        "list_scheduled_tasks",
				Description: openai.String("List all currently scheduled tasks for this chat."),
				Parameters: openai.FunctionParameters{
					"type": "object",
					"properties": map[string]any{
						"reason": map[string]any{
							"type":        "string",
							"description": "Why you are listing tasks",
						},
					},
					"required": []string{},
				},
			},
		},
	}

	localScheduleTaskTool = openai.ChatCompletionToolUnionParam{
		OfFunction: &openai.ChatCompletionFunctionToolParam{
			Function: openai.FunctionDefinitionParam{
				Name:        "local_schedule_task",
				Description: openai.String("Schedule a task to run after a given interval. The task will run in the background and the result will appear as a new user message. Use this for one-shot reminders or recurring checks."),
				Parameters: openai.FunctionParameters{
					"type": "object",
					"properties": map[string]any{
						"task": map[string]any{
							"type":        "string",
							"description": "The task description that will be executed when the timer fires",
						},
						"interval": map[string]any{
							"type":        "string",
							"description": "Time interval (e.g. '10m', '1h', 'daily', 'hourly', 'weekly')",
						},
						"repeat": map[string]any{
							"type":        "boolean",
							"description": "Whether the task should repeat after each interval",
						},
						"reason": map[string]any{
							"type":        "string",
							"description": "Why you are scheduling this task",
						},
					},
					"required": []string{"task", "interval", "repeat"},
				},
			},
		},
	}

	localCancelTaskTool = openai.ChatCompletionToolUnionParam{
		OfFunction: &openai.ChatCompletionFunctionToolParam{
			Function: openai.FunctionDefinitionParam{
				Name:        "local_cancel_task",
				Description: openai.String("Cancel a previously scheduled local task by its task ID."),
				Parameters: openai.FunctionParameters{
					"type": "object",
					"properties": map[string]any{
						"task_id": map[string]any{
							"type":        "string",
							"description": "The ID of the task to cancel (e.g. 'task_1')",
						},
					},
					"required": []string{"task_id"},
				},
			},
		},
	}

	localListTasksTool = openai.ChatCompletionToolUnionParam{
		OfFunction: &openai.ChatCompletionFunctionToolParam{
			Function: openai.FunctionDefinitionParam{
				Name:        "local_list_scheduled_tasks",
				Description: openai.String("List all currently scheduled local tasks."),
				Parameters: openai.FunctionParameters{
					"type": "object",
					"properties": map[string]any{
						"reason": map[string]any{
							"type":        "string",
							"description": "Why you are listing tasks",
						},
					},
					"required": []string{},
				},
			},
		},
	}

	memoryAddTool = openai.ChatCompletionToolUnionParam{
		OfFunction: &openai.ChatCompletionFunctionToolParam{
			Function: openai.FunctionDefinitionParam{
				Name:        "memory_add",
				Description: openai.String("Store a fact or preference about the user for long-term recall across conversations."),
				Parameters: openai.FunctionParameters{
					"type": "object",
					"properties": map[string]any{
						"content": map[string]any{
							"type":        "string",
							"description": "The fact or preference to remember",
						},
					},
					"required": []string{"content"},
				},
			},
		},
	}

	memoryViewTool = openai.ChatCompletionToolUnionParam{
		OfFunction: &openai.ChatCompletionFunctionToolParam{
			Function: openai.FunctionDefinitionParam{
				Name:        "memory_view",
				Description: openai.String("List all stored memories to recall what you know about the user."),
				Parameters: openai.FunctionParameters{
					"type":       "object",
					"properties": map[string]any{},
					"required":   []string{},
				},
			},
		},
	}

	memoryUpdateTool = openai.ChatCompletionToolUnionParam{
		OfFunction: &openai.ChatCompletionFunctionToolParam{
			Function: openai.FunctionDefinitionParam{
				Name:        "memory_update",
				Description: openai.String("Update an existing memory with new content. Use when the user corrects a stored fact."),
				Parameters: openai.FunctionParameters{
					"type": "object",
					"properties": map[string]any{
						"memory_id": map[string]any{
							"type":        "string",
							"description": "The ID of the memory to update",
						},
						"content": map[string]any{
							"type":        "string",
							"description": "The new content for the memory",
						},
					},
					"required": []string{"memory_id", "content"},
				},
			},
		},
	}

	memoryDeleteTool = openai.ChatCompletionToolUnionParam{
		OfFunction: &openai.ChatCompletionFunctionToolParam{
			Function: openai.FunctionDefinitionParam{
				Name:        "memory_delete",
				Description: openai.String("Delete a stored memory by its ID. Use when a fact is no longer relevant."),
				Parameters: openai.FunctionParameters{
					"type": "object",
					"properties": map[string]any{
						"memory_id": map[string]any{
							"type":        "string",
							"description": "The ID of the memory to delete",
						},
					},
					"required": []string{"memory_id"},
				},
			},
		},
	}

	orchestratorTools = []openai.ChatCompletionToolUnionParam{bashTool, spawnAgentsTool, webSearchTool, urlFetchTool, mailTool, localScheduleTaskTool, localCancelTaskTool, localListTasksTool, memoryAddTool, memoryViewTool, memoryUpdateTool, memoryDeleteTool}
	subAgentTools     = []openai.ChatCompletionToolUnionParam{bashTool, webSearchTool, urlFetchTool}
	TelegramTools     = []openai.ChatCompletionToolUnionParam{bashTool, spawnAgentsTool, sendDocumentTool, sendImageTool, webSearchTool, urlFetchTool, mailTool, scheduleTaskTool, cancelTaskTool, listTasksTool, memoryAddTool, memoryViewTool, memoryUpdateTool, memoryDeleteTool}
)

func OpenAIManager(ctx context.Context, localMessages *[]openai.ChatCompletionMessageParamUnion) string {
	return openAIManagerWithTools(ctx, localMessages, orchestratorTools)
}

func OpenAIManagerWithTools(ctx context.Context, localMessages *[]openai.ChatCompletionMessageParamUnion, tools []openai.ChatCompletionToolUnionParam) string {
	return openAIManagerWithTools(ctx, localMessages, tools)
}

func openAIManagerWithTools(ctx context.Context, localMessages *[]openai.ChatCompletionMessageParamUnion, tools []openai.ChatCompletionToolUnionParam) string {
	maxToolIterations := 40

	for range maxToolIterations {
		chatCompletion, err := openaiClient.Chat.Completions.New(
			ctx,
			openai.ChatCompletionNewParams{
				Messages:          *localMessages,
				Model:             currentModel,
				Tools:             tools,
				ParallelToolCalls: openai.Bool(true),
			},
		)
		if err != nil {
			if PrintHook != nil {
				PrintHook(fmt.Sprintf("[ERROR] %v\n", err))
			} else {
				DebugPrint("[ERROR] %v\n", err)
			}
			return ""
		}

		msg := chatCompletion.Choices[0].Message.ToParam()
		*localMessages = append(*localMessages, msg)

		toolCalls := chatCompletion.Choices[0].Message.ToolCalls

		if len(toolCalls) == 0 {
			return chatCompletion.Choices[0].Message.Content
		}

		DebugPrint("===Tool Calls===\n")
		for _, tool := range toolCalls {
			var toolArguments map[string]any

			err := json.Unmarshal([]byte(tool.Function.Arguments), &toolArguments)
			if err != nil {
				if PrintHook != nil {
					PrintHook(fmt.Sprintf("[ERROR] parsing arguments: %v\n", err))
				} else {
					DebugPrint("[ERROR] parsing arguments: %v\n", err)
				}
				continue
			}

			if StatusHook != nil {
				StatusHook(fmt.Sprintf("⚡ %s", summarizeToolCall(tool.Function.Name, toolArguments)))
			}
			if PrintHook != nil {
				PrintHook(fmt.Sprintf("🔧 %s\n", summarizeToolCall(tool.Function.Name, toolArguments)))
			}

			toolResult, err := ExecuteTool(tool.Function.Name, toolArguments)
			if err != nil {
				toolResult = err.Error()
			}

			*localMessages = append(*localMessages, openai.ToolMessage(toolResult, tool.ID))
		}
	}

	return ""
}


func summarizeToolCall(name string, args map[string]any) string {
	switch name {
	case "bash_tool":
		if reason, ok := args["reason"].(string); ok && reason != "" {
			return reason
		}
		if cmd, ok := args["command"].(string); ok && cmd != "" {
			if len(cmd) > 60 {
				return cmd[:60] + "..."
			}
			return cmd
		}
	case "spawn_agents":
		if reason, ok := args["reason"].(string); ok && reason != "" {
			return "Spawning agents \u2014 " + reason
		}
		if agents, ok := args["sub_agents"].([]any); ok {
			return fmt.Sprintf("Spawning %d sub-agents", len(agents))
		}
	case "web_search":
		if topic, ok := args["topic"].(string); ok && topic != "" {
			if len(topic) > 60 {
				return "Searching: \u201c" + topic[:60] + "...\u201d"
			}
			return "Searching: \u201c" + topic + "\u201d"
		}
	case "url_fetch":
		if url, ok := args["url"].(string); ok && url != "" {
			if len(url) > 60 {
				return "Fetching: " + url[:60] + "..."
			}
			return "Fetching: " + url
		}
	case "mail":
		if reason, ok := args["reason"].(string); ok && reason != "" {
			return "Mail \u2014 " + reason
		}
		if action, ok := args["action"].(string); ok && action != "" {
			return "Mail: " + action
		}
	case "schedule_task", "local_schedule_task":
		if reason, ok := args["reason"].(string); ok && reason != "" {
			return "Scheduling \u2014 " + reason
		}
		if task, ok := args["task"].(string); ok && task != "" {
			if len(task) > 60 {
				return "Schedule: " + task[:60] + "..."
			}
			return "Schedule: " + task
		}
	case "memory_add":
		if reason, ok := args["reason"].(string); ok && reason != "" {
			return "Remembering \u2014 " + reason
		}
		if content, ok := args["content"].(string); ok && content != "" {
			if len(content) > 60 {
				return "Remembering: " + content[:60] + "..."
			}
			return "Remembering: " + content
		}
	case "memory_view":
		return "Viewing stored memories"
	case "memory_delete":
		if id, ok := args["memory_id"].(string); ok && id != "" {
			return "Deleting memory: " + id
		}
	case "memory_update":
		if id, ok := args["memory_id"].(string); ok && id != "" {
			return "Updating memory: " + id
		}
	case "send_document_over_telegram":
		if path, ok := args["filepath"].(string); ok && path != "" {
			return "Sending document: " + path
		}
	case "send_image_over_telegram":
		if path, ok := args["filepath"].(string); ok && path != "" {
			return "Sending image: " + path
		}
	case "cancel_task", "local_cancel_task":
		if id, ok := args["task_id"].(string); ok && id != "" {
			return "Cancelling task: " + id
		}
	case "list_scheduled_tasks", "local_list_scheduled_tasks":
		return "Listing scheduled tasks"
	}

	// Fallback: try to find a "reason" field in any tool
	if reason, ok := args["reason"].(string); ok && reason != "" {
		if len(reason) > 60 {
			return reason[:60] + "..."
		}
		return reason
	}

	return name
}
