package utils

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

// var openaiClient openai.Client = openai.NewClient(
//
//	option.WithAPIKey(getGroqKey()),
//	option.WithBaseURL("https://api.groq.com/openai/v1"),
//
// )
var openaiClient openai.Client

type MessageType string

const (
	MessageTypeUser      MessageType = "user"
	MessageTypeSystem    MessageType = "system"
	MessageTypeAssistant MessageType = "assistant"
	MessageTypeTool      MessageType = "tool"
)

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
	key, exists := os.LookupEnv("GROQ_API_KEY")
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
		option.WithBaseURL("https://api.groq.com/openai/v1"),
	)
	openaiClient = client
	return nil
}

func OpenAIManager(ctx context.Context, localMessages *[]openai.ChatCompletionMessageParamUnion) string {
	maxToolIterations := 40

	for range maxToolIterations {
		chatCompletion, err := openaiClient.Chat.Completions.New(
			context.TODO(),
			openai.ChatCompletionNewParams{
				Messages: *localMessages,
				Model:    "openai/gpt-oss-20b",
				Tools: []openai.ChatCompletionToolUnionParam{
					{
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
					},
				},
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
