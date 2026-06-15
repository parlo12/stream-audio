package main

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

// requireBookOwnership is a Gin middleware for routes with a :book_id path
// param. It loads the book scoped to the authenticated user and aborts with
// 404 if the book does not exist OR does not belong to the caller (returning
// 404 rather than 403 so the endpoint never reveals that another user's book
// exists). On success the loaded book is stored in the context under "book"
// so handlers can reuse it via c.MustGet("book").(Book).
func requireBookOwnership() gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := getUserIDFromContext(c)
		if userID == 0 {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			return
		}

		bookID, err := strconv.ParseUint(c.Param("book_id"), 10, 64)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": "Invalid book_id"})
			return
		}

		book, err := verifyBookOwnership(uint(bookID), userID)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"error": "Book not found"})
			return
		}

		c.Set("book", *book)
		c.Next()
	}
}

// verifyBookOwnership loads a book only if it belongs to userID. It returns
// gorm.ErrRecordNotFound both when the book is missing and when it belongs to
// someone else, so callers can treat "not yours" as "not found". Use this for
// routes that carry book_id in the body/form instead of the path.
func verifyBookOwnership(bookID, userID uint) (*Book, error) {
	var book Book
	if err := db.Where("id = ? AND user_id = ?", bookID, userID).First(&book).Error; err != nil {
		return nil, err
	}
	return &book, nil
}
