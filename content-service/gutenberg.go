package main

// Project Gutenberg free-books catalog.
//
// We ingest Gutenberg's sanctioned OFFLINE catalog (pg_catalog.csv — weekly,
// ~75k public-domain titles) into our own Postgres so users can search it fast
// with no dependency on gutenberg.org being up, and Gutenberg's "don't
// crawl/robot us" policy is respected (we only fetch an individual book file
// when a user actually imports it).
//
//   GET  /user/gutenberg/search?q=&limit=&offset=  — full-text search
//   POST /user/gutenberg/import  {gutenberg_id}     — fetch the book, run it
//        through the normal upload→parse→narrate pipeline (counts as an upload)
//
// Catalog source: https://www.gutenberg.org/cache/epub/feeds/pg_catalog.csv

import (
	"bufio"
	"encoding/csv"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm/clause"
)

const gutenbergCatalogURL = "https://www.gutenberg.org/cache/epub/feeds/pg_catalog.csv"

// GutenbergBook is one catalog row (public-domain metadata).
type GutenbergBook struct {
	GutenbergID uint   `gorm:"primaryKey" json:"gutenberg_id"` // Gutenberg's "Text#"
	Title       string `gorm:"type:text" json:"title"`
	Authors     string `gorm:"type:text" json:"authors"`
	Language    string `json:"language"`
	Subjects    string `gorm:"type:text" json:"subjects"`
	Bookshelves string `gorm:"type:text" json:"bookshelves"`
	UpdatedAt   time.Time `json:"-"`
}

// initGutenbergCatalog migrates the table, ensures the search index, and
// ingests the catalog if empty; then refreshes weekly. Call from the API
// instance only (owns migrations). Non-blocking.
func initGutenbergCatalog() {
	if err := db.AutoMigrate(&GutenbergBook{}); err != nil {
		log.Printf("⚠️ gutenberg: migrate failed: %v", err)
		return
	}
	// Full-text search index over title + authors (created once).
	db.Exec(`CREATE INDEX IF NOT EXISTS idx_gutenberg_fts ON gutenberg_books
	         USING GIN (to_tsvector('english', coalesce(title,'') || ' ' || coalesce(authors,'')))`)

	go func() {
		var count int64
		db.Model(&GutenbergBook{}).Count(&count)
		if count == 0 {
			// Retry — a cold container's first outbound dial can time out.
			for attempt := 1; attempt <= 4; attempt++ {
				log.Printf("📚 gutenberg: catalog empty — ingesting (attempt %d)…", attempt)
				if err := ingestGutenbergCatalog(); err != nil {
					log.Printf("⚠️ gutenberg: ingest attempt %d failed: %v", attempt, err)
					time.Sleep(time.Duration(attempt*15) * time.Second)
					continue
				}
				break
			}
		}
		ticker := time.NewTicker(7 * 24 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			if err := ingestGutenbergCatalog(); err != nil {
				log.Printf("⚠️ gutenberg: weekly refresh failed: %v", err)
			}
		}
	}()
}

// ingestGutenbergCatalog downloads pg_catalog.csv and upserts every text title.
func ingestGutenbergCatalog() error {
	client := &http.Client{Timeout: 5 * time.Minute}
	resp, err := client.Get(gutenbergCatalogURL)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("catalog HTTP %d", resp.StatusCode)
	}

	r := csv.NewReader(bufio.NewReader(resp.Body))
	r.FieldsPerRecord = -1 // tolerate ragged rows
	header, err := r.Read()
	if err != nil {
		return err
	}
	col := map[string]int{}
	for i, h := range header {
		col[strings.TrimSpace(h)] = i
	}
	get := func(rec []string, name string) string {
		if i, ok := col[name]; ok && i < len(rec) {
			return strings.TrimSpace(rec[i])
		}
		return ""
	}

	batch := make([]GutenbergBook, 0, 1000)
	total := 0
	flush := func() {
		if len(batch) == 0 {
			return
		}
		db.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "gutenberg_id"}},
			DoUpdates: clause.AssignmentColumns([]string{"title", "authors", "language", "subjects", "bookshelves", "updated_at"}),
		}).Create(&batch)
		total += len(batch)
		batch = batch[:0]
	}

	for {
		rec, err := r.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			continue // skip malformed rows
		}
		// Only textual books (skip Sound/Collection/Dataset/etc).
		if t := get(rec, "Type"); t != "" && t != "Text" {
			continue
		}
		idStr := get(rec, "Text#")
		id, convErr := strconv.ParseUint(idStr, 10, 64)
		if convErr != nil {
			continue
		}
		batch = append(batch, GutenbergBook{
			GutenbergID: uint(id),
			Title:       get(rec, "Title"),
			Authors:     get(rec, "Authors"),
			Language:    get(rec, "Language"),
			Subjects:    get(rec, "Subjects"),
			Bookshelves: get(rec, "Bookshelves"),
			UpdatedAt:   time.Now(),
		})
		if len(batch) >= 1000 {
			flush()
		}
	}
	flush()
	log.Printf("📚 gutenberg: ingested/updated %d titles", total)
	return nil
}

// RefreshGutenbergHandler — POST /admin/gutenberg/refresh (force re-ingest).
func RefreshGutenbergHandler(c *gin.Context) {
	go func() {
		if err := ingestGutenbergCatalog(); err != nil {
			log.Printf("⚠️ gutenberg: manual refresh failed: %v", err)
		}
	}()
	c.JSON(http.StatusAccepted, gin.H{"message": "Catalog refresh started"})
}

// gutenbergResult is the trimmed search-result shape.
type gutenbergResult struct {
	GutenbergID uint   `json:"gutenberg_id"`
	Title       string `json:"title"`
	Author      string `json:"author"`
	Language    string `json:"language"`
}

const gutenbergSearchMax = 40

// searchGutenbergBooks runs the catalog full-text query (shared with the
// unified /user/freebooks/search in freebooks.go).
// websearch_to_tsquery handles phrases/partial words nicely; rank by
// relevance. English-only titles dominate the catalog.
func searchGutenbergBooks(q string, limit, offset int) ([]GutenbergBook, error) {
	var rows []GutenbergBook
	err := db.Raw(`
		SELECT * FROM gutenberg_books
		WHERE to_tsvector('english', coalesce(title,'') || ' ' || coalesce(authors,''))
		      @@ websearch_to_tsquery('english', ?)
		ORDER BY ts_rank(
		    to_tsvector('english', coalesce(title,'') || ' ' || coalesce(authors,'')),
		    websearch_to_tsquery('english', ?)
		) DESC
		LIMIT ? OFFSET ?`, q, q, limit, offset).Scan(&rows).Error
	return rows, err
}

// SearchGutenbergHandler — GET /user/gutenberg/search?q=&limit=&offset=
func SearchGutenbergHandler(c *gin.Context) {
	q := strings.TrimSpace(c.Query("q"))
	if len(q) < 2 {
		c.JSON(http.StatusOK, gin.H{"results": []gutenbergResult{}, "message": "Type at least 2 characters."})
		return
	}
	limit := envIntQuery(c, "limit", 20, gutenbergSearchMax)
	offset := envIntQuery(c, "offset", 0, 1_000_000)

	rows, err := searchGutenbergBooks(q, limit, offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "search failed"})
		return
	}

	results := make([]gutenbergResult, 0, len(rows))
	for _, b := range rows {
		results = append(results, gutenbergResult{
			GutenbergID: b.GutenbergID,
			Title:       b.Title,
			Author:      formatAuthor(b.Authors),
			Language:    b.Language,
		})
	}
	c.JSON(http.StatusOK, gin.H{"results": results})
}

// ImportGutenbergRequest — POST /user/gutenberg/import
type ImportGutenbergRequest struct {
	GutenbergID uint `json:"gutenberg_id" binding:"required"`
}

// ImportGutenbergHandler fetches the Gutenberg book file and runs it through
// the normal pipeline. Counts as an upload (per product decision).
func ImportGutenbergHandler(c *gin.Context) {
	userID := getUserIDFromContext(c)
	accountType := accountTypeFromClaims(c)

	var req ImportGutenbergRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "gutenberg_id required"})
		return
	}

	var g GutenbergBook
	if err := db.First(&g, req.GutenbergID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Book not found in the free catalog"})
		return
	}

	importTextBook(c, userID, accountType, truncate(g.Title, 250), formatAuthor(g.Authors),
		func() (string, error) { return fetchGutenbergText(g.GutenbergID) })
	log.Printf("📚 gutenberg: user %d imported PG#%d", userID, g.GutenbergID)
}

// importTextBook is the shared free-book import tail: quota check → Book row →
// fetch text (source-specific) → store at the standard upload key → consume
// upload credit → enqueue cover + parse. Writes the HTTP response itself.
// Used by the Gutenberg import and the unified /user/freebooks/import.
func importTextBook(c *gin.Context, userID uint, accountType, title, author string, fetchText func() (string, error)) {
	// Uploads quota (free-book imports count as a normal upload).
	if d := checkAndConsume(userID, accountType, "uploads", 0, 0); !d.Allowed {
		quota429(c, d)
		return
	}

	// Create the book record up front so we have an ID for the storage key.
	book := Book{
		Title:    title,
		Author:   author,
		Category: "Classics",
		Genre:    "Classic",
		Status:   "parsing",
		UserID:   userID,
	}
	if err := db.Create(&book).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not create book"})
		return
	}

	// Fetch the plain-text content (server-side, one file only).
	text, err := fetchText()
	if err != nil {
		db.Model(&Book{}).Where("id = ?", book.ID).Update("status", "chunking_failed")
		c.JSON(http.StatusBadGateway, gin.H{"error": "Couldn't download this book right now. Try again."})
		return
	}

	// Write to a temp file and store at the standard upload key.
	tmp := filepath.Join(os.TempDir(), fmt.Sprintf("freebook_%d_%d.txt", userID, book.ID))
	if err := os.WriteFile(tmp, []byte(text), 0o600); err != nil {
		db.Model(&Book{}).Where("id = ?", book.ID).Update("status", "chunking_failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "storage error"})
		return
	}
	defer os.Remove(tmp)

	key := uploadKey(userID, book.ID, ".txt")
	if err := store.PutFile(c.Request.Context(), key, tmp, "text/plain"); err != nil {
		db.Model(&Book{}).Where("id = ?", book.ID).Update("status", "chunking_failed")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "storage error"})
		return
	}
	db.Model(&Book{}).Where("id = ?", book.ID).Update("file_path", key)

	// Consume the upload credit + kick off cover fetch and parsing.
	checkAndConsume(userID, accountType, "uploads", 1, book.ID)
	if err := enqueueFetchCover(book.ID, book.Title, book.Author); err != nil {
		log.Printf("⚠️ freebooks: cover enqueue failed for book %d: %v", book.ID, err)
	}
	if err := enqueueParseBook(book.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not queue parsing"})
		return
	}

	log.Printf("📚 freebooks: book %d created for user %d (%s)", book.ID, userID, book.Title)
	c.JSON(http.StatusOK, gin.H{"message": "Added to your library", "book": book})
}

// fetchGutenbergText downloads the UTF-8 plain text and strips PG boilerplate.
func fetchGutenbergText(id uint) (string, error) {
	// Cache path is the most stable; fall back to the files/ layout.
	urls := []string{
		fmt.Sprintf("https://www.gutenberg.org/cache/epub/%d/pg%d.txt", id, id),
		fmt.Sprintf("https://www.gutenberg.org/files/%d/%d-0.txt", id, id),
		fmt.Sprintf("https://www.gutenberg.org/files/%d/%d.txt", id, id),
	}
	client := &http.Client{Timeout: 90 * time.Second}
	var lastErr error
	for _, u := range urls {
		reqHTTP, _ := http.NewRequest(http.MethodGet, u, nil)
		reqHTTP.Header.Set("User-Agent", "Narrafied/1.0 (+https://narrafied.com)")
		resp, err := client.Do(reqHTTP)
		if err != nil {
			lastErr = err
			continue
		}
		if resp.StatusCode != http.StatusOK {
			resp.Body.Close()
			lastErr = fmt.Errorf("HTTP %d", resp.StatusCode)
			continue
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, 30<<20)) // 30MB cap
		resp.Body.Close()
		if err != nil {
			lastErr = err
			continue
		}
		return stripGutenbergBoilerplate(string(body)), nil
	}
	return "", lastErr
}

// stripGutenbergBoilerplate removes the header before "*** START OF ..." and
// the license footer after "*** END OF ...".
func stripGutenbergBoilerplate(text string) string {
	upper := strings.ToUpper(text)
	if i := strings.Index(upper, "*** START OF TH"); i >= 0 {
		if nl := strings.IndexByte(text[i:], '\n'); nl >= 0 {
			text = text[i+nl+1:]
			upper = strings.ToUpper(text)
		}
	}
	if i := strings.Index(upper, "*** END OF TH"); i >= 0 {
		text = text[:i]
	}
	return strings.TrimSpace(text)
}

// formatAuthor turns Gutenberg's "Last, First" (possibly multiple, "; "-joined)
// into "First Last" and keeps only the first author for display.
func formatAuthor(authors string) string {
	if authors == "" {
		return "Unknown"
	}
	first := strings.Split(authors, ";")[0]
	first = strings.TrimSpace(first)
	// Drop trailing "(1900-1950)" life dates if present.
	if p := strings.Index(first, ","); p >= 0 {
		last := strings.TrimSpace(first[:p])
		rest := strings.TrimSpace(first[p+1:])
		// rest is "First Middle, birthyear-deathyear" — keep only the name,
		// trimming the trailing ", " before the life dates.
		if d := strings.IndexAny(rest, "0123456789("); d >= 0 {
			rest = strings.Trim(rest[:d], " ,")
		}
		rest = strings.Trim(rest, " ,")
		if rest != "" {
			return rest + " " + last
		}
		return last
	}
	return first
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// envIntQuery parses a bounded int query param.
func envIntQuery(c *gin.Context, name string, def, max int) int {
	v := c.Query(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		return def
	}
	if n > max {
		return max
	}
	return n
}
