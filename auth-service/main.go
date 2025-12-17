package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	"github.com/stripe/stripe-go/v78"
	"github.com/stripe/stripe-go/v78/checkout/session"
	"github.com/stripe/stripe-go/v78/customer"
	"github.com/stripe/stripe-go/v78/subscription"
	"github.com/stripe/stripe-go/v78/webhook"
)

// Global variables
var jwtSecretKey = []byte(getEnv("JWT_SECRET", "your_secret_key"))
var db *gorm.DB

// User defines the schema for the "users" table.
type User struct {
	ID               uint      `gorm:"primaryKey"`
	Username         string    `gorm:"unique;not null"`
	Email            string    `gorm:"unique;not null"`
	Password         string    `gorm:"not null"` // stored as a bcrypt hash
	AccountType      string    `gorm:"not null"` // e.g., "free" or "paid"
	IsPublic         bool      `gorm:"default:true"`
	State            string    // user's state or location
	StripeCustomerID string    // for paid accounts
	BooksRead        int       `gorm:"default:0"`
	IsAdmin          bool      `gorm:"default:false"`               // Admin access flag
	LastActiveAt     time.Time `gorm:"default:CURRENT_TIMESTAMP"`   // Last activity timestamp
	// Device tracking fields for account restoration
	PhoneNumber      string    `gorm:"index"`                       // User's phone number
	DeviceModel      string    // e.g., "iPhone 14 Pro", "Samsung Galaxy S21"
	DeviceID         string    `gorm:"index"`                       // iOS IDFA or Android GAID
	PushToken        string    // FCM/APNS push notification token
	IPAddress        string    // Last known IP address
	OSVersion        string    // e.g., "iOS 17.2", "Android 14"
	AppVersion       string    // App version for tracking
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// UserHistory stores deleted/deactivated account data for restoration
type UserHistory struct {
	ID               uint      `gorm:"primaryKey"`
	OriginalUserID   uint      `gorm:"index;not null"`              // Original user ID
	Username         string    `json:"username"`
	Email            string    `gorm:"index;not null"`
	Password         string    // Hashed password
	AccountType      string
	IsPublic         bool
	State            string
	StripeCustomerID string
	BooksRead        int
	PhoneNumber      string    `gorm:"index"`
	DeviceModel      string
	DeviceID         string    `gorm:"index"`
	PushToken        string
	IPAddress        string    `gorm:"index"`
	OSVersion        string
	AppVersion       string
	Status           string    `gorm:"not null;default:'deactivated'"` // "deactivated" or "deleted"
	DeletionReason   string    // Optional reason from user
	DeletedAt        time.Time `gorm:"not null"`                      // When account was deleted
	OriginalCreatedAt time.Time                                       // Original account creation date
	RestoredAt       *time.Time                                       // If account was restored
	RestoredToUserID *uint                                            // New user ID if restored
}

// UserBookHistory stores book progress for deleted/deactivated accounts
type UserBookHistory struct {
	ID                uint      `gorm:"primaryKey"`
	UserHistoryID     uint      `gorm:"index;not null"`              // FK to UserHistory
	BookTitle         string    `gorm:"not null"`
	BookAuthor        string
	BookID            uint      // Original book ID
	Category          string
	Genre             string
	CurrentPosition   float64   // Last playback position in seconds
	Duration          float64   // Total duration
	ChunkIndex        int       // Last page/chunk index
	CompletionPercent float64   // Percentage completed
	LastPlayedAt      time.Time // When user last played this book
	AudioPath         string    // Path to audio file if still exists
	CoverURL          string    // Cover image URL
	CreatedAt         time.Time
}

// Request structures for binding and validation
type SignupRequest struct {
	Username    string `json:"username" binding:"required"`
	Email       string `json:"email" binding:"required,email"`
	Password    string `json:"password" binding:"required,min=6"`
	State       string `json:"state" binding:"required"`
	// Device information for account restoration
	PhoneNumber string `json:"phone_number"`
	DeviceModel string `json:"device_model"`
	DeviceID    string `json:"device_id"`    // iOS IDFA or Android GAID
	PushToken   string `json:"push_token"`   // FCM/APNS token
	OSVersion   string `json:"os_version"`   // iOS/Android version
	AppVersion  string `json:"app_version"`  // App version
}

type LoginRequest struct {
	Username    string `json:"username" binding:"required"`
	Password    string `json:"password" binding:"required"`
	// Device information for tracking
	DeviceModel string `json:"device_model"`
	DeviceID    string `json:"device_id"`
	PushToken   string `json:"push_token"`
	OSVersion   string `json:"os_version"`
	AppVersion  string `json:"app_version"`
}

type DeactivateAccountRequest struct {
	Reason   string `json:"reason"`    // Optional reason for deactivation
	Password string `json:"password" binding:"required"` // Confirm with password
}

type DeleteAccountRequest struct {
	Reason   string `json:"reason"`    // Optional reason for deletion
	Password string `json:"password" binding:"required"` // Confirm with password
}

type RestoreAccountRequest struct {
	Email       string `json:"email" binding:"required,email"`
	PhoneNumber string `json:"phone_number"`
	DeviceID    string `json:"device_id"`
}

func main() {
	// Initialize the database connection and run migrations
	setupDatabase()

	// Set Gin mode based on environment variable; default to release
	ginMode := os.Getenv("GIN_MODE")
	if ginMode == "" {
		ginMode = gin.ReleaseMode
	}
	gin.SetMode(ginMode)

	router := gin.Default()

	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// Endpoints for signup and login
	router.POST("/signup", signupHandler)
	router.POST("/login", loginHandler)
	// Account restoration (public endpoint)
	router.POST("/restore-account", restoreAccountHandler)

	// Protected routes group
	authorized := router.Group("/user")
	authorized.Use(authMiddleware())
	{
		authorized.GET("/profile", profileHandler)
		// adding stripe checkout session
		authorized.POST("/stripe/create-checkout-session", createCheckoutSessionHandler)
		authorized.GET("/account-type", getAccountTypeHandler)
		// Subscription management
		authorized.GET("/subscription/status", getSubscriptionStatusHandler)
		authorized.POST("/subscription/cancel", cancelSubscriptionHandler)
		// Activity tracking
		authorized.POST("/activity/ping", updateUserActivityHandler)
		// Account deactivation and deletion
		authorized.POST("/deactivate", deactivateAccountHandler)
		authorized.POST("/delete", deleteAccountHandler)
	}

	// Admin routes group
	admin := router.Group("/admin")
	admin.Use(authMiddleware(), adminMiddleware())
	{
		admin.GET("/stats", getAdminStatsHandler)
		admin.GET("/users", listUsersHandler)
		admin.GET("/users/active", getActiveUsersHandler)
		admin.POST("/users/:user_id/admin", makeUserAdminHandler)
	}

	router.POST("/stripe/webhook", stripeWebhookHandler)

	// Use port from env or default to 8082
	port := os.Getenv("PORT")
	if port == "" {
		port = "8082"
	}
	log.Printf("Auth service is listening on port %s", port)

	for _, r := range router.Routes() {
		log.Printf("→ %s %s", r.Method, r.Path)
	}

	router.Run(":" + port)
}

// getEnv is assumed to be your helper that reads an env var or returns the default.
func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func setupDatabase() {
	// Read from env, or default to sensible values
	dbHost := getEnv("DB_HOST", "localhost")
	dbUser := getEnv("DB_USER", "postgres")
	dbPassword := getEnv("DB_PASSWORD", "")
	dbName := getEnv("DB_NAME", "postgres")
	dbPort := getEnv("DB_PORT", "5432")
	sslMode := getEnv("DB_SSLMODE", "") // “disable” for local, override to “require” in prod

	// Build the DSN string
	// I got a security flaw here this needs to be mask
	// mask := string.ReplaceAll(dsn, dbPassword, "********")
	dsn := fmt.Sprintf(
		"host=%s user=%s password=%s dbname=%s port=%s sslmode=%s TimeZone=UTC",
		dbHost, dbUser, dbPassword, dbName, dbPort, sslMode,
	)

	log.Printf("🔍 DSN=%q\n", dsn)

	var err error
	// Open the connection
	db, err = gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatalf("Could not connect to the database: %v", err)
	}

	// Run migrations
	if err := db.AutoMigrate(&User{}, &UserHistory{}, &UserBookHistory{}); err != nil {
		log.Fatalf("AutoMigrate failed: %v", err)
	}

	log.Println("✅ Database connected and migrated (users, user_histories, user_book_histories)")
}

// signupHandler validates input and creates a new user in the database
func signupHandler(c *gin.Context) {
	var req SignupRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid signup data", "details": err.Error()})
		return
	}

	// Extract client IP address
	clientIP := c.ClientIP()

	// Check if a user with the same username or email already exists
	var existing User
	if err := db.Where("username = ? OR email = ?", req.Username, req.Email).First(&existing).Error; err == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "User with this username or email already exists"})
		return
	}

	// Check if there's a deleted/deactivated account that can be restored
	var history UserHistory
	canRestore := false
	if req.Email != "" {
		// Try to find matching history by email, phone, or device ID
		query := db.Where("email = ?", req.Email).Where("restored_at IS NULL")

		if req.PhoneNumber != "" {
			query = query.Or("phone_number = ?", req.PhoneNumber)
		}
		if req.DeviceID != "" {
			query = query.Or("device_id = ?", req.DeviceID)
		}

		if err := query.Order("deleted_at DESC").First(&history).Error; err == nil {
			canRestore = true
			log.Printf("🔍 Found deleted account for email %s (deleted %v ago)", req.Email, time.Since(history.DeletedAt))
		}
	}

	// If account can be restored, suggest restoration
	if canRestore {
		c.JSON(http.StatusConflict, gin.H{
			"error": "Account previously existed",
			"can_restore": true,
			"message": "We found a previous account associated with this information. Would you like to restore it?",
			"history_id": history.ID,
			"deleted_at": history.DeletedAt,
			"original_username": history.Username,
		})
		return
	}

	// Hash the password using bcrypt
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to hash password"})
		return
	}

	// Create a new user with default free account type and public profile
	user := User{
		Username:    req.Username,
		Email:       req.Email,
		Password:    string(hashedPassword),
		AccountType: "free",
		IsPublic:    true,
		State:       req.State,
		PhoneNumber: req.PhoneNumber,
		DeviceModel: req.DeviceModel,
		DeviceID:    req.DeviceID,
		PushToken:   req.PushToken,
		IPAddress:   clientIP,
		OSVersion:   req.OSVersion,
		AppVersion:  req.AppVersion,
	}

	// Save the user to the database
	if err := db.Create(&user).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to register user", "details": err.Error()})
		return
	}

	log.Printf("✅ New user registered: %s (ID: %d) from %s", user.Username, user.ID, clientIP)
	c.JSON(http.StatusOK, gin.H{"message": "User registered", "user_id": user.ID})
}

// loginHandler validates credentials and returns a JWT token
func loginHandler(c *gin.Context) {
	var req LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid login data", "details": err.Error()})
		return
	}

	// Find the user by username
	var user User
	if err := db.Where("username = ?", req.Username).First(&user).Error; err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid username or password"})
		return
	}

	// Compare the provided password with the stored hashed password
	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.Password)); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid username or password"})
		return
	}

	// Update device information and last active timestamp
	clientIP := c.ClientIP()
	updates := map[string]interface{}{
		"last_active_at": time.Now(),
		"ip_address":     clientIP,
	}
	if req.DeviceModel != "" {
		updates["device_model"] = req.DeviceModel
	}
	if req.DeviceID != "" {
		updates["device_id"] = req.DeviceID
	}
	if req.PushToken != "" {
		updates["push_token"] = req.PushToken
	}
	if req.OSVersion != "" {
		updates["os_version"] = req.OSVersion
	}
	if req.AppVersion != "" {
		updates["app_version"] = req.AppVersion
	}

	db.Model(&user).Updates(updates)
	log.Printf("✅ User %s logged in from %s (%s)", user.Username, clientIP, req.DeviceModel)

	// Create JWT token with user claims
	claims := jwt.MapClaims{
		"username": user.Username,
		"user_id":  user.ID,
		"is_admin": user.IsAdmin,
		"exp":      time.Now().Add(time.Hour * 72).Unix(),
		"iat":      time.Now().Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString(jwtSecretKey)
	if err != nil {
		log.Printf("Error signing token: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate token"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"token": tokenString})
}

// Stripe handler function
func createCheckoutSessionHandler(c *gin.Context) {
	// 1. Get user ID from token
	claims, exists := c.Get("claims")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	userClaims := claims.(jwt.MapClaims)
	userID := uint(userClaims["user_id"].(float64))

	// 2. Lookup user from DB
	var user User
	if err := db.First(&user, userID).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "User not found"})
		return
	}

	// 3. Set Stripe API key
	stripe.Key = getEnv("STRIPE_SECRET_KEY", "")

	// 4. Create Stripe customer if not exists
	var customerID string
	if user.StripeCustomerID != "" {
		customerID = user.StripeCustomerID
	} else {
		params := &stripe.CustomerParams{
			Email: stripe.String(user.Email),
		}
		cus, err := customer.New(params)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create Stripe customer"})
			return
		}
		customerID = cus.ID
		user.StripeCustomerID = customerID
		db.Save(&user) // Save to DB
	}

	// 5. Create Stripe Checkout session
	params := &stripe.CheckoutSessionParams{
		Customer:           stripe.String(customerID),
		PaymentMethodTypes: stripe.StringSlice([]string{"card"}),
		Mode:               stripe.String(string(stripe.CheckoutSessionModeSubscription)),
		LineItems: []*stripe.CheckoutSessionLineItemParams{
			{
				Price:    stripe.String("price_1Rq20XChBqCooXQK4rkn86Vr"), // 🔁 Replace with your Stripe Price ID
				Quantity: stripe.Int64(1),
			},
			{
				Price:    stripe.String("price_1Rq1zUChBqCooXQK1QsUsfFr"),
				Quantity: stripe.Int64(1),
			},
		},
		SuccessURL: stripe.String("http://68.183.22.205/thank-you-page"),
		CancelURL:  stripe.String("http://68.183.22.205/cancel"),
	}
	s, err := session.New(params)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create Stripe Checkout session", "details": err.Error()})
		return
	}

	// 6. Return checkout URL
	c.JSON(http.StatusOK, gin.H{"url": s.URL})
}

//adding stripe webhookhandler

func stripeWebhookHandler(c *gin.Context) {
	const MaxBodyBytes = int64(65536)
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, MaxBodyBytes)

	payload, err := ioutil.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Error reading request body"})
		return
	}

	endpointSecret := getEnv("STRIPE_WEBHOOK_SECRET", "")
	sigHeader := c.GetHeader("Stripe-Signature")
	event, err := webhook.ConstructEvent(payload, sigHeader, endpointSecret)

	if err != nil {
		log.Printf("⚠️ Webhook signature verification failed: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Signature verification failded"})
		return
	}

	switch event.Type {

	case "checkout.session.completed":
		var session stripe.CheckoutSession
		if err := json.Unmarshal(event.Data.Raw, &session); err != nil {
			log.Printf("⚠️ Failed to parse session: %v", err)
			c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to parse session"})
			return
		}
		customerID := session.Customer.ID
		updateUserAccountType(customerID, "paid")

	case "customer.subscription.deleted":
		var sub stripe.Subscription
		if err := json.Unmarshal(event.Data.Raw, &sub); err != nil {
			log.Printf("⚠️ Failed to parse subscription deletion: %v", err)
			c.JSON(http.StatusBadRequest, gin.H{"error": "Failed to parse subscription"})
			return
		}
		customerID := sub.Customer.ID
		updateUserAccountType(customerID, "free")

	}

	c.JSON(http.StatusOK, gin.H{"status": "received"})
}

// update account Type function

func updateUserAccountType(customerID, newType string) {
	var user User
	if err := db.Where("stripe_customer_id = ?", customerID).First(&user).Error; err != nil {
		log.Printf("❌ No user found for stripe customer ID: %s", customerID)
		return
	}

	user.AccountType = newType
	if err := db.Save(&user).Error; err != nil {
		log.Printf("❌ Failed to update user %d account type to %s: %v", user.ID, newType, err)
		return
	}
	log.Printf("✅ User %s account update to %s", user.Email, newType)
}

func getAccountTypeHandler(c *gin.Context) {
	claims, exists := c.Get("claims")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Missing claims"})
		return
	}
	userClaims := claims.(jwt.MapClaims)
	userID := uint(userClaims["user_id"].(float64))

	var user User
	if err := db.First(&user, userID).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "User not found"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"account_type": user.AccountType,
	})
}

// profileHandler returns user profile info by querying the database using claims from the token
func profileHandler(c *gin.Context) {
	// Retrieve the claims set in the middleware
	claims, exists := c.Get("claims")
	if !exists {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Claims not found"})
		return
	}
	// makersspace
	userClaims, ok := claims.(jwt.MapClaims)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Invalid token claims"})
		return
	}
	// Extract user_id from token claims (note: JSON numbers are float64)
	userIDFloat, ok := userClaims["user_id"].(float64)
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "User ID not found in token"})
		return
	}
	userID := uint(userIDFloat)

	// Query the user from the database
	var user User
	if err := db.First(&user, userID).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "User not found"})
		return
	}

	// Return user profile details (excluding sensitive fields like password)
	c.JSON(http.StatusOK, gin.H{
		"username":     user.Username,
		"email":        user.Email,
		"account_type": user.AccountType,
		"is_public":    user.IsPublic,
		"state":        user.State,
		"books_read":   user.BooksRead,
		"created_at":   user.CreatedAt,
	})
}

// authMiddleware validates the JWT token from the Authorization header.
func authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		tokenString, err := extractToken(c.GetHeader("Authorization"))
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": err.Error()})
			return
		}
		token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
			// Ensure that the token method conforms to what you expect:
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, errors.New("unexpected signing method")
			}
			return jwtSecretKey, nil
		})
		if err != nil || !token.Valid {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			return
		}
		// Save claims in context for later handlers to use
		c.Set("claims", token.Claims)
		c.Next()
	}
}

// extractToken extracts the token string from the header.
// It expects the header to be in the format "Bearer <token>".
func extractToken(authHeader string) (string, error) {
	if authHeader == "" {
		return "", errors.New("Authorization header missing")
	}
	parts := strings.Split(authHeader, " ")
	if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
		return "", errors.New("Authorization header format must be Bearer {token}")
	}
	return parts[1], nil
}

// getSubscriptionStatusHandler retrieves the user's current subscription status from Stripe
// GET /user/subscription/status
func getSubscriptionStatusHandler(c *gin.Context) {
	// 1. Get user ID from token
	claims, exists := c.Get("claims")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	userClaims := claims.(jwt.MapClaims)
	userID := uint(userClaims["user_id"].(float64))

	// 2. Lookup user from DB
	var user User
	if err := db.First(&user, userID).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "User not found"})
		return
	}

	// 3. Check if user has a Stripe customer ID
	if user.StripeCustomerID == "" {
		c.JSON(http.StatusOK, gin.H{
			"account_type":        user.AccountType,
			"has_subscription":    false,
			"subscription_status": "none",
			"message":             "No subscription found",
		})
		return
	}

	// 4. Set Stripe API key
	stripe.Key = getEnv("STRIPE_SECRET_KEY", "")

	// 5. List subscriptions for the customer
	params := &stripe.SubscriptionListParams{
		Customer: stripe.String(user.StripeCustomerID),
	}
	iter := subscription.List(params)

	var activeSub *stripe.Subscription
	for iter.Next() {
		sub := iter.Subscription()
		if sub.Status == stripe.SubscriptionStatusActive || sub.Status == stripe.SubscriptionStatusTrialing {
			activeSub = sub
			break
		}
	}

	if err := iter.Err(); err != nil {
		log.Printf("❌ Error fetching subscriptions: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch subscription status"})
		return
	}

	// 6. Return subscription details
	if activeSub != nil {
		c.JSON(http.StatusOK, gin.H{
			"account_type":           user.AccountType,
			"has_subscription":       true,
			"subscription_id":        activeSub.ID,
			"subscription_status":    activeSub.Status,
			"current_period_start":   time.Unix(activeSub.CurrentPeriodStart, 0).Format(time.RFC3339),
			"current_period_end":     time.Unix(activeSub.CurrentPeriodEnd, 0).Format(time.RFC3339),
			"cancel_at_period_end":   activeSub.CancelAtPeriodEnd,
			"canceled_at":            activeSub.CanceledAt,
			"plan_name":              activeSub.Items.Data[0].Price.Nickname,
			"plan_amount":            activeSub.Items.Data[0].Price.UnitAmount,
			"plan_currency":          activeSub.Items.Data[0].Price.Currency,
			"plan_interval":          activeSub.Items.Data[0].Price.Recurring.Interval,
		})
	} else {
		c.JSON(http.StatusOK, gin.H{
			"account_type":        user.AccountType,
			"has_subscription":    false,
			"subscription_status": "inactive",
			"message":             "No active subscription found",
		})
	}
}

// cancelSubscriptionHandler cancels the user's active subscription
// POST /user/subscription/cancel
func cancelSubscriptionHandler(c *gin.Context) {
	// 1. Get user ID from token
	claims, exists := c.Get("claims")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	userClaims := claims.(jwt.MapClaims)
	userID := uint(userClaims["user_id"].(float64))

	// 2. Lookup user from DB
	var user User
	if err := db.First(&user, userID).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "User not found"})
		return
	}

	// 3. Check if user has a Stripe customer ID
	if user.StripeCustomerID == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No subscription found to cancel"})
		return
	}

	// 4. Set Stripe API key
	stripe.Key = getEnv("STRIPE_SECRET_KEY", "")

	// 5. Find active subscription
	params := &stripe.SubscriptionListParams{
		Customer: stripe.String(user.StripeCustomerID),
	}
	iter := subscription.List(params)

	var activeSub *stripe.Subscription
	for iter.Next() {
		sub := iter.Subscription()
		if sub.Status == stripe.SubscriptionStatusActive || sub.Status == stripe.SubscriptionStatusTrialing {
			activeSub = sub
			break
		}
	}

	if err := iter.Err(); err != nil {
		log.Printf("❌ Error fetching subscriptions: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch subscription"})
		return
	}

	if activeSub == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No active subscription found to cancel"})
		return
	}

	// 6. Cancel the subscription at period end (user keeps access until billing cycle ends)
	cancelParams := &stripe.SubscriptionParams{
		CancelAtPeriodEnd: stripe.Bool(true),
	}
	canceledSub, err := subscription.Update(activeSub.ID, cancelParams)
	if err != nil {
		log.Printf("❌ Error canceling subscription: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to cancel subscription", "details": err.Error()})
		return
	}

	log.Printf("✅ User %s (%d) canceled subscription %s", user.Email, user.ID, canceledSub.ID)

	// 7. Return cancellation details
	c.JSON(http.StatusOK, gin.H{
		"message":                "Subscription canceled successfully",
		"subscription_id":        canceledSub.ID,
		"cancel_at_period_end":   canceledSub.CancelAtPeriodEnd,
		"current_period_end":     time.Unix(canceledSub.CurrentPeriodEnd, 0).Format(time.RFC3339),
		"access_until":           time.Unix(canceledSub.CurrentPeriodEnd, 0).Format(time.RFC3339),
		"info":                   "Your subscription will remain active until the end of your current billing period",
	})
}

// ============================================================================
// ACCOUNT DEACTIVATION AND DELETION HANDLERS
// ============================================================================

// deactivateAccountHandler temporarily deactivates a user account
// POST /user/deactivate
func deactivateAccountHandler(c *gin.Context) {
	// 1. Get user ID from token
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// 2. Parse request
	var req DeactivateAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request", "details": err.Error()})
		return
	}

	// 3. Fetch user from database
	var user User
	if err := db.First(&user, userID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	}

	// 4. Verify password
	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.Password)); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Incorrect password"})
		return
	}

	// 5. Start transaction to save history and delete user
	tx := db.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// 6. Fetch user's books and progress from content service (we'll store metadata)
	var bookHistories []UserBookHistory
	// Query content service database for user's books
	// Note: This would require a cross-service call or shared database
	// For now, we'll just log this - implement based on your architecture
	log.Printf("📚 Archiving books for user %d (deactivation)", user.ID)

	// 7. Create history record
	now := time.Now()
	history := UserHistory{
		OriginalUserID:    user.ID,
		Username:          user.Username,
		Email:             user.Email,
		Password:          user.Password,
		AccountType:       user.AccountType,
		IsPublic:          user.IsPublic,
		State:             user.State,
		StripeCustomerID:  user.StripeCustomerID,
		BooksRead:         user.BooksRead,
		PhoneNumber:       user.PhoneNumber,
		DeviceModel:       user.DeviceModel,
		DeviceID:          user.DeviceID,
		PushToken:         user.PushToken,
		IPAddress:         user.IPAddress,
		OSVersion:         user.OSVersion,
		AppVersion:        user.AppVersion,
		Status:            "deactivated",
		DeletionReason:    req.Reason,
		DeletedAt:         now,
		OriginalCreatedAt: user.CreatedAt,
	}

	if err := tx.Create(&history).Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create history record"})
		return
	}

	// 8. Save book histories
	for _, bookHistory := range bookHistories {
		bookHistory.UserHistoryID = history.ID
		if err := tx.Create(&bookHistory).Error; err != nil {
			tx.Rollback()
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save book history"})
			return
		}
	}

	// 9. Delete user from active table
	if err := tx.Delete(&user).Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to deactivate account"})
		return
	}

	// 10. Commit transaction
	if err := tx.Commit().Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit deactivation"})
		return
	}

	log.Printf("⏸️  Account deactivated: %s (ID: %d) - Reason: %s", user.Email, user.ID, req.Reason)
	c.JSON(http.StatusOK, gin.H{
		"message":    "Account deactivated successfully",
		"history_id": history.ID,
		"email":      user.Email,
		"info":       "Your account data has been saved and can be restored at any time",
	})
}

// deleteAccountHandler permanently deletes a user account (but keeps history for 90 days)
// POST /user/delete
func deleteAccountHandler(c *gin.Context) {
	// 1. Get user ID from token
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// 2. Parse request
	var req DeleteAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request", "details": err.Error()})
		return
	}

	// 3. Fetch user from database
	var user User
	if err := db.First(&user, userID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	}

	// 4. Verify password
	if err := bcrypt.CompareHashAndPassword([]byte(user.Password), []byte(req.Password)); err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Incorrect password"})
		return
	}

	// 5. Cancel Stripe subscription if exists
	if user.StripeCustomerID != "" {
		stripe.Key = getEnv("STRIPE_SECRET_KEY", "")
		params := &stripe.SubscriptionListParams{
			Customer: stripe.String(user.StripeCustomerID),
		}
		iter := subscription.List(params)
		for iter.Next() {
			sub := iter.Subscription()
			if sub.Status == stripe.SubscriptionStatusActive || sub.Status == stripe.SubscriptionStatusTrialing {
				// Cancel immediately for account deletion
				cancelParams := &stripe.SubscriptionCancelParams{}
				_, err := subscription.Cancel(sub.ID, cancelParams)
				if err != nil {
					log.Printf("⚠️  Failed to cancel Stripe subscription: %v", err)
				} else {
					log.Printf("✅ Canceled Stripe subscription %s", sub.ID)
				}
			}
		}
	}

	// 6. Start transaction
	tx := db.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// 7. Create history record
	now := time.Now()
	history := UserHistory{
		OriginalUserID:    user.ID,
		Username:          user.Username,
		Email:             user.Email,
		Password:          user.Password,
		AccountType:       user.AccountType,
		IsPublic:          user.IsPublic,
		State:             user.State,
		StripeCustomerID:  user.StripeCustomerID,
		BooksRead:         user.BooksRead,
		PhoneNumber:       user.PhoneNumber,
		DeviceModel:       user.DeviceModel,
		DeviceID:          user.DeviceID,
		PushToken:         user.PushToken,
		IPAddress:         user.IPAddress,
		OSVersion:         user.OSVersion,
		AppVersion:        user.AppVersion,
		Status:            "deleted",
		DeletionReason:    req.Reason,
		DeletedAt:         now,
		OriginalCreatedAt: user.CreatedAt,
	}

	if err := tx.Create(&history).Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create history record"})
		return
	}

	// 8. Delete user from active table
	if err := tx.Delete(&user).Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to delete account"})
		return
	}

	// 9. Commit transaction
	if err := tx.Commit().Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit deletion"})
		return
	}

	log.Printf("🗑️  Account deleted: %s (ID: %d) - Reason: %s", user.Email, user.ID, req.Reason)
	c.JSON(http.StatusOK, gin.H{
		"message":    "Account deleted successfully",
		"history_id": history.ID,
		"info":       "Your account has been deleted. Data will be kept for 90 days and can be restored if you change your mind.",
	})
}

// restoreAccountHandler restores a previously deleted/deactivated account
// POST /restore-account (public endpoint)
func restoreAccountHandler(c *gin.Context) {
	var req RestoreAccountRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request", "details": err.Error()})
		return
	}

	// 1. Find matching history record
	var history UserHistory
	query := db.Where("email = ?", req.Email).Where("restored_at IS NULL")

	// Also match by phone number or device ID for additional verification
	if req.PhoneNumber != "" {
		query = query.Or(db.Where("phone_number = ?", req.PhoneNumber).Where("restored_at IS NULL"))
	}
	if req.DeviceID != "" {
		query = query.Or(db.Where("device_id = ?", req.DeviceID).Where("restored_at IS NULL"))
	}

	if err := query.Order("deleted_at DESC").First(&history).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{
			"error":   "No deleted account found",
			"message": "We couldn't find a deleted account matching this information",
		})
		return
	}

	// 2. Check if restoration window has expired (optional: 90 days)
	daysSinceDeletion := time.Since(history.DeletedAt).Hours() / 24
	if daysSinceDeletion > 90 {
		c.JSON(http.StatusGone, gin.H{
			"error":   "Restoration period expired",
			"message": "Account data was deleted more than 90 days ago and can no longer be restored",
			"deleted_at": history.DeletedAt,
		})
		return
	}

	// 3. Start transaction to restore user
	tx := db.Begin()
	defer func() {
		if r := recover(); r != nil {
			tx.Rollback()
		}
	}()

	// 4. Recreate user account
	now := time.Now()
	restoredUser := User{
		Username:         history.Username,
		Email:            history.Email,
		Password:         history.Password,
		AccountType:      history.AccountType,
		IsPublic:         history.IsPublic,
		State:            history.State,
		StripeCustomerID: history.StripeCustomerID,
		BooksRead:        history.BooksRead,
		PhoneNumber:      history.PhoneNumber,
		DeviceModel:      history.DeviceModel,
		DeviceID:         req.DeviceID, // Use new device ID if provided
		PushToken:        history.PushToken,
		IPAddress:        c.ClientIP(),
		OSVersion:        history.OSVersion,
		AppVersion:       history.AppVersion,
		LastActiveAt:     now,
	}

	if err := tx.Create(&restoredUser).Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to restore account", "details": err.Error()})
		return
	}

	// 5. Update history record to mark as restored
	if err := tx.Model(&history).Updates(map[string]interface{}{
		"restored_at":       &now,
		"restored_to_user_id": &restoredUser.ID,
	}).Error; err != nil {
		tx.Rollback()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update history"})
		return
	}

	// 6. Restore book histories (would need to recreate books in content service)
	var bookHistories []UserBookHistory
	if err := tx.Where("user_history_id = ?", history.ID).Find(&bookHistories).Error; err == nil {
		log.Printf("📚 Found %d books to restore for user %s", len(bookHistories), restoredUser.Email)
		// Note: Actual book restoration would require calling content service
	}

	// 7. Commit transaction
	if err := tx.Commit().Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to commit restoration"})
		return
	}

	log.Printf("♻️  Account restored: %s (New ID: %d, Original ID: %d)", restoredUser.Email, restoredUser.ID, history.OriginalUserID)

	// 8. Generate JWT token for immediate login
	claims := jwt.MapClaims{
		"username": restoredUser.Username,
		"user_id":  restoredUser.ID,
		"is_admin": restoredUser.IsAdmin,
		"exp":      time.Now().Add(time.Hour * 72).Unix(),
		"iat":      time.Now().Unix(),
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString(jwtSecretKey)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"message":      "Account restored successfully",
			"user_id":      restoredUser.ID,
			"username":     restoredUser.Username,
			"books_count":  len(bookHistories),
			"account_type": restoredUser.AccountType,
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message":      "Account restored successfully",
		"user_id":      restoredUser.ID,
		"username":     restoredUser.Username,
		"token":        tokenString,
		"books_count":  len(bookHistories),
		"account_type": restoredUser.AccountType,
		"deleted_at":   history.DeletedAt,
		"restored_at":  now,
		"info":         "Welcome back! Your account and data have been restored.",
	})
}

// ============================================================================
// ADMIN HANDLERS
// ============================================================================

// adminMiddleware checks if the authenticated user has admin privileges
func adminMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Get user ID from context (set by authMiddleware)
		userID, exists := c.Get("user_id")
		if !exists {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			return
		}

		// Check if user is admin
		var user User
		if err := db.First(&user, userID).Error; err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": "User not found"})
			return
		}

		if !user.IsAdmin {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "Admin access required"})
			return
		}

		c.Next()
	}
}

// updateUserActivityHandler updates the user's last_active_at timestamp
// POST /user/activity/ping
func updateUserActivityHandler(c *gin.Context) {
	userID, exists := c.Get("user_id")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}

	// Update last_active_at
	if err := db.Model(&User{}).Where("id = ?", userID).Update("last_active_at", time.Now()).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update activity"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Activity updated"})
}

// getAdminStatsHandler returns overall platform statistics
// GET /admin/stats
func getAdminStatsHandler(c *gin.Context) {
	var stats struct {
		TotalUsers      int64 `json:"total_users"`
		PaidUsers       int64 `json:"paid_users"`
		FreeUsers       int64 `json:"free_users"`
		ActiveUsers     int64 `json:"active_users_7d"`
		NewUsersToday   int64 `json:"new_users_today"`
		NewUsersThisWeek int64 `json:"new_users_this_week"`
	}

	// Total users
	db.Model(&User{}).Count(&stats.TotalUsers)

	// Paid users
	db.Model(&User{}).Where("account_type = ?", "paid").Count(&stats.PaidUsers)

	// Free users
	db.Model(&User{}).Where("account_type = ?", "free").Count(&stats.FreeUsers)

	// Active users in last 7 days
	sevenDaysAgo := time.Now().AddDate(0, 0, -7)
	db.Model(&User{}).Where("last_active_at >= ?", sevenDaysAgo).Count(&stats.ActiveUsers)

	// New users today
	today := time.Now().Truncate(24 * time.Hour)
	db.Model(&User{}).Where("created_at >= ?", today).Count(&stats.NewUsersToday)

	// New users this week
	db.Model(&User{}).Where("created_at >= ?", sevenDaysAgo).Count(&stats.NewUsersThisWeek)

	c.JSON(http.StatusOK, stats)
}

// listUsersHandler returns a paginated list of all users
// GET /admin/users?page=1&limit=50&account_type=paid
func listUsersHandler(c *gin.Context) {
	// Pagination parameters
	page := 1
	limit := 50
	if p, err := strconv.Atoi(c.DefaultQuery("page", "1")); err == nil && p > 0 {
		page = p
	}
	if l, err := strconv.Atoi(c.DefaultQuery("limit", "50")); err == nil && l > 0 && l <= 200 {
		limit = l
	}

	offset := (page - 1) * limit

	// Build query
	query := db.Model(&User{})

	// Filter by account type
	if accountType := c.Query("account_type"); accountType != "" {
		query = query.Where("account_type = ?", accountType)
	}

	// Filter by admin status
	if isAdmin := c.Query("is_admin"); isAdmin == "true" {
		query = query.Where("is_admin = ?", true)
	}

	// Search by username or email
	if search := c.Query("search"); search != "" {
		query = query.Where("username ILIKE ? OR email ILIKE ?", "%"+search+"%", "%"+search+"%")
	}

	// Get total count
	var total int64
	query.Count(&total)

	// Get users
	var users []User
	if err := query.Select("id, username, email, account_type, is_admin, is_public, state, stripe_customer_id, books_read, last_active_at, created_at, updated_at").
		Order("created_at DESC").
		Limit(limit).
		Offset(offset).
		Find(&users).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch users"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"users":       users,
		"total":       total,
		"page":        page,
		"limit":       limit,
		"total_pages": (total + int64(limit) - 1) / int64(limit),
	})
}

// getActiveUsersHandler returns users who have been active in the last N days
// GET /admin/users/active?days=7
func getActiveUsersHandler(c *gin.Context) {
	// Default to 7 days
	days := 7
	if d, err := strconv.Atoi(c.DefaultQuery("days", "7")); err == nil && d > 0 {
		days = d
	}

	cutoffDate := time.Now().AddDate(0, 0, -days)

	type ActiveUser struct {
		ID           uint      `json:"id"`
		Username     string    `json:"username"`
		Email        string    `json:"email"`
		AccountType  string    `json:"account_type"`
		LastActiveAt time.Time `json:"last_active_at"`
		DaysActive   int       `json:"days_active"`
		BooksRead    int       `json:"books_read"`
	}

	var activeUsers []ActiveUser
	if err := db.Model(&User{}).
		Select("id, username, email, account_type, last_active_at, books_read, EXTRACT(DAY FROM NOW() - last_active_at)::int as days_active").
		Where("last_active_at >= ?", cutoffDate).
		Order("last_active_at DESC").
		Find(&activeUsers).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch active users"})
		return
	}

	// Calculate activity stats
	var weeklyActive, dailyActive int64
	oneDayAgo := time.Now().AddDate(0, 0, -1)
	db.Model(&User{}).Where("last_active_at >= ?", cutoffDate).Count(&weeklyActive)
	db.Model(&User{}).Where("last_active_at >= ?", oneDayAgo).Count(&dailyActive)

	c.JSON(http.StatusOK, gin.H{
		"active_users":        activeUsers,
		"total_active":        len(activeUsers),
		"weekly_active_count": weeklyActive,
		"daily_active_count":  dailyActive,
		"days_filter":         days,
	})
}

// makeUserAdminHandler promotes or demotes a user to/from admin
// POST /admin/users/:user_id/admin
func makeUserAdminHandler(c *gin.Context) {
	userID := c.Param("user_id")

	type AdminRequest struct {
		IsAdmin bool `json:"is_admin" binding:"required"`
	}

	var req AdminRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request", "details": err.Error()})
		return
	}

	// Update user admin status
	if err := db.Model(&User{}).Where("id = ?", userID).Update("is_admin", req.IsAdmin).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update admin status"})
		return
	}

	action := "granted"
	if !req.IsAdmin {
		action = "revoked"
	}

	log.Printf("✅ Admin access %s for user ID %s", action, userID)
	c.JSON(http.StatusOK, gin.H{
		"message":  fmt.Sprintf("Admin access %s successfully", action),
		"user_id":  userID,
		"is_admin": req.IsAdmin,
	})
}
