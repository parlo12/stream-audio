// ===============
// File: bookCoverUpload.go (snippet)
// ===============
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
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

	// save file quickly to a local temp (then upload to R2)
	uploadDir := "./uploads/covers"
	os.MkdirAll(uploadDir, os.ModePerm)
	seed := fmt.Sprintf("%s_%d", bookID, time.Now().Unix())
	dest := filepath.Join(uploadDir, seed+ext)
	c.SaveUploadedFile(file, dest)

	// Deterministic R2 key + public URL (covers are public for discovery).
	bidU, _ := strconv.ParseUint(bookID, 10, 64)
	key := coverKey(uint(bidU), seed, ext)
	coverURL := store.PublicURL(key)
	c.JSON(http.StatusAccepted, gin.H{"message": "upload in progress", "cover_url": coverURL})

	// async upload + DB + MQTT
	go func(bID, localPath, objKey, url string) {
		if _, err := uploadArtifact(context.Background(), localPath, objKey); err != nil {
			fmt.Println("cover R2 upload failed:", err)
			return
		}
		var book Book
		if err := db.First(&book, bID).Error; err != nil {
			fmt.Println("book lookup failed:", err)
			return
		}
		book.CoverPath = objKey
		book.CoverURL = url
		db.Save(&book)

		payload := map[string]interface{}{"book_id": book.ID, "cover_url": url, "timestamp": time.Now().UTC().Format(time.RFC3339)}
		data, _ := json.Marshal(payload)
		topic := fmt.Sprintf("users/%d/cover_uploaded", book.UserID)
		PublishEvent(topic, data)
	}(bookID, dest, key, coverURL)
}
