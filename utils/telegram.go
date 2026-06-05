package utils

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"strings"

	bot "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"github.com/microcosm-cc/bluemonday"
	"github.com/openai/openai-go/v3"
	"github.com/russross/blackfriday/v2"
)

var telegramBot *bot.BotAPI
var CurrentTelegramChatID int64

const (
	telegramFileURL       = "https://api.telegram.org"
	telegramMaxMessageLen = 4000
)

type getFileResponse struct {
	OK     bool         `json:"ok"`
	Result fileResult   `json:"result"`
}

type fileResult struct {
	FileID       string `json:"file_id"`
	FileUniqueID string `json:"file_unique_id"`
	FileSize     int    `json:"file_size"`
	FilePath     string `json:"file_path"`
}

type groqTranscriptionResponse struct {
	Text string `json:"text"`
}

func telegramBotKeyCheck() (string, error) {
	key, exists := os.LookupEnv("TELEGRAM_BOT_TOKEN")
	if !exists {
		return "", fmt.Errorf("TELEGRAM_BOT_TOKEN not set in environment")
	}
	return key, nil
}

func groqAPIKey() (string, error) {
	key, exists := os.LookupEnv("GROQ_API_KEY")
	if !exists {
		return "", fmt.Errorf("GROQ_API_KEY not set in environment")
	}
	return key, nil
}

func botClient(key string) error {
	var err error
	telegramBot, err = bot.NewBotAPI(key)
	if err != nil {
		return fmt.Errorf("failed to initialize Telegram bot: %v", err)
	}
	fmt.Println("Telegram bot initialized successfully.")
	return nil
}

func StartTelegramBot(ctx context.Context, db *sql.DB) {
	fmt.Println("Starting Telegram bot in background mode...")

	botKey, err := telegramBotKeyCheck()
	if err != nil {
		fmt.Println(err)
		return
	}

	if err := botClient(botKey); err != nil {
		fmt.Println(err)
		return
	}

	telegramEventCh := make(chan TelegramEvent, 100)
	InitScheduler(telegramEventCh)

	go telegramProducer(ctx, db, telegramEventCh)
	go telegramConsumer(ctx, db, telegramEventCh)
}

func telegramProducer(ctx context.Context, db *sql.DB, eventCh chan<- TelegramEvent) {
	_, err := telegramBotKeyCheck()
	if err != nil {
		fmt.Println(err)
		return
	}

	updateConfig := bot.NewUpdate(0)
	updateConfig.Timeout = 30
	updates := telegramBot.GetUpdatesChan(updateConfig)

	fmt.Println("Listening for Telegram events...")

	for {
		select {
		case <-ctx.Done():
			fmt.Println("Telegram bot shutting down...")
			return
		case update, ok := <-updates:
			if !ok {
				return
			}

			message := update.Message
			if message == nil {
				continue
			}

			// Commands handled directly, no LLM
			if message.IsCommand() {
				commandsHandler(message, db)
				continue
			}

			channel := TelegramChannel(message.Chat.ID)
			receivedMessage := strings.TrimSpace(message.Text)

			// STT: voice messages -> Groq Whisper
			if message.Voice != nil {
				fmt.Println("Voice message detected. Transcribing...")
				text, err := handleAudio(message.Voice.FileID)
				if err != nil {
					sendMessage(fmt.Sprintf("Error transcribing audio: %v", err), message)
					continue
				}
				receivedMessage = text
				fmt.Printf("Transcribed: %s\n", text)
			}

			// Reply-to context
			if message.ReplyToMessage != nil {
				replyText := strings.TrimSpace(message.ReplyToMessage.Text)
				if replyText != "" {
					if receivedMessage != "" {
						receivedMessage = fmt.Sprintf(
							"Reply context:\n%s\n\nUser message:\n%s",
							replyText, receivedMessage,
						)
					} else {
						receivedMessage = fmt.Sprintf("Reply context:\n%s", replyText)
					}
				}
			}

			if receivedMessage == "" {
				continue
			}

			fmt.Printf("[TG] %d: %.120s\n", message.Chat.ID, receivedMessage)

			eventCh <- TelegramEvent{
				Type:    TelegramUserMessage,
				ChatID:  message.Chat.ID,
				Channel: channel,
				Payload: receivedMessage,
			}
		}
	}
}

func telegramConsumer(ctx context.Context, db *sql.DB, eventCh <-chan TelegramEvent) {
	for {
		select {
		case <-ctx.Done():
			fmt.Println("Telegram consumer shutting down...")
			return
		case event, ok := <-eventCh:
			if !ok {
				return
			}

			CurrentTelegramChatID = event.ChatID

			params := LoadConversation(db, event.Channel)
			if params == nil {
				params = make([]openai.ChatCompletionMessageParamUnion, 0, 100)
				params = append(params, openai.SystemMessage(SystemPrompt))
			}

			var snap []openai.ChatCompletionMessageParamUnion
			ephemeral := event.Type == TelegramScheduledTask

			if ephemeral {
				snap = make([]openai.ChatCompletionMessageParamUnion, len(params))
				copy(snap, params)
				snap = append(snap, openai.UserMessage("⏰ " + event.Payload))
			} else {
				params = append(params, openai.UserMessage(event.Payload))
			}

			messages := &params
			if ephemeral {
				messages = &snap
			}

			response := OpenAIManagerWithTools(ctx, messages, TelegramTools)
			if response == "" {
				response = "I'm sorry, I couldn't process that request."
			}

			sendMessageToChatID(response, event.ChatID)
			fmt.Printf("[TG] Response sent to %d (%s)\n", event.ChatID, event.Type)

			if !ephemeral {
				SaveConversation(db, event.Channel, params)
			}
		}
	}
}

func sendMessageToChatID(text string, chatID int64) {
	if strings.TrimSpace(text) == "" {
		return
	}
	chunks := splitTelegramMessage(text, telegramMaxMessageLen)
	for _, chunk := range chunks {
		html := mdToTelegramHTML(chunk)
		msg := bot.NewMessage(chatID, html)
		msg.ParseMode = "HTML"
		if _, err := telegramBot.Send(msg); err != nil {
			fmt.Printf("Error sending Telegram message: %v\n", err)
			return
		}
	}
}

// splitTelegramMessage splits a message into chunks respecting Unicode rune boundaries.
func splitTelegramMessage(text string, maxLen int) []string {
	if maxLen <= 0 {
		return []string{text}
	}

	runes := []rune(text)
	if len(runes) <= maxLen {
		return []string{text}
	}

	chunks := make([]string, 0, (len(runes)+maxLen-1)/maxLen)
	for start := 0; start < len(runes); start += maxLen {
		end := min(start+maxLen, len(runes))
		chunks = append(chunks, string(runes[start:end]))
	}
	return chunks
}

// mdToTelegramHTML converts Markdown to Telegram-safe HTML using blackfriday + bluemonday.
func mdToTelegramHTML(md string) string {
	unsafe := blackfriday.Run([]byte(md))

	p := bluemonday.NewPolicy()
	p.AllowElements("b", "strong", "i", "em", "u")
	p.AllowElements("pre", "code")
	p.AllowElements("a")
	p.AllowAttrs("href").OnElements("a")

	safe := p.SanitizeBytes(unsafe)
	return string(safe)
}

// sendMessage sends a text response to a Telegram message, preserving reply-to context.
func sendMessage(text string, message *bot.Message) {
	if message == nil || strings.TrimSpace(text) == "" {
		return
	}

	chatID := message.Chat.ID
	messageID := message.MessageID
	chunks := splitTelegramMessage(text, telegramMaxMessageLen)

	for i, chunk := range chunks {
		html := mdToTelegramHTML(chunk)
		msg := bot.NewMessage(chatID, html)
		msg.ParseMode = "HTML"
		if i == 0 {
			msg.ReplyToMessageID = messageID
		}

		if _, err := telegramBot.Send(msg); err != nil {
			fmt.Printf("Error sending Telegram message: %v\n", err)
			return
		}
	}
}

// commandsHandler processes Telegram bot commands (/start, /help, /clear).
func commandsHandler(message *bot.Message, db *sql.DB) {
	switch message.Command() {
	case "start":
		sendMessage("👋 Welcome to Immortal Agent!\nUse /help to see available commands.", message)
	case "help":
		sendMessage("📋 *Available Commands*\n\n/start - Show welcome message\n/help - Show this help menu\n/clear - Clear conversation history for this chat", message)
	case "clear":
		channel := TelegramChannel(message.Chat.ID)
		ClearConversation(db, channel)
		sendMessage("✅ Conversation history cleared for this chat.", message)
	default:
		sendMessage(fmt.Sprintf("Unknown command: %s", message.Command()), message)
	}
}

// getTelegramFileUrl performs the 2-hop Telegram file URL resolution.
func getTelegramFileURL(botKey, fileID string) (string, error) {
	resp, err := http.Get(fmt.Sprintf(
		telegramFileURL+"/bot%s/getFile?file_id=%s", botKey, fileID,
	))
	if err != nil {
		return "", fmt.Errorf("error querying Telegram for file: %v", err)
	}
	defer resp.Body.Close()

	var tgResp getFileResponse
	if err := json.NewDecoder(resp.Body).Decode(&tgResp); err != nil {
		return "", fmt.Errorf("error parsing Telegram getFile response: %v", err)
	}
	if !tgResp.OK {
		return "", fmt.Errorf("Telegram getFile returned error")
	}

	return fmt.Sprintf(
		"https://api.telegram.org/file/bot%s/%s", botKey, tgResp.Result.FilePath,
	), nil
}

// fetchTelegramFile downloads a file from Telegram by file ID.
func fetchTelegramFile(botKey, fileID string) ([]byte, error) {
	fileURL, err := getTelegramFileURL(botKey, fileID)
	if err != nil {
		return nil, err
	}

	fileResp, err := http.Get(fileURL)
	if err != nil {
		return nil, fmt.Errorf("error fetching file from Telegram: %v", err)
	}
	defer fileResp.Body.Close()

	return io.ReadAll(fileResp.Body)
}

// handleAudio transcribes a Telegram voice message using Groq Whisper.
func handleAudio(fileID string) (string, error) {
	botKey, err := telegramBotKeyCheck()
	if err != nil {
		return "", err
	}

	groqKey, err := groqAPIKey()
	if err != nil {
		return "", err
	}

	audioBytes, err := fetchTelegramFile(botKey, fileID)
	if err != nil {
		return "", fmt.Errorf("error fetching audio from Telegram: %v", err)
	}

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	part, _ := w.CreateFormFile("file", "voice.ogg")
	part.Write(audioBytes)
	w.WriteField("model", "whisper-large-v3-turbo")
	w.Close()

	req, err := http.NewRequest(
		"POST", "https://api.groq.com/openai/v1/audio/transcriptions",
		&buf,
	)
	if err != nil {
		return "", fmt.Errorf("error creating Groq request: %v", err)
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", groqKey))
	req.Header.Set("Content-Type", w.FormDataContentType())

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("error calling Groq API: %v", err)
	}
	defer resp.Body.Close()

	var groqResp groqTranscriptionResponse
	if err := json.NewDecoder(resp.Body).Decode(&groqResp); err != nil {
		return "", fmt.Errorf("error parsing Groq response: %v", err)
	}

	return groqResp.Text, nil
}

// sendDocument sends a file as a document to the specified Telegram chat.
func sendDocument(chatID int64, filepath string) error {
	if chatID <= 0 {
		return fmt.Errorf("telegram chat id is not set")
	}

	msg := bot.NewDocument(chatID, bot.FilePath(filepath))
	if _, err := telegramBot.Send(msg); err != nil {
		return fmt.Errorf("error sending document: %v", err)
	}
	return nil
}

// sendImage sends an image to the specified Telegram chat.
func sendImage(chatID int64, filepath string) error {
	if chatID <= 0 {
		return fmt.Errorf("telegram chat id is not set")
	}

	msg := bot.NewPhoto(chatID, bot.FilePath(filepath))
	if _, err := telegramBot.Send(msg); err != nil {
		return fmt.Errorf("error sending image: %v", err)
	}
	return nil
}


