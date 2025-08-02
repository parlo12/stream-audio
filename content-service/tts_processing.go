package main

// content-service/tts_processing.go
// this file handles TTS processing using OpenAI's API.
// It generates SSML from plain text using GPT, then converts that SSML to audio using OpenAI's TTS API.
// It also checks for existing audio files to avoid redundant processing.
// It processes books in the database, converting their text content to audio files.

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

func wrapSSML(text string) string {
	t := strings.TrimSpace(text)
	if strings.HasPrefix(t, "<speak") {
		return t
	}
	return "<speak>\n" + t + "\n</speak>"
}

const openaiTTSEndpoint = "https://api.openai.com/v1/audio/speech"

type TTSPayload struct {
	Input          string  `json:"input"`
	InputFormat    string  `json:"input_format,omitempty"`
	Model          string  `json:"model"`
	Voice          string  `json:"voice"`
	Instructions   string  `json:"instructions,omitempty"`
	ResponseFormat string  `json:"response_format,omitempty"`
	Speed          float64 `json:"speed,omitempty"`
}

func generateSSML(rawText string) (string, error) {
	systemContent := `You are an expressive audiobook narrator.
Convert this into SSML:
- Use <break time="500ms"/> at natural pauses
- Wrap key phrases in <emphasis>
- Use <prosody rate="80%">…</prosody> for sad passages
- Use <prosody rate="110%">…</prosody> for action passages
Output only the SSML wrapped in one <speak>…</speak> block.`

	reqBody := ChatRequest{
		Model: "gpt-4o",
		Messages: []ChatMessage{
			{Role: "system", Content: systemContent},
			{Role: "user", Content: rawText},
		},
		Temperature: 0.7,
		MaxTokens:   1500,
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
		return "", fmt.Errorf("GPT SSML call failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := ioutil.ReadAll(resp.Body)
		return "", fmt.Errorf("GPT SSML returned %d: %s", resp.StatusCode, b)
	}

	var chatResp ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return "", fmt.Errorf("decode SSML JSON: %w", err)
	}
	if len(chatResp.Choices) == 0 {
		return "", errors.New("no SSML choices returned")
	}

	raw := strings.TrimSpace(chatResp.Choices[0].Message.Content)
	raw = strings.ReplaceAll(raw, "```", "")
	raw = strings.ReplaceAll(raw, "```ssml", "")
	raw = strings.ReplaceAll(raw, "```xml", "")
	raw = strings.TrimPrefix(raw, "```xml")
	raw = strings.ReplaceAll(raw, "```xml ssml", "")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	ssml := wrapSSML(raw)
	log.Printf("SSML: %s", ssml)
	return ssml, nil
}

func convertTextToAudio(text string, bookID uint) (string, error) {
	ssml, err := generateSSML(text)
	if err != nil {
		return "", fmt.Errorf("SSML generation failed: %w", err)
	}
	ssml = wrapSSML(ssml)

	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return "", errors.New("OPENAI_API_KEY not set")
	}

	payload := TTSPayload{
		Input:          ssml,
		Model:          "gpt-4o-mini-tts",
		Voice:          "alloy",
		Instructions:   "Interpret SSML with breaks, prosody, emphasis. Do not speak tags.",
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
