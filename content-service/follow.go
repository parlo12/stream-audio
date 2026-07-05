package main

// Follow graph: users follow other (public) users discovered via state or
// contacts. One-directional, no approval (only public profiles are
// discoverable, so following a public account is like following a public
// account on Twitter/X).
//
//   POST   /user/follow            {user_id}   → follow
//   DELETE /user/follow/:user_id               → unfollow
//   GET    /user/following                     → people I follow (+ book previews)
//   GET    /user/follow/counts                 → {following, followers}
//
// The follows table lives in content-service (alongside discovery) but
// references the shared users table owned by auth-service.

import (
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm/clause"
)

// Follow is one edge of the social graph: FollowerID follows FolloweeID.
type Follow struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	FollowerID uint      `gorm:"index:idx_follow_pair,unique;not null;index" json:"follower_id"`
	FolloweeID uint      `gorm:"index:idx_follow_pair,unique;not null;index" json:"followee_id"`
	CreatedAt  time.Time `json:"created_at"`
}

// FollowRequest — POST /user/follow
type FollowRequest struct {
	UserID uint `json:"user_id" binding:"required"`
}

// FollowUserHandler — POST /user/follow
func FollowUserHandler(c *gin.Context) {
	followerID := c.GetUint("user_id")

	var req FollowRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "user_id required"})
		return
	}
	if req.UserID == followerID {
		c.JSON(http.StatusBadRequest, gin.H{"error": "You can't follow yourself"})
		return
	}

	// Only allow following a user that exists and is public.
	var target struct {
		ID       uint
		IsPublic bool
	}
	if err := db.Table("users").Select("id, is_public").Where("id = ?", req.UserID).Scan(&target).Error; err != nil || target.ID == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		return
	}
	if !target.IsPublic {
		c.JSON(http.StatusForbidden, gin.H{"error": "This profile is private"})
		return
	}

	// Idempotent: unique (follower, followee) makes a repeat a no-op.
	res := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&Follow{
		FollowerID: followerID,
		FolloweeID: req.UserID,
	})
	if res.Error != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not follow"})
		return
	}
	log.Printf("➕ user %d followed %d", followerID, req.UserID)

	// Notify the followee — but only on a genuinely NEW follow (RowsAffected>0),
	// so re-tapping/replays don't spam. Push carries the follower's username.
	if res.RowsAffected > 0 {
		var followerName string
		db.Table("users").Select("username").Where("id = ?", followerID).Scan(&followerName)
		if followerName == "" {
			followerName = "Someone"
		}
		go sendPushToUser(req.UserID, "New follower 👋",
			fmt.Sprintf("%s started following you on Narrafied.", followerName),
			map[string]interface{}{"type": "new_follower", "follower_id": followerID})
	}

	c.JSON(http.StatusOK, gin.H{"following": true, "user_id": req.UserID})
}

// UnfollowUserHandler — DELETE /user/follow/:user_id
func UnfollowUserHandler(c *gin.Context) {
	followerID := c.GetUint("user_id")

	targetID, err := strconv.ParseUint(c.Param("user_id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid user id"})
		return
	}

	if err := db.Where("follower_id = ? AND followee_id = ?", followerID, uint(targetID)).
		Delete(&Follow{}).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not unfollow"})
		return
	}
	log.Printf("➖ user %d unfollowed %d", followerID, targetID)
	c.JSON(http.StatusOK, gin.H{"following": false, "user_id": uint(targetID)})
}

// ListFollowingHandler — GET /user/following
// Returns the people the caller follows, with the same book-preview shape as
// discovery so the client reuses one card.
func ListFollowingHandler(c *gin.Context) {
	followerID := c.GetUint("user_id")

	var followeeIDs []uint
	db.Model(&Follow{}).Where("follower_id = ?", followerID).
		Order("created_at DESC").Pluck("followee_id", &followeeIDs)

	if len(followeeIDs) == 0 {
		c.JSON(http.StatusOK, gin.H{"people": []discoveredPerson{}})
		return
	}

	var users []discoveryUser
	db.Table("users").
		Select("id, username, state, phone_number").
		Where("id IN ?", followeeIDs).
		Scan(&users)

	// buildPeople marks is_following (all true here) and attaches books.
	c.JSON(http.StatusOK, gin.H{"people": buildPeople(followerID, users, false)})
}

// ListFollowersHandler — GET /user/followers
// People who follow the caller (so they can see who followed them).
func ListFollowersHandler(c *gin.Context) {
	userID := c.GetUint("user_id")

	var followerIDs []uint
	db.Model(&Follow{}).Where("followee_id = ?", userID).
		Order("created_at DESC").Pluck("follower_id", &followerIDs)

	if len(followerIDs) == 0 {
		c.JSON(http.StatusOK, gin.H{"people": []discoveredPerson{}})
		return
	}

	var users []discoveryUser
	db.Table("users").
		Select("id, username, state, phone_number").
		Where("id IN ?", followerIDs).
		Scan(&users)

	// buildPeople marks is_following from the CALLER's perspective, so a
	// follower the caller also follows shows "Following" (mutual) — handy for
	// follow-back.
	c.JSON(http.StatusOK, gin.H{"people": buildPeople(userID, users, false)})
}

// FollowCountsHandler — GET /user/follow/counts
func FollowCountsHandler(c *gin.Context) {
	userID := c.GetUint("user_id")

	var following, followers int64
	db.Model(&Follow{}).Where("follower_id = ?", userID).Count(&following)
	db.Model(&Follow{}).Where("followee_id = ?", userID).Count(&followers)

	c.JSON(http.StatusOK, gin.H{
		"following": following,
		"followers": followers,
	})
}
