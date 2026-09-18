package utils

import (
	"testing"
	"time"
)

func TestRateLimiterAllow(t *testing.T) {
	rl := NewRateLimiter(3, 1*time.Second)
	defer rl.Stop()

	ip := "192.168.1.1"

	// First 3 requests should be allowed
	for i := 0; i < 3; i++ {
		if !rl.Allow(ip) {
			t.Errorf("Request %d should be allowed", i+1)
		}
	}

	// 4th request should be denied
	if rl.Allow(ip) {
		t.Error("4th request should be denied")
	}

	// After waiting for the window to pass, requests should be allowed again
	time.Sleep(1100 * time.Millisecond)
	if !rl.Allow(ip) {
		t.Error("Request after window reset should be allowed")
	}
}

func TestRateLimiterMultipleIPs(t *testing.T) {
	rl := NewRateLimiter(2, 1*time.Second)
	defer rl.Stop()

	ip1 := "192.168.1.1"
	ip2 := "192.168.1.2"

	// First IP: 2 requests allowed
	if !rl.Allow(ip1) {
		t.Error("1st request from ip1 should be allowed")
	}
	if !rl.Allow(ip1) {
		t.Error("2nd request from ip1 should be allowed")
	}
	if rl.Allow(ip1) {
		t.Error("3rd request from ip1 should be denied")
	}

	// Second IP: should have independent limit
	if !rl.Allow(ip2) {
		t.Error("1st request from ip2 should be allowed")
	}
	if !rl.Allow(ip2) {
		t.Error("2nd request from ip2 should be allowed")
	}
	if rl.Allow(ip2) {
		t.Error("3rd request from ip2 should be denied")
	}
}

func TestRateLimiterWindowReset(t *testing.T) {
	rl := NewRateLimiter(1, 100*time.Millisecond)
	defer rl.Stop()

	ip := "192.168.1.1"

	// Use the single allowed request
	if !rl.Allow(ip) {
		t.Error("1st request should be allowed")
	}

	// Next request should be denied
	if rl.Allow(ip) {
		t.Error("2nd request should be denied before window reset")
	}

	// Wait for window to reset
	time.Sleep(150 * time.Millisecond)

	// Should be allowed again
	if !rl.Allow(ip) {
		t.Error("Request after window reset should be allowed")
	}
}
