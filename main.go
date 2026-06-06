package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"immortal/tui"
	"immortal/utils"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/openai/openai-go/v3"
	"golang.org/x/term"
)

var (
	flagBaseURL = flag.String("base-url", "https://openrouter.ai/api/v1", "LLM API base URL")
	flagModel   = flag.String("model", "deepseek-v4-flash", "LLM model name")
	flagClear   = flag.Bool("clear", false, "clear all conversation history")
	flagTUI     = flag.Bool("tui", false, "force TUI mode")
	flagNoTUI   = flag.Bool("no-tui", false, "disable TUI mode (headless)")
	flagNoTG    = flag.Bool("no-tg", false, "disable Telegram bot")
)

var responses int = 0

func GetTasksFromFile() []string {
	data, err := os.ReadFile("messages.txt")
	if err != nil {
		return []string{}
	}
	return strings.Split(string(data), "\n")
}

func PushToChannelA(ctx context.Context, events chan<- utils.Event, wg *sync.WaitGroup) {
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
					case events <- utils.Event{Type: utils.EventTypeUserMessage, Payload: task}:
					case <-ctx.Done():
						return
					}
				}
			}
			time.Sleep(5 * time.Second)
		}
	}
}

func runAgent(wg *sync.WaitGroup, ctx context.Context, events <-chan utils.Event, db *sql.DB, responseCh chan<- string) {
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

			// Detect scheduled task firings (⏰ prefix) — log them but
			// don't persist to conversation history (ephemeral).
			isScheduled := strings.HasPrefix(content, "⏰ ")

			// Load full conversation from DB
			params := utils.LoadConversation(db, "default")
			if params == nil {
				params = make([]openai.ChatCompletionMessageParamUnion, 0, 100)
				params = append(params, openai.SystemMessage(utils.SystemPrompt))
			}

			// For scheduled tasks, snapshot history instead of mutating it
			var snap []openai.ChatCompletionMessageParamUnion
			if isScheduled {
				snap = make([]openai.ChatCompletionMessageParamUnion, len(params))
				copy(snap, params)
				snap = append(snap, openai.UserMessage(content))
				params = snap
			} else {
				params = append(params, openai.UserMessage(content))
			}

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

				// Persist full conversation — skip for scheduled task firings
				if !isScheduled {
					utils.SaveConversation(db, "default", params)
				}
			}

			// Always signal completion to TUI so thinking indicator clears,
			// even on empty response (error/empty cases).
			if responseCh != nil {
				select {
				case responseCh <- response:
				case <-ctx.Done():
					return
				default:
				}
			}
		}
	}
}

func main() {
	flag.Parse()
	start := time.Now()

	fmt.Println("Initializing database...")
	db := utils.InitDB()
	defer db.Close()
	fmt.Println("Database initialized.")

	if *flagClear {
		fmt.Println("Clearing all conversation history...")
		utils.ClearConversations(db)
		fmt.Println("Done.")
		return
	}

	fmt.Printf("Initializing OpenAI client [model=%s, base-url=%s]...\n", *flagModel, *flagBaseURL)
	err := utils.InitOpenAIClient(*flagBaseURL, *flagModel)
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
	eventsChannels := make(chan utils.Event, 100)
	responseCh := make(chan string, 100)
	utils.InitLocalScheduler(eventsChannels)

	go PushToChannelA(ctx, eventsChannels, &wg)

	go runAgent(&wg, ctx, eventsChannels, db, responseCh)

	// Auto-start Telegram bot if TELEGRAM_BOT_TOKEN is set and not disabled
	if os.Getenv("TELEGRAM_BOT_TOKEN") != "" && !*flagNoTG {
		wg.Add(1)
		go func() {
			defer wg.Done()
			utils.StartTelegramBot(ctx, db)
		}()
		fmt.Println("Telegram bot goroutine started.")
	}

	// Start TUI if appropriate
	useTUI := false
	if *flagTUI {
		useTUI = true
	} else if !*flagNoTUI && term.IsTerminal(int(os.Stdin.Fd())) {
		useTUI = true
	}

	if useTUI {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tui.RunTUI(ctx, db, eventsChannels, responseCh)
		}()
		fmt.Println("TUI started.")
	}

	wg.Wait()
	end := time.Since(start)
	fmt.Printf("Total time: %v\n", end)
	fmt.Println("Total Responses:", responses)
	fmt.Println("All systems shut down successfully.")
}
