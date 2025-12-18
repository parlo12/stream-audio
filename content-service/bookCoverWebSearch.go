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
	"regexp"
	"strings"
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

	// Log raw response for debugging (first 2000 chars to see more of the structure)
	if len(bodyBytes) > 2000 {
		log.Printf("OpenAI Response (truncated): %s...", string(bodyBytes[:2000]))
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

// URL regex pattern for extracting URLs from text
var urlRegex = regexp.MustCompile(`https?://[^\s\)\]"'<>]+`)

// isImageURL checks if a URL points to an image
func isImageURL(url string) bool {
	urlLower := strings.ToLower(url)
	imageExtensions := []string{".jpg", ".jpeg", ".png", ".webp", ".gif"}
	for _, ext := range imageExtensions {
		if strings.Contains(urlLower, ext) {
			return true
		}
	}
	return false
}

// isImageCDNURL checks if URL is from a known book cover CDN
func isImageCDNURL(url string) bool {
	cdns := []string{
		"images-na.ssl-images-amazon.com",
		"m.media-amazon.com",
		"images.gr-assets.com",
		"i.gr-assets.com",
		"books.google.com",
		"covers.openlibrary.org",
		"prodimage.images-bn.com",
		"images-us.bookshop.org",
		"img.thriftbooks.com",
	}
	for _, cdn := range cdns {
		if strings.Contains(url, cdn) {
			return true
		}
	}
	return false
}

// cleanURL removes trailing punctuation and cleans up URL
func cleanURL(url string) string {
	url = strings.TrimSpace(url)
	// Remove trailing punctuation that might have been captured
	url = strings.TrimSuffix(url, ",")
	url = strings.TrimSuffix(url, ".")
	url = strings.TrimSuffix(url, ";")
	return url
}

// extractAllURLsFromText extracts all URLs from text using regex
func extractAllURLsFromText(text string) []string {
	matches := urlRegex.FindAllString(text, -1)
	var urls []string
	for _, match := range matches {
		urls = append(urls, cleanURL(match))
	}
	return urls
}

// findBestImageURL finds the best image URL from a list of URLs
func findBestImageURL(urls []string) string {
	// First pass: look for URLs with image extensions
	for _, url := range urls {
		if isImageURL(url) {
			log.Printf("🖼️ Found image URL with extension: %s", truncateString(url, 100))
			return url
		}
	}
	// Second pass: look for URLs from known CDNs
	for _, url := range urls {
		if isImageCDNURL(url) {
			log.Printf("🖼️ Found CDN image URL: %s", truncateString(url, 100))
			return url
		}
	}
	return ""
}

// extractImageURLFromResponse parses the OpenAI Responses API output to find the image URL
func extractImageURLFromResponse(response *ResponsesAPIResponse) string {
	var allURLs []string

	// First, try to extract from output_text (simplest case)
	if response.OutputText != "" {
		log.Printf("📝 Found output_text: %s", truncateString(response.OutputText, 300))
		urls := extractAllURLsFromText(response.OutputText)
		allURLs = append(allURLs, urls...)
	}

	// Parse the output items
	for i, item := range response.Output {
		log.Printf("📦 Output item %d: type=%s, role=%s, content_count=%d", i, item.Type, item.Role, len(item.Content))

		if item.Type == "message" && len(item.Content) > 0 {
			for j, content := range item.Content {
				log.Printf("   Content %d: type=%s, text_len=%d, annotations=%d", j, content.Type, len(content.Text), len(content.Annotations))

				// Check for both "text" and "output_text" content types (API variations)
				if (content.Type == "output_text" || content.Type == "text") && content.Text != "" {
					log.Printf("📝 Parsing message content: %s", truncateString(content.Text, 300))
					urls := extractAllURLsFromText(content.Text)
					allURLs = append(allURLs, urls...)
				}

				// Also check annotations for URLs
				for _, annotation := range content.Annotations {
					if annotation.Type == "url_citation" && annotation.URL != "" {
						log.Printf("📎 Found annotation URL: %s", annotation.URL)
						allURLs = append(allURLs, annotation.URL)
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
					allURLs = append(allURLs, source.URL)
				}
			}
		}
	}

	log.Printf("📊 Total URLs found: %d", len(allURLs))

	// Find the best image URL from all collected URLs
	return findBestImageURL(allURLs)
}

// truncateString truncates a string to maxLen characters
func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
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
		if strings.Contains(strings.ToLower(imageURL), ".png") {
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
