package httpapi

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

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

func clientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		if ip := strings.TrimSpace(parts[0]); ip != "" {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil && host != "" {
		return host
	}
	return r.RemoteAddr
}
