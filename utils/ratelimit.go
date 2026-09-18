package utils

import (
	"sync"
	"sync/atomic"
	"time"
)

// RateLimiter implements token bucket rate limiting per IP address
type RateLimiter struct {
	mu       sync.RWMutex
	buckets  map[string]*TokenBucket
	maxReqs  int           // requests per window
	window   time.Duration // time window for rate limit
	ticker   *time.Ticker
	stopped  atomic.Bool
}

// TokenBucket tracks requests for a single IP
type TokenBucket struct {
	tokens    int
	lastReset time.Time
}

// NewRateLimiter creates a new rate limiter with specified max requests per window
func NewRateLimiter(maxReqs int, window time.Duration) *RateLimiter {
	rl := &RateLimiter{
		buckets: make(map[string]*TokenBucket),
		maxReqs: maxReqs,
		window:  window,
		ticker:  time.NewTicker(5 * time.Minute),
	}

	// Start cleanup goroutine
	go rl.cleanupExpired()

	return rl
}

// Allow checks if a request from the given IP is allowed
func (rl *RateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	bucket, exists := rl.buckets[ip]
	now := time.Now()

	// Create new bucket if it doesn't exist
	if !exists {
		rl.buckets[ip] = &TokenBucket{
			tokens:    rl.maxReqs - 1,
			lastReset: now,
		}
		return true
	}

	// Reset if window has passed
	if now.Sub(bucket.lastReset) > rl.window {
		bucket.tokens = rl.maxReqs - 1
		bucket.lastReset = now
		return true
	}

	// Check if we have tokens
	if bucket.tokens > 0 {
		bucket.tokens--
		return true
	}

	return false
}

// cleanupExpired removes old entries that haven't been used
func (rl *RateLimiter) cleanupExpired() {
	for range rl.ticker.C {
		if rl.stopped.Load() {
			return
		}

		rl.mu.Lock()
		now := time.Now()
		for ip, bucket := range rl.buckets {
			if now.Sub(bucket.lastReset) > 2*time.Hour {
				delete(rl.buckets, ip)
			}
		}
		rl.mu.Unlock()
	}
}

// Stop stops the cleanup goroutine
func (rl *RateLimiter) Stop() {
	rl.stopped.Store(true)
	rl.ticker.Stop()
}
