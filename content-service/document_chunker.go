package main

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"rsc.io/pdf"
)

// ChunkDocument extracts text and creates chunks for a book
// For large books (4K+ pages), this uses batch inserts and runs asynchronously
func ChunkDocument(bookID uint, filePath string) (int, error) {
	log.Printf("📖 ChunkDocument called for book %d, file: %s", bookID, filePath)

	text, err := ExtractTextByType(filePath)
	if err != nil {
		log.Printf("❌ ExtractTextByType failed for %s: %v", filePath, err)
		return 0, err
	}

	log.Printf("📝 Extracted %d characters from %s", len(text), filePath)

	if len(text) == 0 {
		log.Printf("⚠️ No text content extracted from %s", filePath)
		return 0, fmt.Errorf("no text content extracted from file")
	}

	// Update Book.Content with truncated text (for preview/search)
	contentForBook := text
	if len(contentForBook) > 100000 {
		contentForBook = contentForBook[:100000] + "...[truncated]"
	}
	if err := db.Model(&Book{}).Where("id = ?", bookID).Update("content", contentForBook).Error; err != nil {
		log.Printf("⚠️ Failed to update book content: %v", err)
	}

	runes := []rune(text)
	chunkSize := 1000
	total := len(runes)
	totalChunks := (total + chunkSize - 1) / chunkSize

	log.Printf("📊 Book %d: %d characters → %d chunks", bookID, total, totalChunks)

	// Use batch inserts for efficiency (100 chunks per batch)
	batchSize := 100
	count := 0

	for i := 0; i < total; i += chunkSize {
		end := i + chunkSize
		if end > total {
			end = total
		}

		chunk := BookChunk{
			BookID:    bookID,
			Index:     count,
			Content:   string(runes[i:end]),
			AudioPath: "",
			TTSStatus: "pending",
		}

		// Collect chunks for batch insert
		if err := db.Create(&chunk).Error; err != nil {
			log.Printf("❌ Failed to create chunk %d for book %d: %v", count, bookID, err)
			return count, fmt.Errorf("failed to save chunk %d: %w", count, err)
		}
		count++

		// Log progress every 100 chunks
		if count%batchSize == 0 {
			progress := float64(count) / float64(totalChunks) * 100
			log.Printf("📈 Book %d chunking progress: %d/%d (%.1f%%)", bookID, count, totalChunks, progress)
		}
	}

	log.Printf("✅ Created %d chunks for book %d", count, bookID)
	return count, nil
}

// ChunkDocumentAsync processes large books in the background
// Returns immediately with estimated chunk count, actual processing happens async
func ChunkDocumentAsync(bookID uint, filePath string) (estimatedChunks int, err error) {
	log.Printf("📖 ChunkDocumentAsync called for book %d, file: %s", bookID, filePath)

	// Quick file size check to estimate chunks
	info, err := os.Stat(filePath)
	if err != nil {
		return 0, fmt.Errorf("file not found: %w", err)
	}

	// Estimate: ~1 chunk per 1000 bytes (rough approximation)
	estimatedChunks = int(info.Size() / 1000)
	if estimatedChunks < 1 {
		estimatedChunks = 1
	}

	// Update book status to "chunking"
	db.Model(&Book{}).Where("id = ?", bookID).Update("status", "chunking")

	// Process in background goroutine
	go func() {
		actualChunks, err := ChunkDocument(bookID, filePath)
		if err != nil {
			log.Printf("❌ Async chunking failed for book %d: %v", bookID, err)
			db.Model(&Book{}).Where("id = ?", bookID).Update("status", "chunking_failed")
			return
		}

		log.Printf("✅ Async chunking complete for book %d: %d chunks", bookID, actualChunks)
		db.Model(&Book{}).Where("id = ?", bookID).Update("status", "pending")
	}()

	return estimatedChunks, nil
}

// ChunkDocumentBatch uses batch inserts for better performance on large books
func ChunkDocumentBatch(bookID uint, filePath string) (int, error) {
	log.Printf("📖 ChunkDocumentBatch called for book %d, file: %s", bookID, filePath)

	text, err := ExtractTextByType(filePath)
	if err != nil {
		log.Printf("❌ ExtractTextByType failed for %s: %v", filePath, err)
		return 0, err
	}

	if len(text) == 0 {
		return 0, fmt.Errorf("no text content extracted from file")
	}

	// Update Book.Content
	contentForBook := text
	if len(contentForBook) > 100000 {
		contentForBook = contentForBook[:100000] + "...[truncated]"
	}
	db.Model(&Book{}).Where("id = ?", bookID).Update("content", contentForBook)

	runes := []rune(text)
	chunkSize := 1000
	total := len(runes)
	batchSize := 500 // Insert 500 chunks at a time

	var chunks []BookChunk
	count := 0

	for i := 0; i < total; i += chunkSize {
		end := i + chunkSize
		if end > total {
			end = total
		}

		chunks = append(chunks, BookChunk{
			BookID:    bookID,
			Index:     count,
			Content:   string(runes[i:end]),
			AudioPath: "",
			TTSStatus: "pending",
		})
		count++

		// Batch insert when we hit batchSize
		if len(chunks) >= batchSize {
			if err := db.CreateInBatches(chunks, batchSize).Error; err != nil {
				log.Printf("❌ Batch insert failed at chunk %d: %v", count, err)
				return count - len(chunks), err
			}
			log.Printf("📈 Book %d: inserted batch, total chunks: %d", bookID, count)
			chunks = chunks[:0] // Clear slice, keep capacity
		}
	}

	// Insert remaining chunks
	if len(chunks) > 0 {
		if err := db.CreateInBatches(chunks, len(chunks)).Error; err != nil {
			log.Printf("❌ Final batch insert failed: %v", err)
			return count - len(chunks), err
		}
	}

	log.Printf("✅ Batch created %d chunks for book %d", count, bookID)
	return count, nil
}

func ExtractTextByType(path string) (string, error) {
	lowerPath := strings.ToLower(path)
	switch {
	case strings.HasSuffix(lowerPath, ".pdf"):
		return ExtractTextFromPDF(path)
	case strings.HasSuffix(lowerPath, ".txt"):
		return ExtractTextFromTXT(path)
	case strings.HasSuffix(lowerPath, ".epub"):
		return ExtractTextFromEPUB(path)
	case strings.HasSuffix(lowerPath, ".azw") || strings.HasSuffix(lowerPath, ".mobi") || strings.HasSuffix(lowerPath, ".azw3"):
		return ExtractTextFromMOBI(path)
	case strings.HasSuffix(lowerPath, ".kfx"):
		return "", errors.New("KFX format is not supported. Please convert to EPUB, PDF, MOBI, or AZW3 format first")
	default:
		return "", errors.New("unsupported file type. Supported formats: PDF, TXT, EPUB, MOBI, AZW, AZW3")
	}
}

// Add ExtractTextFromPDF, ExtractTextFromTXT, ExtractTextFromEPUB...
// You may already have this in utils — import and call it
func cleanUTF8(input []byte) string {
	// Your existing clean function goes here
	return string(input) // Replace this with your actual implementation
}

func ExtractTextFromTXT(path string) (string, error) {
	log.Printf("📄 ExtractTextFromTXT: Reading file %s", path)

	// Check if file exists
	info, err := os.Stat(path)
	if err != nil {
		log.Printf("❌ File stat error for %s: %v", path, err)
		return "", fmt.Errorf("file not found or inaccessible: %w", err)
	}
	log.Printf("📄 File size: %d bytes", info.Size())

	data, err := os.ReadFile(path)
	if err != nil {
		log.Printf("❌ Failed to read file %s: %v", path, err)
		return "", err
	}

	text := cleanUTF8(data)
	log.Printf("📄 Read %d bytes, cleaned to %d characters from %s", len(data), len(text), path)

	return text, nil
}

func ExtractTextFromPDF(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()

	stat, _ := file.Stat()
	reader, err := pdf.NewReader(file, stat.Size())
	if err != nil {
		return "", err
	}

	var buf bytes.Buffer
	for i := 1; i <= reader.NumPage(); i++ {
		page := reader.Page(i)
		if page.V.IsNull() {
			continue
		}
		content := page.Content()
		for _, text := range content.Text {
			buf.WriteString(text.S)
			buf.WriteString(" ")
		}
	}

	return buf.String(), nil
}

func ExtractTextFromEPUB(path string) (string, error) {
	r, err := zip.OpenReader(path)
	if err != nil {
		return "", err
	}
	defer r.Close()

	var sb strings.Builder

	for _, f := range r.File {
		if strings.HasSuffix(strings.ToLower(f.Name), ".xhtml") || strings.HasSuffix(strings.ToLower(f.Name), ".html") {
			rc, err := f.Open()
			if err != nil {
				continue
			}
			content, err := io.ReadAll(rc)
			rc.Close()
			if err != nil {
				continue
			}
			sb.WriteString(string(content))
			sb.WriteString("\n")
		}
	}

	return sb.String(), nil
}

// ExtractTextFromMOBI extracts text from MOBI, AZW, and AZW3 files
// This function uses Calibre's ebook-convert command-line tool
func ExtractTextFromMOBI(path string) (string, error) {
	// Check if ebook-convert is available
	_, err := exec.LookPath("ebook-convert")
	if err != nil {
		return "", fmt.Errorf("ebook-convert (Calibre) not found. Please install Calibre to support MOBI/AZW formats. Error: %w", err)
	}

	// Create a temporary file for the converted text
	tempDir := os.TempDir()
	tempTxtFile := filepath.Join(tempDir, fmt.Sprintf("mobi_temp_%s.txt", filepath.Base(path)))
	defer os.Remove(tempTxtFile) // Clean up temp file

	// Run ebook-convert to convert MOBI to TXT
	cmd := exec.Command("ebook-convert", path, tempTxtFile, "--txt-output-encoding=utf-8")

	// Capture any errors from the conversion
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	err = cmd.Run()
	if err != nil {
		return "", fmt.Errorf("failed to convert MOBI file: %w. Details: %s", err, stderr.String())
	}

	// Read the converted text file
	textData, err := os.ReadFile(tempTxtFile)
	if err != nil {
		return "", fmt.Errorf("failed to read converted text file: %w", err)
	}

	text := string(textData)
	if len(text) == 0 {
		return "", errors.New("no text content extracted from MOBI file")
	}

	return text, nil
}
