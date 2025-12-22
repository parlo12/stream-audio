package main

// content-service/tts_processing.go
// This file handles TTS processing using OpenAI's API.
// It prepares text for expressive narration using GPT, then converts it to audio using OpenAI's TTS API.
// Note: OpenAI TTS does NOT support SSML - we use plain text with the "instructions" field for voice control.
// It also checks for existing audio files to avoid redundant processing.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"gorm.io/gorm"
)

const openaiTTSEndpoint = "https://api.openai.com/v1/audio/speech"

type TTSPayload struct {
	Input          string  `json:"input"`
	Model          string  `json:"model"`
	Voice          string  `json:"voice"`
	Instructions   string  `json:"instructions,omitempty"`
	ResponseFormat string  `json:"response_format,omitempty"`
	Speed          float64 `json:"speed,omitempty"`
}

// prepareNarratorText enhances raw text for expressive TTS narration
// OpenAI TTS does NOT support SSML, so we use plain text with natural pauses
func prepareNarratorText(rawText string) (string, error) {
	systemContent := `You are preparing text for an audiobook narrator. Your job is to enhance the text for natural, expressive reading.

Rules:
1. Output ONLY the enhanced plain text - no XML, no SSML, no tags
2. Add "..." for dramatic pauses where appropriate
3. Keep the original meaning and words intact
4. Add paragraph breaks between major scene changes
5. Do NOT add any markup, tags, or special formatting
6. Do NOT wrap in <speak> or any other tags
7. Do NOT output "xml" or any code block markers

Simply return the enhanced plain text ready to be read aloud.`

	reqBody := ChatRequest{
		Model: "gpt-4o",
		Messages: []ChatMessage{
			{Role: "system", Content: systemContent},
			{Role: "user", Content: rawText},
		},
		Temperature: 0.5,
		MaxTokens:   2000,
	}

	bodyBytes, _ := json.Marshal(reqBody)
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return "", errors.New("OPENAI_API_KEY not set")
	}

	req, _ := http.NewRequest("POST", openAIChatURL, bytes.NewReader(bodyBytes))
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("GPT text prep call failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := ioutil.ReadAll(resp.Body)
		return "", fmt.Errorf("GPT text prep returned %d: %s", resp.StatusCode, b)
	}

	var chatResp ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return "", fmt.Errorf("decode text prep JSON: %w", err)
	}
	if len(chatResp.Choices) == 0 {
		return "", errors.New("no text prep choices returned")
	}

	// Clean up any residual markup that GPT might have added
	text := strings.TrimSpace(chatResp.Choices[0].Message.Content)
	text = cleanupForTTS(text)

	log.Printf("📝 Prepared narrator text: %s", truncateLog(text, 200))
	return text, nil
}

// cleanupForTTS removes any XML/SSML tags and code block markers from text
func cleanupForTTS(text string) string {
	// Remove code block markers
	text = strings.ReplaceAll(text, "```xml", "")
	text = strings.ReplaceAll(text, "```ssml", "")
	text = strings.ReplaceAll(text, "```", "")

	// Remove common SSML/XML tags if GPT still adds them
	tagsToRemove := []string{
		"<speak>", "</speak>",
		"<break", "/>",
		"<emphasis>", "</emphasis>",
		"<prosody", "</prosody>",
		"rate=\"", "time=\"",
		"xml", "ssml",
	}
	for _, tag := range tagsToRemove {
		text = strings.ReplaceAll(text, tag, "")
	}

	// Clean up any remaining angle brackets that look like tags
	// This regex-like cleanup removes patterns like <...>
	result := strings.Builder{}
	inTag := false
	for _, ch := range text {
		if ch == '<' {
			inTag = true
			continue
		}
		if ch == '>' {
			inTag = false
			continue
		}
		if !inTag {
			result.WriteRune(ch)
		}
	}
	text = result.String()

	// Clean up extra whitespace
	text = strings.TrimSpace(text)

	// Replace multiple spaces with single space
	for strings.Contains(text, "  ") {
		text = strings.ReplaceAll(text, "  ", " ")
	}

	return text
}

// truncateLog truncates a string for logging purposes
func truncateLog(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func convertTextToAudio(text string, bookID uint) (string, error) {
	// Prepare text for narration (no SSML - OpenAI TTS doesn't support it)
	narratorText, err := prepareNarratorText(text)
	if err != nil {
		// Fall back to original text if preparation fails
		log.Printf("⚠️ Text preparation failed, using original: %v", err)
		narratorText = text
	}

	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return "", errors.New("OPENAI_API_KEY not set")
	}

	// Use instructions to guide expressive narration instead of SSML
	instructions := `You are an expressive audiobook narrator. Read with emotion and drama:
- Pause naturally at sentence endings and paragraph breaks
- Use varied pacing: slower for emotional moments, faster for action
- Emphasize key words and phrases
- Convey character emotions through tone
- Add subtle pauses at ellipses (...)`

	payload := TTSPayload{
		Input:          narratorText,
		Model:          "gpt-4o-mini-tts",
		Voice:          "alloy",
		Instructions:   instructions,
		ResponseFormat: "mp3",
		Speed:          1.0,
	}
	reqBody, _ := json.Marshal(payload)

	req, err := http.NewRequest("POST", openaiTTSEndpoint, bytes.NewReader(reqBody))
	if err != nil {
		return "", fmt.Errorf("create TTS request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("TTS API request error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := ioutil.ReadAll(resp.Body)
		return "", fmt.Errorf("TTS API returned %d: %s", resp.StatusCode, body)
	}

	if err := os.MkdirAll("./audio", 0755); err != nil {
		return "", err
	}

	filename := fmt.Sprintf("audio_%d.mp3", bookID)
	path := "./audio/" + filename

	outFile, err := os.Create(path)
	if err != nil {
		return "", fmt.Errorf("create audio file: %w", err)
	}
	defer outFile.Close()

	if _, err := io.Copy(outFile, resp.Body); err != nil {
		return "", fmt.Errorf("write audio: %w", err)
	}
	return path, nil
}

func processBookConversion(book Book) {
	// 0) Ensure file exists
	if _, err := os.Stat(book.FilePath); os.IsNotExist(err) {
		log.Printf("🚫 File does not exist for book ID %d: %s", book.ID, book.FilePath)
		updateBookStatus(book.ID, "failed")
		return
	}

	// 1) Compute content hash if not already stored
	if book.ContentHash == "" {
		hash, err := computeFileHash(book.FilePath)
		if err != nil {
			log.Printf("❌ Failed to compute content hash for book ID %d: %v", book.ID, err)
			updateBookStatus(book.ID, "failed")
			return
		}
		book.ContentHash = hash
		if err := db.Model(&Book{}).Where("id = ?", book.ID).Update("content_hash", hash).Error; err != nil {
			log.Printf("⚠️ Failed to save content hash: %v", err)
		}
	}

	// 2) Check if audio already exists for this content hash
	var dup Book
	err := db.Where("content_hash = ? AND audio_path IS NOT NULL AND audio_path <> ''", book.ContentHash).First(&dup).Error
	if err == nil {
		log.Printf("🔁 Reusing audio from book ID %d for book ID %d", dup.ID, book.ID)
		if err := db.Model(&Book{}).Where("id = ?", book.ID).Updates(Book{
			AudioPath: dup.AudioPath,
			Status:    "TTS reused",
		}).Error; err != nil {
			log.Printf("⚠️ Error saving reused audio for book ID %d: %v", book.ID, err)
		}
		return
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		log.Printf("⚠️ Error checking for existing audio: %v", err)
	}

	// 3) Read file content
	contentBytes, err := os.ReadFile(book.FilePath)
	if err != nil {
		log.Printf("📛 Error reading file for book ID %d: %v", book.ID, err)
		updateBookStatus(book.ID, "failed")
		return
	}

	// 4) Convert to TTS
	ttsPath, err := convertTextToAudio(string(contentBytes), book.ID)
	if err != nil {
		log.Printf("🎙️ Error converting text to audio for book ID %d: %v", book.ID, err)
		updateBookStatus(book.ID, "failed")
		return
	}
	log.Printf("✅ TTS audio file generated: %s for book ID %d", ttsPath, book.ID)

	// 5) Save TTS result before adding effects
	if err := db.Model(&Book{}).Where("id = ?", book.ID).Updates(map[string]interface{}{
		"audio_path": ttsPath,
		"status":     "TTS completed",
	}).Error; err != nil {
		log.Printf("⚠️ Error updating TTS result for book ID %d: %v", book.ID, err)
		return
	}

	// 6) Launch sound effects and merging in the background
	log.Printf("🚀 Launching effects merge with hash: %s for book ID %d", book.ContentHash, book.ID)
	go processSoundEffectsAndMerge(book, book.ContentHash, nil)
}

// updateBookStatus updates the status of a book in the database.
func updateBookStatus(bookID uint, status string) {
	var book Book
	if err := db.First(&book, bookID).Error; err != nil {
		log.Printf("Error finding book with ID %d: %v", bookID, err)
		return
	}

	if err := db.Model(&Book{}).Where("id = ?", book.ID).Update("status", status).Error; err != nil {
		log.Printf("Error updating status for book ID %d: %v", book.ID, err)
	}
}
