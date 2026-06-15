package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
)

// errQuotaExceeded is returned mid-batch when a page would exceed the user's
// transcription budget; the batch handler stops (capped) on this.
var errQuotaExceeded = errors.New("quota exceeded")

// PlanLimit is the per-tier monthly budget for a metric (rows are editable
// without a redeploy).
type PlanLimit struct {
	AccountType  string `gorm:"primaryKey"`
	Metric       string `gorm:"primaryKey"`
	MonthlyLimit int64
	HardCap      bool
}

// UsageEvent is the append-only metering ledger (audit trail / disputes).
type UsageEvent struct {
	ID        uint `gorm:"primaryKey"`
	UserID    uint `gorm:"index"`
	Metric    string
	Amount    int64
	BookID    uint
	CreatedAt time.Time
}

var rdb *redis.Client

// initRedis builds the counter client from REDIS_URL (same Redis as asynq).
func initRedis() error {
	opt, err := redis.ParseURL(getEnv("REDIS_URL", "redis://redis:6379"))
	if err != nil {
		return err
	}
	rdb = redis.NewClient(opt)
	return rdb.Ping(context.Background()).Err()
}

// seedPlanLimits inserts placeholder per-tier limits if the table is empty.
// Adjust these rows via SQL to match the real cost model — no redeploy needed.
func seedPlanLimits() {
	// Idempotent per-row: inserts any missing default metric without
	// overwriting limits an operator has customized via SQL.
	defaults := []PlanLimit{
		{AccountType: "free", Metric: "transcribe_pages", MonthlyLimit: 20, HardCap: true},
		{AccountType: "free", Metric: "uploads", MonthlyLimit: 1, HardCap: true},
		{AccountType: "free", Metric: "stream_pages", MonthlyLimit: 2000, HardCap: false}, // abuse cap, not a paywall
		{AccountType: "paid", Metric: "transcribe_pages", MonthlyLimit: 1000, HardCap: false},
		{AccountType: "paid", Metric: "uploads", MonthlyLimit: 20, HardCap: false},
		{AccountType: "paid", Metric: "stream_pages", MonthlyLimit: 100000, HardCap: false},
	}
	for _, d := range defaults {
		row := d
		db.Where(PlanLimit{AccountType: d.AccountType, Metric: d.Metric}).FirstOrCreate(&row)
	}
}

// QuotaDecision is the result of a quota check.
type QuotaDecision struct {
	Allowed  bool
	Used     int64
	Limit    int64 // -1 = unlimited (no configured row)
	ResetsAt time.Time
	Metric   string
}

func usagePeriod() string { return time.Now().UTC().Format("2006-01") } // monthly window

func monthEnd() time.Time {
	now := time.Now().UTC()
	return time.Date(now.Year(), now.Month()+1, 1, 0, 0, 0, 0, time.UTC)
}

func planLimitFor(accountType, metric string) (int64, bool) {
	var pl PlanLimit
	if err := db.Where("account_type = ? AND metric = ?", accountType, metric).First(&pl).Error; err != nil {
		return 0, false
	}
	return pl.MonthlyLimit, true
}

// checkAndConsume atomically reserves `amount` of a metric for the month and
// records it in the ledger. amount==0 is a read-only pre-check (no consume).
// Fails OPEN if Redis is unavailable (don't take the whole pipeline down for a
// counter blip) — logged.
func checkAndConsume(userID uint, accountType, metric string, amount int64, bookID uint) QuotaDecision {
	resets := monthEnd()
	limit, ok := planLimitFor(accountType, metric)
	if !ok { // no configured limit → unlimited
		if amount > 0 {
			recordUsage(userID, metric, amount, bookID)
		}
		return QuotaDecision{Allowed: true, Limit: -1, ResetsAt: resets, Metric: metric}
	}

	ctx := context.Background()
	key := fmt.Sprintf("usage:%d:%s:%s", userID, metric, usagePeriod())

	if amount == 0 {
		cur, _ := rdb.Get(ctx, key).Int64()
		return QuotaDecision{Allowed: cur < limit, Used: cur, Limit: limit, ResetsAt: resets, Metric: metric}
	}

	newVal, err := rdb.IncrBy(ctx, key, amount).Result()
	if err != nil {
		log.Printf("⚠️ quota counter unavailable (%s) — failing open: %v", key, err)
		recordUsage(userID, metric, amount, bookID)
		return QuotaDecision{Allowed: true, Limit: limit, ResetsAt: resets, Metric: metric}
	}
	if newVal == amount { // first write this month
		rdb.Expire(ctx, key, 35*24*time.Hour)
	}
	if newVal > limit {
		rdb.DecrBy(ctx, key, amount) // rollback the reservation
		return QuotaDecision{Allowed: false, Used: newVal - amount, Limit: limit, ResetsAt: resets, Metric: metric}
	}
	recordUsage(userID, metric, amount, bookID)
	return QuotaDecision{Allowed: true, Used: newVal, Limit: limit, ResetsAt: resets, Metric: metric}
}

func recordUsage(userID uint, metric string, amount int64, bookID uint) {
	if err := db.Create(&UsageEvent{UserID: userID, Metric: metric, Amount: amount, BookID: bookID, CreatedAt: time.Now()}).Error; err != nil {
		log.Printf("⚠️ failed to write usage_event: %v", err)
	}
}

// quota429 writes the structured paywall response.
func quota429(c *gin.Context, d QuotaDecision) {
	c.JSON(http.StatusTooManyRequests, gin.H{
		"error":       "quota_exceeded",
		"quota":       d.Metric,
		"used":        d.Used,
		"limit":       d.Limit,
		"resets_at":   d.ResetsAt.UTC().Format(time.RFC3339),
		"upgrade_url": getEnv("UPGRADE_URL", "https://narrafied.com/upgrade"),
	})
}

func pauseAheadPages() int { return envInt("PAUSE_AHEAD_PAGES", 60) }
