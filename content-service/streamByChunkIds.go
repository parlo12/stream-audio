package main

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
	

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// StreamByChunkIDsRequest is the request payload for streaming by chunk IDs.
type StreamByChunkIDsRequest struct {
	ChunkIDs []uint `json:"chunk_ids" binding:"required,min=1,max=10"`
	BookID   uint   `json:"book_id" binding:"required"`
}

var once sync.Once

// streamAudioByChunkIDsHandler streams audio by matching chunk IDs.
func streamAudioByChunkIDsHandler(c *gin.Context) {
	var req StreamByChunkIDsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body", "details": err.Error()})
		return
	}

	claims, _ := c.Get("claims")
	userID := extractUserIDFromClaims(claims)

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
		c.File(audioPath)
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

	// Save job to DB
	job := TTSQueueJob{
		BookID:   req.BookID,
		ChunkIDs: joinUintSlice(req.ChunkIDs),
		Status:   "queued",
		UserID:   userID,
	}
	db.Create(&job)
	c.JSON(http.StatusAccepted, gin.H{"message": "Your request has been queued."})
}

func joinUintSlice(nums []uint) string {
	var parts []string
	for _, n := range nums {
		parts = append(parts, fmt.Sprintf("%d", n))
	}
	return strings.Join(parts, ",")
}

func extractUserIDFromClaims(claims any) uint {
	if m, ok := claims.(map[string]any); ok {
		if uid, ok := m["user_id"].(float64); ok {
			return uint(uid)
		}
	}
	return 0
}

func startTTSWorker() {
	once.Do(func() {
		go func() {
			for {
				var job TTSQueueJob
				res := db.
					Where("status = ?", "queued").
					Order("created_at, id").
					First(&job)

				// No work to do right now
				if errors.Is(res.Error, gorm.ErrRecordNotFound) {
					time.Sleep(5 * time.Second)
					continue
				}
				// Something went wrong talking to the DB
				if res.Error != nil {
					log.Printf("❌ error fetching queued TTS job: %v", res.Error)
					time.Sleep(10 * time.Second)
					continue
				}

				// Mark it in-flight
				if err := db.Model(&job).Update("status", "processing").Error; err != nil {
					log.Printf("❌ failed to mark job #%d processing: %v", job.ID, err)
					// skip processing this one for now
					time.Sleep(5 * time.Second)
					continue
				}

				// Do the work
				if err := processMergedChunks(job.BookID); err != nil {
					log.Printf("❌ processing job #%d failed: %v", job.ID, err)
					db.Model(&job).Update("status", "failed")
					continue
				}

				// Finally, mark complete
				if err := db.Model(&job).Update("status", "complete").Error; err != nil {
					log.Printf("❌ failed to mark job #%d complete: %v", job.ID, err)
				}
			}
		}()
	})
}

func parseChunkIDs(s string) []uint {
	parts := strings.Split(s, ",")
	var ids []uint
	for _, p := range parts {
		var v uint
		fmt.Sscanf(p, "%d", &v)
		ids = append(ids, v)
	}
	return ids
}
