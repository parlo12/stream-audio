package main

import (
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// PlaybackProgress tracks where a user stopped listening to a book
type PlaybackProgress struct {
	ID                 uint      `gorm:"primaryKey" json:"id"`
	UserID             uint      `gorm:"index;not null" json:"user_id"`
	BookID             uint      `gorm:"index;not null" json:"book_id"`
	CurrentPosition    float64   `gorm:"not null;default:0" json:"current_position"`     // Current playback position in seconds
	Duration           float64   `gorm:"not null;default:0" json:"duration"`             // Total duration of the book in seconds
	ChunkIndex         int       `gorm:"not null;default:0" json:"chunk_index"`          // Current chunk/page index
	CompletionPercent  float64   `gorm:"not null;default:0" json:"completion_percent"`   // Percentage completed (0-100)
	LastPlayedAt       time.Time `gorm:"not null" json:"last_played_at"`                 // When the user last played this book
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

// UpdateProgressRequest defines the JSON structure for updating progress
type UpdateProgressRequest struct {
	BookID          uint    `json:"book_id" binding:"required"`
	CurrentPosition float64 `json:"current_position" binding:"required"` // Position in seconds
	Duration        float64 `json:"duration"`                            // Total duration (optional, will be calculated if not provided)
	ChunkIndex      int     `json:"chunk_index"`                         // Current chunk/page index
}

// ProgressResponse returns progress information for a book
type ProgressResponse struct {
	BookID            uint      `json:"book_id"`
	CurrentPosition   float64   `json:"current_position"`
	Duration          float64   `json:"duration"`
	ChunkIndex        int       `json:"chunk_index"`
	CompletionPercent float64   `json:"completion_percent"`
	LastPlayedAt      time.Time `json:"last_played_at"`
}

// UpdatePlaybackProgressHandler updates the user's playback progress for a book
// POST /user/books/:book_id/progress
func UpdatePlaybackProgressHandler(c *gin.Context) {
	// 1. Get user ID from JWT token
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not authenticated"})
		return
	}

	// 2. Parse request body
	var req UpdateProgressRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request body", "details": err.Error()})
		return
	}

	// 3. Validate that current_position is non-negative
	if req.CurrentPosition < 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "current_position must be non-negative"})
		return
	}

	// 4. Verify the book exists and belongs to the user
	var book Book
	if err := db.Where("id = ? AND user_id = ?", req.BookID, userID).First(&book).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Book not found or does not belong to user"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error", "details": err.Error()})
		}
		return
	}

	// 5. Calculate duration if not provided (from book chunks)
	duration := req.Duration
	if duration == 0 {
		var chunks []BookChunk
		if err := db.Where("book_id = ?", req.BookID).Order("index").Find(&chunks).Error; err == nil {
			if len(chunks) > 0 {
				lastChunk := chunks[len(chunks)-1]
				duration = float64(lastChunk.EndTime)
			}
		}
	}

	// 6. Calculate completion percentage
	completionPercent := 0.0
	if duration > 0 {
		completionPercent = (req.CurrentPosition / duration) * 100
		if completionPercent > 100 {
			completionPercent = 100
		}
	}

	// 7. Find or create progress record
	var progress PlaybackProgress
	result := db.Where("user_id = ? AND book_id = ?", userID, req.BookID).First(&progress)

	if result.Error == gorm.ErrRecordNotFound {
		// Create new progress record
		progress = PlaybackProgress{
			UserID:            userID.(uint),
			BookID:            req.BookID,
			CurrentPosition:   req.CurrentPosition,
			Duration:          duration,
			ChunkIndex:        req.ChunkIndex,
			CompletionPercent: completionPercent,
			LastPlayedAt:      time.Now(),
		}
		if err := db.Create(&progress).Error; err != nil {
			log.Printf("❌ Failed to create progress: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save progress", "details": err.Error()})
			return
		}
		log.Printf("✅ Created new progress for user %d, book %d at %.2fs", userID, req.BookID, req.CurrentPosition)
	} else if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error", "details": result.Error.Error()})
		return
	} else {
		// Update existing progress record
		progress.CurrentPosition = req.CurrentPosition
		progress.Duration = duration
		progress.ChunkIndex = req.ChunkIndex
		progress.CompletionPercent = completionPercent
		progress.LastPlayedAt = time.Now()

		if err := db.Save(&progress).Error; err != nil {
			log.Printf("❌ Failed to update progress: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update progress", "details": err.Error()})
			return
		}
		log.Printf("✅ Updated progress for user %d, book %d to %.2fs (%.1f%%)", userID, req.BookID, req.CurrentPosition, completionPercent)
	}

	// 8. Return updated progress
	c.JSON(http.StatusOK, ProgressResponse{
		BookID:            progress.BookID,
		CurrentPosition:   progress.CurrentPosition,
		Duration:          progress.Duration,
		ChunkIndex:        progress.ChunkIndex,
		CompletionPercent: progress.CompletionPercent,
		LastPlayedAt:      progress.LastPlayedAt,
	})
}

// GetPlaybackProgressHandler retrieves the user's playback progress for a specific book
// GET /user/books/:book_id/progress
func GetPlaybackProgressHandler(c *gin.Context) {
	// 1. Get user ID from JWT token
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not authenticated"})
		return
	}

	// 2. Get book ID from URL parameter
	bookID := c.Param("book_id")

	// 3. Verify the book exists and belongs to the user
	var book Book
	if err := db.Where("id = ? AND user_id = ?", bookID, userID).First(&book).Error; err != nil {
		if err == gorm.ErrRecordNotFound {
			c.JSON(http.StatusNotFound, gin.H{"error": "Book not found or does not belong to user"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error", "details": err.Error()})
		}
		return
	}

	// 4. Find progress record
	var progress PlaybackProgress
	result := db.Where("user_id = ? AND book_id = ?", userID, bookID).First(&progress)

	if result.Error == gorm.ErrRecordNotFound {
		// No progress found - return default values (start from beginning)
		c.JSON(http.StatusOK, ProgressResponse{
			BookID:            book.ID,
			CurrentPosition:   0,
			Duration:          0,
			ChunkIndex:        0,
			CompletionPercent: 0,
			LastPlayedAt:      time.Time{},
		})
		return
	} else if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Database error", "details": result.Error.Error()})
		return
	}

	// 5. Return progress
	c.JSON(http.StatusOK, ProgressResponse{
		BookID:            progress.BookID,
		CurrentPosition:   progress.CurrentPosition,
		Duration:          progress.Duration,
		ChunkIndex:        progress.ChunkIndex,
		CompletionPercent: progress.CompletionPercent,
		LastPlayedAt:      progress.LastPlayedAt,
	})
}

// GetAllPlaybackProgressHandler retrieves all playback progress for the authenticated user
// GET /user/progress
func GetAllPlaybackProgressHandler(c *gin.Context) {
	// 1. Get user ID from JWT token
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not authenticated"})
		return
	}

	// 2. Retrieve all progress records for the user, ordered by last played
	var progressRecords []PlaybackProgress
	if err := db.Where("user_id = ?", userID).Order("last_played_at DESC").Find(&progressRecords).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve progress", "details": err.Error()})
		return
	}

	// 3. Build response
	var response []ProgressResponse
	for _, p := range progressRecords {
		response = append(response, ProgressResponse{
			BookID:            p.BookID,
			CurrentPosition:   p.CurrentPosition,
			Duration:          p.Duration,
			ChunkIndex:        p.ChunkIndex,
			CompletionPercent: p.CompletionPercent,
			LastPlayedAt:      p.LastPlayedAt,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"progress": response,
		"count":    len(response),
	})
}

// DeletePlaybackProgressHandler deletes progress for a specific book (reset to start)
// DELETE /user/books/:book_id/progress
func DeletePlaybackProgressHandler(c *gin.Context) {
	// 1. Get user ID from JWT token
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "User not authenticated"})
		return
	}

	// 2. Get book ID from URL parameter
	bookID := c.Param("book_id")

	// 3. Delete progress record
	result := db.Where("user_id = ? AND book_id = ?", userID, bookID).Delete(&PlaybackProgress{})
	if result.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete progress", "details": result.Error.Error()})
		return
	}

	if result.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "No progress found for this book"})
		return
	}

	log.Printf("🗑️  Deleted progress for user %d, book %s", userID, bookID)
	c.JSON(http.StatusOK, gin.H{"message": "Progress deleted successfully"})
}