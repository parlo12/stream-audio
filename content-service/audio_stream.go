package main

import (
	"fmt"
	"net/http"
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

	// Serve the latest merged audio (use first match). This legacy endpoint
	// globs local disk; serveMedia handles the on-disk file.
	serveMedia(c, matches[len(matches)-1])
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
    
    // Convert 1-based page to 0-based index
    chunkIndex := pageIndex - 1
    
    // Query for the chunk with final_audio_path
    var chunk BookChunk
    err := db.Where("book_id = ? AND \"index\" = ?", bookID, chunkIndex).
        First(&chunk).Error
    
    if err != nil {
        c.JSON(http.StatusNotFound, gin.H{"error": "Page not found"})
        return
    }
    
    // Check if final_audio_path exists
    if chunk.FinalAudioPath == "" {
        c.JSON(http.StatusNotFound, gin.H{"error": "Audio not ready for this page"})
        return
    }

    // Serve from R2 (302 presigned) or legacy disk (fallback).
    serveMedia(c, chunk.FinalAudioPath)
}
