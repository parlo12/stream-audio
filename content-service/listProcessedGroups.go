package main

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// listProcessedChunkGroupsHandler returns all processed chunk ranges for a book.
func listProcessedChunkGroupsHandler(c *gin.Context) {
	bookIDStr := c.Param("book_id")
	bookID, err := strconv.Atoi(bookIDStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid book ID"})
		return
	}

	var groups []ProcessedChunkGroup
	if err := db.Where("book_id = ?", bookID).Order("start_idx").Find(&groups).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch processed chunk groups", "details": err.Error()})
		return
	}

	results := make([]gin.H, 0)
	for _, g := range groups {
		results = append(results, gin.H{
			"start_index": g.StartIdx,
			"end_index":   g.EndIdx,
			"audio_path":  g.AudioPath,
		})
	}

	c.JSON(http.StatusOK, results)
}
