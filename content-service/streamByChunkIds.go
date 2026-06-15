package main

import (
	"net/http"
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
)

// StreamByChunkIDsRequest is the request payload for streaming by chunk IDs.
type StreamByChunkIDsRequest struct {
	ChunkIDs []uint `json:"chunk_ids" binding:"required,min=1,max=10"`
	BookID   uint   `json:"book_id" binding:"required"`
}

// streamAudioByChunkIDsHandler streams audio by matching chunk IDs.
func streamAudioByChunkIDsHandler(c *gin.Context) {
	var req StreamByChunkIDsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body", "details": err.Error()})
		return
	}

	claims, _ := c.Get("claims")
	userID := extractUserIDFromClaims(claims)

	// SECURITY (S6): the book must belong to the caller. The chunk query below
	// is already scoped to book_id, so verifying book ownership closes the IDOR.
	if _, err := verifyBookOwnership(req.BookID, userID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Book not found"})
		return
	}

	var chunks []BookChunk
	if err := db.Where("id IN ? AND book_id = ?", req.ChunkIDs, req.BookID).Find(&chunks).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch chunks", "details": err.Error()})
		return
	}
	if len(chunks) != len(req.ChunkIDs) {
		c.JSON(http.StatusNotFound, gin.H{"error": "Some chunks not found"})
		return
	}
	sort.Slice(chunks, func(i, j int) bool { return chunks[i].Index < chunks[j].Index })
	startIdx := chunks[0].Index
	endIdx := chunks[len(chunks)-1].Index

	if audioPath, found := checkIfChunkGroupProcessed(req.BookID, startIdx, endIdx); found {
		serveMedia(c, audioPath)
		return
	}

	var combined strings.Builder
	for _, chunk := range chunks {
		combined.WriteString(chunk.Content)
	}
	if len(combined.String()) > 2000 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Combined text exceeds TTS limit (2000 bytes)"})
		return
	}

	// Enqueue the merge on the worker fleet (durable; replaces TTSQueueJob).
	if err := enqueueMergeChunks(req.BookID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not queue request", "details": err.Error()})
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"message": "Your request has been queued."})
}

func extractUserIDFromClaims(claims any) uint {
	if m, ok := claims.(map[string]any); ok {
		if uid, ok := m["user_id"].(float64); ok {
			return uint(uid)
		}
	}
	return 0
}

// (Durable job processing now lives in queue.go via asynq; the old
// TTSQueueJob-polling worker and crash-recovery sweeper were removed.)
