package utils

import (
	"context"
	"fmt"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
)

var openaiClient openai.Client = openai.NewClient(
	option.WithAPIKey("GROQ_API_KEY"),
	option.WithBaseURL("https://api.groq.com/openai/v1"),
)

type MessageType string

const (
	MessageTypeUser      MessageType = "user"
	MessageTypeSystem    MessageType = "system"
	MessageTypeAssistant MessageType = "assistant"
	MessageTypeTool      MessageType = "tool"
)

// var LocalMessages = []openai.ChatCompletionMessageParamUnion{
// 	openai.SystemMessage("You are a helpful assistant"),
// }

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

func OpenAIManager(ctx context.Context, localMessages []openai.ChatCompletionMessageParamUnion) string {

	// uM := OpenAIMessages{
	// 	MessageType: MessageTypeUser,
	// 	Content:     userMessage,
	// }
	// LocalMessages = append(LocalMessages, uM.ToChatCompletionMessageParam(""))

	chatCompletionn, err := openaiClient.Chat.Completions.New(
		context.TODO(),
		openai.ChatCompletionNewParams{
			Messages: localMessages,
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

	fmt.Println("===Tool Calls===")
	fmt.Println(chatCompletionn.Choices[0].Message.ToolCalls)

	response := chatCompletionn.Choices[0].Message.Content
	if response != "" {
		return response
	} else {
		return ""
	}
}
