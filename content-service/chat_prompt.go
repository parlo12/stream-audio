package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strings"
	"time"
)

// ChatMessage represents one message for the ChatGPT chat/completions API.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatRequest is the payload for the /v1/chat/completions endpoint.
type ChatRequest struct {
	Model          string        `json:"model"`
	Messages       []ChatMessage `json:"messages"`
	MaxTokens      int           `json:"max_tokens"`
	Temperature    float32       `json:"temperature"`
	ResponseFormat *ResponseFormat `json:"response_format,omitempty"`
}

// ResponseFormat specifies the format of the model's output
type ResponseFormat struct {
	Type string `json:"type"` // "json_object" or "text"
}

// ChatResponse models the subset of the response we need.
type ChatResponse struct {
	Choices []struct {
		Message ChatMessage `json:"message"`
	} `json:"choices"`
}

// summarizeBookText truncates or passes through up to 500 chars for context.
func summarizeBookText(bookText string) string {
	if len(bookText) > 500 {
		return bookText[:500]
	}
	return bookText
}

// generateOverallSoundPrompt reads the book file, summarizes it, and asks GPT to generate
// a concise (<=300 chars) background music prompt.
func generateOverallSoundPrompt(bookFilePath string) (string, error) {
	data, err := os.ReadFile(bookFilePath)
	if err != nil {
		return "", fmt.Errorf("read book file: %w", err)
	}
	excerpt := summarizeBookText(string(data))

	userContent := fmt.Sprintf(
		"Analyze this audiobook excerpt and produce a concise (max 300 chars) background music prompt recommending instrumentation, mood, and style: %s",
		excerpt,
	)

	reqPayload := ChatRequest{
		Model:       "gpt-4o",
		Messages:    []ChatMessage{{Role: "system", Content: "You are an audio production assistant."}, {Role: "user", Content: userContent}},
		MaxTokens:   100,
		Temperature: 0.7,
	}
	bodyBytes, err := json.Marshal(reqPayload)
	if err != nil {
		return "", fmt.Errorf("marshal request: %w", err)
	}

	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return "", errors.New("OPENAI_API_KEY not set")
	}
	req, err := http.NewRequest("POST", "https://api.openai.com/v1/chat/completions", bytes.NewReader(bodyBytes))
	if err != nil {
		return "", fmt.Errorf("build HTTP request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("HTTP request error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := ioutil.ReadAll(resp.Body)
		return "", fmt.Errorf("GPT returned %d: %s", resp.StatusCode, respBody)
	}

	var chatResp ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return "", fmt.Errorf("decode GPT response: %w", err)
	}
	if len(chatResp.Choices) == 0 {
		return "", errors.New("no GPT choices returned")
	}

	output := strings.TrimSpace(chatResp.Choices[0].Message.Content)
	// enforce 300-char limit
	if len(output) > 300 {
		output = output[:300]
	}
	return output, nil
}
