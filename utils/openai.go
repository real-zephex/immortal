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

	orchestratorTools = []openai.ChatCompletionToolUnionParam{bashTool, spawnAgentsTool, webSearchTool, urlFetchTool, mailTool, localScheduleTaskTool, localCancelTaskTool, localListTasksTool}
	subAgentTools     = []openai.ChatCompletionToolUnionParam{bashTool, webSearchTool, urlFetchTool}
	TelegramTools     = []openai.ChatCompletionToolUnionParam{bashTool, spawnAgentsTool, sendDocumentTool, sendImageTool, webSearchTool, urlFetchTool, mailTool, scheduleTaskTool, cancelTaskTool, listTasksTool}
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
			DebugPrint("[ERROR] %v\n", err)
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
			if StatusHook != nil {
				StatusHook(fmt.Sprintf("⚡ %s", tool.Function.Name))
			}
			if PrintHook != nil {
				PrintHook(fmt.Sprintf("🔧 Tool: %s\n", tool.Function.Name))
			}

			var toolArguments map[string]any

			err := json.Unmarshal([]byte(tool.Function.Arguments), &toolArguments)
			if err != nil {
				DebugPrint("[ERROR] parsing arguments: %v\n", err)
				continue
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
