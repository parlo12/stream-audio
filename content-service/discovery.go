package main

// Social discovery ("Home" sections):
//
//   GET  /user/discover/state    — public users in the caller's state and a
//                                  preview of their book lists.
//   POST /user/discover/contacts — contact matching WITHOUT contact upload:
//                                  the app hashes phone numbers on-device
//                                  (SHA-256 of digits-only, US 10-digit
//                                  numbers prefixed with "1") and sends only
//                                  the hashes; we hash our users' stored
//                                  numbers the same way and return matches.
//
// Privacy rules: only users with is_public = true are discoverable, the
// caller is always excluded, and only each user's newest books (title/author/
// cover) are exposed — never progress, stats, or contact details.

import (
	"crypto/sha256"
	"encoding/hex"
	"log"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// discoveredPerson is one row of a discovery response.
type discoveredPerson struct {
	UserID    uint             `json:"user_id"`
	Username  string           `json:"username"`
	State     string           `json:"state"`
	BookCount int64            `json:"book_count"`
	Books     []discoveredBook `json:"books"`
}

type discoveredBook struct {
	ID       uint   `json:"id"`
	Title    string `json:"title"`
	Author   string `json:"author"`
	CoverURL string `json:"cover_url"`
}

// discoveryUser is a read-only projection of the users table (owned by
// auth-service; same shared Postgres).
type discoveryUser struct {
	ID          uint
	Username    string
	State       string
	PhoneNumber string
}

const discoverPeopleLimit = 20
const discoverBooksPerPerson = 5

// normalizePhone reduces a phone number to a canonical digit string so the
// on-device hash and the server-side hash agree: strip everything but digits;
// bare 10-digit (US) numbers get a leading "1". The iOS ContactsMatcher MUST
// mirror this exactly.
func normalizePhone(raw string) string {
	var digits strings.Builder
	for _, r := range raw {
		if r >= '0' && r <= '9' {
			digits.WriteRune(r)
		}
	}
	d := digits.String()
	if len(d) == 10 {
		return "1" + d
	}
	return d
}

func phoneHash(normalized string) string {
	sum := sha256.Sum256([]byte(normalized))
	return hex.EncodeToString(sum[:])
}

// buildPeople loads book previews for a set of discovery users.
func buildPeople(users []discoveryUser) []discoveredPerson {
	people := make([]discoveredPerson, 0, len(users))
	for _, u := range users {
		var count int64
		db.Model(&Book{}).Where("user_id = ?", u.ID).Count(&count)
		if count == 0 {
			continue // nothing to show — skip empty libraries
		}

		var books []Book
		db.Where("user_id = ?", u.ID).
			Order("created_at DESC").
			Limit(discoverBooksPerPerson).
			Find(&books)

		preview := make([]discoveredBook, 0, len(books))
		for _, b := range books {
			preview = append(preview, discoveredBook{
				ID:       b.ID,
				Title:    b.Title,
				Author:   b.Author,
				CoverURL: b.CoverURL,
			})
		}

		people = append(people, discoveredPerson{
			UserID:    u.ID,
			Username:  u.Username,
			State:     u.State,
			BookCount: count,
			Books:     preview,
		})
	}
	return people
}

// DiscoverByStateHandler — GET /user/discover/state
func DiscoverByStateHandler(c *gin.Context) {
	userID := c.GetUint("user_id")

	var callerState string
	if err := db.Table("users").Select("state").Where("id = ?", userID).Scan(&callerState).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "could not load profile"})
		return
	}
	callerState = strings.TrimSpace(callerState)
	if callerState == "" {
		c.JSON(http.StatusOK, gin.H{"state": "", "people": []discoveredPerson{},
			"message": "Add a state to your profile to see readers near you."})
		return
	}

	var users []discoveryUser
	if err := db.Table("users").
		Select("id, username, state, phone_number").
		Where("LOWER(TRIM(state)) = LOWER(?) AND is_public = true AND id <> ?", callerState, userID).
		Limit(discoverPeopleLimit).
		Scan(&users).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "discovery query failed"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"state":  callerState,
		"people": buildPeople(users),
	})
}

// ContactHashRequest carries on-device-computed SHA-256 phone hashes.
type ContactHashRequest struct {
	PhoneHashes []string `json:"phone_hashes" binding:"required"`
}

const maxContactHashes = 3000

// DiscoverContactsHandler — POST /user/discover/contacts
func DiscoverContactsHandler(c *gin.Context) {
	userID := c.GetUint("user_id")

	var req ContactHashRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "phone_hashes required"})
		return
	}
	if len(req.PhoneHashes) > maxContactHashes {
		req.PhoneHashes = req.PhoneHashes[:maxContactHashes]
	}

	wanted := make(map[string]bool, len(req.PhoneHashes))
	for _, h := range req.PhoneHashes {
		wanted[strings.ToLower(strings.TrimSpace(h))] = true
	}

	// User base is small: hash every stored number on the fly. When this
	// grows, precompute a phone_hash column instead.
	var candidates []discoveryUser
	if err := db.Table("users").
		Select("id, username, state, phone_number").
		Where("phone_number <> '' AND is_public = true AND id <> ?", userID).
		Scan(&candidates).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "discovery query failed"})
		return
	}

	var matched []discoveryUser
	for _, u := range candidates {
		normalized := normalizePhone(u.PhoneNumber)
		if len(normalized) < 10 {
			continue // junk numbers can't match meaningfully
		}
		if wanted[phoneHash(normalized)] {
			matched = append(matched, u)
			if len(matched) >= discoverPeopleLimit {
				break
			}
		}
	}

	log.Printf("👥 contact discovery for user %d: %d hashes in, %d matches", userID, len(req.PhoneHashes), len(matched))
	c.JSON(http.StatusOK, gin.H{"people": buildPeople(matched)})
}
