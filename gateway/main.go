package main

import (
	"crypto/rand"
	"encoding/hex"
	"log"
	"log/slog"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

func main() {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()

	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	router.Use(requestIDMiddleware(), structuredLogger(logger), gin.Recovery(), bodyLimitMiddleware())

	gatewayPort := getEnv("GATEWAY_PORT", "8080")
	authSvcURL := getEnv("AUTH_SERVICE_URL", "http://auth-service:8082")
	contentSvcURL := getEnv("CONTENT_SERVICE_URL", "http://content-service:8083")

	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "up"})
	})

	authProxy := mustNewProxy(authSvcURL)
	contentProxy := mustNewProxy(contentSvcURL)

	// Brute-force-sensitive auth endpoints get per-IP rate limiting.
	authLimiter := newIPRateLimiter()
	rl := rateLimitMiddleware(authLimiter)
	router.Any("/signup", rl, wrapProxy(authProxy))
	router.Any("/login", rl, wrapProxy(authProxy))
	router.Any("/auth/*proxyPath", rl, wrapProxy(authProxy))

	// Stripe webhook must NOT be rate limited (legitimate bursts on retries).
	router.POST("/stripe/webhook", wrapProxy(authProxy))

	router.Any("/content/*proxyPath", wrapProxy(contentProxy))
	router.Any("/admin/*proxyPath", wrapProxy(contentProxy))

	logger.Info("gateway listening", "port", gatewayPort, "auth", authSvcURL, "content", contentSvcURL)

	srv := &http.Server{
		Addr:              ":" + gatewayPort,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		// No WriteTimeout: streamed audio responses can be long-lived.
	}
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("gateway failed: %v", err)
	}
}

// mustNewProxy parses targetURL and returns a ReverseProxy (with bounded
// transport timeouts) or exits.
func mustNewProxy(targetURL string) *httputil.ReverseProxy {
	u, err := url.Parse(targetURL)
	if err != nil {
		log.Fatalf("bad proxy URL %q: %v", targetURL, err)
	}
	p := httputil.NewSingleHostReverseProxy(u)
	p.Transport = &http.Transport{
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
		ResponseHeaderTimeout: 30 * time.Second,
		IdleConnTimeout:       90 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		MaxIdleConns:          100,
	}
	return p
}

// wrapProxy delegates to the given proxy, forwarding the request ID upstream.
func wrapProxy(p *httputil.ReverseProxy) gin.HandlerFunc {
	return func(c *gin.Context) {
		if rid := c.GetString("request_id"); rid != "" {
			c.Request.Header.Set("X-Request-ID", rid)
		}
		p.ServeHTTP(c.Writer, c.Request)
	}
}

// ---- middleware ----

// requestIDMiddleware assigns/propagates a correlation ID per request.
func requestIDMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		rid := c.GetHeader("X-Request-ID")
		if rid == "" {
			rid = randomHex(8)
		}
		c.Set("request_id", rid)
		c.Writer.Header().Set("X-Request-ID", rid)
		c.Next()
	}
}

// structuredLogger emits one JSON line per request.
func structuredLogger(logger *slog.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		logger.Info("request",
			"method", c.Request.Method,
			"path", c.Request.URL.Path,
			"status", c.Writer.Status(),
			"ip", c.ClientIP(),
			"request_id", c.GetString("request_id"),
			"latency_ms", time.Since(start).Milliseconds(),
		)
	}
}

// bodyLimitMiddleware caps inbound request bodies.
func bodyLimitMiddleware() gin.HandlerFunc {
	max := int64(envInt("MAX_PROXY_BODY_BYTES", 64<<20)) // 64 MB default
	return func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, max)
		c.Next()
	}
}

// ipRateLimiter holds a per-IP token-bucket limiter with idle eviction.
type ipRateLimiter struct {
	mu       sync.Mutex
	limiters map[string]*ipEntry
	r        rate.Limit
	burst    int
}

type ipEntry struct {
	lim  *rate.Limiter
	seen time.Time
}

func newIPRateLimiter() *ipRateLimiter {
	perMin := envInt("AUTH_RATE_PER_MIN", 10)
	burst := envInt("AUTH_RATE_BURST", 5)
	l := &ipRateLimiter{
		limiters: map[string]*ipEntry{},
		r:        rate.Limit(float64(perMin) / 60.0),
		burst:    burst,
	}
	go l.cleanupLoop()
	return l
}

func (l *ipRateLimiter) get(ip string) *rate.Limiter {
	l.mu.Lock()
	defer l.mu.Unlock()
	e, ok := l.limiters[ip]
	if !ok {
		e = &ipEntry{lim: rate.NewLimiter(l.r, l.burst)}
		l.limiters[ip] = e
	}
	e.seen = time.Now()
	return e.lim
}

func (l *ipRateLimiter) cleanupLoop() {
	t := time.NewTicker(10 * time.Minute)
	for range t.C {
		l.mu.Lock()
		for ip, e := range l.limiters {
			if time.Since(e.seen) > 15*time.Minute {
				delete(l.limiters, ip)
			}
		}
		l.mu.Unlock()
	}
}

func rateLimitMiddleware(l *ipRateLimiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !l.get(c.ClientIP()).Allow() {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "Too many requests, slow down"})
			return
		}
		c.Next()
	}
}

// ---- helpers ----

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "na"
	}
	return hex.EncodeToString(b)
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}
