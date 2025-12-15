package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// BookSuggestion represents a single book search result
type BookSuggestion struct {
	Title    string `json:"title"`
	Author   string `json:"author"`
	CoverURL string `json:"cover_url"`
	Summary  string `json:"summary"`
}

// SearchBooksRequest represents the request body for book search
type SearchBooksRequest struct {
	Query string `json:"query" binding:"required"`
}

// SearchBooksResponse represents the response for book search
type SearchBooksResponse struct {
	Results []BookSuggestion `json:"results"`
}

// SearchBooksHandler handles the POST /user/search-books endpoint
// It uses OpenAI's Responses API with web search to find books matching the query
func SearchBooksHandler(c *gin.Context) {
	// 1. Parse and validate request
	var req SearchBooksRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Query parameter is required"})
		return
	}

	// 2. Validate query is not empty
	if strings.TrimSpace(req.Query) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Query cannot be empty"})
		return
	}

	log.Printf("🔍 Searching for books: %s", req.Query)

	// 3. Search for books using OpenAI Chat API (more reliable than Responses API)
	results, err := searchBooksWithChatCompletion(req.Query)
	if err != nil {
		log.Printf("❌ Failed to search books: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to search books", "details": err.Error()})
		return
	}

	// 4. Return results (even if empty array)
	log.Printf("✅ Found %d book results for query: %s", len(results), req.Query)
	c.JSON(http.StatusOK, SearchBooksResponse{Results: results})
}

// searchBooksWithOpenAI uses OpenAI's Responses API with web search to find books
// It returns up to 5 book suggestions with title, author, cover URL, and summary
func searchBooksWithOpenAI(query string) ([]BookSuggestion, error) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return nil, errors.New("OPENAI_API_KEY not set")
	}

	// Construct the search prompt
	searchPrompt := fmt.Sprintf(`Search the web for books matching the query: "%s"

Find up to 5 relevant books and return ONLY a JSON array with this exact structure (no markdown, no code blocks, no explanations):
[
  {
    "title": "Full Book Title",
    "author": "Author Full Name",
    "cover_url": "https://direct-image-url.jpg",
    "summary": "A compelling 1-2 sentence summary of the book."
  }
]

Requirements:
- Use official book covers from reputable sources (Amazon, Goodreads, OpenLibrary, publisher sites)
- Cover URLs must be direct image links (ending in .jpg, .jpeg, .png)
- Prefer high-resolution covers (around 1000x1600px or similar)
- Summaries should be concise but engaging (1-2 sentences)
- Return only the JSON array, nothing else`, query)

	// Use OpenAI Responses API with web search
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

	// Create HTTP request
	req, err := http.NewRequest("POST", "https://api.openai.com/v1/responses", bytes.NewBuffer(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	// Execute request with timeout
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	// Check response status
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("OpenAI API error (status %d): %s", resp.StatusCode, string(body))
	}

	// Read response body
	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Log response for debugging (truncated)
	if len(bodyBytes) > 500 {
		log.Printf("OpenAI Response (truncated): %s...", string(bodyBytes[:500]))
	} else {
		log.Printf("OpenAI Response: %s", string(bodyBytes))
	}

	// Parse OpenAI response
	var apiResponse ResponsesAPIResponse
	if err := json.Unmarshal(bodyBytes, &apiResponse); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Extract book results from the response
	results, err := extractBookResults(&apiResponse)
	if err != nil {
		return nil, fmt.Errorf("failed to extract book results: %w", err)
	}

	if len(results) == 0 {
		return nil, fmt.Errorf("no books found for query: %s", query)
	}

	return results, nil
}

// extractBookResults parses the OpenAI Responses API output to extract book suggestions
func extractBookResults(response *ResponsesAPIResponse) ([]BookSuggestion, error) {
	var jsonText string

	// First, try to extract from output_text
	if response.OutputText != "" {
		jsonText = response.OutputText
	}

	// Otherwise, parse the output items
	if jsonText == "" {
		for _, item := range response.Output {
			if item.Type == "message" && len(item.Content) > 0 {
				for _, content := range item.Content {
					if content.Type == "output_text" && content.Text != "" {
						jsonText = content.Text
						break
					}
				}
			}
			if jsonText != "" {
				break
			}
		}
	}

	if jsonText == "" {
		return nil, errors.New("no text output found in response")
	}

	// Clean the JSON text (remove markdown code blocks, etc.)
	jsonText = cleanJSONText(jsonText)

	log.Printf("Cleaned JSON text: %s", jsonText)

	// Parse the JSON array
	var results []BookSuggestion
	if err := json.Unmarshal([]byte(jsonText), &results); err != nil {
		// Try to find JSON array in the text
		jsonText = extractJSONArray(jsonText)
		if err := json.Unmarshal([]byte(jsonText), &results); err != nil {
			return nil, fmt.Errorf("failed to parse book results: %w. Text: %s", err, jsonText)
		}
	}

	// Validate and filter results
	validResults := make([]BookSuggestion, 0)
	for _, result := range results {
		if result.Title != "" && result.Author != "" {
			validResults = append(validResults, result)
		}
	}

	return validResults, nil
}

// cleanJSONText removes markdown formatting and other artifacts from JSON text
func cleanJSONText(text string) string {
	// Remove markdown code blocks
	text = strings.ReplaceAll(text, "```json", "")
	text = strings.ReplaceAll(text, "```", "")

	// Trim whitespace
	text = strings.TrimSpace(text)

	return text
}

// extractJSONArray attempts to extract a JSON array from text
func extractJSONArray(text string) string {
	// Find the first '[' and last ']'
	start := strings.Index(text, "[")
	end := strings.LastIndex(text, "]")

	if start >= 0 && end > start {
		return text[start : end+1]
	}

	return text
}

// Alternative implementation using Chat Completions API (fallback option)
// This can be used if the Responses API is not available or fails
func searchBooksWithChatCompletion(query string) ([]BookSuggestion, error) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return nil, errors.New("OPENAI_API_KEY not set")
	}

	systemPrompt := `You are a book information expert. Return information about real, published books only.

CRITICAL REQUIREMENTS:
1. Provide REAL book cover image URLs only (no placeholders, no AI-generated images)
2. Use Open Library cover URLs: https://covers.openlibrary.org/b/isbn/[ISBN]-L.jpg
3. Or use direct Amazon/Goodreads image URLs
4. If you don't have a real cover URL, use: "https://covers.openlibrary.org/b/isbn/0000000000-M.jpg"
5. Return ONLY a valid JSON array
6. NO markdown, NO code blocks, NO explanations, NO apologies
7. Even if the query has typos (like "Harry Porter" for "Harry Potter"), return the correct books`

	userPrompt := fmt.Sprintf(`Find up to 5 books matching: "%s"

For each book, provide:
- title: Full official title
- author: Author's full name
- cover_url: Real image URL from Open Library (https://covers.openlibrary.org/b/isbn/XXXXXXXXXX-L.jpg) or Amazon
- summary: 1-2 sentence description

IMPORTANT: Return ONLY the JSON array, nothing else. Example format:
[{"title":"Book Title","author":"Author Name","cover_url":"https://covers.openlibrary.org/b/isbn/9780439708180-L.jpg","summary":"Book summary."}]

Return the JSON array:`, query)

	reqBody := ChatRequest{
		Model: "gpt-4o",
		Messages: []ChatMessage{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		Temperature:    0.7,
		MaxTokens:      2000,
		ResponseFormat: &ResponseFormat{Type: "json_object"},
	}

	bodyBytes, _ := json.Marshal(reqBody)

	req, _ := http.NewRequest("POST", openAIChatURL, bytes.NewReader(bodyBytes))
	req.Header.Set("Authorization", "Bearer "+apiKey)
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("chat completion request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("chat completion returned %d: %s", resp.StatusCode, b)
	}

	var chatResp ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&chatResp); err != nil {
		return nil, fmt.Errorf("decode chat response: %w", err)
	}

	if len(chatResp.Choices) == 0 {
		return nil, errors.New("no chat completion choices returned")
	}

	// Parse the JSON from the response
	jsonText := cleanJSONText(chatResp.Choices[0].Message.Content)
	jsonText = extractJSONArray(jsonText)

	var results []BookSuggestion
	if err := json.Unmarshal([]byte(jsonText), &results); err != nil {
		return nil, fmt.Errorf("failed to parse book results: %w", err)
	}

	return results, nil
}
