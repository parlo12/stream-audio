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

// isImageURL checks if a URL points to a direct image (not a wiki page)
func isImageURL(url string) bool {
	urlLower := strings.ToLower(url)

	// Exclude wiki page URLs that contain image filenames but aren't direct images
	// e.g., https://commons.wikimedia.org/wiki/File:image.jpg is a PAGE, not an image
	excludePatterns := []string{
		"/wiki/file:",
		"/wiki/file%3a",
		"index.php/file:",
		"index.php/file%3a",
	}
	for _, pattern := range excludePatterns {
		if strings.Contains(urlLower, pattern) {
			return false
		}
	}

	// Check for direct image URLs
	imageExtensions := []string{".jpg", ".jpeg", ".png", ".webp", ".gif"}
	for _, ext := range imageExtensions {
		// Must end with the extension or have it followed by query params
		if strings.HasSuffix(urlLower, ext) ||
		   strings.Contains(urlLower, ext+"?") ||
		   strings.Contains(urlLower, ext+"&") {
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
	// Create request with proper headers to avoid 403 errors
	req, err := http.NewRequest("GET", imageURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers to mimic a browser request
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	req.Header.Set("Referer", "https://www.google.com/")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
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

// tryOpenLibraryCover attempts to get a book cover from Open Library's API
// This is a reliable fallback as Open Library provides direct image URLs
func tryOpenLibraryCover(title, author string) string {
	// Clean title for URL
	cleanTitle := strings.ReplaceAll(title, " ", "+")
	cleanAuthor := strings.ReplaceAll(author, " ", "+")

	// Try Open Library search API to get the book's OLID
	searchURL := fmt.Sprintf("https://openlibrary.org/search.json?title=%s&author=%s&limit=1", cleanTitle, cleanAuthor)

	req, err := http.NewRequest("GET", searchURL, nil)
	if err != nil {
		log.Printf("⚠️ Open Library search request failed: %v", err)
		return ""
	}
	req.Header.Set("User-Agent", "StreamAudio/1.0 (book cover fetcher)")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		log.Printf("⚠️ Open Library search failed: %v", err)
		return ""
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ""
	}

	var searchResult struct {
		Docs []struct {
			CoverI int `json:"cover_i"`
		} `json:"docs"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&searchResult); err != nil {
		return ""
	}

	if len(searchResult.Docs) > 0 && searchResult.Docs[0].CoverI > 0 {
		// Open Library cover URL - L = large size
		coverURL := fmt.Sprintf("https://covers.openlibrary.org/b/id/%d-L.jpg", searchResult.Docs[0].CoverI)
		log.Printf("📚 Found Open Library cover: %s", coverURL)
		return coverURL
	}

	return ""
}

// fetchAndSaveBookCover is the main entry point for fetching and saving a book cover
// It searches the web for the cover, downloads it, and returns the local path and public URL
func fetchAndSaveBookCover(title, author, bookID string) (localPath string, publicURL string, err error) {
	var imageURL string
	var downloadErr error

	// Step 1: Try OpenAI web search first
	imageURL, err = fetchBookCoverFromWeb(title, author)
	if err == nil && imageURL != "" {
		// Try to download the found image
		localPath, downloadErr = downloadAndSaveImage(imageURL, bookID)
		if downloadErr == nil {
			// Success!
			host := getEnv("STREAM_HOST", "http://localhost:8083")
			filename := filepath.Base(localPath)
			publicURL = fmt.Sprintf("%s/covers/%s", host, filename)
			return localPath, publicURL, nil
		}
		log.Printf("⚠️ Failed to download from OpenAI result: %v, trying Open Library fallback...", downloadErr)
	} else {
		log.Printf("⚠️ OpenAI search failed: %v, trying Open Library fallback...", err)
	}

	// Step 2: Fallback to Open Library
	imageURL = tryOpenLibraryCover(title, author)
	if imageURL != "" {
		localPath, downloadErr = downloadAndSaveImage(imageURL, bookID)
		if downloadErr == nil {
			host := getEnv("STREAM_HOST", "http://localhost:8083")
			filename := filepath.Base(localPath)
			publicURL = fmt.Sprintf("%s/covers/%s", host, filename)
			return localPath, publicURL, nil
		}
		log.Printf("⚠️ Failed to download from Open Library: %v", downloadErr)
	}

	// Both methods failed
	if downloadErr != nil {
		return "", "", fmt.Errorf("failed to download image: %w", downloadErr)
	}
	return "", "", fmt.Errorf("no valid book cover found")
}
