package main

// SMS OTP phone verification via Twilio Verify.
//
//   POST /user/phone/start  {phone_number}         → sends a 6-digit SMS code
//   POST /user/phone/verify {phone_number, code}   → on approval, stores the
//                                                     number as VERIFIED
//
// Twilio Verify owns code generation, expiry, rate-limiting and fraud
// checks, so we store nothing here. Only phone_verified=true numbers are
// matchable in contact discovery (content-service discovery.go), which closes
// the spoofing hole where anyone could claim someone else's number.
//
// Requires env: TWILIO_ACCOUNT_SID, TWILIO_AUTH_TOKEN, TWILIO_VERIFY_SERVICE_SID.
// When unset the endpoints return 503 so the feature degrades cleanly.

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt"
)

func twilioConfigured() bool {
	return getEnv("TWILIO_ACCOUNT_SID", "") != "" &&
		getEnv("TWILIO_AUTH_TOKEN", "") != "" &&
		getEnv("TWILIO_VERIFY_SERVICE_SID", "") != ""
}

// toE164 normalizes user input to E.164 (+1XXXXXXXXXX default US). Returns ""
// when it can't confidently format — the caller rejects those.
func toE164(raw string) string {
	var b strings.Builder
	for _, r := range raw {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	d := b.String()
	switch {
	case len(d) == 10:
		return "+1" + d
	case len(d) == 11 && strings.HasPrefix(d, "1"):
		return "+" + d
	case strings.HasPrefix(strings.TrimSpace(raw), "+") && len(d) >= 11 && len(d) <= 15:
		return "+" + d
	default:
		return ""
	}
}

// twilioVerifyPost calls a Twilio Verify sub-resource with form params.
func twilioVerifyPost(resource string, form url.Values) (map[string]interface{}, int, error) {
	sid := getEnv("TWILIO_ACCOUNT_SID", "")
	token := getEnv("TWILIO_AUTH_TOKEN", "")
	service := getEnv("TWILIO_VERIFY_SERVICE_SID", "")

	endpoint := fmt.Sprintf("https://verify.twilio.com/v2/Services/%s/%s", service, resource)
	req, err := http.NewRequest(http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, 0, err
	}
	req.SetBasicAuth(sid, token)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var parsed map[string]interface{}
	_ = json.Unmarshal(body, &parsed)
	return parsed, resp.StatusCode, nil
}

// startPhoneVerificationHandler — POST /user/phone/start
func startPhoneVerificationHandler(c *gin.Context) {
	if _, ok := c.Get("claims"); !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	if !twilioConfigured() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Phone verification isn't available right now."})
		return
	}

	var req struct {
		PhoneNumber string `json:"phone_number" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "phone_number required"})
		return
	}
	e164 := toE164(req.PhoneNumber)
	if e164 == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Enter a valid phone number."})
		return
	}

	form := url.Values{}
	form.Set("To", e164)
	form.Set("Channel", "sms")
	parsed, code, err := twilioVerifyPost("Verifications", form)
	if err != nil {
		log.Printf("⚠️ twilio start error: %v", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "Couldn't send the code. Try again."})
		return
	}
	if code < 200 || code >= 300 {
		msg := "Couldn't send the code."
		if m, ok := parsed["message"].(string); ok && m != "" {
			msg = m
		}
		log.Printf("⚠️ twilio start non-2xx (%d): %v", code, parsed)
		c.JSON(http.StatusBadGateway, gin.H{"error": msg})
		return
	}
	c.JSON(http.StatusOK, gin.H{"sent": true, "phone_number": e164})
}

// checkPhoneVerificationHandler — POST /user/phone/verify
func checkPhoneVerificationHandler(c *gin.Context) {
	claims, ok := c.Get("claims")
	if !ok {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	userID := uint(claims.(jwt.MapClaims)["user_id"].(float64))

	if !twilioConfigured() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Phone verification isn't available right now."})
		return
	}

	var req struct {
		PhoneNumber string `json:"phone_number" binding:"required"`
		Code        string `json:"code" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "phone_number and code required"})
		return
	}
	e164 := toE164(req.PhoneNumber)
	if e164 == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Enter a valid phone number."})
		return
	}

	form := url.Values{}
	form.Set("To", e164)
	form.Set("Code", strings.TrimSpace(req.Code))
	parsed, code, err := twilioVerifyPost("VerificationCheck", form)
	if err != nil {
		log.Printf("⚠️ twilio check error: %v", err)
		c.JSON(http.StatusBadGateway, gin.H{"error": "Couldn't verify the code. Try again."})
		return
	}
	// Twilio returns 404 when the code expired / no pending verification.
	status, _ := parsed["status"].(string)
	if code < 200 || code >= 300 || status != "approved" {
		c.JSON(http.StatusBadRequest, gin.H{"verified": false, "error": "Incorrect or expired code."})
		return
	}

	// Approved → store the number as verified (makes the user discoverable).
	if err := db.Model(&User{}).Where("id = ?", userID).
		Updates(map[string]interface{}{"phone_number": e164, "phone_verified": true}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Verified, but couldn't save your number."})
		return
	}
	log.Printf("✅ user %d verified phone %s", userID, e164)
	c.JSON(http.StatusOK, gin.H{"verified": true, "phone_number": e164})
}
