package main

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// CastEvent records when a user sends playback to an external output (AirPlay,
// Bluetooth, Chromecast). Analytics + a future "recently cast to" UX. Discovery
// and the actual casting happen on the device; this is just the audit trail.
type CastEvent struct {
	ID         uint `gorm:"primaryKey"`
	UserID     uint `gorm:"index"`
	BookID     uint
	DeviceName string
	RouteType  string // airplay | bluetooth | chromecast | speaker
	CreatedAt  time.Time
}

type castEventRequest struct {
	BookID     uint   `json:"book_id"`
	DeviceName string `json:"device_name"`
	RouteType  string `json:"route_type"`
}

// RecordCastEventHandler stores a cast event for the authenticated user.
// NOTE: needs an explicit nginx `location /user/cast-events` → 8083 or it 404s
// on auth-service (every content-service /user/* route does).
func RecordCastEventHandler(c *gin.Context) {
	userID := getUserIDFromContext(c)
	if userID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "unauthenticated"})
		return
	}

	var req castEventRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid cast event", "details": err.Error()})
		return
	}

	ev := CastEvent{
		UserID:     userID,
		BookID:     req.BookID,
		DeviceName: req.DeviceName,
		RouteType:  req.RouteType,
		CreatedAt:  time.Now(),
	}
	if err := db.Create(&ev).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to record cast event"})
		return
	}

	c.JSON(http.StatusAccepted, gin.H{"id": ev.ID})
}
