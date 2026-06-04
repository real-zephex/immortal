package main

import (
	"context"
	"fmt"
	"immortal/utils"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/openai/openai-go/v3"
)

var responses int = 0

type EventType string

const (
	EventTypeUserMessage EventType = "user_message"
)

type Event struct {
	Type    EventType `json:"event"`
	Payload any
}

var (
	messages []utils.OpenAIMessages
	OpenAIMessages []openai.ChatCompletionMessageParamUnion
	mu       sync.Mutex
)

func GetTasksFromFile() []string {
	data, err := os.ReadFile("messages.txt")
	if err != nil {
		return []string{}
	}
	return strings.Split(string(data), "\n")
}

func PushToChannelA(ctx context.Context, events chan<- Event, wg *sync.WaitGroup) {
	defer wg.Done()
	for {
		select {
		case <-ctx.Done():
			return
		default:
			tasks := GetTasksFromFile()
			if len(tasks) > 0 {
				os.WriteFile("messages.txt", []byte(""), 0644)
				for _, task := range tasks {
					if strings.TrimSpace(task) == "" {
						continue
					}
					select {
					case events <- Event{Type: EventTypeUserMessage, Payload: task}:
					case <-ctx.Done():
						return
					}
				}
			}
			for i := 0; i < 5; i++ {
				select {
				case <-ctx.Done():
					return
				case <-time.After(1 * time.Second):
					fmt.Printf("Polling in %d seconds...\n", 5-i)
				}
			}
		}
	}
}

func runAgent(ctx context.Context, events <-chan Event) {
	f, err := os.OpenFile("responses.txt", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Printf("Error opening responses file: %v\n", err)
		return
	}
	defer f.Close()

	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			select {
			case <-ctx.Done():
				return
			default:
			}

			content, ok := event.Payload.(string)
			if !ok {
				continue
			}

			mu.Lock()
			tempUserMessage := &utils.OpenAIMessages{
				MessageType: utils.MessageTypeUser,
				Content: content,
			}
			// messages = append(messages, utils.OpenAIMessages{
			// 	MessageType: utils.MessageTypeUser,
			// 	Content:     content,
			// })
			OpenAIMessages = append(OpenAIMessages, tempUserMessage.ToChatCompletionMessageParam(""))
			mu.Unlock()

			//TODO: Convert messages to OpenAI message params
			response := utils.OpenAIManager(ctx, OpenAIMessages)
			if response != "" {
				mu.Lock()
				responses++
				currentCount := responses
				timestamp := time.Now().Format("2006-01-02 15:04:05")
				logEntry := fmt.Sprintf("[%s]\nUser: %s\nAssistant: %s\n%s\n",
					timestamp, content, response, strings.Repeat("-", 40))

				if _, err := f.WriteString(logEntry); err != nil {
					fmt.Printf("Error writing to file: %v\n", err)
				}

				fmt.Printf("Processed task: %s (Total: %d)\n", content, currentCount)
				tempAssistantMessage := &utils.OpenAIMessages{
					MessageType: utils.MessageTypeAssistant,
					Content: content,
				}
				OpenAIMessages = append(OpenAIMessages, tempAssistantMessage.ToChatCompletionMessageParam(""))
				// messages = append(messages, utils.Message{Role: "assistant", Content: response})
				mu.Unlock()
			}
		}
	}
}

func main() {
	start := time.Now()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigs
		fmt.Println("\nShutting down...")
		cancel()
	}()

	var wg sync.WaitGroup
	wg.Add(1)
	eventsChannels := make(chan Event, 100)

	go PushToChannelA(ctx, eventsChannels, &wg)

	aiWorkers := 1
	for range aiWorkers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			runAgent(ctx, eventsChannels)
		}()
	}

	wg.Wait()
	end := time.Since(start)
	fmt.Printf("Total time: %v\n", end)
	fmt.Println("Total Responses:", responses)
	fmt.Println("All systems shut down successfully.")
}
