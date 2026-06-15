package main

// fileuploadgo uploadBookFileHandler handles file uploads for books.
// It expects form-data with keys "book_id" and "file".
// It saves the file to a specified directory and updates the book record in the database.
// It also processes the uploaded file by chunking it into smaller parts for further processing.

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)


func uploadBookFileHandler(c *gin.Context) {
	bookIDStr := c.PostForm("book_id")
	if bookIDStr == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "book_id is required"})
		return
	}
	bookIDU64, err := strconv.ParseUint(bookIDStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid book_id"})
		return
	}

	// SECURITY (S6): the book must belong to the authenticated user. 404 (not
	// 403) so we don't reveal that another user's book exists.
	userID := getUserIDFromContext(c)
	if userID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	bookPtr, err := verifyBookOwnership(uint(bookIDU64), userID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Book not found"})
		return
	}
	book := *bookPtr

	file, err := c.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "File upload error", "details": err.Error()})
		return
	}

	// SECURITY (S7): enforce a max upload size at the app layer.
	if file.Size > maxUploadBytes() {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{
			"error": "File too large",
			"max_bytes": maxUploadBytes(),
		})
		return
	}

	// Check for unsupported KFX format explicitly (clearer error than the
	// generic "invalid type" below).
	if strings.HasSuffix(strings.ToLower(filepath.Base(file.Filename)), ".kfx") {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "KFX format is not supported",
			"message": "Please convert your KFX file to EPUB, PDF, MOBI, or AZW3 format first",
			"suggestion": "You can use Calibre or online converters to convert KFX files",
		})
		return
	}

	// Validate file type and capture the canonical extension. We never trust
	// the client filename for anything but its (validated) extension.
	ext := validUploadExt(file.Filename)
	if ext == "" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error": "Invalid file type. Supported formats: PDF, TXT, EPUB, MOBI, AZW, AZW3",
			"note":  "KFX format is not supported. Please convert to one of the supported formats first.",
		})
		return
	}

	// SECURITY (S7): save under a per-owner/per-book directory with a fixed
	// name, so the client filename never touches the path. This prevents
	// path traversal (../) and cross-user overwrite of a shared filename.
	bookDir := uploadDirForBook(userID, book.ID)
	if err := os.MkdirAll(bookDir, 0o755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create upload directory", "details": err.Error()})
		return
	}
	dest := filepath.Join(bookDir, "original"+ext)
	if err := c.SaveUploadedFile(file, dest); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save file", "details": err.Error()})
		return
	}

	// Q11: re-uploading replaces content. Clear any existing chunks/processed
	// groups (and their audio) so we don't duplicate pages on re-upload.
	resetBookContent(book.ID)

	// Compute file hash
	hash, err := computeFileHash(dest)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to compute file hash", "details": err.Error()})
		return
	}

	// Upload the source document to R2; store the object key. The local `dest`
	// remains on disk for the chunking step below (extraction reads it
	// directly), then becomes scratch.
	srcKey := uploadKey(userID, book.ID, ext)
	if err := store.PutFile(c.Request.Context(), srcKey, dest, contentTypeForExt(dest)); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to store upload", "details": err.Error()})
		return
	}

	// Update book record
	book.FilePath = srcKey
	book.Status = "processing"
	book.ContentHash = hash
	if err := db.Save(&book).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update book record", "details": err.Error()})
		return
	}

	// Check file size to determine sync vs async processing
	fileInfo, _ := os.Stat(dest)
	fileSizeBytes := fileInfo.Size()
	fileSizeMB := float64(fileSizeBytes) / (1024 * 1024)

	// Large files (> 5MB or estimated > 1000 chunks) use async processing
	estimatedChunks := int(fileSizeBytes / 1000)
	usesAsync := fileSizeMB > 5 || estimatedChunks > 1000

	if usesAsync {
		// Async processing for large books - returns immediately
		log.Printf("📚 Large book detected (%.2f MB, ~%d chunks), using async processing", fileSizeMB, estimatedChunks)

		estimatedPages, err := ChunkDocumentAsync(book.ID, dest)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to start document processing", "details": err.Error()})
			return
		}

		c.JSON(http.StatusAccepted, gin.H{
			"message":          "File uploaded, chunking in progress (large file)",
			"book_id":          book.ID,
			"estimated_pages":  estimatedPages,
			"file_path":        dest,
			"content_hash":     hash,
			"status":           "chunking",
			"async":            true,
			"file_size_mb":     fileSizeMB,
			"note":             "Poll GET /user/books/{book_id} to check status. Status will be 'pending' when chunking is complete.",
		})
		return
	}

	// Sync processing for smaller books (uses batch inserts for efficiency)
	numPages, err := ChunkDocumentBatch(book.ID, dest)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to paginate document", "details": err.Error()})
		return
	}

	// Query the chunk table to confirm all pages saved
	var actualChunks []BookChunk
	if err := db.Where("book_id = ?", book.ID).Find(&actualChunks).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to verify saved pages"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":      "File uploaded and split into pages successfully",
		"book_id":      book.ID,
		"total_pages":  numPages,
		"file_path":    dest,
		"content_hash": hash,
		"page_indices": len(actualChunks),
		"async":        false,
	})
}

// uploadBaseDir is the root under which all uploaded documents are stored.
const uploadBaseDir = "./uploads"

// uploadDirForBook returns the per-owner/per-book directory for an upload. The
// path is derived purely from numeric IDs, so it can never escape uploadBaseDir
// regardless of the client-supplied filename (S7).
func uploadDirForBook(userID, bookID uint) string {
	return filepath.Join(uploadBaseDir,
		strconv.FormatUint(uint64(userID), 10),
		strconv.FormatUint(uint64(bookID), 10))
}

// validUploadExt returns the lower-cased, allow-listed extension for a filename,
// or "" if the type is not supported. Only the extension of the base name is
// considered — the rest of the client filename is ignored.
func validUploadExt(filename string) string {
	lower := strings.ToLower(filepath.Base(filename))
	for _, e := range []string{".pdf", ".txt", ".epub", ".mobi", ".azw3", ".azw"} {
		if strings.HasSuffix(lower, e) {
			return e
		}
	}
	return ""
}

// maxUploadBytes is the app-layer upload size cap (default 50 MB), overridable
// via MAX_UPLOAD_BYTES.
func maxUploadBytes() int64 {
	if v := os.Getenv("MAX_UPLOAD_BYTES"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			return n
		}
	}
	return 50 << 20
}

// removeFileIfExists deletes a file path if it is non-empty, logging (not
// failing) on error. Used by cascade/reset cleanup.
func removeFileIfExists(path string) {
	if path == "" {
		return
	}
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		log.Printf("⚠️ could not remove file %s: %v", path, err)
	}
}

// resetBookContent removes a book's existing chunks and processed groups (rows
// and their on-disk audio) so a re-upload replaces content instead of
// duplicating it (Q11). Does not touch the Book row itself.
func resetBookContent(bookID uint) {
	var chunks []BookChunk
	db.Where("book_id = ?", bookID).Find(&chunks)
	for _, ch := range chunks {
		removeFileIfExists(ch.AudioPath)
		removeFileIfExists(ch.FinalAudioPath)
	}
	var groups []ProcessedChunkGroup
	db.Where("book_id = ?", bookID).Find(&groups)
	for _, g := range groups {
		removeFileIfExists(g.AudioPath)
	}
	db.Unscoped().Where("book_id = ?", bookID).Delete(&BookChunk{})
	db.Unscoped().Where("book_id = ?", bookID).Delete(&ProcessedChunkGroup{})
}

// computeFileHash computes the SHA256 hash of the file at the given path and returns it as a hex string.
func computeFileHash(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()

	hasher := sha256.New()
	if _, err := io.Copy(hasher, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(hasher.Sum(nil)), nil
}


