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

	// Read response body for debugging
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	// Log raw response for debugging (first 500 chars)
	if len(bodyBytes) > 500 {
		log.Printf("OpenAI Response (truncated): %s...", string(bodyBytes[:500]))
	} else {
		log.Printf("OpenAI Response: %s", string(bodyBytes))
	}

	var apiResponse ResponsesAPIResponse
	if err := json.Unmarshal(bodyBytes, &apiResponse); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}

	// Extract the image URL from the response
	imageURL := extractImageURLFromResponse(&apiResponse)
	if imageURL == "" {
		// Log the response structure for debugging
		log.Printf("⚠️ Could not extract URL from response. Output items count: %d", len(apiResponse.Output))
		if len(apiResponse.Output) > 0 {
			log.Printf("First output item type: %s", apiResponse.Output[0].Type)
		}
		return "", fmt.Errorf("no valid image URL found in response")
	}

	log.Printf("✅ Found book cover URL: %s", imageURL)
	return imageURL, nil
}

// isImageURL checks if a URL points to an image
func isImageURL(url string) bool {
	urlLower := ""
	for i := 0; i < len(url); i++ {
		c := url[i]
		if c >= 'A' && c <= 'Z' {
			urlLower += string(c + 32) // Convert to lowercase
		} else {
			urlLower += string(c)
		}
	}

	return containsAny(urlLower, []string{".jpg", ".jpeg", ".png", ".webp", ".gif"})
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

// extractURLFromText extracts a valid image URL from text
func extractURLFromText(text string) string {
	// Try to find URLs in the text using simple pattern matching
	// Look for http:// or https:// followed by characters until whitespace

	httpIndex := -1
	for i := 0; i < len(text)-7; i++ {
		if text[i:i+7] == "http://" || (i < len(text)-8 && text[i:i+8] == "https://") {
			httpIndex = i
			break
		}
	}

	if httpIndex == -1 {
		return ""
	}

	// Find the end of the URL (whitespace, newline, or end of string)
	endIndex := len(text)
	for i := httpIndex; i < len(text); i++ {
		c := text[i]
		if c == ' ' || c == '\n' || c == '\r' || c == '\t' || c == ')' || c == ']' {
			endIndex = i
			break
		}
	}

	url := text[httpIndex:endIndex]

	// Validate it's an image URL
	if isImageURL(url) {
		return cleanURL(url)
	}

	return ""
}

// extractImageURLFromResponse parses the OpenAI Responses API output to find the image URL
func extractImageURLFromResponse(response *ResponsesAPIResponse) string {
	// First, try to extract from output_text (simplest case)
	if response.OutputText != "" {
		url := extractURLFromText(response.OutputText)
		if url != "" {
			return url
		}
		// Also check for known CDN URLs
		url = extractCDNURLFromText(response.OutputText)
		if url != "" {
			return url
		}
	}

	// Otherwise, parse the output items
	for _, item := range response.Output {
		if item.Type == "message" && len(item.Content) > 0 {
			for _, content := range item.Content {
				if content.Type == "output_text" && content.Text != "" {
					log.Printf("📝 Parsing message content: %s...", truncateString(content.Text, 200))

					url := extractURLFromText(content.Text)
					if url != "" {
						return url
					}
					// Also check for known CDN URLs
					url = extractCDNURLFromText(content.Text)
					if url != "" {
						return url
					}
				}

				// Also check annotations for URLs
				for _, annotation := range content.Annotations {
					if annotation.Type == "url_citation" && annotation.URL != "" {
						log.Printf("📎 Found annotation URL: %s", annotation.URL)
						// Check if this URL is an image or from a known CDN
						if isImageURL(annotation.URL) || isKnownImageCDN(annotation.URL) {
							return annotation.URL
						}
					}
				}
			}
		}

		// Check sources from web_search_call actions
		if item.Type == "web_search_call" && item.Action != nil {
			log.Printf("🔍 Found %d web search sources", len(item.Action.Sources))
			for _, source := range item.Action.Sources {
				if source.URL != "" {
					log.Printf("   Source URL: %s", truncateString(source.URL, 100))
					if isImageURL(source.URL) || isKnownImageCDN(source.URL) {
						return source.URL
					}
				}
			}
		}
	}

	return ""
}

// truncateString truncates a string to maxLen characters
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// extractCDNURLFromText finds URLs from known book cover CDNs in text
func extractCDNURLFromText(text string) string {
	cdnPatterns := []string{
		"images-na.ssl-images-amazon.com",
		"m.media-amazon.com",
		"images.gr-assets.com",
		"i.gr-assets.com",
		"books.google.com",
		"covers.openlibrary.org",
		"prodimage.images-bn.com",
	}

	for _, cdn := range cdnPatterns {
		idx := -1
		for i := 0; i <= len(text)-len(cdn); i++ {
			if text[i:i+len(cdn)] == cdn {
				idx = i
				break
			}
		}
		if idx == -1 {
			continue
		}

		// Find the start of the URL (look backwards for http)
		start := idx
		for start > 0 && !(text[start:start+4] == "http") {
			start--
		}

		// Find the end of the URL
		end := idx + len(cdn)
		for end < len(text) {
			c := text[end]
			if c == ' ' || c == '\n' || c == '\r' || c == '"' || c == '\'' || c == ')' || c == ']' || c == '>' {
				break
			}
			end++
		}

		if start >= 0 && end > start {
			url := text[start:end]
			if len(url) > 10 { // basic validation
				return url
			}
		}
	}

	return ""
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
