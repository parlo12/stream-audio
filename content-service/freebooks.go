package main

// Unified free-books search & import across sources:
//
//   - "gutenberg": our locally-ingested Project Gutenberg catalog (gutenberg.go)
//   - "archive":   Internet Archive, queried live via advancedsearch.php,
//                  restricted to fully-downloadable texts (format:DjVuTXT,
//                  excluding lending-only collections and IA's Gutenberg mirror
//                  so we don't duplicate results).
//
//   GET  /user/freebooks/search?q=&limit=          — merged, deduped results
//   POST /user/freebooks/import {source, source_id} — fetch + normal pipeline
//
// The legacy /user/gutenberg/* endpoints stay for build-16 clients.
//
// Archive text quality note: downloads are OCR text (…_djvu.txt), so scan
// artifacts are possible — Gutenberg results are listed first and win dedupe
// ties for that reason.

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// FreeBookResult is one row of the unified search response.
type FreeBookResult struct {
	Source   string `json:"source"`    // "gutenberg" | "archive"
	SourceID string `json:"source_id"` // PG Text# (numeric string) or IA identifier
	Title    string `json:"title"`
	Author   string `json:"author"`
	Language string `json:"language,omitempty"`
	Year     string `json:"year,omitempty"`
}

const (
	archiveSearchURL   = "https://archive.org/advancedsearch.php"
	archiveMetadataURL = "https://archive.org/metadata/%s"
	archiveDownloadURL = "https://archive.org/download/%s/%s"
	archiveSearchMax   = 20
	freeBookTextCap    = 30 << 20 // 30MB, same as Gutenberg fetch
)

var archiveIdentifierRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,120}$`)

// SearchFreeBooksHandler — GET /user/freebooks/search?q=&limit=
// Queries the local Gutenberg catalog and the Internet Archive concurrently,
// then merges (Gutenberg first) and dedupes by normalized title+author.
func SearchFreeBooksHandler(c *gin.Context) {
	q := strings.TrimSpace(c.Query("q"))
	if len(q) < 2 {
		c.JSON(http.StatusOK, gin.H{"results": []FreeBookResult{}, "message": "Type at least 2 characters."})
		return
	}
	limit := envIntQuery(c, "limit", 20, gutenbergSearchMax)

	var (
		wg       sync.WaitGroup
		pgRows   []GutenbergBook
		iaRows   []FreeBookResult
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		rows, err := searchGutenbergBooks(q, limit, 0)
		if err != nil {
			log.Printf("⚠️ freebooks: gutenberg search failed: %v", err)
			return
		}
		pgRows = rows
	}()
	go func() {
		defer wg.Done()
		rows, err := searchInternetArchive(c.Request.Context(), q, limit)
		if err != nil {
			// Archive being slow/down must never break search — degrade to PG only.
			log.Printf("⚠️ freebooks: archive search failed: %v", err)
			return
		}
		iaRows = rows
	}()
	wg.Wait()

	seen := make(map[string]bool, len(pgRows)+len(iaRows))
	results := make([]FreeBookResult, 0, len(pgRows)+len(iaRows))
	for _, b := range pgRows {
		r := FreeBookResult{
			Source:   "gutenberg",
			SourceID: strconv.FormatUint(uint64(b.GutenbergID), 10),
			Title:    b.Title,
			Author:   formatAuthor(b.Authors),
			Language: b.Language,
		}
		seen[dedupeKey(r.Title, r.Author)] = true
		results = append(results, r)
	}
	for _, r := range iaRows {
		if seen[dedupeKey(r.Title, r.Author)] {
			continue
		}
		seen[dedupeKey(r.Title, r.Author)] = true
		results = append(results, r)
	}
	c.JSON(http.StatusOK, gin.H{"results": results})
}

// dedupeKey normalizes title+author so the same book from both sources
// collapses to one result (Gutenberg wins — it's appended first).
func dedupeKey(title, author string) string {
	norm := func(s string) string {
		s = strings.ToLower(s)
		var b strings.Builder
		for _, r := range s {
			if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
				b.WriteRune(r)
			}
		}
		return b.String()
	}
	return norm(title) + "|" + norm(author)
}

// flexString decodes IA fields that may arrive as a string, number, or array.
type flexString string

func (f *flexString) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err == nil {
		*f = flexString(s)
		return nil
	}
	var arr []string
	if err := json.Unmarshal(data, &arr); err == nil {
		if len(arr) > 0 {
			*f = flexString(arr[0])
		}
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(data, &n); err == nil {
		*f = flexString(n.String())
		return nil
	}
	*f = "" // tolerate anything else
	return nil
}

// searchInternetArchive queries advancedsearch.php for downloadable texts.
func searchInternetArchive(ctx context.Context, q string, limit int) ([]FreeBookResult, error) {
	if limit <= 0 || limit > archiveSearchMax {
		limit = archiveSearchMax
	}
	// Only items with a DjVuTXT file (guaranteed plain-text download), no
	// lending-only material, and skip IA's Gutenberg mirror (we have PG locally).
	iaQuery := fmt.Sprintf(
		`title:(%s) AND mediatype:(texts) AND format:(DjVuTXT) AND -collection:(inlibrary) AND -collection:(printdisabled) AND -collection:(gutenberg)`,
		quoteIAQuery(q))

	params := url.Values{}
	params.Set("q", iaQuery)
	params.Add("fl[]", "identifier")
	params.Add("fl[]", "title")
	params.Add("fl[]", "creator")
	params.Add("fl[]", "year")
	params.Add("fl[]", "language")
	params.Add("sort[]", "downloads desc") // popularity ≈ relevance for classics
	params.Set("rows", strconv.Itoa(limit))
	params.Set("page", "1")
	params.Set("output", "json")

	ctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, archiveSearchURL+"?"+params.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Narrafied/1.0 (+https://narrafied.com)")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("archive search HTTP %d", resp.StatusCode)
	}

	var out struct {
		Response struct {
			Docs []struct {
				Identifier string     `json:"identifier"`
				Title      flexString `json:"title"`
				Creator    flexString `json:"creator"`
				Year       flexString `json:"year"`
				Language   flexString `json:"language"`
			} `json:"docs"`
		} `json:"response"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&out); err != nil {
		return nil, err
	}

	results := make([]FreeBookResult, 0, len(out.Response.Docs))
	for _, d := range out.Response.Docs {
		if d.Identifier == "" || string(d.Title) == "" {
			continue
		}
		// IA creators arrive as "Last, First, 1819-1891" — reuse the
		// Gutenberg formatter so display and dedupe match across sources.
		author := formatAuthor(strings.TrimSpace(string(d.Creator)))
		results = append(results, FreeBookResult{
			Source:   "archive",
			SourceID: d.Identifier,
			Title:    strings.TrimSpace(string(d.Title)),
			Author:   author,
			Language: normalizeIALanguage(string(d.Language)),
			Year:     strings.TrimSpace(string(d.Year)),
		})
	}
	return results, nil
}

// quoteIAQuery embeds the user's query as a quoted Lucene phrase-ish term.
func quoteIAQuery(q string) string {
	return `"` + strings.ReplaceAll(q, `"`, ` `) + `"`
}

// normalizeIALanguage maps IA's inconsistent language values to what the
// catalog uses ("en", "fr", …) where easy; passes through otherwise.
func normalizeIALanguage(l string) string {
	switch strings.ToLower(strings.TrimSpace(l)) {
	case "eng", "english", "en":
		return "en"
	case "fre", "fra", "french", "fr":
		return "fr"
	case "ger", "deu", "german", "de":
		return "de"
	case "spa", "spanish", "es":
		return "es"
	case "ita", "italian", "it":
		return "it"
	}
	return strings.TrimSpace(l)
}

// ImportFreeBookRequest — POST /user/freebooks/import
type ImportFreeBookRequest struct {
	Source   string `json:"source" binding:"required"`
	SourceID string `json:"source_id" binding:"required"`
}

// ImportFreeBookHandler routes an import to the right source fetcher, then the
// shared upload→parse→narrate pipeline (importTextBook).
func ImportFreeBookHandler(c *gin.Context) {
	userID := getUserIDFromContext(c)
	accountType := accountTypeFromClaims(c)

	var req ImportFreeBookRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "source and source_id required"})
		return
	}

	switch req.Source {
	case "gutenberg":
		id, err := strconv.ParseUint(req.SourceID, 10, 64)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid gutenberg id"})
			return
		}
		var g GutenbergBook
		if err := db.First(&g, uint(id)).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "Book not found in the free catalog"})
			return
		}
		importTextBook(c, userID, accountType, truncate(g.Title, 250), formatAuthor(g.Authors),
			func() (string, error) { return fetchGutenbergText(g.GutenbergID) })
		log.Printf("📚 freebooks: user %d imported PG#%d", userID, g.GutenbergID)

	case "archive":
		if !archiveIdentifierRe.MatchString(req.SourceID) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid archive identifier"})
			return
		}
		title, author, textFile, err := fetchArchiveItemInfo(c.Request.Context(), req.SourceID)
		if err != nil {
			log.Printf("⚠️ freebooks: archive metadata failed for %q: %v", req.SourceID, err)
			c.JSON(http.StatusBadGateway, gin.H{"error": "Couldn't fetch this book right now. Try again."})
			return
		}
		importTextBook(c, userID, accountType, truncate(title, 250), author,
			func() (string, error) { return fetchArchiveText(req.SourceID, textFile) })
		log.Printf("📚 freebooks: user %d imported archive item %q", userID, req.SourceID)

	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "unknown source"})
	}
}

// fetchArchiveItemInfo reads the item's metadata and locates its plain-text
// (DjVuTXT) file. Returns title, author, and the text file name.
func fetchArchiveItemInfo(ctx context.Context, identifier string) (title, author, textFile string, err error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fmt.Sprintf(archiveMetadataURL, identifier), nil)
	if err != nil {
		return "", "", "", err
	}
	req.Header.Set("User-Agent", "Narrafied/1.0 (+https://narrafied.com)")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", "", "", fmt.Errorf("metadata HTTP %d", resp.StatusCode)
	}

	var meta struct {
		Metadata struct {
			Title   flexString `json:"title"`
			Creator flexString `json:"creator"`
		} `json:"metadata"`
		Files []struct {
			Name   string `json:"name"`
			Format string `json:"format"`
		} `json:"files"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&meta); err != nil {
		return "", "", "", err
	}

	for _, f := range meta.Files {
		if f.Format == "DjVuTXT" || strings.HasSuffix(f.Name, "_djvu.txt") {
			textFile = f.Name
			break
		}
	}
	if textFile == "" {
		return "", "", "", fmt.Errorf("no plain-text file on item")
	}
	title = strings.TrimSpace(string(meta.Metadata.Title))
	if title == "" {
		title = identifier
	}
	author = formatAuthor(strings.TrimSpace(string(meta.Metadata.Creator)))
	return title, author, textFile, nil
}

// fetchArchiveText downloads the item's OCR plain text (capped at 30MB).
func fetchArchiveText(identifier, textFile string) (string, error) {
	u := fmt.Sprintf(archiveDownloadURL, url.PathEscape(identifier), url.PathEscape(textFile))
	client := &http.Client{Timeout: 90 * time.Second}
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", "Narrafied/1.0 (+https://narrafied.com)")
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, freeBookTextCap))
	if err != nil {
		return "", err
	}
	text := cleanOCRText(string(body))
	if len(text) < 500 {
		return "", fmt.Errorf("text too short (%d bytes) — likely a bad scan", len(text))
	}
	return text, nil
}

var (
	// "beauti-\nful" → "beautiful" (lowercase→lowercase across a line break is
	// almost always OCR hyphenation, not a real compound).
	ocrHyphenRe = regexp.MustCompile(`([a-z])-\r?\n\s*([a-z])`)
	// Standalone page-number lines (possibly bracketed): "47", "[ 47 ]", "- 47 -".
	ocrPageNumRe = regexp.MustCompile(`(?m)^\s*[\[\(\-–— ]*\d{1,4}[\]\)\-–— ]*\s*$`)
	// 3+ blank lines → one blank line.
	ocrBlankRunRe = regexp.MustCompile(`\n{3,}`)
)

// cleanOCRText scrubs common scanner artifacts from Internet Archive _djvu.txt
// files before they reach the TTS pipeline (audit H4): de-hyphenates words
// split across line wraps, drops page-number-only lines and digitization
// boilerplate, and collapses blank-line runs. Deterministic and conservative —
// no model involved, so it can't rewrite book text.
func cleanOCRText(text string) string {
	text = ocrHyphenRe.ReplaceAllString(text, "$1$2")
	text = ocrPageNumRe.ReplaceAllString(text, "")

	// Drop digitization/boilerplate lines.
	lines := strings.Split(text, "\n")
	kept := lines[:0]
	for _, line := range lines {
		l := strings.ToLower(strings.TrimSpace(line))
		if strings.Contains(l, "digitized by") ||
			strings.Contains(l, "downloaded from") ||
			strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://") ||
			strings.Contains(l, "archive.org") {
			continue
		}
		kept = append(kept, line)
	}
	text = strings.Join(kept, "\n")

	text = ocrBlankRunRe.ReplaceAllString(text, "\n\n")
	return strings.TrimSpace(text)
}
