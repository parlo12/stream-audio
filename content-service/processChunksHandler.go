package main

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
)

// convertTextToAudio converts text to audio using OpenAI's TTS API.

func ProcessChunksTTSHandler(c *gin.Context) {

	authHeader := c.GetHeader("Authorization")
	token, err := extractToken(authHeader)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Missing or invalid token"})
		return
	}

	accountType := accountTypeFromClaims(c)
	if accountType == "" {
		at, err := getUserAccountType(token)
		if err != nil {
			log.Printf("Error checking account type: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to verify account type"})
			return
		}
		accountType = at
	}

	var req struct {
		BookID uint  `json:"book_id"`
		Pages  []int `json:"pages"` // 1-based page numbers
	}
	if err := c.ShouldBindJSON(&req); err != nil || len(req.Pages) == 0 || len(req.Pages) > 2 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "You must provide 1 or 2 pages to process"})
		return
	}

	// SECURITY (S6): the book must belong to the caller. 404 (not 403) so we
	// don't reveal that another user's book exists.
	if _, err := verifyBookOwnership(req.BookID, getUserIDFromContext(c)); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Book not found"})
		return
	}

	// Quota: consume the transcription pages this request will process.
	if d := checkAndConsume(getUserIDFromContext(c), accountType, "transcribe_pages", int64(len(req.Pages)), req.BookID); !d.Allowed {
		quota429(c, d)
		return
	}

	// Convert pages (index + 1) to chunk indices for the specific book
	var chunks []BookChunk
	if err := db.Where("book_id = ? AND index IN ?", req.BookID, toZeroBasedIndexes(req.Pages)).
		Order("index ASC").
		Find(&chunks).Error; err != nil || len(chunks) != len(req.Pages) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid page numbers for the given book_id"})
		return
	}

	// Process each chunk. Already-completed pages are a no-op success (look-ahead
	// may have finished them), not an error.
	var audioPaths []string
	maxIndex := -1
	for _, chunk := range chunks {
		if chunk.Index > maxIndex {
			maxIndex = chunk.Index
		}
		if chunk.TTSStatus == "completed" {
			continue
		}
		pageIndex := chunk.Index + 1 // Convert to 1-based index for user-friendly messages
		db.Model(&chunk).Update("TTSStatus", "processing")
		audioPath, err := convertTextToAudioForChunk(chunk)
		if err != nil {
			db.Model(&chunk).Update("TTSStatus", "failed")
			continue
		}
		chunk.AudioPath = audioPath
		chunk.TTSStatus = "completed"
		db.Save(&chunk)
		audioPaths = append(audioPaths, audioPath)

		// ✅ NEW: trigger the per-page final merge
		book := Book{}
		if err := db.First(&book, chunk.BookID).Error; err != nil {
			log.Printf("failed to find book %d: %v", chunk.BookID, err)
			continue
		} else {
			// Launch sound effects and merging in the background
			log.Printf("🚀 Launching effects merge for book ID %d, chunk index %d", book.ID, pageIndex)
			go processSoundEffectsAndMerge(book, book.ContentHash, []int{chunk.Index})
		}
	}

	// Attempt to merge (optional). Q7: check the error we actually returned.
	if errs := processMergedChunks(req.BookID); errs != nil {
		log.Printf("merge processing failed: %v", errs)
	}

	// Look-ahead: transcribe + HLS-package the next pages so HLS is ready before
	// the listener advances (makes HLS the primary playback path, not MP3
	// fallback). Bounded by LOOKAHEAD_PAGES; also re-triggered as progress moves.
	if maxIndex >= 0 {
		_ = enqueueLookAhead(req.BookID, maxIndex+1, lookAheadPages(), getUserIDFromContext(c), accountType)
	}

	c.JSON(http.StatusOK, gin.H{
		"message":     "TTS processing started",
		"audio_paths": audioPaths,
	})

}

func toZeroBasedIndexes(pages []int) []int {
	indices := make([]int, len(pages))
	for i, p := range pages {
		indices[i] = p - 1
	}
	return indices
}

func extractIDs(chunks []BookChunk) []uint {
	ids := make([]uint, len(chunks))
	for i, ch := range chunks {
		ids[i] = ch.ID
	}
	return ids
}
