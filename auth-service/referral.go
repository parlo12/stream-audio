package main

// Referral program ("sharing feature"):
//
//   1. Every user has a shareable referral code (lazily generated) and an
//      invite link https://narrafied.com/invite/{code}.
//   2. A new user passes referral_code at signup → users.referred_by is set.
//   3. When the referred account converts to PAID (Stripe checkout completes
//      or an Apple IAP receipt is validated), the referrer earns ONE free
//      month of premium, tracked as a ReferralCredit row and materialized as
//      users.premium_until.
//   4. effectiveAccountType() treats a future premium_until as "paid", so
//      credit months work identically for Stripe and IAP users without
//      touching either billing system.
//
// Anti-abuse: one credit per referred account ever (unique index on
// referred_user_id), self-referral blocked by user id and by signup device
// fingerprint, and credits are only granted on a paid conversion.

import (
	"crypto/rand"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt"
	"gorm.io/gorm/clause"
)

// ReferralCredit is the append-only ledger of awarded referral months.
// referred_user_id is unique: an account can only ever generate one credit,
// no matter how many times billing events fire for it.
type ReferralCredit struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	ReferrerUserID uint      `gorm:"index;not null" json:"referrer_user_id"`
	ReferredUserID uint      `gorm:"uniqueIndex;not null" json:"referred_user_id"`
	Months         int       `gorm:"not null;default:1" json:"months"`
	Source         string    `json:"source"` // "stripe" | "apple_iap"
	CreatedAt      time.Time `json:"created_at"`
}

// referralCodeAlphabet omits easily-confused characters (I, L, O, 0, 1).
const referralCodeAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"
const referralCodeLength = 8

func generateReferralCode() (string, error) {
	b := make([]byte, referralCodeLength)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	for i := range b {
		b[i] = referralCodeAlphabet[int(b[i])%len(referralCodeAlphabet)]
	}
	return string(b), nil
}

// ensureReferralCode returns the user's code, generating and persisting one
// on first use. Retries on the (unlikely) unique-index collision.
func ensureReferralCode(user *User) (string, error) {
	if user.ReferralCode != nil && *user.ReferralCode != "" {
		return *user.ReferralCode, nil
	}
	var lastErr error
	for attempt := 0; attempt < 5; attempt++ {
		code, err := generateReferralCode()
		if err != nil {
			return "", err
		}
		res := db.Model(&User{}).
			Where("id = ? AND (referral_code IS NULL OR referral_code = '')", user.ID).
			Update("referral_code", code)
		if res.Error != nil {
			lastErr = res.Error
			continue // likely a collision with another user's code; retry
		}
		if res.RowsAffected == 0 {
			// Another request generated it concurrently — reload and use that.
			var fresh User
			if err := db.First(&fresh, user.ID).Error; err == nil && fresh.ReferralCode != nil {
				user.ReferralCode = fresh.ReferralCode
				return *fresh.ReferralCode, nil
			}
			continue
		}
		user.ReferralCode = &code
		return code, nil
	}
	return "", lastErr
}

// resolveReferralCode maps a signup's referral code to the referrer's user id.
// Returns 0 (organic signup) for empty/unknown codes or a device-fingerprint
// self-referral. Invalid codes never block signup.
func resolveReferralCode(rawCode, signupDeviceID string) uint {
	code := strings.ToUpper(strings.TrimSpace(rawCode))
	if code == "" {
		return 0
	}
	var referrer User
	if err := db.Where("referral_code = ?", code).First(&referrer).Error; err != nil {
		log.Printf("ℹ️ signup used unknown referral code %q — ignored", code)
		return 0
	}
	if signupDeviceID != "" && referrer.DeviceID == signupDeviceID {
		log.Printf("🚫 self-referral blocked: code %s used from referrer's own device", code)
		return 0
	}
	return referrer.ID
}

// effectiveAccountType is the single source of truth for tier checks: a user
// is "paid" if their billing tier says so OR they hold unexpired referral
// credit. Everything that reports account_type (JWT claims, /user/account-type,
// profile, subscription status) must go through this.
func effectiveAccountType(user *User) string {
	if user.AccountType == "paid" {
		return "paid"
	}
	if user.PremiumUntil != nil && user.PremiumUntil.After(time.Now()) {
		return "paid"
	}
	if user.AccountType == "" {
		return "free"
	}
	return user.AccountType
}

// extendPremiumUntil returns the new entitlement expiry after adding `months`:
// stacking on the current expiry when it is still in the future, else starting
// from now. Pure function for testability.
func extendPremiumUntil(current *time.Time, now time.Time, months int) time.Time {
	base := now
	if current != nil && current.After(now) {
		base = *current
	}
	return base.AddDate(0, months, 0)
}

// awardReferralCredit grants the referrer one free month for a referred
// user's first paid conversion. Idempotent: the unique index on
// referred_user_id makes replays (webhook retries, repeated receipt
// validations) no-ops.
func awardReferralCredit(referred *User, source string) {
	if referred.ReferredBy == 0 || referred.ReferredBy == referred.ID {
		return
	}
	var referrer User
	if err := db.First(&referrer, referred.ReferredBy).Error; err != nil {
		log.Printf("⚠️ referral: referrer %d for user %d not found", referred.ReferredBy, referred.ID)
		return
	}
	// Second self-referral guard: same device fingerprint at conversion time.
	if referred.DeviceID != "" && referred.DeviceID == referrer.DeviceID {
		log.Printf("🚫 referral credit blocked: user %d shares a device with referrer %d", referred.ID, referrer.ID)
		return
	}

	credit := ReferralCredit{
		ReferrerUserID: referrer.ID,
		ReferredUserID: referred.ID,
		Months:         1,
		Source:         source,
	}
	res := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&credit)
	if res.Error != nil {
		log.Printf("⚠️ referral: could not record credit for referred user %d: %v", referred.ID, res.Error)
		return
	}
	if res.RowsAffected == 0 {
		return // already awarded for this referred account
	}

	until := extendPremiumUntil(referrer.PremiumUntil, time.Now(), credit.Months)
	if err := db.Model(&User{}).Where("id = ?", referrer.ID).Update("premium_until", until).Error; err != nil {
		log.Printf("⚠️ referral: credit %d recorded but premium_until update failed: %v", credit.ID, err)
		return
	}
	log.Printf("🎁 referral: user %d earned 1 free month (source=%s, referred=%d, premium_until=%s)",
		referrer.ID, source, referred.ID, until.Format(time.RFC3339))
}

// awardReferralForStripeCustomer is the Stripe-webhook hook: called after
// checkout.session.completed marks the customer paid.
func awardReferralForStripeCustomer(customerID string) {
	var user User
	if err := db.Where("stripe_customer_id = ?", customerID).First(&user).Error; err != nil {
		return
	}
	awardReferralCredit(&user, "stripe")
}

// getReferralInfoHandler — GET /user/referral
// Returns the caller's code, invite link, share text, and program stats.
func getReferralInfoHandler(c *gin.Context) {
	claims, exists := c.Get("claims")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	userID := uint(claims.(jwt.MapClaims)["user_id"].(float64))

	var user User
	if err := db.First(&user, userID).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "User not found"})
		return
	}

	code, err := ensureReferralCode(&user)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not generate referral code"})
		return
	}

	var invitedCount int64
	db.Model(&User{}).Where("referred_by = ?", user.ID).Count(&invitedCount)

	var convertedCount int64
	var monthsEarned int64
	db.Model(&ReferralCredit{}).Where("referrer_user_id = ?", user.ID).Count(&convertedCount)
	db.Model(&ReferralCredit{}).Where("referrer_user_id = ?", user.ID).
		Select("COALESCE(SUM(months), 0)").Scan(&monthsEarned)

	inviteBase := strings.TrimRight(getEnv("INVITE_BASE_URL", "https://narrafied.com/invite"), "/")
	inviteURL := inviteBase + "/" + code

	resp := gin.H{
		"referral_code":      code,
		"invite_url":         inviteURL,
		"share_text":         "I turn my documents into AI-narrated audiobooks with Narrafied. Sign up with my invite code " + code + ": " + inviteURL,
		"invited_count":      invitedCount,
		"converted_count":    convertedCount,
		"free_months_earned": monthsEarned,
	}
	if user.PremiumUntil != nil {
		resp["premium_until"] = user.PremiumUntil.Format(time.RFC3339)
	}
	c.JSON(http.StatusOK, resp)
}

// ValidateReceiptRequest matches what the iOS AppleIAPManager already sends.
type ValidateReceiptRequest struct {
	TransactionID string `json:"transaction_id" binding:"required"`
	ProductID     string `json:"product_id" binding:"required"`
	PurchaseDate  string `json:"purchase_date"`
	OriginalID    string `json:"original_id"`
}

// validateReceiptHandler — POST /user/subscription/validate-receipt
//
// The iOS app has called this after every StoreKit purchase since the hybrid
// payment work, but the endpoint never existed (404) — so Apple IAP
// entitlements never synced server-side. This implements it: marks the caller
// paid and triggers the referral award.
//
// TODO(before public launch): verify the transaction against the App Store
// Server API (signed JWS) instead of trusting the client. Until then this is
// acceptable for TestFlight but is spoofable.
func validateReceiptHandler(c *gin.Context) {
	claims, exists := c.Get("claims")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	userID := uint(claims.(jwt.MapClaims)["user_id"].(float64))

	var req ValidateReceiptRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid receipt data", "details": err.Error()})
		return
	}

	expectedProduct := getEnv("IAP_PRODUCT_ID", "com.narrafied.premium.monthly")
	if req.ProductID != expectedProduct {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Unknown product", "product_id": req.ProductID})
		return
	}

	var user User
	if err := db.First(&user, userID).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "User not found"})
		return
	}

	if user.AccountType != "paid" {
		if err := db.Model(&User{}).Where("id = ?", user.ID).Update("account_type", "paid").Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not update account"})
			return
		}
		log.Printf("✅ IAP receipt accepted for user %d (tx %s) — account_type=paid", user.ID, req.TransactionID)
	}

	awardReferralCredit(&user, "apple_iap")

	c.JSON(http.StatusOK, gin.H{
		"status":       "ok",
		"account_type": "paid",
	})
}

// inviteRedirectHandler — GET /invite/:code (public)
// Sends invite-link clicks to the download destination (TestFlight now, App
// Store later) via INVITE_REDIRECT_URL. The code itself travels in the share
// text; the new user types it at signup.
func inviteRedirectHandler(c *gin.Context) {
	target := getEnv("INVITE_REDIRECT_URL", "https://narrafied.com")
	c.Redirect(http.StatusFound, target)
}

// MARK: Phone number (contact-discovery enrollment)

// UpdatePhoneRequest — POST /user/phone
type UpdatePhoneRequest struct {
	PhoneNumber string `json:"phone_number" binding:"required"`
}

// updatePhoneHandler stores the caller's phone number so friends' contact
// matching (content-service discovery.go) can find them. Accepts anything
// with ≥10 digits; stores the raw input (normalization happens at match
// time on both sides).
//
// TODO(before public launch): verify ownership with an SMS OTP — without it
// a user could enter someone else's number and appear in that person's
// friends' matches.
func updatePhoneHandler(c *gin.Context) {
	claims, exists := c.Get("claims")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	userID := uint(claims.(jwt.MapClaims)["user_id"].(float64))

	var req UpdatePhoneRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "phone_number required"})
		return
	}

	digits := 0
	for _, r := range req.PhoneNumber {
		if r >= '0' && r <= '9' {
			digits++
		}
	}
	if digits < 10 || digits > 15 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Enter a valid phone number (10–15 digits)"})
		return
	}

	if err := db.Model(&User{}).Where("id = ?", userID).
		Update("phone_number", strings.TrimSpace(req.PhoneNumber)).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not save phone number"})
		return
	}

	log.Printf("📱 user %d set a phone number (contact discovery enabled)", userID)
	c.JSON(http.StatusOK, gin.H{
		"message":      "Phone number saved",
		"phone_number": strings.TrimSpace(req.PhoneNumber),
	})
}

// MARK: Profile visibility

// UpdateVisibilityRequest — POST /user/visibility
type UpdateVisibilityRequest struct {
	IsPublic *bool `json:"is_public" binding:"required"`
}

// updateVisibilityHandler toggles the caller's profile between public
// (discoverable in state/contact discovery and followable) and private.
func updateVisibilityHandler(c *gin.Context) {
	claims, exists := c.Get("claims")
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	userID := uint(claims.(jwt.MapClaims)["user_id"].(float64))

	var req UpdateVisibilityRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.IsPublic == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "is_public required"})
		return
	}

	if err := db.Model(&User{}).Where("id = ?", userID).
		Update("is_public", *req.IsPublic).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not update visibility"})
		return
	}
	log.Printf("👁️ user %d set profile is_public=%v", userID, *req.IsPublic)
	c.JSON(http.StatusOK, gin.H{"is_public": *req.IsPublic})
}
