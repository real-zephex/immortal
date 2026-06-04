package main

import (
	"context"
	"database/sql"
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
			time.Sleep(5 * time.Second)
		}
	}
}

func runAgent(wg *sync.WaitGroup, ctx context.Context, events <-chan Event, db *sql.DB) {
	defer wg.Done()

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

			// Load full conversation from DB
			params := utils.LoadConversation(db, "default")
			if params == nil {
				params = make([]openai.ChatCompletionMessageParamUnion, 0, 100)
			}
			params = append(params, openai.UserMessage(content))

			response := utils.OpenAIManager(ctx, &params)
			if response != "" {
				responses++
				currentCount := responses
				timestamp := time.Now().Format("2006-01-02 15:04:05")
				logEntry := fmt.Sprintf("[%s]\nUser: %s\nAssistant: %s\n%s\n",
					timestamp, content, response, strings.Repeat("-", 40))

				if _, err := f.WriteString(logEntry); err != nil {
					fmt.Printf("Error writing to file: %v\n", err)
				}

				fmt.Printf("Processed task: %s (Total: %d)\n", content, currentCount)

				// Persist full conversation (preserves tool calls + results)
				utils.SaveConversation(db, "default", params)
			}
		}
	}
}

func main() {
	start := time.Now()

	fmt.Println("Initializing database...")
	db := utils.InitDB()
	defer db.Close()
	fmt.Println("Database initialized.")

	fmt.Println("Initializing OpenAI client...")
	err := utils.InitOpenAIClient()
	if err != nil {
		fmt.Printf("Error initializing OpenAI client: %v\n", err)
		return
	}
	fmt.Println("OpenAI client initialized successfully.")

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
	wg.Add(2)
	eventsChannels := make(chan Event, 100)

	go PushToChannelA(ctx, eventsChannels, &wg)

	go runAgent(&wg, ctx, eventsChannels, db)

	// Auto-start Telegram bot if TELEGRAM_BOT_TOKEN is set
	if os.Getenv("TELEGRAM_BOT_TOKEN") != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			utils.StartTelegramBot(ctx, db)
		}()
		fmt.Println("Telegram bot goroutine started.")
	}

	wg.Wait()
	end := time.Since(start)
	fmt.Printf("Total time: %v\n", end)
	fmt.Println("Total Responses:", responses)
	fmt.Println("All systems shut down successfully.")
}
