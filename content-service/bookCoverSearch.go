// ===============
// File: bookCoverSearch.go
// Description: Search for book covers and allow user to select one
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
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// CoverSearchRequest is the request body for searching book covers
type CoverSearchRequest struct {
	Title  string `json:"title" binding:"required"`
	Author string `json:"author"`
}

// CoverOption represents a single cover option returned to the user
type CoverOption struct {
	URL         string `json:"url"`
	Source      string `json:"source"`
	Description string `json:"description,omitempty"`
}

// CoverSearchResponse is the response containing multiple cover options
type CoverSearchResponse struct {
	Title   string        `json:"title"`
	Author  string        `json:"author"`
	Covers  []CoverOption `json:"covers"`
	Message string        `json:"message,omitempty"`
}

// SelectCoverRequest is the request body for selecting a cover
type SelectCoverRequest struct {
	CoverURL string `json:"cover_url" binding:"required"`
}

// SearchBookCoversHandler handles POST /user/search-book-covers
// Returns multiple cover options for the user to choose from
func SearchBookCoversHandler(c *gin.Context) {
	var req CoverSearchRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "title is required"})
		return
	}

	log.Printf("🔍 Searching covers for: %s by %s", req.Title, req.Author)

	// Search for covers using OpenAI
	covers, err := searchMultipleCovers(req.Title, req.Author)
	if err != nil {
		log.Printf("⚠️ Cover search error: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to search for covers",
			"details": err.Error(),
		})
		return
	}

	if len(covers) == 0 {
		c.JSON(http.StatusOK, CoverSearchResponse{
			Title:   req.Title,
			Author:  req.Author,
			Covers:  []CoverOption{},
			Message: "No covers found for this book",
		})
		return
	}

	log.Printf("✅ Found %d cover options for %s", len(covers), req.Title)

	c.JSON(http.StatusOK, CoverSearchResponse{
		Title:  req.Title,
		Author: req.Author,
		Covers: covers,
	})
}

// SelectBookCoverHandler handles POST /user/books/:book_id/select-cover
// Downloads and saves the selected cover URL for a book
func SelectBookCoverHandler(c *gin.Context) {
	bookID := c.Param("book_id")
	if bookID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "book_id is required"})
		return
	}

	var req SelectCoverRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cover_url is required"})
		return
	}

	// Validate URL
	if !strings.HasPrefix(req.CoverURL, "http://") && !strings.HasPrefix(req.CoverURL, "https://") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid cover URL"})
		return
	}

	// Get user ID from JWT
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// Verify book belongs to user
	var book Book
	if err := db.Where("id = ? AND user_id = ?", bookID, userID).First(&book).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Book not found"})
		return
	}

	log.Printf("📥 Downloading selected cover for book %s: %s", bookID, req.CoverURL)

	// Download and save the selected cover
	localPath, err := downloadAndSaveImage(req.CoverURL, bookID)
	if err != nil {
		log.Printf("❌ Failed to download cover: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to download cover",
			"details": err.Error(),
		})
		return
	}

	// Generate public URL
	host := getEnv("STREAM_HOST", "https://narrafied.com")
	filename := localPath[strings.LastIndex(localPath, "/")+1:]
	publicURL := fmt.Sprintf("%s/covers/%s", host, filename)

	// Update book record
	book.CoverPath = localPath
	book.CoverURL = publicURL
	if err := db.Save(&book).Error; err != nil {
		log.Printf("❌ Failed to update book cover: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update book"})
		return
	}

	log.Printf("✅ Cover saved for book %s: %s", bookID, publicURL)

	c.JSON(http.StatusOK, gin.H{
		"message":    "Cover saved successfully",
		"cover_path": localPath,
		"cover_url":  publicURL,
	})
}

// searchMultipleCovers searches for multiple book cover options
func searchMultipleCovers(title, author string) ([]CoverOption, error) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("OPENAI_API_KEY environment variable not set")
	}

	authorStr := author
	if authorStr == "" {
		authorStr = "unknown author"
	}

	// Construct search prompt for multiple covers
	searchPrompt := fmt.Sprintf(
		`Search for book cover images for the book titled "%s" by %s.

Find 3-5 different book cover image URLs from reputable sources like:
- Amazon
- Goodreads
- Barnes & Noble
- Publisher websites
- Google Books

For each cover found, provide:
1. The direct image URL (must be a valid image URL ending in .jpg, .jpeg, .png, or .webp, or from a known image CDN)
2. The source website name

Format your response as a JSON array like this:
[
  {"url": "https://example.com/cover1.jpg", "source": "Amazon"},
  {"url": "https://example.com/cover2.jpg", "source": "Goodreads"}
]

Only include direct image URLs that can be downloaded. Do not include HTML pages.
Return ONLY the JSON array, no other text.`,
		title, authorStr)

	requestBody := ResponsesRequest{
		Model: "gpt-4o",
		Tools: []ResponseTool{
			{
				Type: "web_search",
			},
		},
		Input:   searchPrompt,
		Include: []string{"web_search_call.action.sources"},
	}

	jsonBody, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequest("POST", "https://api.openai.com/v1/responses", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("OpenAI API error (status %d): %s", resp.StatusCode, string(body))
	}

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	log.Printf("OpenAI cover search response length: %d bytes", len(bodyBytes))

	var apiResponse ResponsesAPIResponse
	if err := json.Unmarshal(bodyBytes, &apiResponse); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Extract covers from response
	covers := extractCoversFromResponse(&apiResponse)

	// Also collect image URLs from web search sources
	for _, item := range apiResponse.Output {
		if item.Type == "web_search_call" && item.Action != nil {
			for _, source := range item.Action.Sources {
				if isImageURL(source.URL) {
					// Check if already in list
					exists := false
					for _, c := range covers {
						if c.URL == source.URL {
							exists = true
							break
						}
					}
					if !exists {
						covers = append(covers, CoverOption{
							URL:    source.URL,
							Source: extractDomain(source.URL),
						})
					}
				}
			}
		}
	}

	// Validate and filter covers - keep only valid image URLs
	validCovers := []CoverOption{}
	for _, cover := range covers {
		if cover.URL != "" && (isImageURL(cover.URL) || isKnownImageCDN(cover.URL)) {
			validCovers = append(validCovers, cover)
		}
	}

	// Limit to 10 covers max
	if len(validCovers) > 10 {
		validCovers = validCovers[:10]
	}

	return validCovers, nil
}

// extractCoversFromResponse extracts cover options from OpenAI response
func extractCoversFromResponse(response *ResponsesAPIResponse) []CoverOption {
	var covers []CoverOption

	// Try to find JSON array in the response
	for _, item := range response.Output {
		if item.Type == "message" && len(item.Content) > 0 {
			for _, content := range item.Content {
				if content.Type == "output_text" && content.Text != "" {
					// Try to extract JSON array from text
					jsonCovers := extractJSONCovers(content.Text)
					covers = append(covers, jsonCovers...)

					// Also extract any standalone URLs
					urls := extractAllURLs(content.Text)
					for _, url := range urls {
						if isImageURL(url) || isKnownImageCDN(url) {
							exists := false
							for _, c := range covers {
								if c.URL == url {
									exists = true
									break
								}
							}
							if !exists {
								covers = append(covers, CoverOption{
									URL:    url,
									Source: extractDomain(url),
								})
							}
						}
					}
				}

				// Check URL citations in annotations
				for _, annotation := range content.Annotations {
					if annotation.Type == "url_citation" && annotation.URL != "" {
						if isImageURL(annotation.URL) || isKnownImageCDN(annotation.URL) {
							covers = append(covers, CoverOption{
								URL:    annotation.URL,
								Source: extractDomain(annotation.URL),
							})
						}
					}
				}
			}
		}
	}

	return covers
}

// extractJSONCovers tries to extract cover options from JSON in the text
func extractJSONCovers(text string) []CoverOption {
	var covers []CoverOption

	// Find JSON array in text
	startIdx := strings.Index(text, "[")
	endIdx := strings.LastIndex(text, "]")

	if startIdx == -1 || endIdx == -1 || endIdx <= startIdx {
		return covers
	}

	jsonStr := text[startIdx : endIdx+1]

	// Try to parse as array of cover options
	var parsed []struct {
		URL    string `json:"url"`
		Source string `json:"source"`
	}

	if err := json.Unmarshal([]byte(jsonStr), &parsed); err != nil {
		return covers
	}

	for _, p := range parsed {
		if p.URL != "" {
			covers = append(covers, CoverOption{
				URL:    p.URL,
				Source: p.Source,
			})
		}
	}

	return covers
}

// extractAllURLs extracts all URLs from text
func extractAllURLs(text string) []string {
	var urls []string
	urlRegex := regexp.MustCompile(`https?://[^\s\)\]"'<>]+`)
	matches := urlRegex.FindAllString(text, -1)
	for _, match := range matches {
		// Clean up URL
		url := strings.TrimSuffix(match, ",")
		url = strings.TrimSuffix(url, ".")
		urls = append(urls, url)
	}
	return urls
}

// extractDomain extracts the domain name from a URL for display
func extractDomain(url string) string {
	// Simple domain extraction
	url = strings.TrimPrefix(url, "https://")
	url = strings.TrimPrefix(url, "http://")

	slashIdx := strings.Index(url, "/")
	if slashIdx > 0 {
		url = url[:slashIdx]
	}

	// Remove www.
	url = strings.TrimPrefix(url, "www.")

	return url
}

// isKnownImageCDN checks if URL is from a known image CDN
func isKnownImageCDN(url string) bool {
	cdns := []string{
		"images-na.ssl-images-amazon.com",
		"m.media-amazon.com",
		"images.gr-assets.com",
		"i.gr-assets.com",
		"books.google.com",
		"covers.openlibrary.org",
		"prodimage.images-bn.com",
		"images-us.bookshop.org",
		"cloudflare-ipfs.com",
		"img.thriftbooks.com",
	}

	for _, cdn := range cdns {
		if strings.Contains(url, cdn) {
			return true
		}
	}

	return false
}
