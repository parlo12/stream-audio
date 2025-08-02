// ===============
// File: bookCoverUpload.go (snippet)
// ===============
package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

func uploadBookCoverHandler(c *gin.Context) {
	bookID := c.Param("book_id")
	file, err := c.FormFile("cover")
	if bookID == "" || err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "book_id and cover file are required"})
		return
	}

	// validate extensions
	ext := strings.ToLower(filepath.Ext(file.Filename))
	if ext != ".jpg" && ext != ".jpeg" && ext != ".png" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Only JPG, JPEG, PNG allowed"})
		return
	}

	// save file quickly
	uploadDir := "./uploads/covers"
	os.MkdirAll(uploadDir, os.ModePerm)
	filename := fmt.Sprintf("%s_%d%s", bookID, time.Now().Unix(), ext)
	dest := filepath.Join(uploadDir, filename)
	c.SaveUploadedFile(file, dest)

	// immediate response
	host := getEnv("STREAM_HOST", "https://content-service-9ncuf.ondigitalocean.app:8083")
	coverURL := fmt.Sprintf("%s/covers/%s", host, filename)
	c.JSON(http.StatusAccepted, gin.H{"message": "upload in progress", "cover_url": coverURL})

	// async DB + MQTT
	go func(bID, path, url string) {
		var book Book
		if err := db.First(&book, bID).Error; err != nil {
			fmt.Println("book lookup failed:", err)
			return
		}
		book.CoverPath = path
		book.CoverURL = url
		db.Save(&book)

		// publish via MQTT
		payload := map[string]interface{}{"book_id": book.ID, "cover_url": url, "timestamp": time.Now().UTC().Format(time.RFC3339)}
		data, _ := json.Marshal(payload)
		topic := fmt.Sprintf("users/%d/cover_uploaded", book.UserID)
		PublishEvent(topic, data)
	}(bookID, dest, coverURL)
}
