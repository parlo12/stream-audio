package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/sideshow/apns2"
	"github.com/sideshow/apns2/payload"
	"github.com/sideshow/apns2/token"
)

// DeviceToken is an APNs device token registered by a user's device. One row
// per physical device token; a token re-registered by another user is reassigned.
type DeviceToken struct {
	ID        uint      `gorm:"primaryKey"`
	UserID    uint      `gorm:"index"`
	Token     string    `gorm:"uniqueIndex;size:300"`
	Platform  string    `gorm:"default:'ios'"`
	CreatedAt time.Time `json:"-"`
	UpdatedAt time.Time `json:"-"`
}

var (
	apnsClient *apns2.Client
	apnsTopic  string // bundle id, used as the APNs topic
)

// initAPNs wires the token-based (.p8) APNs client from env. If the key isn't
// configured, push is simply disabled (registration still works; sends no-op),
// so the service runs fine before the Apple credentials are added.
//
// Env:
//
//	APNS_KEY_ID    - 10-char Key ID of the .p8 auth key
//	APNS_TEAM_ID   - Apple Developer Team ID
//	APNS_BUNDLE_ID - app bundle id / APNs topic (default com.rmhrealestate.AudioBook)
//	APNS_P8        - the .p8 contents (PEM, \n-escaped ok) OR a path to the file
//	APNS_ENV       - "production" (default) or "sandbox"
func initAPNs() {
	keyID := getEnv("APNS_KEY_ID", "")
	teamID := getEnv("APNS_TEAM_ID", "")
	apnsTopic = getEnv("APNS_BUNDLE_ID", "com.rmhrealestate.AudioBook")
	p8 := getEnv("APNS_P8", "")
	if keyID == "" || teamID == "" || p8 == "" {
		log.Println("ℹ️ APNs not configured (APNS_KEY_ID/APNS_TEAM_ID/APNS_P8 unset) — push notifications disabled")
		return
	}

	var keyBytes []byte
	if strings.Contains(p8, "BEGIN PRIVATE KEY") {
		// Inline PEM (env vars often arrive with literal \n) — unescape.
		keyBytes = []byte(strings.ReplaceAll(p8, "\\n", "\n"))
	} else {
		b, err := os.ReadFile(p8) // treat as a file path
		if err != nil {
			log.Printf("⚠️ APNs disabled: cannot read APNS_P8 file %q: %v", p8, err)
			return
		}
		keyBytes = b
	}

	authKey, err := token.AuthKeyFromBytes(keyBytes)
	if err != nil {
		log.Printf("⚠️ APNs disabled: bad .p8 auth key: %v", err)
		return
	}
	apnsClient = apns2.NewTokenClient(&token.Token{AuthKey: authKey, KeyID: keyID, TeamID: teamID})
	env := getEnv("APNS_ENV", "production")
	if env == "production" {
		apnsClient.Production()
	} else {
		apnsClient.Development()
	}
	log.Printf("✅ APNs push initialized (env=%s, topic=%s)", env, apnsTopic)
}

// RegisterDeviceTokenHandler handles POST /user/device-token — the app sends its
// APNs token here after registering for remote notifications. Idempotent: the
// token is upserted and (re)assigned to the calling user.
func RegisterDeviceTokenHandler(c *gin.Context) {
	userID := getUserIDFromContext(c)
	if userID == 0 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	var req struct {
		Token    string `json:"token"`
		Platform string `json:"platform"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || strings.TrimSpace(req.Token) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "token is required"})
		return
	}
	platform := req.Platform
	if platform == "" {
		platform = "ios"
	}
	// Upsert by token; reassign to this user if it moved devices/accounts.
	row := DeviceToken{Token: req.Token}
	db.Where(DeviceToken{Token: req.Token}).
		Assign(DeviceToken{UserID: userID, Platform: platform}).
		FirstOrCreate(&row)
	c.JSON(http.StatusOK, gin.H{"status": "registered"})
}

// sendPushToUser delivers an alert push to every device a user has registered.
// Best-effort: logs failures, prunes stale tokens (410 / BadDeviceToken /
// Unregistered). No-op if APNs isn't configured.
func sendPushToUser(userID uint, title, body string, data map[string]interface{}) {
	if apnsClient == nil {
		return
	}
	var tokens []DeviceToken
	db.Where("user_id = ?", userID).Find(&tokens)
	for _, dt := range tokens {
		pl := payload.NewPayload().AlertTitle(title).AlertBody(body).Sound("default")
		for k, v := range data {
			pl = pl.Custom(k, v)
		}
		res, err := apnsClient.Push(&apns2.Notification{
			DeviceToken: dt.Token,
			Topic:       apnsTopic,
			Payload:     pl,
		})
		if err != nil {
			log.Printf("⚠️ APNs push to user %d failed: %v", userID, err)
			continue
		}
		if res.StatusCode == http.StatusGone || res.Reason == "BadDeviceToken" || res.Reason == "Unregistered" {
			db.Where("token = ?", dt.Token).Delete(&DeviceToken{})
			log.Printf("🧹 pruned stale device token for user %d (%s)", userID, res.Reason)
		}
	}
}

// ---- event helpers (non-blocking; safe to call from worker handlers) ----

func notifyAudiobookReady(book Book) {
	go sendPushToUser(book.UserID, "Your audiobook is ready 🎧",
		fmt.Sprintf("“%s” is ready to play.", book.Title),
		map[string]interface{}{"book_id": book.ID, "type": "audiobook_ready"})
}

func notifyBookCompleted(book Book) {
	go sendPushToUser(book.UserID, "Audiobook complete ✅",
		fmt.Sprintf("All chapters of “%s” are ready.", book.Title),
		map[string]interface{}{"book_id": book.ID, "type": "book_completed"})
}

func notifyBatchReady(book Book, pagesReady int) {
	go sendPushToUser(book.UserID, "More pages ready",
		fmt.Sprintf("“%s” now has %d pages ready to play.", book.Title, pagesReady),
		map[string]interface{}{"book_id": book.ID, "pages_ready": pagesReady, "type": "batch_ready"})
}

func notifyCoverReady(book Book) {
	go sendPushToUser(book.UserID, "Cover art added",
		fmt.Sprintf("“%s” now has its cover.", book.Title),
		map[string]interface{}{"book_id": book.ID, "type": "cover_ready"})
}
