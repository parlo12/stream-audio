package main

// fileuploadgo uploadBookFileHandler handles file uploads for books.
// It expects form-data with keys "book_id" and "file".
// It saves the file to a specified directory and updates the book record in the database.
// It also processes the uploaded file by chunking it into smaller parts for further processing.

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"errors"
	"fmt"

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
	if !strings.HasSuffix(strings.ToLower(file.Filename), ".pdf") &&
		!strings.HasSuffix(strings.ToLower(file.Filename), ".txt") {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid file type. Only PDF and TXT files are allowed."})
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

	// Chunk (paginate) the document
	numPages, err := ChunkDocument(book.ID, dest)
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


