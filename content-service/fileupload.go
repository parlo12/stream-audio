package main

// fileuploadgo uploadBookFileHandler handles file uploads for books.
// It expects form-data with keys "book_id" and "file".
// It saves the file to a specified directory and updates the book record in the database.
// It also processes the uploaded file by chunking it into smaller parts for further processing.

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)


func uploadBookFileHandler(c *gin.Context) {
	bookID := c.PostForm("book_id")
	if bookID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "book_id is required"})
		return
	}

	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "File upload error", "details": err.Error()})
		return
	}

	// Validate file type
	lowerFilename := strings.ToLower(file.Filename)
	validExtensions := []string{".pdf", ".txt", ".epub", ".mobi", ".azw", ".azw3"}
	isValid := false
	for _, ext := range validExtensions {
		if strings.HasSuffix(lowerFilename, ext) {
			isValid = true
			break
		}
	}

	if !isValid {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Invalid file type. Supported formats: PDF, TXT, EPUB, MOBI, AZW, AZW3",
			"note":  "KFX format is not supported. Please convert to one of the supported formats first.",
		})
		return
	}

	// Check for unsupported KFX format explicitly
	if strings.HasSuffix(lowerFilename, ".kfx") {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "KFX format is not supported",
			"message": "Please convert your KFX file to EPUB, PDF, MOBI, or AZW3 format first",
			"suggestion": "You can use Calibre or online converters to convert KFX files",
		})
		return
	}

	// Ensure uploads directory exists
	uploadDir := "./uploads"
	if _, err := os.Stat(uploadDir); os.IsNotExist(err) {
		if err := os.MkdirAll(uploadDir, os.ModePerm); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create upload directory", "details": err.Error()})
			return
		}
	}

	// Save uploaded file
	dest := filepath.Join(uploadDir, file.Filename)
	if err := c.SaveUploadedFile(file, dest); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save file", "details": err.Error()})
		return
	}

	// Look up the book
	var book Book
	if err := db.First(&book, bookID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Book not found", "details": err.Error()})
		return
	}

	// Compute file hash
	hash, err := computeFileHash(dest)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to compute file hash", "details": err.Error()})
		return
	}

	// Update book record
	book.FilePath = dest
	book.Status = "processing"
	book.ContentHash = hash
	if err := db.Save(&book).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update book record", "details": err.Error()})
		return
	}

	// Check file size to determine sync vs async processing
	fileInfo, _ := os.Stat(dest)
	fileSizeBytes := fileInfo.Size()
	fileSizeMB := float64(fileSizeBytes) / (1024 * 1024)

	// Large files (> 5MB or estimated > 1000 chunks) use async processing
	estimatedChunks := int(fileSizeBytes / 1000)
	usesAsync := fileSizeMB > 5 || estimatedChunks > 1000

	if usesAsync {
		// Async processing for large books - returns immediately
		log.Printf("📚 Large book detected (%.2f MB, ~%d chunks), using async processing", fileSizeMB, estimatedChunks)

		estimatedPages, err := ChunkDocumentAsync(book.ID, dest)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to start document processing", "details": err.Error()})
			return
		}

		c.JSON(http.StatusAccepted, gin.H{
			"message":          "File uploaded, chunking in progress (large file)",
			"book_id":          book.ID,
			"estimated_pages":  estimatedPages,
			"file_path":        dest,
			"content_hash":     hash,
			"status":           "chunking",
			"async":            true,
			"file_size_mb":     fileSizeMB,
			"note":             "Poll GET /user/books/{book_id} to check status. Status will be 'pending' when chunking is complete.",
		})
		return
	}

	// Sync processing for smaller books (uses batch inserts for efficiency)
	numPages, err := ChunkDocumentBatch(book.ID, dest)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to paginate document", "details": err.Error()})
		return
	}

	// Query the chunk table to confirm all pages saved
	var actualChunks []BookChunk
	if err := db.Where("book_id = ?", book.ID).Find(&actualChunks).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to verify saved pages"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":      "File uploaded and split into pages successfully",
		"book_id":      book.ID,
		"total_pages":  numPages,
		"file_path":    dest,
		"content_hash": hash,
		"page_indices": len(actualChunks),
		"async":        false,
	})

	// 🔍 Debugging: Check if page 11 (index 10) exists
	var missingChunk BookChunk
	err = db.Where("book_id = ? AND index = ?", book.ID, 10).First(&missingChunk).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		fmt.Println("⚠️ Page 11 (index 10) is missing for book", book.ID)
	} else if err != nil {
		fmt.Println("❌ Error querying page 11:", err)
	} else {
		fmt.Println("✅ Page 11 exists:", missingChunk.AudioPath)
	}
}

// computeFileHash computes the SHA256 hash of the file at the given path and returns it as a hex string.
func computeFileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}


