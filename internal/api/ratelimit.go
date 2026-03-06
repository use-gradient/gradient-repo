package api

import (
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// RateLimitConfig holds rate limiting configuration.
type RateLimitConfig struct {
	// Per-IP limits
	IPRequestsPerSecond float64 // requests per second per IP (default: 20)
	IPBurst             int     // max burst per IP (default: 40)

	// Per-Org limits (authenticated requests)
	OrgRequestsPerSecond float64 // requests per second per org (default: 100)
	OrgBurst             int     // max burst per org (default: 200)

	// Global limit
	GlobalRequestsPerSecond float64 // requests per second globally (default: 1000)
	GlobalBurst             int     // max burst globally (default: 2000)

	// Cleanup interval for stale limiters
	CleanupInterval time.Duration
}

// DefaultRateLimitConfig returns sensible defaults for rate limiting.
func DefaultRateLimitConfig() RateLimitConfig {
	return RateLimitConfig{
		IPRequestsPerSecond:     20,
		IPBurst:                 40,
		OrgRequestsPerSecond:    100,
		OrgBurst:                200,
		GlobalRequestsPerSecond: 1000,
		GlobalBurst:             2000,
		CleanupInterval:         5 * time.Minute,
	}
}

// RateLimiter implements per-IP and per-org rate limiting using token buckets.
type RateLimiter struct {
	config      RateLimitConfig
	ipLimiters  map[string]*limiterEntry
	orgLimiters map[string]*limiterEntry
	global      *rate.Limiter
	mu          sync.RWMutex
	stopCleanup chan struct{}
}

type limiterEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

// NewRateLimiter creates a new rate limiter with the given config.
func NewRateLimiter(cfg RateLimitConfig) *RateLimiter {
	rl := &RateLimiter{
		config:      cfg,
		ipLimiters:  make(map[string]*limiterEntry),
		orgLimiters: make(map[string]*limiterEntry),
		global:      rate.NewLimiter(rate.Limit(cfg.GlobalRequestsPerSecond), cfg.GlobalBurst),
		stopCleanup: make(chan struct{}),
	}

	// Start background cleanup of stale limiters
	if cfg.CleanupInterval > 0 {
		go rl.cleanupLoop()
	}

	return rl
}

// getIPLimiter returns or creates a rate limiter for the given IP.
func (rl *RateLimiter) getIPLimiter(ip string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	entry, exists := rl.ipLimiters[ip]
	if !exists {
		entry = &limiterEntry{
			limiter:  rate.NewLimiter(rate.Limit(rl.config.IPRequestsPerSecond), rl.config.IPBurst),
			lastSeen: time.Now(),
		}
		rl.ipLimiters[ip] = entry
	} else {
		entry.lastSeen = time.Now()
	}
	return entry.limiter
}

// getOrgLimiter returns or creates a rate limiter for the given org.
func (rl *RateLimiter) getOrgLimiter(orgID string) *rate.Limiter {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	entry, exists := rl.orgLimiters[orgID]
	if !exists {
		entry = &limiterEntry{
			limiter:  rate.NewLimiter(rate.Limit(rl.config.OrgRequestsPerSecond), rl.config.OrgBurst),
			lastSeen: time.Now(),
		}
		rl.orgLimiters[orgID] = entry
	} else {
		entry.lastSeen = time.Now()
	}
	return entry.limiter
}

// cleanupLoop periodically removes stale limiter entries.
func (rl *RateLimiter) cleanupLoop() {
	ticker := time.NewTicker(rl.config.CleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-rl.stopCleanup:
			return
		case <-ticker.C:
			rl.cleanup()
		}
	}
}

// cleanup removes limiters not seen in the last 10 minutes.
func (rl *RateLimiter) cleanup() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	cutoff := time.Now().Add(-10 * time.Minute)
	for ip, entry := range rl.ipLimiters {
		if entry.lastSeen.Before(cutoff) {
			delete(rl.ipLimiters, ip)
		}
	}
	for orgID, entry := range rl.orgLimiters {
		if entry.lastSeen.Before(cutoff) {
			delete(rl.orgLimiters, orgID)
		}
	}
}

// Stop shuts down the cleanup goroutine.
func (rl *RateLimiter) Stop() {
	close(rl.stopCleanup)
}

// Middleware returns an HTTP middleware that enforces rate limits.
func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip rate limiting for health checks
		if r.URL.Path == "/api/v1/health" {
			next.ServeHTTP(w, r)
			return
		}

		// Check global limit first
		if !rl.global.Allow() {
			log.Printf("[ratelimit] global rate limit exceeded from %s", clientIP(r))
			writeRateLimitResponse(w, "global rate limit exceeded")
			return
		}

		// Per-IP rate limit
		ip := clientIP(r)
		ipLimiter := rl.getIPLimiter(ip)
		if !ipLimiter.Allow() {
			log.Printf("[ratelimit] IP rate limit exceeded for %s", ip)
			writeRateLimitResponse(w, fmt.Sprintf("rate limit exceeded for IP %s", ip))
			return
		}

		// Per-Org rate limit (if authenticated)
		orgID := r.Header.Get("X-Org-ID")
		if orgID == "" {
			// Try to extract from context (set by auth middleware)
			if v := r.Context().Value(ContextKeyOrgID); v != nil {
				orgID, _ = v.(string)
			}
		}
		if orgID != "" && orgID != "dev-org" {
			orgLimiter := rl.getOrgLimiter(orgID)
			if !orgLimiter.Allow() {
				log.Printf("[ratelimit] org rate limit exceeded for %s", orgID)
				writeRateLimitResponse(w, fmt.Sprintf("rate limit exceeded for organization %s", orgID))
				return
			}
		}

		// Set rate limit headers
		w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%.0f", rl.config.IPRequestsPerSecond))
		w.Header().Set("X-RateLimit-Remaining", fmt.Sprintf("%d", rl.config.IPBurst-int(ipLimiter.Tokens())))

		next.ServeHTTP(w, r)
	})
}

// clientIP extracts the real client IP from the request.
func clientIP(r *http.Request) string {
	// Check X-Forwarded-For first (from reverse proxy)
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.SplitN(xff, ",", 2)
		return strings.TrimSpace(parts[0])
	}
	// Check X-Real-IP
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}
	// Fall back to RemoteAddr
	ip := r.RemoteAddr
	if idx := strings.LastIndex(ip, ":"); idx != -1 {
		ip = ip[:idx]
	}
	return ip
}

// writeRateLimitResponse writes a 429 response with Retry-After header.
func writeRateLimitResponse(w http.ResponseWriter, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Retry-After", "1")
	w.WriteHeader(http.StatusTooManyRequests)
	fmt.Fprintf(w, `{"error":"%s","retry_after_seconds":1}`, message)
}

// Stats returns current rate limiter statistics.
func (rl *RateLimiter) Stats() map[string]interface{} {
	rl.mu.RLock()
	defer rl.mu.RUnlock()

	return map[string]interface{}{
		"tracked_ips":  len(rl.ipLimiters),
		"tracked_orgs": len(rl.orgLimiters),
		"config": map[string]interface{}{
			"ip_rps":     rl.config.IPRequestsPerSecond,
			"ip_burst":   rl.config.IPBurst,
			"org_rps":    rl.config.OrgRequestsPerSecond,
			"org_burst":  rl.config.OrgBurst,
			"global_rps": rl.config.GlobalRequestsPerSecond,
		},
	}
}
