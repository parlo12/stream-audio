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
	"os/exec"
	"strings"
	"time"

	"gorm.io/gorm"
)

const openaiTTSEndpoint = "https://api.openai.com/v1/audio/speech"

// Voice constants for different speaker types
const (
	VoiceNarrator = "alloy"  // Neutral voice for narration
	VoiceMale     = "onyx"   // Deep male voice for male characters
	VoiceFemale   = "nova"   // Female voice for female characters
)

type TTSPayload struct {
	Input          string  `json:"input"`
	Model          string  `json:"model"`
	Voice          string  `json:"voice"`
	Instructions   string  `json:"instructions,omitempty"`
	ResponseFormat string  `json:"response_format,omitempty"`
	Speed          float64 `json:"speed,omitempty"`
}

// DialogueSegment represents a segment of text with speaker info
type DialogueSegment struct {
	Type       string `json:"type"`        // "narrator", "dialogue"
	Speaker    string `json:"speaker"`     // Character name (empty for narrator)
	Gender     string `json:"gender"`      // "male", "female", "unknown"
	Text       string `json:"text"`        // The actual text to speak
	IsDialogue bool   `json:"is_dialogue"` // True if character is speaking
}

// DialogueAnalysis is the response from GPT for dialogue parsing
type DialogueAnalysis struct {
	Segments []DialogueSegment `json:"segments"`
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

// analyzeDialogue uses GPT to parse text into narrator and character dialogue segments
func analyzeDialogue(rawText string) ([]DialogueSegment, error) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return nil, errors.New("OPENAI_API_KEY not set")
	}

	systemContent := `You are analyzing text for an audiobook production. Your job is to split the text into segments for different voice actors.

IMPORTANT RULES:
1. Identify dialogue (text in quotes) vs narration (everything else)
2. For each dialogue segment, determine the speaker's gender (male/female/unknown)
3. Look for context clues: "he said", "she replied", character names, pronouns
4. Dialogue should be read in FIRST PERSON by the character (just the words they speak)
5. Narration includes dialogue tags like "he said" or "she whispered"
6. Keep segments in the exact order they appear in the text
7. Do NOT modify the original text content

Return a JSON object with this exact structure:
{
  "segments": [
    {"type": "narrator", "speaker": "", "gender": "", "text": "The knight approached slowly.", "is_dialogue": false},
    {"type": "dialogue", "speaker": "Knight", "gender": "male", "text": "Who goes there?", "is_dialogue": true},
    {"type": "narrator", "speaker": "", "gender": "", "text": "he demanded.", "is_dialogue": false},
    {"type": "dialogue", "speaker": "Princess", "gender": "female", "text": "It is I, the princess.", "is_dialogue": true}
  ]
}

Return ONLY valid JSON, no other text or markdown.`

	reqBody := ChatRequest{
		Model: "gpt-4o",
		Messages: []ChatMessage{
			{Role: "system", Content: systemContent},
			{Role: "user", Content: rawText},
		},
		Temperature: 0.3,
		MaxTokens:   4000,
	}

	bodyBytes, _ := json.Marshal(reqBody)

	req, _ := http.NewRequest("POST", openAIChatURL, bytes.NewReader(bodyBytes))
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("dialogue analysis call failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := ioutil.ReadAll(resp.Body)
		return nil, fmt.Errorf("dialogue analysis returned %d: %s", resp.StatusCode, b)
	}

	var chatResp ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return nil, fmt.Errorf("decode dialogue analysis JSON: %w", err)
	}
	if len(chatResp.Choices) == 0 {
		return nil, errors.New("no dialogue analysis choices returned")
	}

	// Parse the JSON response
	responseText := strings.TrimSpace(chatResp.Choices[0].Message.Content)
	// Remove any markdown code block markers
	responseText = strings.TrimPrefix(responseText, "```json")
	responseText = strings.TrimPrefix(responseText, "```")
	responseText = strings.TrimSuffix(responseText, "```")
	responseText = strings.TrimSpace(responseText)

	var analysis DialogueAnalysis
	if err := json.Unmarshal([]byte(responseText), &analysis); err != nil {
		log.Printf("⚠️ Failed to parse dialogue analysis, using fallback: %v", err)
		// Fallback: return entire text as narrator segment
		return []DialogueSegment{{
			Type:       "narrator",
			Speaker:    "",
			Gender:     "",
			Text:       rawText,
			IsDialogue: false,
		}}, nil
	}

	log.Printf("🎭 Analyzed dialogue: %d segments found", len(analysis.Segments))
	return analysis.Segments, nil
}

// getVoiceForSegment returns the appropriate voice based on segment type and gender
func getVoiceForSegment(segment DialogueSegment) string {
	if !segment.IsDialogue || segment.Type == "narrator" {
		return VoiceNarrator
	}

	switch strings.ToLower(segment.Gender) {
	case "male":
		return VoiceMale
	case "female":
		return VoiceFemale
	default:
		return VoiceNarrator
	}
}

// getInstructionsForSegment returns voice instructions based on segment type
func getInstructionsForSegment(segment DialogueSegment) string {
	if segment.IsDialogue {
		switch strings.ToLower(segment.Gender) {
		case "male":
			return `You are voicing a male character in an audiobook. Speak in FIRST PERSON as this character:
- Use a natural male speaking voice
- Convey the character's emotions through tone
- Speak as if YOU are this character saying these words
- Be expressive and dramatic when appropriate`
		case "female":
			return `You are voicing a female character in an audiobook. Speak in FIRST PERSON as this character:
- Use a natural female speaking voice
- Convey the character's emotions through tone
- Speak as if YOU are this character saying these words
- Be expressive and dramatic when appropriate`
		default:
			return `You are voicing a character in an audiobook. Speak in FIRST PERSON:
- Convey emotions through your tone
- Be expressive and natural`
		}
	}

	// Narrator instructions
	return `You are an audiobook narrator. Read with expression:
- Pause naturally at sentence endings
- Use varied pacing for different moods
- Maintain a clear, engaging narration style`
}

// generateSegmentAudio generates audio for a single dialogue segment
func generateSegmentAudio(segment DialogueSegment, bookID uint, segmentIndex int) (string, error) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return "", errors.New("OPENAI_API_KEY not set")
	}

	text := cleanupForTTS(segment.Text)
	if strings.TrimSpace(text) == "" {
		return "", nil // Skip empty segments
	}

	voice := getVoiceForSegment(segment)
	instructions := getInstructionsForSegment(segment)

	log.Printf("🎙️ Generating segment %d: voice=%s, type=%s, speaker=%s", segmentIndex, voice, segment.Type, segment.Speaker)

	payload := TTSPayload{
		Input:          text,
		Model:          "gpt-4o-mini-tts",
		Voice:          voice,
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

	filename := fmt.Sprintf("segment_%d_%d.mp3", bookID, segmentIndex)
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

// mergeAudioSegments concatenates multiple audio files using FFmpeg
func mergeAudioSegments(segmentPaths []string, outputPath string) error {
	if len(segmentPaths) == 0 {
		return errors.New("no segments to merge")
	}

	if len(segmentPaths) == 1 {
		// Just copy the single file
		input, err := os.ReadFile(segmentPaths[0])
		if err != nil {
			return err
		}
		return os.WriteFile(outputPath, input, 0644)
	}

	// Create a file list for FFmpeg concat
	// The list file will be in the same directory as segments, so use just filenames
	listPath := "./audio/concat_list.txt"
	var listContent strings.Builder
	for _, path := range segmentPaths {
		// Extract just the filename since concat list is relative to its location
		// path is like "./audio/segment_X_Y.mp3", we need just "segment_X_Y.mp3"
		filename := path
		if strings.HasPrefix(path, "./audio/") {
			filename = strings.TrimPrefix(path, "./audio/")
		} else if idx := strings.LastIndex(path, "/"); idx >= 0 {
			filename = path[idx+1:]
		}
		listContent.WriteString(fmt.Sprintf("file '%s'\n", filename))
	}
	if err := os.WriteFile(listPath, []byte(listContent.String()), 0644); err != nil {
		return fmt.Errorf("create concat list: %w", err)
	}
	defer os.Remove(listPath)

	// Use FFmpeg to concatenate
	cmd := exec.Command("ffmpeg", "-y", "-f", "concat", "-safe", "0", "-i", listPath, "-c", "copy", outputPath)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("ffmpeg concat failed: %w, output: %s", err, string(output))
	}

	log.Printf("✅ Merged %d segments into %s", len(segmentPaths), outputPath)
	return nil
}

// convertTextToAudioMultiVoice converts text to audio with different voices for characters
func convertTextToAudioMultiVoice(text string, bookID uint) (string, error) {
	log.Printf("🎭 Starting multi-voice TTS for book %d", bookID)

	// Step 1: Analyze dialogue to identify speakers and genders
	segments, err := analyzeDialogue(text)
	if err != nil {
		log.Printf("⚠️ Dialogue analysis failed, falling back to single voice: %v", err)
		return convertTextToAudioSingleVoice(text, bookID)
	}

	if len(segments) == 0 {
		log.Printf("⚠️ No segments found, falling back to single voice")
		return convertTextToAudioSingleVoice(text, bookID)
	}

	// Step 2: Generate audio for each segment
	var segmentPaths []string
	for i, segment := range segments {
		if strings.TrimSpace(segment.Text) == "" {
			continue
		}

		path, err := generateSegmentAudio(segment, bookID, i)
		if err != nil {
			log.Printf("⚠️ Failed to generate segment %d: %v", i, err)
			continue
		}
		if path != "" {
			segmentPaths = append(segmentPaths, path)
		}
	}

	if len(segmentPaths) == 0 {
		log.Printf("⚠️ No audio segments generated, falling back to single voice")
		return convertTextToAudioSingleVoice(text, bookID)
	}

	// Step 3: Merge all segments into final audio
	if err := os.MkdirAll("./audio", 0755); err != nil {
		return "", err
	}

	finalPath := fmt.Sprintf("./audio/audio_%d.mp3", bookID)
	if err := mergeAudioSegments(segmentPaths, finalPath); err != nil {
		log.Printf("⚠️ Failed to merge segments: %v", err)
		// Try to return the first segment at least
		if len(segmentPaths) > 0 {
			return segmentPaths[0], nil
		}
		return "", err
	}

	// Clean up individual segment files
	for _, path := range segmentPaths {
		os.Remove(path)
	}

	log.Printf("✅ Multi-voice TTS completed for book %d: %s", bookID, finalPath)
	return finalPath, nil
}

// convertTextToAudioSingleVoice is the fallback single-voice TTS (original behavior)
func convertTextToAudioSingleVoice(text string, bookID uint) (string, error) {
	// Prepare text for narration
	narratorText, err := prepareNarratorText(text)
	if err != nil {
		log.Printf("⚠️ Text preparation failed, using original: %v", err)
		narratorText = text
	}

	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return "", errors.New("OPENAI_API_KEY not set")
	}

	instructions := `You are an expressive audiobook narrator. Read with emotion and drama:
- Pause naturally at sentence endings and paragraph breaks
- Use varied pacing: slower for emotional moments, faster for action
- Emphasize key words and phrases
- Convey character emotions through tone
- Add subtle pauses at ellipses (...)`

	payload := TTSPayload{
		Input:          narratorText,
		Model:          "gpt-4o-mini-tts",
		Voice:          VoiceNarrator,
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

// convertTextToAudio is the main entry point for TTS conversion
// It uses multi-voice system with different voices for male/female characters
func convertTextToAudio(text string, bookID uint) (string, error) {
	// Use multi-voice TTS system for character dialogue
	return convertTextToAudioMultiVoice(text, bookID)
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
