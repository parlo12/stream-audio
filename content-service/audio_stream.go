package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/gin-gonic/gin"
)

// Serve the final merged audio after sound effects processing
func streamMergedChunkAudioHandler(c *gin.Context) {
	bookIDStr := c.Param("book_id")
	bookID, err := strconv.Atoi(bookIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid book ID"})
		return
	}

	// Check for latest merged audio for this book
	pattern := fmt.Sprintf("./audio/merged_chunk_audio_%d*.mp3", bookID)
	matches, err := filepath.Glob(pattern)
	if err != nil || len(matches) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Merged audio file not found for this book"})
		return
	}

	// Serve the latest merged audio (use first match)
	audioPath := matches[len(matches)-1]
	c.Header("Content-Type", "audio/mpeg")
	c.File(audioPath)
}

func streamSinglePageAudioHandler(c *gin.Context) {
	bookIDStr := c.Param("book_id")
	pageStr := c.Param("page")

	bookID, err1 := strconv.Atoi(bookIDStr)
	pageIndex, err2 := strconv.Atoi(pageStr)

	if err1 != nil || err2 != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid book ID or page number"})
		return
	}

	// 🔁 Match new pattern with hash suffix
	var finalPath string
	err := db.Table("book_chunks").
		Select("final_audio_path").
		Where("book_id = ? AND \"index\" = ?", bookID, pageIndex).
		Take(&finalPath).Error
	if err != nil || finalPath == "" {
		c.JSON(http.StatusNotFound, gin.H{"error": "Final audio not available for this page"})
		return
	}
	if _, err := os.Stat(finalPath); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Audio file missing on disk"})
		return
	}
	c.Header("Content-Type", "audio/mpeg")
	c.File(finalPath)
}
