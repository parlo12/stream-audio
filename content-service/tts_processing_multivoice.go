package main

// tts_processing_multivoice.go
// Enhanced TTS processing with multi-voice character support

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
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gorm.io/gorm"
)

// TTSPayloadEnhanced uses the new instructions-based approach
type TTSPayloadEnhanced struct {
	Input          string  `json:"input"`
	Model          string  `json:"model"`
	Voice          string  `json:"voice"`
	Instructions   string  `json:"instructions,omitempty"`
	ResponseFormat string  `json:"response_format,omitempty"`
	Speed          float64 `json:"speed,omitempty"`
}

// generateNarrationInstructions creates natural narration instructions
func generateNarrationInstructions(isDialogue bool, characterName string, gender string, age string) string {
	if !isDialogue {
		// Narrator instructions
		return `You are an expressive audiobook narrator.
Speak naturally with appropriate pacing and emotion.
Pause briefly at commas and periods.
Use emphasis for important words.
Sound warm and engaging, like telling a story to a friend.
Do NOT read punctuation marks aloud - interpret them as natural pauses and intonation.`
	}

	// Character dialogue instructions
	baseInstruction := "You are voicing a character in an audiobook."

	// Add gender-specific guidance
	switch gender {
	case "male":
		baseInstruction += " Speak with a natural male voice."
	case "female":
		baseInstruction += " Speak with a natural female voice."
	case "child":
		baseInstruction += " Speak with a youthful, energetic voice."
	}

	// Add age-specific guidance
	switch age {
	case "child":
		baseInstruction += " Sound playful and expressive, like a child would speak."
	case "elderly":
		baseInstruction += " Speak with wisdom and a mature tone."
	case "adult":
		baseInstruction += " Speak naturally and confidently."
	}

	baseInstruction += `
Express emotions naturally through your voice.
Pause at punctuation for natural speech rhythm.
Do NOT say punctuation marks like 'question mark' or 'period' - just interpret them naturally.
Sound conversational and authentic.`

	return baseInstruction
}

// convertTextToAudioMultiVoice generates audio with character-appropriate voices
func convertTextToAudioMultiVoice(dialogueLine DialogueLine, voice string, outputPath string) error {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return errors.New("OPENAI_API_KEY not set")
	}

	// Clean text for narration (removes symbols)
	cleanedText := cleanTextForNarration(dialogueLine.Text)

	// Generate appropriate instructions
	instructions := generateNarrationInstructions(
		dialogueLine.IsDialogue,
		dialogueLine.Speaker,
		"adult", // Default, would come from character detection
		"adult",
	)

	payload := TTSPayloadEnhanced{
		Input:          cleanedText,
		Model:          "gpt-4o-mini-tts", // Latest model with better steerability
		Voice:          voice,
		Instructions:   instructions,
		ResponseFormat: "mp3",
		Speed:          1.0,
	}

	reqBody, _ := json.Marshal(payload)

	req, err := http.NewRequest("POST", openaiTTSEndpoint, bytes.NewReader(reqBody))
	if err != nil {
		return fmt.Errorf("create TTS request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 120 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("TTS API request error: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := ioutil.ReadAll(resp.Body)
		return fmt.Errorf("TTS API returned %d: %s", resp.StatusCode, body)
	}

	// Ensure output directory exists
	if err := os.MkdirAll(filepath.Dir(outputPath), 0755); err != nil {
		return err
	}

	outFile, err := os.Create(outputPath)
	if err != nil {
		return fmt.Errorf("create audio file: %w", err)
	}
	defer outFile.Close()

	if _, err := io.Copy(outFile, resp.Body); err != nil {
		return fmt.Errorf("write audio: %w", err)
	}

	log.Printf("🎙️ Generated audio: %s (Speaker: %s, Voice: %s)", outputPath, dialogueLine.Speaker, voice)
	return nil
}

// mergeAudioFiles combines multiple audio files into one using FFmpeg
func mergeAudioFiles(inputFiles []string, outputPath string) error {
	if len(inputFiles) == 0 {
		return fmt.Errorf("no audio files to merge")
	}

	if len(inputFiles) == 1 {
		// Just copy the single file
		return copyFile(inputFiles[0], outputPath)
	}

	// Create concat file for FFmpeg
	concatFile := "/tmp/concat_list.txt"
	var concatContent strings.Builder
	for _, file := range inputFiles {
		// FFmpeg concat requires absolute paths
		absPath, _ := filepath.Abs(file)
		concatContent.WriteString(fmt.Sprintf("file '%s'\n", absPath))
	}

	if err := os.WriteFile(concatFile, []byte(concatContent.String()), 0644); err != nil {
		return fmt.Errorf("create concat file: %w", err)
	}
	defer os.Remove(concatFile)

	// FFmpeg concat command
	cmd := exec.Command("ffmpeg",
		"-f", "concat",
		"-safe", "0",
		"-i", concatFile,
		"-c", "copy",
		"-y",
		outputPath,
	)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg merge failed: %w\nOutput: %s", err, output)
	}

	log.Printf("✅ Merged %d audio files into: %s", len(inputFiles), outputPath)
	return nil
}

// copyFile copies a file from src to dst
func copyFile(src, dst string) error {
	sourceFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer sourceFile.Close()

	destFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destFile.Close()

	_, err = io.Copy(destFile, sourceFile)
	return err
}

// processBookConversionWithCharacters processes a book with multi-voice character support
func processBookConversionWithCharacters(book Book) {
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

	bookText := string(contentBytes)

	// 4) Detect characters in the book
	log.Printf("🔍 Detecting characters for book ID %d...", book.ID)
	characters, err := detectCharacters(bookText)
	if err != nil {
		log.Printf("⚠️ Character detection failed for book ID %d: %v. Using default narrator.", book.ID, err)
		characters = []Character{{Name: "Narrator", Gender: "neutral", Voice: "alloy", Age: "adult"}}
	}

	// 5) Split text into dialogue lines
	log.Printf("📝 Splitting dialogue for book ID %d...", book.ID)
	dialogueLines, err := splitDialogue(bookText, characters)
	if err != nil {
		log.Printf("⚠️ Dialogue split failed for book ID %d: %v. Processing as single narrator.", book.ID, err)
		dialogueLines = []DialogueLine{{
			Speaker:    "Narrator",
			Text:       bookText,
			IsDialogue: false,
		}}
	}

	// 6) Generate audio for each dialogue line
	log.Printf("🎙️ Generating audio for %d dialogue segments...", len(dialogueLines))
	audioDir := fmt.Sprintf("./audio/book_%d_segments", book.ID)
	if err := os.MkdirAll(audioDir, 0755); err != nil {
		log.Printf("❌ Failed to create audio directory: %v", err)
		updateBookStatus(book.ID, "failed")
		return
	}

	var audioFiles []string
	for i, line := range dialogueLines {
		voice := getVoiceForSpeaker(line.Speaker, characters)
		segmentPath := fmt.Sprintf("%s/segment_%03d.mp3", audioDir, i)

		if err := convertTextToAudioMultiVoice(line, voice, segmentPath); err != nil {
			log.Printf("❌ Failed to generate audio for segment %d: %v", i, err)
			updateBookStatus(book.ID, "failed")
			return
		}

		audioFiles = append(audioFiles, segmentPath)
	}

	// 7) Merge all audio segments
	finalAudioPath := fmt.Sprintf("./audio/book_%d_complete.mp3", book.ID)
	log.Printf("🔧 Merging %d audio segments...", len(audioFiles))
	if err := mergeAudioFiles(audioFiles, finalAudioPath); err != nil {
		log.Printf("❌ Failed to merge audio files: %v", err)
		updateBookStatus(book.ID, "failed")
		return
	}

	log.Printf("✅ TTS audio file generated: %s for book ID %d", finalAudioPath, book.ID)

	// 8) Save TTS result before adding effects
	if err := db.Model(&Book{}).Where("id = ?", book.ID).Updates(map[string]interface{}{
		"audio_path": finalAudioPath,
		"status":     "TTS completed (multi-voice)",
	}).Error; err != nil {
		log.Printf("⚠️ Error updating TTS result for book ID %d: %v", book.ID, err)
		return
	}

	// 9) Launch sound effects and merging in the background
	log.Printf("🚀 Launching effects merge for book ID %d", book.ID)
	go processSoundEffectsAndMerge(book, book.ContentHash, nil)

	// 10) Clean up temporary segment files (optional)
	// Uncomment to delete temp files after merge:
	// os.RemoveAll(audioDir)
}
