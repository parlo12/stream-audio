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

		// Fresh-transcription budget — audio-SECONDS of genuinely-NEW synthesis
		// (cache-miss renders, our real cost). Cached reuse is unlimited/free and
		// never charged. HardCap = a real paywall. free = shelf-only (0 new
		// transcription); starter = 2h; premium = 8h. "paid" aliases premium
		// until App-Store IAP products map users to starter/premium.
		{AccountType: "free", Metric: "transcribe_seconds", MonthlyLimit: 0, HardCap: true},
		{AccountType: "starter", Metric: "transcribe_seconds", MonthlyLimit: 7200, HardCap: true},
		{AccountType: "premium", Metric: "transcribe_seconds", MonthlyLimit: 28800, HardCap: true},
		{AccountType: "paid", Metric: "transcribe_seconds", MonthlyLimit: 28800, HardCap: true},
		// Free-tier listening paywall (~2h then upgrade). Paid tiers: no row =
		// unlimited. stream_pages is enforced at the stream path already; free's
		// existing soft row is switched to this hard cap by a deploy-time SQL
		// UPDATE (FirstOrCreate below won't modify an existing row).
		{AccountType: "starter", Metric: "stream_pages", MonthlyLimit: 100000, HardCap: false},
		{AccountType: "premium", Metric: "stream_pages", MonthlyLimit: 100000, HardCap: false},
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

func planLimitFor(accountType, metric string) (limit int64, hardCap bool, ok bool) {
	var pl PlanLimit
	if err := db.Where("account_type = ? AND metric = ?", accountType, metric).First(&pl).Error; err != nil {
		return 0, false, false
	}
	return pl.MonthlyLimit, pl.HardCap, true
}

// checkAndConsume atomically reserves `amount` of a metric for the month and
// records it in the ledger. amount==0 is a read-only pre-check (no consume).
// Fails OPEN if Redis is unavailable (don't take the whole pipeline down for a
// counter blip) — logged.
func checkAndConsume(userID uint, accountType, metric string, amount int64, bookID uint) QuotaDecision {
	resets := monthEnd()
	limit, hardCap, ok := planLimitFor(accountType, metric)
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
		// Soft (metered) limits never block the pre-check — they only inform.
		allowed := !hardCap || cur < limit
		return QuotaDecision{Allowed: allowed, Used: cur, Limit: limit, ResetsAt: resets, Metric: metric}
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
	if newVal > limit && hardCap {
		// Hard cap → wall (rollback the reservation, deny). Only free-tier
		// abuse metrics should be hard-capped.
		rdb.DecrBy(ctx, key, amount)
		return QuotaDecision{Allowed: false, Used: newVal - amount, Limit: limit, ResetsAt: resets, Metric: metric}
	}
	// Soft (metered) limit: record the usage and ALWAYS allow — a large book
	// must never stall mid-transcription. Overage is visible in usage_events
	// for later billing/analytics; the lazy pause-ahead model keeps real spend
	// bounded by what's actually listened to.
	recordUsage(userID, metric, amount, bookID)
	return QuotaDecision{Allowed: true, Used: newVal, Limit: limit, ResetsAt: resets, Metric: metric}
}

func recordUsage(userID uint, metric string, amount int64, bookID uint) {
	if err := db.Create(&UsageEvent{UserID: userID, Metric: metric, Amount: amount, BookID: bookID, CreatedAt: time.Now()}).Error; err != nil {
		log.Printf("⚠️ failed to write usage_event: %v", err)
	}
}

// addUsage records consumption WITHOUT gating: the caller already passed the
// pre-check and the work (a fresh render) already happened, so we never deny or
// roll back here — we just reflect reality (bounded ~1-unit overshoot past a
// cap). Increments the monthly counter and the append-only ledger.
func addUsage(userID uint, accountType, metric string, amount int64, bookID uint) {
	if amount <= 0 {
		return
	}
	recordUsage(userID, metric, amount, bookID)
	if rdb == nil {
		return
	}
	ctx := context.Background()
	key := fmt.Sprintf("usage:%d:%s:%s", userID, metric, usagePeriod())
	if newVal, err := rdb.IncrBy(ctx, key, amount).Result(); err == nil && newVal == amount {
		rdb.Expire(ctx, key, 35*24*time.Hour)
	}
}

// consumeFreshTranscription gates a cache-MISS page render on the user's
// monthly transcription-time budget (metric "transcribe_seconds"). Only
// genuinely-new synthesis — our real cost — reaches here; the caller checks the
// dedup cache first and never charges a reuse. Returns errQuotaExceeded if the
// user is at their cap; otherwise a charge() to call with the rendered audio's
// duration in seconds after a successful render.
func consumeFreshTranscription(userID uint, accountType string, bookID uint) (func(seconds float64), error) {
	if d := checkAndConsume(userID, accountType, "transcribe_seconds", 0, bookID); !d.Allowed {
		return nil, errQuotaExceeded
	}
	return func(seconds float64) {
		addUsage(userID, accountType, "transcribe_seconds", int64(seconds+0.5), bookID)
	}, nil
}

// transcriptionUsageHandler (GET /user/transcription-usage) reports the caller's
// monthly fresh-transcription budget so the app can show "X hrs of new
// transcription left" and drive the upgrade prompt. Limit -1 = unlimited.
func transcriptionUsageHandler(c *gin.Context) {
	uid := getUserIDFromContext(c)
	at := accountTypeFromClaims(c)
	d := checkAndConsume(uid, at, "transcribe_seconds", 0, 0)
	remaining := int64(-1) // unlimited
	if d.Limit >= 0 {
		if remaining = d.Limit - d.Used; remaining < 0 {
			remaining = 0
		}
	}
	hrs := func(sec int64) float64 {
		if sec < 0 {
			return -1
		}
		return float64(sec) / 3600.0
	}
	c.JSON(http.StatusOK, gin.H{
		"plan":              at,
		"seconds_used":      d.Used,
		"seconds_limit":     d.Limit,
		"seconds_remaining": remaining,
		"hours_limit":       hrs(d.Limit),
		"hours_remaining":   hrs(remaining),
		"resets_at":         d.ResetsAt.UTC().Format(time.RFC3339),
	})
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

// lookAheadPages is how many pages ahead of the listener to pre-transcribe +
// HLS-package so HLS is the primary playback path. Small by design (bounds cost
// and worker load); re-triggered as playback progresses.
func lookAheadPages() int { return envInt("LOOKAHEAD_PAGES", 3) }
