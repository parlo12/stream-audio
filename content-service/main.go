package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt"

	_ "github.com/lib/pq"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// Global variables
var db *gorm.DB

// Use the JWT secret from an environment variable.
var jwtSecretKey = []byte(mustEnv("JWT_SECRET"))

// mustEnv returns the env var value or exits — services must never run
// with a default/guessable secret.
func mustEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		log.Fatalf("FATAL: required environment variable %s is not set", key)
	}
	return v
}

// Allowed categories for validation
// Allowed book categories — keep in sync with the iOS Upload screen's `categories`
// list (UploadBookView.swift). Matched case-insensitively (isValidCategory).
var allowedCategories = []string{"Fiction", "Non-fiction", "Poetry", "Children's", "Young Adult", "Classics", "Drama", "Comics & Graphic Novels", "Anthology", "Reference"}

// Book represents the model for a book uploaded by a user.
type Book struct {
	ID          uint   `gorm:"primaryKey"`
	Title       string `gorm:"not null"`
	Author      string // Optional author field
	Content     string `gorm:"type:text"` // Text content of the book
	ContentHash string `gorm:"index"`
	FilePath    string // Local storage file path.
	AudioPath   string // Path/URL of the generated (merged) audio.
	Status      string `gorm:"default:'pending'"`
	Category    string `gorm:"not null;index"`
	Genre       string `gorm:"index"`
	UserID      uint   `gorm:"index"`
	CoverPath   string // Optional cover image path
	CoverURL    string // Optional cover image URL for public access
	VoiceMap     string `gorm:"type:text"` // JSON character→{gender,voice} cast (voice continuity, audit H1)
	ScorePalette string `gorm:"type:text"` // JSON []ScoreCue — per-book music palette (audit H2)
	AudioProfile string `gorm:"type:text"`
	TTSEngine    string `gorm:"size:32"` // voice engine pinned at creation ("openai"|"kokoro"; empty = openai) // JSON AudioProfile — fiction/genre/era (audit H3)
	Index       int    // Index of the book in the list
	CreatedAt   time.Time
	UpdatedAt   time.Time
}

// BookRequest defines the expected JSON structure for creating a book.
type BookRequest struct {
	Title    string `json:"title" binding:"required"`
	Author   string `json:"author"`
	Category string `json:"category" binding:"required"`
	Genre    string `json:"genre"`
}

// Chunk represents the model for chunks or segments of boook
type BookChunk struct {
	ID     uint `gorm:"primaryKey"`
	// Composite index on (book_id, index): every per-page merge/HLS/claim does
	// WHERE book_id=? AND index=? thousands of times per large book; without it
	// each scans the whole book_id partition. Non-unique (existing data may
	// hold retry-era duplicates; the parse lock prevents new ones).
	BookID uint `gorm:"index;index:idx_bookchunk_book_index"`
	Index  int  `gorm:"index:idx_bookchunk_book_index"` // Index of the chunk in the book
	Content        string `gorm:"type:text"` // Text content of the chunk
	AudioPath      string `gorm:"not null"`
	FinalAudioPath string `json:"final_audio_path"` // 👈 New field
	HLSPath        string `json:"hls_path"`         // R2 key of the HLS playlist (Phase 5C)
	TimingMap      string `gorm:"type:text" json:"-"` // segment rune-span → seconds table (audit 2B)
	TTSStatus      string // values: "pending", "processing", "completed", "failed"
	StartTime      int64  // Start time in seconds
	EndTime        int64  // End time in seconds
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type TTSQueueJob struct {
	ID        uint   `gorm:"primaryKey"`
	BookID    uint   `gorm:"index"`
	ChunkIDs  string // Comma-separated chunk ID list
	Status    string `gorm:"default:'queued'"` // queued, processing, complete, failed
	CreatedAt time.Time
	UpdatedAt time.Time
	UserID    uint `gorm:"index"`
}
type BookResponse struct {
	ID          uint   `json:"id"`
	Title       string `json:"title"`
	Author      string `json:"author"`
	Category    string `json:"category"`
	Content     string `json:"content,omitempty"` // Optional, can be omitted for public response
	ContentHash string `json:"content_hash"`
	Genre       string `json:"genre"`
	FilePath    string `json:"file_path"`
	AudioPath   string `json:"audio_path"`
	Status      string `json:"status"`
	StreamURL   string `json:"stream_url"`
	CoverURL    string `json:"cover_url"`
	CoverPath   string `json:"cover_path"`
}

func main() {

	// err := godotenv.Load()
	// if err != nil {
	// 	log.Println("⚠️ Could not load .env file, using system env variables")
	// }
	// Set up the database connection and run migrations.
	setupDatabase()

	// Initialize object storage (Cloudflare R2). Media is stored in R2 and
	// streamed via presigned URLs; the service can't serve media without it.
	var serr error
	store, serr = newR2StoreFromEnv()
	if serr != nil {
		log.Fatalf("FATAL: media storage not configured: %v", serr)
	}
	log.Println("✅ Media store (R2) initialized")

	// MQTT initialization
	go InitMQTT()

	// Redis counter client for quotas (every mode — workers consume too).
	if err := initRedis(); err != nil {
		log.Fatalf("FATAL: redis (quota) init failed: %v", err)
	}

	// Job-queue enqueuer (asynq) — needed in every mode.
	if err := initQueueClient(); err != nil {
		log.Fatalf("FATAL: queue client init failed: %v", err)
	}

	// APNs push (token-based). No-op if unconfigured. Needed in every mode:
	// the worker sends pushes; the API registers device tokens.
	initAPNs()

	// RUN_MODE selects the role: api (HTTP only), worker (asynq consumer only),
	// or both (default — local dev).
	mode := getEnv("RUN_MODE", "both")
	if mode == "worker" {
		log.Println("▶ RUN_MODE=worker (asynq consumer, no HTTP)")
		if err := startAsyncWorker(); err != nil { // blocks
			log.Fatalf("asynq worker failed: %v", err)
		}
		return
	}
	if mode == "both" {
		go func() {
			if err := startAsyncWorker(); err != nil {
				log.Printf("⚠️ asynq worker stopped: %v", err)
			}
		}()
	}

	// Prometheus collectors (asynq queue metrics from Redis).
	if err := initMetrics(); err != nil {
		log.Printf("⚠️ metrics init failed: %v", err)
	}

	// Initialize Gin router.
	router := gin.Default()

	// Health check/root response
	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok", "service": "content-service"})
	})

	// Prometheus scrape endpoint.
	router.GET("/metrics", metricsHandler())

	// Insanaty check for MQTT
	router.GET("/debug/mqtt", func(c *gin.Context) {
		PublishEvent("debug/ping", []byte("hi from content-service file"))
		c.JSON(200, gin.H{"ok": true})
	})

	// SECURITY: do NOT serve ./audio statically — it exposed every user's
	// generated audiobook publicly with no auth. All audio is streamed through
	// the authenticated /user/... endpoints instead.

	// Static cover files: intentionally public (book covers are not paid
	// content, and the iOS app loads cover_url without an auth header).
	router.Static("/covers", "./uploads/covers")

	// Calling Streaming Route outside of the authorized group
	// router.GET("/user/books/stream/proxy/:id", proxyBookAudioHandler)

	// Protected routes group.
	authorized := router.Group("/user")
	authorized.Use(authMiddleware())
	{ // handles book creation, listing, and file uploads
		// SECURITY (S6): every route that targets a specific :book_id is gated
		// by requireBookOwnership() so a user can only act on their own books.
		// Routes that take book_id in the body/form (upload, /chunks/tts,
		// /chunks/audio-by-id) verify ownership inline in their handlers.
		// Register this device's APNs token for push notifications.
		authorized.POST("/device-token", RegisterDeviceTokenHandler)

		// User-submitted bug/problem report from the app.
		authorized.POST("/bug-report", SubmitBugReportHandler)

		// Remote config: feature flags, copy, colors, displayed pricing, and the
		// min-supported-build version gate. Resolved per-tier from the JWT
		// account_type claim; editable live via SQL (no redeploy). NOTE: needs an
		// explicit nginx `location /user/config` → 8083 or it 404s (auth-service).
		authorized.GET("/config", getUserConfigHandler)

		// Casting: record when a user sends playback to an external output
		// (AirPlay/Bluetooth/Chromecast). Needs an explicit nginx location → 8083.
		authorized.POST("/cast-events", RecordCastEventHandler)

		authorized.POST("/books/:book_id/cover", requireBookOwnership(), uploadBookCoverHandler)

		// Create a new book
		authorized.POST("/books", createBookHandler)
		// List all books for the authenticated user
		authorized.GET("/books", listBooksHandler)

		// Upload a book file
		authorized.POST("/books/upload", uploadBookFileHandler)
		// List all chunks for a book
		authorized.GET("/books/:book_id/chunks/pages", requireBookOwnership(), listBookPagesHandler) // New handler for listing book pages
		// authorized.GET("/books/stream/proxy/:id", proxyBookAudioHandler)

		authorized.GET("/books/stream/proxy/:book_id", proxyBookAudioHandler)
		authorized.POST("/chunks/tts", ProcessChunksTTSHandler)
		authorized.GET("/chunks/tts/merged-audio/:book_id", requireBookOwnership(), streamMergedChunkAudioHandler)
		authorized.GET("/books/:book_id/chunks/:start/:end/audio", requireBookOwnership(), streamChunkGroupAudioHandler)
		//authorized.GET("/chunks/status", checkChunkQueueStatusHandler)

		//Batch Transcribe Book Page-by-Page (Sequentially)
		authorized.POST("/books/:book_id/tts/batch", requireBookOwnership(), BatchTranscribeBookHandler)
		// processing old chunks
		authorized.GET("/books/:book_id/chunks/processed", requireBookOwnership(), listProcessedChunkGroupsHandler)
		// stream audio by chunk IDs
		authorized.POST("/chunks/audio-by-id", streamAudioByChunkIDsHandler)

		// adding a new route to delate a book by ID or title
		authorized.DELETE("/books/:book_id", requireBookOwnership(), deleteBookHandler)

		// adding a new route to pull one book by ID
		authorized.GET("/books/:book_id", requireBookOwnership(), getSingleBookHandler)

		// Presigned direct-to-R2 upload (Phase 3): client uploads the file
		// straight to R2, server only mints the URL + parses on completion.
		authorized.POST("/books/:book_id/upload/initiate", requireBookOwnership(), initiateUploadHandler)
		authorized.POST("/books/:book_id/upload/complete", requireBookOwnership(), completeUploadHandler)

		// adding a route to pull audio and backgrond music for a book
		authorized.GET("/books/:book_id/pages/:page/audio", requireBookOwnership(), streamSinglePageAudioHandler)
		// HLS playlist for a page (Phase 5C) — segments served direct from R2.
		authorized.GET("/books/:book_id/pages/:page/hls.m3u8", requireBookOwnership(), serveHLSHandler)
		// HEAD probe (client decides HLS vs MP3). Gin won't serve HEAD on the GET
		// route, so register it explicitly or HLS is never used on-device.
		authorized.HEAD("/books/:book_id/pages/:page/hls.m3u8", requireBookOwnership(), headHLSHandler)

		// Book search/discovery endpoint - AI-powered book suggestions
		authorized.POST("/search-books", SearchBooksHandler)

		// Book cover search and selection endpoints
		authorized.POST("/search-book-covers", SearchBookCoversHandler)
		authorized.POST("/books/:book_id/select-cover", SelectBookCoverHandler)

		// Playback progress tracking endpoints
		authorized.POST("/books/:book_id/progress", UpdatePlaybackProgressHandler)   // Update progress
		authorized.GET("/books/:book_id/progress", GetPlaybackProgressHandler)       // Get progress for a book
		authorized.GET("/progress", GetAllPlaybackProgressHandler)                   // Get all progress for user
		authorized.DELETE("/books/:book_id/progress", DeletePlaybackProgressHandler) // Reset progress for a book

		// Listening statistics endpoints
		authorized.GET("/stats/most-played", GetMostPlayedBooksHandler) // Get most played books
		authorized.GET("/stats/by-genre", GetStatsByGenreHandler)       // Get stats grouped by genre

		// Social discovery (Home sections). NOTE: needs an nginx
		// location /user/discover → :8083 like every content /user/* route.
		authorized.GET("/discover/state", DiscoverByStateHandler)        // public users in the caller's state
		authorized.POST("/discover/contacts", DiscoverContactsHandler)   // on-device-hashed contact matching

		// Free books (Project Gutenberg catalog). NOTE: needs an nginx
		// location /user/gutenberg → :8083.
		authorized.GET("/gutenberg/search", SearchGutenbergHandler)   // search the free catalog (legacy, build ≤16)
		authorized.POST("/gutenberg/import", ImportGutenbergHandler)  // import a free book → audiobook (legacy, build ≤16)

		// Unified free books (Gutenberg + Internet Archive). NOTE: needs an
		// nginx location /user/freebooks → :8083.
		authorized.GET("/freebooks/search", SearchFreeBooksHandler)  // merged multi-source search
		authorized.POST("/freebooks/import", ImportFreeBookHandler)  // import {source, source_id}

		// Follow graph
		authorized.POST("/follow", FollowUserHandler)              // follow {user_id}
		authorized.DELETE("/follow/:user_id", UnfollowUserHandler) // unfollow
		authorized.GET("/following", ListFollowingHandler)         // people I follow
		authorized.GET("/followers", ListFollowersHandler)         // people who follow me
		authorized.GET("/follow/counts", FollowCountsHandler)      // {following, followers}

	}

	// Admin routes group
	admin := router.Group("/admin")
	admin.Use(authMiddleware(), adminMiddleware())
	{
		admin.DELETE("/users/:user_id/files", deleteUserFilesContentHandler)
		admin.DELETE("/files", deleteFileContentHandler)
		admin.GET("/files/tree", getFileTreeContentHandler)
		admin.GET("/bug-reports", ListBugReportsHandler)
		admin.POST("/gutenberg/refresh", RefreshGutenbergHandler)
	}

	for _, r := range router.Routes() {
		log.Printf("→ %s %s", r.Method, r.Path)
	}

	// Use PORT env var if set; default to 8083.
	port := os.Getenv("PORT")
	if port == "" {

		port = "8083"
	}
	log.Printf("📡 Content service listening on port %s", port)

	//router.Run(":" + port)
	if err := router.Run(":" + port); err != nil {
		log.Fatalf("❌ Failed to start server: %v", err)
	}
}

// setupDatabase connects to PostgreSQL and auto migrates the Book model.
func setupDatabase() {
	dbHost := getEnv("DB_HOST", "")
	dbUser := getEnv("DB_USER", "")
	dbPassword := getEnv("DB_PASSWORD", "")
	dbName := getEnv("DB_NAME", "")
	dbPort := getEnv("DB_PORT", "")
	sslMode := getEnv("DB_SSLMODE", "disable") // “disable” for local, override to “require” in prod
	dsn := fmt.Sprintf(
		"host=%s user=%s password=%s dbname=%s port=%s sslmode=%s TimeZone=UTC",
		dbHost, dbUser, dbPassword, dbName, dbPort, sslMode,
	)

	var err error
	db, err = gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	if sqlDB, derr := db.DB(); derr == nil {
		sqlDB.SetMaxOpenConns(envInt("DB_MAX_OPEN", 20))
		sqlDB.SetMaxIdleConns(envInt("DB_MAX_IDLE", 5))
		sqlDB.SetConnMaxLifetime(30 * time.Minute)
	}

	log.Printf("Connected to database host=%s dbname=%s sslmode=%s", dbHost, dbName, sslMode)

	// Only the API owns schema migrations. Workers skip AutoMigrate so a
	// co-deploy doesn't race two concurrent CREATE TABLEs (Postgres DDL race).
	if getEnv("RUN_MODE", "both") != "worker" {
		if err := db.AutoMigrate(&Book{}, &BookChunk{}, &ProcessedChunkGroup{}, &TTSQueueJob{}, &PlaybackProgress{}, &TranscriptionBatch{}, &PlanLimit{}, &UsageEvent{}, &DeviceToken{}, &BugReport{}, &AppConfig{}, &CastEvent{}, &Follow{}); err != nil {
			log.Fatalf("AutoMigrate failed: %v", err)
		}
		seedPlanLimits()
		seedAppConfig()
		initGutenbergCatalog() // migrate + ingest the free-books catalog (async)
	}
	log.Println("Database connected and migrated successfully")
}

func createBookHandler(c *gin.Context) {
	var req BookRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("Error in book request binding: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid book data", "details": err.Error()})
		return
	}

	if !isValidCategory(req.Category) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid category", "allowed_categories": allowedCategories})
		return
	}

	claims, exists := c.Get("claims")
	if !exists {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Authentication claims missing"})
		return
	}
	userClaims, ok := claims.(jwt.MapClaims)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Invalid token claims"})
		return
	}
	userIDFloat, ok := userClaims["user_id"].(float64)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "User ID not found in token"})
		return
	}
	userID := uint(userIDFloat)

	book := Book{
		Title:    req.Title,
		Author:   req.Author,
		Category: req.Category,
		Genre:    req.Genre,
		Status:   "pending",
		UserID:   userID,
	}
	book.TTSEngine = defaultTTSEngine()
	if err := db.Create(&book).Error; err != nil {
		log.Printf("Error creating book record: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save book", "details": err.Error()})
		return
	}

	// Automatically fetch the book cover on the worker fleet (durable).
	if err := enqueueFetchCover(book.ID, book.Title, book.Author); err != nil {
		log.Printf("⚠️ Failed to enqueue cover fetch for book %d: %v", book.ID, err)
	}

	c.JSON(http.StatusOK, gin.H{"message": "Book saved, cover fetching in progress", "book": book})
}

// deleteBookHandler deletes a book by its ID or title.

func deleteBookHandler(c *gin.Context) {
	// Ownership already verified by requireBookOwnership(); reuse the loaded book.
	book := c.MustGet("book").(Book)

	// Snapshot related rows so we can clean up their on-disk files after the
	// rows are deleted.
	var chunks []BookChunk
	db.Where("book_id = ?", book.ID).Find(&chunks)
	var groups []ProcessedChunkGroup
	db.Where("book_id = ?", book.ID).Find(&groups)

	// Q11: delete all related rows in one transaction so a book never leaves
	// orphaned chunks/progress/jobs behind.
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("book_id = ?", book.ID).Delete(&PlaybackProgress{}).Error; err != nil {
			return err
		}
		if err := tx.Where("book_id = ?", book.ID).Delete(&TTSQueueJob{}).Error; err != nil {
			return err
		}
		if err := tx.Unscoped().Where("book_id = ?", book.ID).Delete(&ProcessedChunkGroup{}).Error; err != nil {
			return err
		}
		if err := tx.Where("book_id = ?", book.ID).Delete(&BookChunk{}).Error; err != nil {
			return err
		}
		return tx.Delete(&Book{}, book.ID).Error
	})
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete book", "details": err.Error()})
		return
	}

	// Best-effort media cleanup (R2 objects or legacy local files).
	for _, ch := range chunks {
		deleteStored(ch.AudioPath)
		deleteStored(ch.FinalAudioPath)
	}
	for _, g := range groups {
		deleteStored(g.AudioPath)
	}
	deleteStored(book.FilePath)
	deleteStored(book.AudioPath)
	deleteStored(book.CoverPath)
	_ = os.RemoveAll(uploadDirForBook(book.UserID, book.ID))

	c.JSON(http.StatusOK, gin.H{"message": "Book deleted successfully"})
}

// adding a new handler for listing book pages
func listBookPagesHandler(c *gin.Context) {
	bookID := c.Param("book_id")
	if bookID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Book ID is required"})
		return
	}

	// Optional pagination
	limit := 20 // default limit
	offset := 0

	if l := c.Query("limit"); l != "" {
		if parsedLimit, err := strconv.Atoi(l); err == nil && parsedLimit > 0 {
			limit = parsedLimit
		}
	}
	if o := c.Query("offset"); o != "" {
		if parsedOffset, err := strconv.Atoi(o); err == nil && parsedOffset >= 0 {
			offset = parsedOffset
		}
	}

	// Fetch the book itself for metadata
	var book Book
	if err := db.First(&book, bookID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Book not found"})
		return
	}

	// Fetch chunks for this book with pagination
	var chunks []BookChunk
	if err := db.Where("book_id = ?", bookID).
		Order("index ASC").
		Limit(limit).
		Offset(offset).
		Find(&chunks).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not retrieve book chunks", "details": err.Error()})
		return
	}

	if len(chunks) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"message": "No pages found for this range"})
		return
	}

	// Check processed status and prepare pages
	pages := make([]map[string]interface{}, 0, len(chunks))
	fullyProcessed := true

	for _, chunk := range chunks {
		if chunk.TTSStatus != "completed" {
			fullyProcessed = false
		}
		pages = append(pages, map[string]interface{}{
			"page":    chunk.Index + 1,
			"content": chunk.Content,
			"status":  chunk.TTSStatus,
			// "audio_url": chunk.AudioPath,
			// Q8: the /pages/:page/audio route is 1-based (it subtracts 1), so
			// emit the 1-based page number, not the 0-based chunk index.
			"audio_url": fmt.Sprintf("%s/user/books/%d/pages/%d/audio",
				getEnv("STREAM_HOST", "https://narrafied.com"), chunk.BookID, chunk.Index+1),
		})
	}

	// Total page count (optional, could cache later for large scale)
	var totalChunks int64
	db.Model(&BookChunk{}).Where("book_id = ?", bookID).Count(&totalChunks)

	// Send JSON response
	c.JSON(http.StatusOK, gin.H{
		"book_id":         book.ID,
		"title":           book.Title,
		"status":          book.Status,
		"total_pages":     totalChunks,
		"limit":           limit,
		"offset":          offset,
		"fully_processed": fullyProcessed,
		"pages":           pages,
	})
}

// listBooksHandler retrieves all books for the authenticated user, optionally filtering by category and genre.
// It returns a list of books with their details, including a public stream URL for each book.
// It expects the user to be authenticated via JWT token.
// The token should contain user_id in its claims.
// If the user_id is not found in the token, it returns an error.
// If the category or genre is provided, it filters the books accordingly.
// If the category is invalid, it returns an error.
// It also adds a public stream URL to each book in the response.
// If the database query fails, it returns an error with details.
// The stream URL is constructed using the STREAM_HOST environment variable, defaulting to "https://narrafied.com"
// It returns a JSON response with the list of books, each containing its ID, title, author, category, genre, file path, audio path, status, stream URL, cover URL, and cover path.
// It uses the Gin framework for handling HTTP requests and responses.
func listBooksHandler(c *gin.Context) {
	claims, exists := c.Get("claims")
	if !exists {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Authentication claims missing"})
		return
	}
	userClaims, ok := claims.(jwt.MapClaims)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Invalid token claims"})
		return
	}
	userIDFloat, ok := userClaims["user_id"].(float64)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "User ID not found in token"})
		return
	}
	userID := uint(userIDFloat)

	category := c.Query("category")
	genre := c.Query("genre")

	var books []Book
	query := db.Where("user_id = ?", userID)
	if category != "" {
		query = query.Where("category = ?", category)
	}
	if genre != "" {
		query = query.Where("genre = ?", genre)
	}
	if err := query.Find(&books).Error; err != nil {
		log.Printf("Error retrieving books for user %d: %v", userID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch books", "details": err.Error()})
		return
	}

	//🛡 Add public stream URL to each book
	streamHost := getEnv("STREAM_HOST", "https://narrafied.com")
	var response []BookResponse
	for _, book := range books {
		streamURL := streamHost + "/user/books/stream/proxy/" + fmt.Sprintf("%d", book.ID)
		response = append(response, BookResponse{
			ID:        book.ID,
			Title:     book.Title,
			Author:    book.Author,
			Category:  book.Category,
			Genre:     book.Genre,
			FilePath:  book.FilePath,
			AudioPath: book.AudioPath,
			Status:    book.Status,
			StreamURL: streamURL,
			CoverURL:  book.CoverURL,
			CoverPath: book.CoverPath,
		})
	}
	c.JSON(http.StatusOK, gin.H{"books": response})
}

func isValidCategory(category string) bool {
	for _, allowed := range allowedCategories {
		if strings.EqualFold(category, allowed) {
			return true
		}
	}
	return false
}

func authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		var tokenString string

		// Try getting token from Authorization header
		authHeader := c.GetHeader("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			tokenString = strings.TrimPrefix(authHeader, "Bearer ")
		}

		// Fallback to query param if header is missing (iOS/AVPlayer)
		if tokenString == "" {
			tokenString = c.Query("token")
		}

		if tokenString == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Missing token"})
			return
		}

		// Parse and validate token. Pin the signing method to HMAC so a token
		// presented with a different algorithm (e.g. alg=none, or RS256 using
		// our secret as a public key) is rejected — matches auth-service.
		token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
			}
			return jwtSecretKey, nil
		})
		if err != nil || !token.Valid {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Invalid token"})
			return
		}

		// Attach claims to context
		if claims, ok := token.Claims.(jwt.MapClaims); ok {
			c.Set("claims", claims)
			// Also set user_id for convenience
			if userIDFloat, ok := claims["user_id"].(float64); ok {
				c.Set("user_id", uint(userIDFloat))
			}
			c.Next()
			return
		}

		c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Invalid token claims"})
	}
}

// adminMiddleware checks if the authenticated user has admin privileges
func adminMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Get claims from context (set by authMiddleware)
		claims, exists := c.Get("claims")
		if !exists {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			return
		}

		// Extract is_admin from JWT token claims
		claimsMap, ok := claims.(jwt.MapClaims)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Invalid token claims"})
			return
		}

		// Check if is_admin claim exists and is true
		isAdmin, exists := claimsMap["is_admin"]
		if !exists {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "Admin access required"})
			return
		}

		// Validate that is_admin is a boolean and is true
		adminBool, ok := isAdmin.(bool)
		if !ok || !adminBool {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "Admin access required"})
			return
		}

		c.Next()
	}
}

// adding helper function to get user account type

func getUserAccountType(token string) (string, error) {
	authServiceURL := getEnv("AUTH_SERVICE_URL", "http://auth-service:8082")

	req, err := http.NewRequest("GET", authServiceURL+"/user/account-type", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("failed to fetch account type")
	}

	var result struct {
		AccountType string `json:"account_type"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	return result.AccountType, nil
}

func BatchTranscribeBookHandler(c *gin.Context) {
	// Ownership already verified by requireBookOwnership(); reuse the book.
	book := c.MustGet("book").(Book)
	userID := getUserIDFromContext(c)

	authHeader := c.GetHeader("Authorization")
	token, err := extractToken(authHeader)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Missing or invalid token"})
		return
	}

	// Prefer the account_type carried in the JWT (no network hop). Fall back to
	// the auth-service HTTP lookup only for older tokens that lack the claim.
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

	// Quota pre-check: deny up front if the user is already at their monthly
	// transcription-page budget (per-page consumption happens in the worker).
	if d := checkAndConsume(userID, accountType, "transcribe_pages", 0, book.ID); !d.Allowed {
		quota429(c, d)
		return
	}

	var chunks []BookChunk
	if err := db.Where("book_id = ? AND tts_status != ?", book.ID, "completed").Order("index ASC").Find(&chunks).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not fetch chunks"})
		return
	}

	if len(chunks) == 0 {
		c.JSON(http.StatusOK, gin.H{"message": "Book already fully processed"})
		return
	}

	// B6: atomic job lock — only one transcription may run per book. Use a
	// dedicated 'transcribing' sentinel (NOT 'processing', which upload already
	// sets to mean "uploaded/ready"); claim only if not already transcribing.
	claim := db.Model(&Book{}).
		Where("id = ? AND status <> ?", book.ID, "transcribing").
		Update("status", "transcribing")
	if claim.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not lock book for processing"})
		return
	}
	if claim.RowsAffected == 0 {
		c.JSON(http.StatusConflict, gin.H{"error": "Transcription already in progress for this book"})
		return
	}

	// Enqueue the first 20-page batch (durable, on the worker fleet). The
	// worker auto-enqueues subsequent batches as each completes, fires an MQTT
	// "pages ready" event, and releases the book lock when done.
	start := chunks[0].Index
	if err := enqueueTranscribeBatch(book.ID, start, start+batchSizePages-1, userID, accountType); err != nil {
		db.Model(&Book{}).Where("id = ?", book.ID).Update("status", "pending")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not enqueue transcription", "details": err.Error()})
		return
	}
	c.JSON(http.StatusAccepted, gin.H{"message": "Transcription queued"})
}

// accountTypeFromClaims returns the account_type embedded in the JWT, or "" if
// the token predates that claim (issued before Phase 5 deploy).
func accountTypeFromClaims(c *gin.Context) string {
	claims, exists := c.Get("claims")
	if !exists {
		return ""
	}
	mc, ok := claims.(jwt.MapClaims)
	if !ok {
		return ""
	}
	at, _ := mc["account_type"].(string)
	return at
}

func getUserIDFromContext(c *gin.Context) uint {
	claims, exists := c.Get("claims")
	if !exists {
		return 0
	}
	userClaims, ok := claims.(jwt.MapClaims)
	if !ok {
		return 0
	}
	return uint(userClaims["user_id"].(float64))
}

func extractToken(authHeader string) (string, error) {
	if authHeader == "" {
		return "", errors.New("authorization header missing")
	}
	parts := strings.Split(authHeader, " ")
	if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
		return "", errors.New("authorization header format must be Bearer {token}")
	}
	return parts[1], nil
}

// getSingleBookHandler retrieves a single book by its ID.
// getSingleBookHandler retrieves a single book by its ID.
func getSingleBookHandler(c *gin.Context) {
	bookID := c.Param("book_id")

	if bookID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Book ID is required"})
		return
	}

	var book Book
	if err := db.First(&book, bookID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Book not found"})
		return
	}

	// add full book data response
	bookResponse := BookResponse{
		ID:          book.ID,
		Title:       book.Title,
		Author:      book.Author,
		Category:    book.Category,
		Content:     book.Content,
		ContentHash: book.ContentHash,
		Genre:       book.Genre,
		FilePath:    book.FilePath,
		AudioPath:   book.AudioPath,
		Status:      book.Status,
	}

	c.JSON(http.StatusOK, gin.H{
		"book": bookResponse,
	})

}

// deleteUserFilesContentHandler deletes all files for a specific user
// DELETE /admin/users/:user_id/files
func deleteUserFilesContentHandler(c *gin.Context) {
	userIDStr := c.Param("user_id")
	userID, err := strconv.ParseUint(userIDStr, 10, 32)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user_id"})
		return
	}

	// Find all books for this user
	var books []Book
	if err := db.Where("user_id = ?", userID).Find(&books).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch user books"})
		return
	}

	// Track deletion stats
	var filesDeleted, audioDeleted, coversDeleted, uploadsDeleted int
	var totalBooksDeleted, totalChunksDeleted int64

	// Delete files for each book
	for _, book := range books {
		// Delete book file
		if book.FilePath != "" {
			if err := os.Remove(book.FilePath); err == nil {
				uploadsDeleted++
				log.Printf("🗑️ Deleted upload: %s", book.FilePath)
			}
		}

		// Delete audio file
		if book.AudioPath != "" {
			if err := os.Remove(book.AudioPath); err == nil {
				audioDeleted++
				log.Printf("🗑️ Deleted audio: %s", book.AudioPath)
			}
		}

		// Delete cover file
		if book.CoverPath != "" {
			if err := os.Remove(book.CoverPath); err == nil {
				coversDeleted++
				log.Printf("🗑️ Deleted cover: %s", book.CoverPath)
			}
		}

		// Find and delete chunk audio files
		var chunks []BookChunk
		db.Where("book_id = ?", book.ID).Find(&chunks)
		for _, chunk := range chunks {
			if chunk.AudioPath != "" {
				if err := os.Remove(chunk.AudioPath); err == nil {
					filesDeleted++
				}
			}
			if chunk.FinalAudioPath != "" {
				if err := os.Remove(chunk.FinalAudioPath); err == nil {
					filesDeleted++
				}
			}
		}

		// Delete chunk audio directories
		audioDir := fmt.Sprintf("./audio/book_%d_segments", book.ID)
		if err := os.RemoveAll(audioDir); err == nil {
			log.Printf("🗑️ Deleted directory: %s", audioDir)
		}
	}

	// Delete database records
	tx := db.Begin()
	if tx.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to start transaction"})
		return
	}

	// Delete playback progress
	tx.Where("user_id = ?", userID).Delete(&PlaybackProgress{})

	// Delete processed chunk groups
	tx.Where("book_id IN (SELECT id FROM books WHERE user_id = ?)", userID).Delete(&ProcessedChunkGroup{})

	// Delete TTS queue jobs
	tx.Where("user_id = ?", userID).Delete(&TTSQueueJob{})

	// Delete book chunks
	result := tx.Where("book_id IN (SELECT id FROM books WHERE user_id = ?)", userID).Delete(&BookChunk{})
	totalChunksDeleted = result.RowsAffected

	// Delete books
	result = tx.Where("user_id = ?", userID).Delete(&Book{})
	totalBooksDeleted = result.RowsAffected

	// Commit transaction
	if err := tx.Commit().Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit deletion"})
		return
	}

	log.Printf("🗑️ Deleted all files and data for user ID %d by admin", userID)
	c.JSON(http.StatusOK, gin.H{
		"message":           "User files deleted successfully",
		"user_id":           userID,
		"books_deleted":     totalBooksDeleted,
		"chunks_deleted":    totalChunksDeleted,
		"uploads_deleted":   uploadsDeleted,
		"audio_deleted":     audioDeleted,
		"covers_deleted":    coversDeleted,
		"chunk_files_deleted": filesDeleted,
	})
}

func getEnv(key, fallback string) string {
	if value, exists := os.LookupEnv(key); exists {
		return value
	}
	return fallback
}

// envInt reads an integer env var or returns def.
func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// deleteFileContentHandler deletes a single file from the server
// DELETE /admin/files/delete
// Body: { "file_path": "audio/book_21_chunk_5.mp3" }
func deleteFileContentHandler(c *gin.Context) {
	type DeleteFileRequest struct {
		FilePath string `json:"file_path" binding:"required"`
	}

	var req DeleteFileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "file_path is required"})
		return
	}

	// Security: Validate that the path is within allowed directories
	allowedPrefixes := []string{"audio/", "covers/", "uploads/"}
	isAllowed := false
	for _, prefix := range allowedPrefixes {
		if strings.HasPrefix(req.FilePath, prefix) {
			isAllowed = true
			break
		}
	}

	if !isAllowed {
		c.JSON(http.StatusForbidden, gin.H{
			"error":   "Invalid file path",
			"message": "File must be in audio/, covers/, or uploads/ directory",
		})
		return
	}

	// Security: Prevent path traversal attacks
	if strings.Contains(req.FilePath, "..") {
		c.JSON(http.StatusForbidden, gin.H{"error": "Invalid file path: path traversal not allowed"})
		return
	}

	// Map the relative path to actual container paths
	// In Docker: audio/ → ./audio/, covers/ → ./uploads/covers/, uploads/ → ./uploads/
	var fullPath string
	switch {
	case strings.HasPrefix(req.FilePath, "audio/"):
		fullPath = "./" + req.FilePath // ./audio/filename
	case strings.HasPrefix(req.FilePath, "covers/"):
		// covers/filename → ./uploads/covers/filename
		filename := strings.TrimPrefix(req.FilePath, "covers/")
		fullPath = "./uploads/covers/" + filename
	case strings.HasPrefix(req.FilePath, "uploads/"):
		fullPath = "./" + req.FilePath // ./uploads/filename
	default:
		c.JSON(http.StatusForbidden, gin.H{"error": "Invalid file path"})
		return
	}

	// Check if file exists
	info, err := os.Stat(fullPath)
	if os.IsNotExist(err) {
		c.JSON(http.StatusNotFound, gin.H{
			"error":     "File not found",
			"file_path": req.FilePath,
		})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to check file",
			"details": err.Error(),
		})
		return
	}

	// Don't allow deleting directories
	if info.IsDir() {
		c.JSON(http.StatusForbidden, gin.H{
			"error":   "Cannot delete directories",
			"message": "Only individual files can be deleted",
		})
		return
	}

	// Get file size before deletion for reporting
	fileSize := info.Size()

	// Delete the file
	if err := os.Remove(fullPath); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"error":   "Failed to delete file",
			"details": err.Error(),
		})
		return
	}

	log.Printf("🗑️ Admin deleted file: %s (%.2f KB)", req.FilePath, float64(fileSize)/1024)

	c.JSON(http.StatusOK, gin.H{
		"message":     "File deleted successfully",
		"file_path":   req.FilePath,
		"size_deleted": fileSize,
	})
}

// FileTreeNode represents a file or directory in the tree structure
type FileTreeNode struct {
	Name     string          `json:"name"`
	Path     string          `json:"path"`
	IsDir    bool            `json:"is_dir"`
	Size     int64           `json:"size,omitempty"`
	Children []*FileTreeNode `json:"children,omitempty"`
}

// getFileTreeContentHandler returns the directory tree structure for audio, covers, and uploads
// GET /admin/files/tree
func getFileTreeContentHandler(c *gin.Context) {
	// Directory mappings in Docker container
	// Host /opt/stream-audio-data/audio → Container ./audio
	// Host /opt/stream-audio-data/covers → Container ./uploads/covers
	// Host /opt/stream-audio-data/uploads → Container ./uploads
	dirMappings := map[string]string{
		"audio":   "./audio",
		"covers":  "./uploads/covers",
		"uploads": "./uploads",
	}

	trees := make(map[string]*FileTreeNode)
	var totalSize int64
	var totalFiles int

	for displayName, containerPath := range dirMappings {
		// Check if directory exists
		if _, err := os.Stat(containerPath); os.IsNotExist(err) {
			// Create empty node for missing directories
			trees[displayName] = &FileTreeNode{
				Name:     displayName,
				Path:     displayName,
				IsDir:    true,
				Children: []*FileTreeNode{},
			}
			continue
		}

		// Build the tree for this directory
		tree, err := buildFileTreeContent(containerPath, "")
		if err != nil {
			log.Printf("Warning: Failed to build tree for %s: %v", displayName, err)
			trees[displayName] = &FileTreeNode{
				Name:     displayName,
				Path:     displayName,
				IsDir:    true,
				Children: []*FileTreeNode{},
			}
			continue
		}

		// Update the name and path to be the display name
		tree.Name = displayName
		tree.Path = displayName
		trees[displayName] = tree

		// Calculate stats for this directory
		dirSize, dirFiles := calculateTreeStatsContent(tree)
		totalSize += dirSize
		totalFiles += dirFiles
	}

	c.JSON(http.StatusOK, gin.H{
		"trees":       trees,
		"directories": []string{"audio", "covers", "uploads"},
		"stats": gin.H{
			"totalSize":  totalSize,
			"totalFiles": totalFiles,
		},
	})
}

// buildFileTreeContent recursively builds a file tree structure
func buildFileTreeContent(basePath string, relativePath string) (*FileTreeNode, error) {
	fullPath := basePath
	if relativePath != "" {
		fullPath = basePath + "/" + relativePath
	}

	info, err := os.Stat(fullPath)
	if err != nil {
		return nil, err
	}

	node := &FileTreeNode{
		Name:  info.Name(),
		Path:  relativePath,
		IsDir: info.IsDir(),
	}

	if !info.IsDir() {
		node.Size = info.Size()
		return node, nil
	}

	// Read directory contents
	entries, err := os.ReadDir(fullPath)
	if err != nil {
		return nil, err
	}

	// Build children
	node.Children = make([]*FileTreeNode, 0, len(entries))
	for _, entry := range entries {
		var childPath string
		if relativePath == "" {
			childPath = entry.Name()
		} else {
			childPath = relativePath + "/" + entry.Name()
		}

		childNode, err := buildFileTreeContent(basePath, childPath)
		if err != nil {
			log.Printf("Warning: Failed to process %s: %v", childPath, err)
			continue
		}
		node.Children = append(node.Children, childNode)
	}

	return node, nil
}

// calculateTreeStatsContent calculates total size and file count for a tree
func calculateTreeStatsContent(node *FileTreeNode) (int64, int) {
	if !node.IsDir {
		return node.Size, 1
	}

	var totalSize int64
	var totalFiles int

	for _, child := range node.Children {
		size, files := calculateTreeStatsContent(child)
		totalSize += size
		totalFiles += files
	}

	return totalSize, totalFiles
}
