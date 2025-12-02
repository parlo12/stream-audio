// ===============
// File: bookCoverWebSearch.go
// Description: Fetches book covers from the web using OpenAI's Responses API with web search
// ===============
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// OpenAI Responses API structures
type ResponsesRequest struct {
	Model  string                   `json:"model"`
	Tools  []ResponseTool           `json:"tools"`
	Input  string                   `json:"input"`
	Include []string                `json:"include,omitempty"`
}

type ResponseTool struct {
	Type    string          `json:"type"`
	Filters *SearchFilters  `json:"filters,omitempty"`
}

type SearchFilters struct {
	AllowedDomains []string `json:"allowed_domains,omitempty"`
}

type ResponsesAPIResponse struct {
	Output       []OutputItem `json:"output"`
	OutputText   string       `json:"output_text,omitempty"`
}

type OutputItem struct {
	Type    string        `json:"type"`
	ID      string        `json:"id,omitempty"`
	Status  string        `json:"status,omitempty"`
	Role    string        `json:"role,omitempty"`
	Content []ContentItem `json:"content,omitempty"`
	Action  *SearchAction `json:"action,omitempty"`
}

type ContentItem struct {
	Type        string       `json:"type"`
	Text        string       `json:"text,omitempty"`
	Annotations []Annotation `json:"annotations,omitempty"`
}

type Annotation struct {
	Type       string `json:"type"`
	StartIndex int    `json:"start_index"`
	EndIndex   int    `json:"end_index"`
	URL        string `json:"url"`
	Title      string `json:"title"`
}

type SearchAction struct {
	Type    string   `json:"type"`
	Query   string   `json:"query,omitempty"`
	Sources []Source `json:"sources,omitempty"`
}

type Source struct {
	URL   string `json:"url"`
	Title string `json:"title"`
}

// ImageSearchResult contains the URL and metadata for a book cover image
type ImageSearchResult struct {
	URL    string
	Title  string
	Width  int
	Height int
}

// fetchBookCoverFromWeb queries the web for a book cover matching the given title and author
// It uses OpenAI's Responses API with web search capability
// Returns the image URL and any error encountered
func fetchBookCoverFromWeb(title, author string) (string, error) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return "", fmt.Errorf("OPENAI_API_KEY environment variable not set")
	}

	// Construct a precise search query for book covers
	searchPrompt := fmt.Sprintf(
		`Find the official book cover image for the book titled "%s" by %s.
The image must be:
- The official book cover (not fan art or unauthorized versions)
- High resolution with dimensions approximately 1000px × 1600px (aspect ratio 0.625)
- From a reputable source (Amazon, Goodreads, publisher websites, or book retailers)
- A direct image URL ending in .jpg, .jpeg, or .png

Return ONLY the direct image URL on a single line. Do not include any explanations, markdown formatting, or additional text.`,
		title, author)

	requestBody := ResponsesRequest{
		Model: "gpt-4o",
		Tools: []ResponseTool{
			{
				Type: "web_search",
			},
		},
		Input: searchPrompt,
		Include: []string{"web_search_call.action.sources"},
	}

	jsonBody, err := json.Marshal(requestBody)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", "https://api.openai.com/v1/responses", bytes.NewBuffer(jsonBody))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("OpenAI API error (status %d): %s", resp.StatusCode, string(body))
	}

	var apiResponse ResponsesAPIResponse
	if err := json.NewDecoder(resp.Body).Decode(&apiResponse); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	// Extract the image URL from the response
	imageURL := extractImageURLFromResponse(&apiResponse)
	if imageURL == "" {
		return "", fmt.Errorf("no valid image URL found in response")
	}

	log.Printf("✅ Found book cover URL: %s", imageURL)
	return imageURL, nil
}

// extractImageURLFromResponse parses the OpenAI Responses API output to find the image URL
func extractImageURLFromResponse(response *ResponsesAPIResponse) string {
	// First, try to extract from output_text (simplest case)
	if response.OutputText != "" {
		url := extractURLFromText(response.OutputText)
		if url != "" {
			return url
		}
	}

	// Otherwise, parse the output items
	for _, item := range response.Output {
		if item.Type == "message" && len(item.Content) > 0 {
			for _, content := range item.Content {
				if content.Type == "output_text" && content.Text != "" {
					url := extractURLFromText(content.Text)
					if url != "" {
						return url
					}
				}
			}
		}
	}

	return ""
}

// extractURLFromText extracts a valid image URL from text
func extractURLFromText(text string) string {
	// Simple validation: check if text contains a URL pattern
	// Look for URLs ending in common image extensions
	if len(text) > 10 &&
	   (len(text) < 500) && // Reasonable URL length
	   (containsAny(text, []string{".jpg", ".jpeg", ".png"})) &&
	   (containsAny(text, []string{"http://", "https://"})) {

		// Clean up the URL (remove any markdown, whitespace, etc.)
		cleaned := cleanURL(text)
		return cleaned
	}
	return ""
}

// containsAny checks if the string contains any of the given substrings
func containsAny(s string, substrs []string) bool {
	for _, substr := range substrs {
		if len(s) >= len(substr) {
			for i := 0; i <= len(s)-len(substr); i++ {
				if s[i:i+len(substr)] == substr {
					return true
				}
			}
		}
	}
	return false
}

// cleanURL removes markdown formatting and whitespace from URLs
func cleanURL(url string) string {
	// Remove leading/trailing whitespace
	cleaned := ""
	start := 0
	end := len(url)

	// Trim leading whitespace
	for start < len(url) && (url[start] == ' ' || url[start] == '\n' || url[start] == '\t') {
		start++
	}

	// Trim trailing whitespace
	for end > start && (url[end-1] == ' ' || url[end-1] == '\n' || url[end-1] == '\t') {
		end--
	}

	cleaned = url[start:end]

	// Remove markdown link formatting if present: [text](url) -> url
	if len(cleaned) > 4 && cleaned[0] == '[' {
		// Find the ]( pattern
		for i := 1; i < len(cleaned)-1; i++ {
			if cleaned[i] == ']' && i+1 < len(cleaned) && cleaned[i+1] == '(' {
				// Extract URL from parentheses
				start := i + 2
				end := len(cleaned)
				if end > start && cleaned[end-1] == ')' {
					end--
				}
				if start < end {
					cleaned = cleaned[start:end]
				}
				break
			}
		}
	}

	return cleaned
}

// downloadAndSaveImage downloads an image from a URL and saves it to the local filesystem
// Returns the local file path and any error encountered
func downloadAndSaveImage(imageURL, bookID string) (string, error) {
	// Download the image
	resp, err := http.Get(imageURL)
	if err != nil {
		return "", fmt.Errorf("failed to download image: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to download image: HTTP status %d", resp.StatusCode)
	}

	// Read image data
	imageData, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read image data: %w", err)
	}

	// Validate minimum image size (should be at least a few KB for a real cover)
	if len(imageData) < 5000 {
		return "", fmt.Errorf("downloaded image is too small (%d bytes), likely invalid", len(imageData))
	}

	// Determine file extension from URL or Content-Type
	ext := ".jpg"
	if contentType := resp.Header.Get("Content-Type"); contentType != "" {
		switch contentType {
		case "image/png":
			ext = ".png"
		case "image/jpeg", "image/jpg":
			ext = ".jpg"
		}
	} else {
		// Fallback: detect from URL
		urlLower := imageURL
		if containsAny(urlLower, []string{".png"}) {
			ext = ".png"
		}
	}

	// Create uploads/covers directory if it doesn't exist
	uploadDir := "./uploads/covers"
	if err := os.MkdirAll(uploadDir, os.ModePerm); err != nil {
		return "", fmt.Errorf("failed to create upload directory: %w", err)
	}

	// Generate filename
	filename := fmt.Sprintf("%s_%d%s", bookID, time.Now().Unix(), ext)
	filePath := filepath.Join(uploadDir, filename)

	// Save the image
	if err := os.WriteFile(filePath, imageData, 0644); err != nil {
		return "", fmt.Errorf("failed to save image: %w", err)
	}

	log.Printf("✅ Book cover downloaded and saved: %s", filePath)
	return filePath, nil
}

// fetchAndSaveBookCover is the main entry point for fetching and saving a book cover
// It searches the web for the cover, downloads it, and returns the local path and public URL
func fetchAndSaveBookCover(title, author, bookID string) (localPath string, publicURL string, err error) {
	// Step 1: Search for the book cover
	imageURL, err := fetchBookCoverFromWeb(title, author)
	if err != nil {
		return "", "", fmt.Errorf("failed to find book cover: %w", err)
	}

	// Step 2: Download and save the image
	localPath, err = downloadAndSaveImage(imageURL, bookID)
	if err != nil {
		return "", "", fmt.Errorf("failed to download image: %w", err)
	}

	// Step 3: Generate public URL
	host := getEnv("STREAM_HOST", "http://localhost:8083")
	filename := filepath.Base(localPath)
	publicURL = fmt.Sprintf("%s/covers/%s", host, filename)

	return localPath, publicURL, nil
}
