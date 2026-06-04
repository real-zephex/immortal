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

type MessageType string

const (
	MessageTypeUser      MessageType = "user"
	MessageTypeSystem    MessageType = "system"
	MessageTypeAssistant MessageType = "assistant"
	MessageTypeTool      MessageType = "tool"
)

const SubAgentSystemPrompt = `You are a sub-agent focused on completing the assigned task.
You have access to bash tools. Respond concisely with only your findings.
Do not ask follow-up questions or request additional tasks.`

type OpenAIMessages struct {
	MessageType MessageType
	Content     string
}

func (r *OpenAIMessages) ToChatCompletionMessageParam(toolId string) openai.ChatCompletionMessageParamUnion {
	switch r.MessageType {
	case MessageTypeUser:
		return openai.UserMessage(r.Content)
	case MessageTypeSystem:
		return openai.SystemMessage(r.Content)
	case MessageTypeAssistant:
		return openai.AssistantMessage(r.Content)
	case MessageTypeTool:
		return openai.ToolMessage(r.Content, toolId)
	default:
		return openai.UserMessage(r.Content)
	}
}

func GetGroqKey() (string, error) {
	key, exists := os.LookupEnv("DEEPSEEK_API_KEY")
	if !exists {
		return "", fmt.Errorf("The API key is not set")
	}
	return key, nil
}

func InitOpenAIClient() error {
	key, err := GetGroqKey()
	if err != nil {
		return err
	}
	client := openai.NewClient(
		option.WithAPIKey(key),
		option.WithBaseURL("https://api.deepseek.com"),
	)
	openaiClient = client
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

	orchestratorTools = []openai.ChatCompletionToolUnionParam{bashTool, spawnAgentsTool}
	subAgentTools     = []openai.ChatCompletionToolUnionParam{bashTool}
)

func OpenAIManager(ctx context.Context, localMessages *[]openai.ChatCompletionMessageParamUnion) string {
	return openAIManagerWithTools(ctx, localMessages, orchestratorTools)
}

func openAIManagerWithTools(ctx context.Context, localMessages *[]openai.ChatCompletionMessageParamUnion, tools []openai.ChatCompletionToolUnionParam) string {
	maxToolIterations := 40

	for range maxToolIterations {
		chatCompletion, err := openaiClient.Chat.Completions.New(
			ctx,
			openai.ChatCompletionNewParams{
				Messages: *localMessages,
				Model:    "deepseek-v4-flash",
				Tools:    tools,
			},
		)
		if err != nil {
			fmt.Println("[ERROR]", err)
			return ""
		}

		msg := chatCompletion.Choices[0].Message.ToParam()
		*localMessages = append(*localMessages, msg)

		toolCalls := chatCompletion.Choices[0].Message.ToolCalls

		if len(toolCalls) == 0 {
			return chatCompletion.Choices[0].Message.Content
		}

		fmt.Println("===Tool Calls===")
		for _, tool := range toolCalls {
			var toolArguments map[string]any

			err := json.Unmarshal([]byte(tool.Function.Arguments), &toolArguments)
			if err != nil {
				fmt.Println("[ERROR] parsing arguments:", err)
				continue
			}

			toolResult, err := ExecuteTool(tool.Function.Name, toolArguments)
			if err != nil {
				toolResult = err.Error()
			}

			toolMessage := OpenAIMessages{
				MessageType: MessageTypeTool,
				Content:     toolResult,
			}
			*localMessages = append(*localMessages, toolMessage.ToChatCompletionMessageParam(tool.ID))
		}
	}

	return ""
}
