package main

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

const presignPutTTL = 15 * time.Minute

type initiateUploadReq struct {
	Filename    string `json:"filename" binding:"required"`
	SizeBytes   int64  `json:"size_bytes"`
	ContentType string `json:"content_type"`
	SHA256      string `json:"sha256"`
}

// initiateUploadHandler (POST /user/books/:book_id/upload/initiate) validates the
// upload, dedups by content hash, and returns a short-lived presigned PUT URL so
// the client uploads the file directly to R2 (never through this server).
func initiateUploadHandler(c *gin.Context) {
	book := c.MustGet("book").(Book) // ownership verified by middleware
	userID := getUserIDFromContext(c)
	accountType := accountTypeFromClaims(c)

	// Uploads quota pre-check (consumed on /complete, once per book).
	if d := checkAndConsume(userID, accountType, "uploads", 0, book.ID); !d.Allowed {
		quota429(c, d)
		return
	}

	var req initiateUploadReq
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request", "details": err.Error()})
		return
	}
	ext := validUploadExt(req.Filename)
	if ext == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "unsupported file type (pdf, txt, epub, mobi, azw, azw3)"})
		return
	}
	if req.SizeBytes > maxUploadBytes() {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "file too large", "max_bytes": maxUploadBytes()})
		return
	}

	// Dedup: if another already-parsed book has this exact content hash, reuse
	// its uploaded object — point this book at the same key and parse, no upload.
	if req.SHA256 != "" {
		var existing Book
		if err := db.Where("content_hash = ? AND file_path <> '' AND id <> ?", req.SHA256, book.ID).
			First(&existing).Error; err == nil && existing.FilePath != "" {
			db.Model(&Book{}).Where("id = ?", book.ID).Updates(map[string]interface{}{
				"file_path":    existing.FilePath,
				"content_hash": req.SHA256,
				"status":       "parsing",
			})
			if err := enqueueParseBook(book.ID); err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": "could not queue parse", "details": err.Error()})
				return
			}
			c.JSON(http.StatusOK, gin.H{"dedup": true, "message": "identical file already uploaded; parsing"})
			return
		}
	}

	key := uploadKey(userID, book.ID, ext)
	url, err := store.PresignPut(c.Request.Context(), key, presignPutTTL, req.ContentType)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not presign upload", "details": err.Error()})
		return
	}
	// Persist the intended key + hash so /complete and the sweeper can find it.
	db.Model(&Book{}).Where("id = ?", book.ID).Updates(map[string]interface{}{
		"file_path":    key,
		"content_hash": req.SHA256,
		"status":       "awaiting_upload",
	})

	c.JSON(http.StatusOK, gin.H{
		"dedup":              false,
		"upload_url":         url,
		"key":                key,
		"content_type":       req.ContentType, // client MUST send exactly this on the PUT
		"expires_in_seconds": int(presignPutTTL.Seconds()),
	})
}

// completeUploadHandler (POST /user/books/:book_id/upload/complete) confirms the
// object landed in R2 and enqueues parsing. Idempotent.
func completeUploadHandler(c *gin.Context) {
	book := c.MustGet("book").(Book)
	if book.FilePath == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no upload was initiated for this book"})
		return
	}
	ok, err := store.Exists(c.Request.Context(), book.FilePath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not verify upload", "details": err.Error()})
		return
	}
	if !ok {
		c.JSON(http.StatusConflict, gin.H{"error": "uploaded object not found in storage"})
		return
	}
	// Count the upload once (only on the first completion — status is still
	// awaiting_upload; idempotent on repeat calls).
	if book.Status == "awaiting_upload" {
		checkAndConsume(getUserIDFromContext(c), accountTypeFromClaims(c), "uploads", 1, book.ID)
	}
	db.Model(&Book{}).Where("id = ?", book.ID).Update("status", "parsing")
	if err := enqueueParseBook(book.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not queue parse", "details": err.Error()})
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"message": "upload complete; parsing", "book_id": book.ID})
}
