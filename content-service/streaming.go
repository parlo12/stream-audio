package main

import (
	"fmt"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt"
)

func proxyBookAudioHandler(c *gin.Context) {
	bookID := c.Param("book_id")
	tokenString := c.Query("token")

	if tokenString == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Token is required"})
		return
	}

	fmt.Println("🎫 Token received:", tokenString)

	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		return jwtSecretKey, nil
	})
	if err != nil || !token.Valid {
		fmt.Println("❌ Invalid or expired token:", err)
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid or expired token"})
		return
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		fmt.Println("❌ Failed to extract claims from token")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Invalid token claims"})
		return
	}

	userIDFloat, ok := claims["user_id"].(float64)
	if !ok {
		fmt.Println("❌ User ID not found in token claims:", claims)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "User ID not found in token"})
		return
	}
	userID := uint(userIDFloat)
	fmt.Printf("✅ Token user ID: %d\n", userID)

	if bookID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Book ID is required"})
		return
	}

	fmt.Println("🔍 Looking up book with ID:", bookID)

	var book Book
	if err := db.First(&book, bookID).Error; err != nil {
		fmt.Println("❌ Book not found:", err)
		c.JSON(http.StatusNotFound, gin.H{"error": "Book not found", "details": err.Error()})
		return
	}

	fmt.Printf("📘 Book found: ID=%d, Title=%s, UserID=%d\n", book.ID, book.Title, book.UserID)

	if book.UserID != userID {
		fmt.Printf("🚫 Unauthorized access attempt. Token UserID=%d, Book Owner=%d\n", userID, book.UserID)
		c.JSON(http.StatusForbidden, gin.H{"error": "You do not have permission to access this book"})
		return
	}

	if book.AudioPath == "" {
		fmt.Println("❌ Audio path is empty for this book")
		c.JSON(http.StatusNotFound, gin.H{"error": "Audio file not available for this book"})
		return
	}

	if _, err := os.Stat(book.AudioPath); os.IsNotExist(err) {
		fmt.Println("❌ Audio file not found on disk:", book.AudioPath)
		c.JSON(http.StatusNotFound, gin.H{"error": "Audio file not found on server", "details": err.Error()})
		return
	}

	fmt.Println("🎧 Serving audio file:", book.AudioPath)
	c.Header("Content-Type", "audio/mpeg")
	c.File(book.AudioPath)
}
