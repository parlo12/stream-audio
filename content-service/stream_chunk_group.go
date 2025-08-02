package main

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// streamChunkGroupAudioHandler returns the merged audio for a specific chunk group if it exists.
func streamChunkGroupAudioHandler(c *gin.Context) {
	bookIDStr := c.Param("book_id")
	startStr := c.Param("start")
	endStr := c.Param("end")

	bookID, err1 := strconv.Atoi(bookIDStr)
	startIdx, err2 := strconv.Atoi(startStr)
	endIdx, err3 := strconv.Atoi(endStr)
	if err1 != nil || err2 != nil || err3 != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid parameters"})
		return
	}

	audioPath, found := checkIfChunkGroupProcessed(uint(bookID), startIdx, endIdx)
	if !found {
		c.JSON(http.StatusNotFound, gin.H{"error": fmt.Sprintf("No audio found for chunks %d-%d", startIdx, endIdx)})
		return
	}

	c.File(audioPath)
}
