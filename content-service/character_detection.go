package main

// character_detection.go
// Detects characters in book text and assigns appropriate voices based on gender/age

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

// Character represents a detected character in the book
type Character struct {
	Name   string `json:"name"`
	Gender string `json:"gender"` // "male", "female", "child", "neutral"
	Voice  string `json:"voice"`  // OpenAI voice to use
	Age    string `json:"age"`    // "adult", "child", "elderly"
}

// DialogueLine represents a line of dialogue with speaker
type DialogueLine struct {
	Speaker    string `json:"speaker"`     // Character name or "narrator"
	Text       string `json:"text"`        // The actual text
	IsDialogue bool   `json:"is_dialogue"` // true if character speech, false if narration
}

// VoiceMapping maps character types to OpenAI voices
var VoiceMapping = map[string]string{
	"male_adult":      "onyx",   // Deep male voice
	"female_adult":    "nova",   // Female voice
	"male_child":      "shimmer", // Higher pitched for children
	"female_child":    "shimmer", // Higher pitched for children
	"elderly_male":    "echo",    // Mature male voice
	"elderly_female":  "fable",   // Mature female voice
	"narrator_male":   "alloy",   // Neutral narrator voice
	"narrator_female": "nova",    // Neutral narrator voice
	"neutral":         "alloy",   // Default neutral voice
}

// detectCharacters uses GPT to identify characters in the text
func detectCharacters(bookText string) ([]Character, error) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("OPENAI_API_KEY not set")
	}

	// Limit text to first 3000 characters for character analysis
	analysisText := bookText
	if len(bookText) > 3000 {
		analysisText = bookText[:3000]
	}

	systemPrompt := `You are a literary analysis AI that identifies characters in book excerpts.
Analyze the text and identify all speaking characters.
For each character, determine:
1. Name (or role if unnamed like "Mother", "Child")
2. Gender (male/female/neutral)
3. Age category (adult/child/elderly)

Return ONLY valid JSON array format:
[
  {"name": "John", "gender": "male", "age": "adult"},
  {"name": "Sarah", "gender": "female", "age": "adult"},
  {"name": "Timmy", "gender": "male", "age": "child"}
]

Do not include the narrator unless they are a character in the story.`

	reqBody := ChatRequest{
		Model: "gpt-4o",
		Messages: []ChatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: fmt.Sprintf("Analyze this book excerpt and identify all characters:\n\n%s", analysisText)},
		},
		Temperature: 0.3,
		MaxTokens:   1000,
	}

	bodyBytes, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", openAIChatURL, bytes.NewReader(bodyBytes))
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("character detection API call failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := ioutil.ReadAll(resp.Body)
		return nil, fmt.Errorf("character detection returned %d: %s", resp.StatusCode, b)
	}

	var chatResp ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return nil, fmt.Errorf("decode character JSON: %w", err)
	}

	if len(chatResp.Choices) == 0 {
		return nil, fmt.Errorf("no character analysis returned")
	}

	// Parse the JSON array from GPT response
	content := strings.TrimSpace(chatResp.Choices[0].Message.Content)

	// Clean up markdown code blocks if present
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	var rawCharacters []struct {
		Name   string `json:"name"`
		Gender string `json:"gender"`
		Age    string `json:"age"`
	}

	if err := json.Unmarshal([]byte(content), &rawCharacters); err != nil {
		log.Printf("⚠️ Failed to parse character JSON, using default narrator: %v", err)
		// Return default narrator voice
		return []Character{{Name: "Narrator", Gender: "neutral", Voice: "alloy", Age: "adult"}}, nil
	}

	// Map to Character with voice assignment
	characters := make([]Character, len(rawCharacters))
	for i, rc := range rawCharacters {
		voiceKey := fmt.Sprintf("%s_%s", rc.Gender, rc.Age)
		voice, exists := VoiceMapping[voiceKey]
		if !exists {
			voice = VoiceMapping["neutral"]
		}

		characters[i] = Character{
			Name:   rc.Name,
			Gender: rc.Gender,
			Voice:  voice,
			Age:    rc.Age,
		}
		log.Printf("🎭 Detected character: %s (%s, %s) → Voice: %s", rc.Name, rc.Gender, rc.Age, voice)
	}

	return characters, nil
}

// splitDialogue splits text into dialogue lines with speaker attribution
func splitDialogue(bookText string, characters []Character) ([]DialogueLine, error) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("OPENAI_API_KEY not set")
	}

	// Build character list for prompt
	characterList := "Characters:\n"
	for _, c := range characters {
		characterList += fmt.Sprintf("- %s (%s, %s)\n", c.Name, c.Gender, c.Age)
	}

	systemPrompt := `You are a dialogue extraction AI.
Split the text into dialogue lines, identifying who is speaking.
For each line, specify:
1. speaker: Character name or "Narrator" for non-dialogue text
2. text: The actual text (cleaned, ready for narration)
3. is_dialogue: true if it's a character speaking, false if narration

IMPORTANT TEXT CLEANING RULES:
- Remove quotation marks from dialogue
- Keep punctuation like periods, commas, exclamation marks for natural speech
- Remove symbols: < > { } [ ] | \ ~ ^
- Convert "..." to brief pause (keep as ...)
- Keep em-dashes (—) for dramatic pauses
- Do NOT spell out punctuation (don't say "question mark", just pause naturally)

Return ONLY valid JSON array:
[
  {"speaker": "Narrator", "text": "The sun rose over the hills.", "is_dialogue": false},
  {"speaker": "John", "text": "Good morning! How are you?", "is_dialogue": true},
  {"speaker": "Narrator", "text": "She smiled warmly.", "is_dialogue": false}
]`

	reqBody := ChatRequest{
		Model: "gpt-4o",
		Messages: []ChatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: fmt.Sprintf("%s\n\nText to split:\n%s", characterList, bookText)},
		},
		Temperature: 0.2,
		MaxTokens:   2000,
	}

	bodyBytes, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", openAIChatURL, bytes.NewReader(bodyBytes))
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 45 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("dialogue split API call failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := ioutil.ReadAll(resp.Body)
		return nil, fmt.Errorf("dialogue split returned %d: %s", resp.StatusCode, b)
	}

	var chatResp ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return nil, fmt.Errorf("decode dialogue JSON: %w", err)
	}

	if len(chatResp.Choices) == 0 {
		return nil, fmt.Errorf("no dialogue split returned")
	}

	content := strings.TrimSpace(chatResp.Choices[0].Message.Content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	var dialogueLines []DialogueLine
	if err := json.Unmarshal([]byte(content), &dialogueLines); err != nil {
		log.Printf("⚠️ Failed to parse dialogue JSON: %v", err)
		// Fallback: treat entire text as narrator
		return []DialogueLine{{
			Speaker:    "Narrator",
			Text:       cleanTextForNarration(bookText),
			IsDialogue: false,
		}}, nil
	}

	log.Printf("📝 Split into %d dialogue segments", len(dialogueLines))
	return dialogueLines, nil
}

// cleanTextForNarration removes problematic symbols and prepares text for TTS
func cleanTextForNarration(text string) string {
	// Remove symbols that shouldn't be read aloud
	text = strings.ReplaceAll(text, "<", "")
	text = strings.ReplaceAll(text, ">", "")
	text = strings.ReplaceAll(text, "{", "")
	text = strings.ReplaceAll(text, "}", "")
	text = strings.ReplaceAll(text, "[", "")
	text = strings.ReplaceAll(text, "]", "")
	text = strings.ReplaceAll(text, "|", "")
	text = strings.ReplaceAll(text, "\\", "")
	text = strings.ReplaceAll(text, "~", "")
	text = strings.ReplaceAll(text, "^", "")
	text = strings.ReplaceAll(text, "*", "")

	// Remove quotation marks (dialogue is already split)
	text = strings.ReplaceAll(text, "\"", "")
	text = strings.ReplaceAll(text, "\u201c", "") // Left double quotation mark
	text = strings.ReplaceAll(text, "\u201d", "") // Right double quotation mark
	text = strings.ReplaceAll(text, "\u2018", "'") // Normalize left single quote to apostrophe
	text = strings.ReplaceAll(text, "\u2019", "'") // Normalize right single quote to apostrophe

	// Keep natural punctuation: . , ! ? ; : ... —
	// These help the TTS engine add natural pauses and intonation

	return strings.TrimSpace(text)
}

// getVoiceForSpeaker returns the appropriate OpenAI voice for a speaker
func getVoiceForSpeaker(speaker string, characters []Character) string {
	// Check if speaker is a known character
	for _, char := range characters {
		if char.Name == speaker {
			return char.Voice
		}
	}

	// Default narrator voice
	if speaker == "Narrator" || speaker == "narrator" {
		return "alloy"
	}

	// Fallback
	return "nova"
}
