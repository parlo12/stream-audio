package main

import (
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// BugReport is a user-submitted problem report from the app. Stored for review
// and also announced over MQTT (topic admin/bug_reports) so it can be watched live.
type BugReport struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	UserID      uint      `gorm:"index" json:"user_id"`
	Message     string    `gorm:"type:text" json:"message"`
	DeviceModel string    `json:"device_model"`
	OSVersion   string    `json:"os_version"`
	AppVersion  string    `json:"app_version"`
	BookID      *uint     `json:"book_id,omitempty"` // current book, if any
	Page        *int      `json:"page,omitempty"`    // current page, if any
	Logs        string    `gorm:"type:text" json:"logs"`
	CreatedAt   time.Time `json:"created_at"`
}

// SubmitBugReportHandler handles POST /user/bug-report — the app sends a problem
// report here. Stores it and publishes an MQTT alert.
func SubmitBugReportHandler(c *gin.Context) {
	userID := getUserIDFromContext(c)
	var req struct {
		Message     string `json:"message"`
		DeviceModel string `json:"device_model"`
		OSVersion   string `json:"os_version"`
		AppVersion  string `json:"app_version"`
		BookID      *uint  `json:"book_id"`
		Page        *int   `json:"page"`
		Logs        string `json:"logs"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Message) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "message is required"})
		return
	}

	// Cap stored logs so a runaway buffer can't bloat the row (keep the tail).
	logs := req.Logs
	if len(logs) > 20000 {
		logs = logs[len(logs)-20000:]
	}

	report := BugReport{
		UserID:      userID,
		Message:     strings.TrimSpace(req.Message),
		DeviceModel: req.DeviceModel,
		OSVersion:   req.OSVersion,
		AppVersion:  req.AppVersion,
		BookID:      req.BookID,
		Page:        req.Page,
		Logs:        logs,
	}
	if err := db.Create(&report).Error; err != nil {
		log.Printf("❌ failed to save bug report from user %d: %v", userID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not save report"})
		return
	}

	// MQTT alert (topic admin/bug_reports) — watchable live; no new credentials.
	payload, _ := json.Marshal(map[string]interface{}{
		"id":        report.ID,
		"user_id":   userID,
		"message":   report.Message,
		"device":    req.DeviceModel,
		"os":        req.OSVersion,
		"app":       req.AppVersion,
		"book_id":   req.BookID,
		"page":      req.Page,
		"timestamp": time.Now().UTC().Format(time.RFC3339),
	})
	PublishEvent("admin/bug_reports", payload)

	log.Printf("🐞 bug report #%d from user %d (book=%v page=%v): %.100s",
		report.ID, userID, report.BookID, report.Page, report.Message)
	c.JSON(http.StatusOK, gin.H{"status": "received", "id": report.ID})
}

// ListBugReportsHandler handles GET /admin/bug-reports — newest first, for review.
func ListBugReportsHandler(c *gin.Context) {
	var reports []BugReport
	db.Order("created_at DESC").Limit(100).Find(&reports)
	c.JSON(http.StatusOK, gin.H{"count": len(reports), "reports": reports})
}
