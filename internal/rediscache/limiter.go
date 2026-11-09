package rediscache

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

// MemoryLimiter provides an in-memory sliding window rate limiter per key.
type MemoryLimiter struct {
	mu      sync.Mutex
	limits  map[string][]time.Time
	window  time.Duration
	maxReqs int
}

// NewMemoryLimiter initializes an in-memory sliding window limiter.
func NewMemoryLimiter(window time.Duration, maxReqs int) *MemoryLimiter {
	return &MemoryLimiter{
		limits:  make(map[string][]time.Time),
		window:  window,
		maxReqs: maxReqs,
	}
}

// Allow reports whether a request for key is permitted within the configured window.
func (l *MemoryLimiter) Allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	cutoff := now.Add(-l.window)
	timestamps := l.limits[key]
	valid := timestamps[:0]
	for _, t := range timestamps {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}

	if len(valid) >= l.maxReqs {
		l.limits[key] = valid
		return false
	}

	l.limits[key] = append(valid, now)

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

const slidingWindowLua = `
local key = KEYS[1]
local now = tonumber(ARGV[1])
local window_ms = tonumber(ARGV[2])
local max_requests = tonumber(ARGV[3])
local member = ARGV[4]
local cutoff = now - window_ms

redis.call('ZREMRANGEBYSCORE', key, '-inf', cutoff)
local count = redis.call('ZCARD', key)

if count < max_requests then
    redis.call('ZADD', key, now, member)
    redis.call('PEXPIRE', key, window_ms + 1000)
    return 1
else
    return 0
end
`

// RateLimiter provides a security-aware sliding window rate limiter.
// It uses Redis sorted sets when available, and strictly falls back to a local
// in-memory sliding window on Redis failure or timeout, ensuring abuse limits
// cannot be bypassed by attacking the cache.
type RateLimiter struct {
	client   *Client
	prefix   string
	window   time.Duration
	maxReqs  int
	fallback *MemoryLimiter
	script   *redis.Script
	seq      atomic.Uint64
}

// NewRateLimiter creates a sliding window RateLimiter.
func NewRateLimiter(client *Client, prefix string, window time.Duration, maxReqs int) *RateLimiter {
	return &RateLimiter{
		client:   client,
		prefix:   prefix,
		window:   window,
		maxReqs:  maxReqs,
		fallback: NewMemoryLimiter(window, maxReqs),
		script:   redis.NewScript(slidingWindowLua),
	}
}

// Allow reports whether a request for key is allowed under the rate limit.
func (r *RateLimiter) Allow(ctx context.Context, key string) bool {
	allowed, _, _ := r.AllowDetailed(ctx, key)
	return allowed
}

// AllowDetailed reports whether the request was allowed, whether fallback was used, and any Redis error.
func (r *RateLimiter) AllowDetailed(ctx context.Context, key string) (allowed bool, fallbackUsed bool, err error) {
	if r == nil {
		return true, false, nil
	}
	now := time.Now()
	if r.client == nil || r.client.rdb == nil {
		return r.fallback.Allow(key, now), true, nil
	}

	redisKey := r.prefix + key
	windowMs := r.window.Milliseconds()
	nowMs := now.UnixNano() / int64(time.Millisecond)
	member := fmt.Sprintf("%d-%d", now.UnixNano(), r.seq.Add(1))

	opCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()

	res, runErr := r.script.Run(opCtx, r.client.rdb, []string{redisKey}, nowMs, windowMs, r.maxReqs, member).Int()
	if runErr != nil {
		slog.Warn("rate_limiter_redis_fallback", "key", redisKey, "error", runErr.Error())
		return r.fallback.Allow(key, now), true, runErr
	}
	return res == 1, false, nil
}
