package api

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/time/rate"
)

const maxRequestBodySize = 1 << 20

// SecurityHeadersMiddleware sets recommended security-related HTTP headers on every response:
// X-Content-Type-Options (nosniff), X-Frame-Options (DENY),
// Content-Security-Policy (default-src 'self'), Strict-Transport-Security,
// and Referrer-Policy (strict-origin-when-cross-origin).
func SecurityHeadersMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("X-Content-Type-Options", "nosniff")
		c.Header("X-Frame-Options", "DENY")
		c.Header("Content-Security-Policy", "default-src 'self'")
		c.Header("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		c.Header("Referrer-Policy", "strict-origin-when-cross-origin")
		c.Next()
	}
}

// BodySizeLimitMiddleware caps the request body size to 1 MB using http.MaxBytesReader.
// Requests exceeding this limit will fail with a 413 Payload Too Large error.
func BodySizeLimitMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxRequestBodySize)
		c.Next()
	}
}

// IPRateLimiter provides per-IP token-bucket rate limiting.
// Visitor entries are periodically purged to prevent unbounded memory growth.
type IPRateLimiter struct {
	visitors map[string]*rate.Limiter
	mu       sync.Mutex
	rate     rate.Limit
	burst    int
	cleanup  time.Duration
}

// NewIPRateLimiter creates a rate limiter that allows r tokens per second with the given burst size.
// Stale entries are cleaned up at the cleanup interval to release memory.
func NewIPRateLimiter(r rate.Limit, burst int, cleanup time.Duration) *IPRateLimiter {
	rl := &IPRateLimiter{
		visitors: make(map[string]*rate.Limiter),
		rate:     r,
		burst:    burst,
		cleanup:  cleanup,
	}
	go rl.cleanupLoop()
	return rl
}

func (rl *IPRateLimiter) getLimiter(ip string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	limiter, exists := rl.visitors[ip]
	if !exists {
		limiter = rate.NewLimiter(rl.rate, rl.burst)
		rl.visitors[ip] = limiter
	}
	return limiter
}

func (rl *IPRateLimiter) cleanupLoop() {
	ticker := time.NewTicker(rl.cleanup)
	defer ticker.Stop()

	for range ticker.C {
		rl.mu.Lock()
		rl.visitors = make(map[string]*rate.Limiter)
		rl.mu.Unlock()
	}
}

// RateLimitMiddleware returns a Gin middleware that enforces per-IP rate limits.
// When the limit is exceeded it responds with 429 Too Many Requests.
func RateLimitMiddleware(rl *IPRateLimiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := c.ClientIP()
		limiter := rl.getLimiter(ip)
		if !limiter.Allow() {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, ErrorResponse{
				Error: "rate limit exceeded: 60 requests per minute per IP",
			})
			return
		}
		c.Next()
	}
}
