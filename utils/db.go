package utils

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/openai/openai-go/v3"
	_ "modernc.org/sqlite"
)

var DB *sql.DB

func InitDB() *sql.DB {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("Failed to get home directory: %v", err)
	}

	dbPath := filepath.Join(home, ".immortal-agent.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS conversations (
			channel TEXT PRIMARY KEY,
			params_json TEXT NOT NULL,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		log.Fatalf("Failed to create conversations table: %v", err)
	}

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS memories (
			id         TEXT PRIMARY KEY,
			content    TEXT NOT NULL,
			updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		log.Fatalf("Failed to create memories table: %v", err)
	}

	return db
}

// SaveConversation stores the full params array as JSON for a channel.
// This preserves user, assistant, tool call, and tool result messages.
func SaveConversation(db *sql.DB, channel string, params []openai.ChatCompletionMessageParamUnion) {
	data, err := json.Marshal(params)
	if err != nil {
		log.Printf("Failed to marshal conversation: %v", err)
		return
	}

	_, err = db.Exec(
		`INSERT INTO conversations (channel, params_json) VALUES (?, ?)
		 ON CONFLICT(channel) DO UPDATE SET params_json = ?, updated_at = CURRENT_TIMESTAMP`,
		channel, string(data), string(data),
	)
	if err != nil {
		log.Printf("Failed to save conversation: %v", err)
	}
}

// LoadConversation loads the full params array from JSON for a channel.
func LoadConversation(db *sql.DB, channel string) []openai.ChatCompletionMessageParamUnion {
	var data string
	err := db.QueryRow("SELECT params_json FROM conversations WHERE channel = ?", channel).Scan(&data)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil
		}
		log.Printf("Failed to load conversation: %v", err)
		return nil
	}

	var params []openai.ChatCompletionMessageParamUnion
	if err := json.Unmarshal([]byte(data), &params); err != nil {
		log.Printf("Failed to unmarshal conversation: %v", err)
		return nil
	}
	return params
}

func TelegramChannel(chatID int64) string {
	return fmt.Sprintf("telegram:%d", chatID)
}

func ClearConversations(db *sql.DB) {
	_, err := db.Exec("DELETE FROM conversations")
	if err != nil {
		log.Printf("Failed to clear conversations: %v", err)
	}
}

func ClearConversation(db *sql.DB, channel string) {
	_, err := db.Exec("DELETE FROM conversations WHERE channel = ?", channel)
	if err != nil {
		log.Printf("Failed to clear conversation %s: %v", channel, err)
	}
}
