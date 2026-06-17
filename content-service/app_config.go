package main

import (
	"encoding/json"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// AppConfig is the remote-config payload the iOS app fetches on launch to drive
// feature flags, copy, colors, displayed pricing, and the min-supported-build
// version gate — all editable via SQL with no redeploy (mirrors PlanLimit).
//
// One row per scope: AccountType "" is the global base; "free"/"paid" rows hold
// tier overrides that are shallow-merged (top-level keys) over the global row.
type AppConfig struct {
	AccountType string `gorm:"primaryKey"` // "" = global base, "free"/"paid" = tier overrides
	Payload     string `gorm:"type:text"`  // JSON object (see seedAppConfig)
	Version     int    // bump on every edit; surfaced as top-level "version" for client cache-busting
	UpdatedAt   time.Time
}

// defaultAppConfigPayload is the global baseline served when no operator edits
// exist. Keep in sync with AudioBook/default-config.json (the iOS bundled
// fallback). Edit live rows via SQL — do NOT rely on changing this constant.
const defaultAppConfigPayload = `{
  "min_supported_build": 0,
  "features": {
    "upgrade_cta_enabled": true,
    "stripe_payment_enabled": true,
    "offline_listening": false
  },
  "pricing": {
    "monthly_price_display": "$24.99",
    "iap_product_id": "com.narrafied.premium.monthly"
  },
  "strings": {
    "upgrade_title": "Upgrade to Premium",
    "upgrade_subtitle": "Unlock unlimited audiobook processing and exclusive features",
    "upgrade_features_header": "Premium Features",
    "upgrade_payment_header": "Choose Your Payment Method",
    "upgrade_features": [
      {"icon": "infinity", "title": "Unlimited Audiobooks", "description": "Upload and process unlimited books"},
      {"icon": "waveform", "title": "Premium TTS Voices", "description": "Access to high-quality text-to-speech voices"},
      {"icon": "icloud", "title": "Cloud Storage", "description": "Store all your audiobooks in the cloud"},
      {"icon": "bolt", "title": "Priority Processing", "description": "Faster book processing and conversions"},
      {"icon": "arrow.down.circle", "title": "Offline Listening", "description": "Download books for offline playback"},
      {"icon": "star", "title": "Premium Support", "description": "Get priority customer support"}
    ],
    "home_upgrade_cta": "Tap here to upgrade",
    "home_books_header": "Your Audiobooks",
    "home_empty_title": "No audiobooks yet",
    "home_empty_subtitle": "Upload your first book to get started"
  },
  "colors": {
    "primary": "#FF6B35",
    "secondary": "#004E89"
  },
  "screens": {
    "paywall": {
      "type": "vstack",
      "props": { "spacing": 30 },
      "children": [
        { "type": "slot", "props": { "id": "header" } },
        { "type": "slot", "props": { "id": "status" } },
        { "type": "slot", "props": { "id": "features" } },
        { "type": "slot", "props": { "id": "payment" } },
        { "type": "slot", "props": { "id": "terms" } }
      ]
    },
    "home": {
      "type": "vstack",
      "props": { "alignment": "leading", "spacing": 20 },
      "children": [
        { "type": "slot", "props": { "id": "upgrade_cta" } },
        { "type": "slot", "props": { "id": "subscription_button" } },
        { "type": "slot", "props": { "id": "continue_listening" } },
        { "type": "slot", "props": { "id": "audiobooks" } }
      ]
    }
  }
}`

// seedAppConfig inserts the global config row if absent. Idempotent: never
// overwrites a payload an operator has customized via SQL.
func seedAppConfig() {
	row := AppConfig{AccountType: "", Payload: defaultAppConfigPayload, Version: 1}
	db.Where(AppConfig{AccountType: ""}).FirstOrCreate(&row)
}

// loadConfigPayload returns the parsed payload + version for a scope, or
// (nil, 0, false) if no row exists or its JSON is invalid.
func loadConfigPayload(accountType string) (map[string]json.RawMessage, int, bool) {
	var row AppConfig
	if err := db.Where("account_type = ?", accountType).First(&row).Error; err != nil {
		return nil, 0, false
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(row.Payload), &m); err != nil {
		log.Printf("⚠️ app_config payload for tier %q is invalid JSON: %v", accountType, err)
		return nil, 0, false
	}
	return m, row.Version, true
}

// getUserConfigHandler serves GET /user/config: the global config shallow-merged
// with any per-tier overrides for the caller's account_type. The app reads the
// resulting flags/copy/colors instead of compile-time constants, so changes ship
// over-the-air. Always returns 200 with at least the global config.
func getUserConfigHandler(c *gin.Context) {
	merged, version, ok := loadConfigPayload("")
	if !ok {
		// Row missing/corrupt (shouldn't happen post-seed) — fall back to the
		// compiled default so the client always gets a usable payload.
		var m map[string]json.RawMessage
		_ = json.Unmarshal([]byte(defaultAppConfigPayload), &m)
		merged, version = m, 1
	}

	// Overlay tier-specific keys (top-level shallow merge: tier wins).
	if at := accountTypeFromClaims(c); at != "" {
		if tier, tierVer, tierOK := loadConfigPayload(at); tierOK {
			for k, v := range tier {
				merged[k] = v
			}
			if tierVer > version {
				version = tierVer
			}
		}
	}

	// Surface the row version as the top-level cache-buster (authoritative over
	// any "version" baked into the payload).
	if vb, err := json.Marshal(version); err == nil {
		merged["version"] = vb
	}

	c.JSON(http.StatusOK, merged)
}
