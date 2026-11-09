package httpapi

import (
	"context"
	"net/http"
	"sync"
	"time"
)

// Limiter defines a rate limiter that checks if a request key is allowed.
type Limiter interface {
	Allow(ctx context.Context, key string) bool
}

// ipLimiter implements an in-memory sliding window rate limiter per client IP.
type ipLimiter struct {
	mu      sync.Mutex
	limits  map[string][]time.Time
	window  time.Duration
	maxReqs int
}

func newIPLimiter(window time.Duration, maxReqs int) *ipLimiter {
	return &ipLimiter{
		limits:  make(map[string][]time.Time),
		window:  window,
		maxReqs: maxReqs,
	}
}

// Allow reports whether a request from ip is permitted within the configured window.
func (l *ipLimiter) Allow(ip string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	cutoff := now.Add(-l.window)
	timestamps := l.limits[ip]
	valid := timestamps[:0]
	for _, t := range timestamps {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}

	if len(valid) >= l.maxReqs {
		l.limits[ip] = valid
		return false
	}

	l.limits[ip] = append(valid, now)

	// Bounded periodic cleanup when cache exceeds threshold
	if len(l.limits) > 1000 {
		for k, v := range l.limits {
			if len(v) == 0 || v[len(v)-1].Before(cutoff) {
				delete(l.limits, k)
			}
		}
	}
	return true
}

type memoryLimiterAdapter struct {
	*ipLimiter
}

func (m *memoryLimiterAdapter) Allow(_ context.Context, key string) bool {
	return m.ipLimiter.Allow(key, time.Now())
}

func newMemoryLimiter(window time.Duration, maxReqs int) Limiter {
	return &memoryLimiterAdapter{ipLimiter: newIPLimiter(window, maxReqs)}
}

var defaultProxyChecker = NewTrustedProxyChecker("")

func clientIP(r *http.Request) string {
	return SafeClientIP(r, defaultProxyChecker)
}
