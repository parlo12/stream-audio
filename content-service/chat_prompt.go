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

// ChatResponse models the subset of the response we need. FinishReason lets
// callers detect max_tokens truncation ("length") and treat it as a failure
// instead of parsing a cut-off tail (audit M2).
type ChatResponse struct {
	Choices []struct {
		Message      ChatMessage `json:"message"`
		FinishReason string      `json:"finish_reason"`
	} `json:"choices"`
}

// summarizeBookText truncates or passes through up to 500 chars for context.
func summarizeBookText(bookText string) string {
	if len(bookText) > 500 {
		return bookText[:500]
	}
	return bookText
}

// generateOverallSoundPrompt summarizes the supplied page text and asks GPT to
// generate a concise (<=300 chars) background music prompt. Q1: callers pass the
// chunk's own content so each page's music reflects that page, not page 1.
func generateOverallSoundPrompt(pageText string) (string, error) {
	excerpt := summarizeBookText(pageText)

	userContent := fmt.Sprintf(
		"Analyze this audiobook excerpt and produce a concise (max 300 chars) background music prompt recommending instrumentation, mood, and style: %s",
		excerpt,
	)

	reqPayload := ChatRequest{
		Model:       "gpt-4o",
		Messages:    []ChatMessage{{Role: "system", Content: "You are an audio production assistant."}, {Role: "user", Content: userContent}},
		MaxTokens:   120, // audit M2: 100 truncated mid-sentence on wordy outputs
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
	// Enforce the 300-char limit rune-safely, cutting at a word boundary so
	// ElevenLabs never receives a half-word (audit M2: the old byte slice
	// could split mid-word and mid-UTF-8-rune).
	if r := []rune(output); len(r) > 300 {
		cut := string(r[:300])
		if i := strings.LastIndex(cut, " "); i > 200 {
			cut = cut[:i]
		}
		output = strings.TrimSpace(cut)
	}
	return output, nil
}
