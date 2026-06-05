package utils

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"
)

type ChatCompletion struct {
	ID                string   `json:"id"`
	Object            string   `json:"object"`
	Created           int64    `json:"created"`
	Model             string   `json:"model"`
	Choices           []Choice `json:"choices"`
	Usage             Usage    `json:"usage"`
	SystemFingerprint string   `json:"system_fingerprint"`
	XGroq             XGroq    `json:"x_groq"`
}

type Choice struct {
	Index        int         `json:"index"`
	Message      Message     `json:"message"`
	Logprobs     interface{} `json:"logprobs"`
	FinishReason string      `json:"finish_reason"`
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Usage struct {
	QueueTime        float64 `json:"queue_time"`
	PromptTokens     int     `json:"prompt_tokens"`
	PromptTime       float64 `json:"prompt_time"`
	CompletionTokens int     `json:"completion_tokens"`
	CompletionTime   float64 `json:"completion_time"`
	TotalTokens      int     `json:"total_tokens"`
	TotalTime        float64 `json:"total_time"`
}

type XGroq struct {
	ID string `json:"id"`
}

type GroqRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
}

func (r *GroqRequest) ToJSON() ([]byte, error) {
	return json.Marshal(r)
}

const (
	BASE_URL string = "https://api.groq.com/openai/v1/chat/completions"
	MODEL    string = "llama-3.1-8b-instant"
)

var (
	API_KEY string
	client  *http.Client = &http.Client{
		Timeout: 30 * time.Second,
	}
)

func getGroqKey() error {
	key, exists := os.LookupEnv("IMMORTAL_API_KEY")
	if !exists {
		return fmt.Errorf("IMMORTAL_API_KEY environment variable not set")
	}
	API_KEY = key
	return nil
}

func gRequest(ctx context.Context, model string, messages []Message) (*http.Request, error) {
	g_req := &GroqRequest{
		Model:    model,
		Messages: messages,
	}
	jsonBody, err := g_req.ToJSON()
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		BASE_URL,
		bytes.NewBuffer(jsonBody),
	)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+API_KEY)
	return req, nil
}

func GroqManager(ctx context.Context, data []Message) string {
	err := getGroqKey()
	if err != nil {
		log.Println(err)
		return ""
	}

	req, err := gRequest(ctx, MODEL, data)
	if err != nil {
		log.Println(err)
		return ""
	}

	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() == nil {
			log.Println(err)
		}
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var body bytes.Buffer
		body.ReadFrom(resp.Body)
		log.Printf("API request failed with status %d: %s\n", resp.StatusCode, body.String())
		return ""
	}

	var response ChatCompletion
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		log.Printf("Failed to decode response: %v\n", err)
		return ""
	}

	if len(response.Choices) > 0 {
		return response.Choices[0].Message.Content
	}
	log.Println("No response from assistant.")
	return ""
}
