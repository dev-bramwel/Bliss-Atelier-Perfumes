package rediscache

import (
	"context"
	"os"
	"sync"
	"testing"
	"time"
)

func TestMemoryLimiter(t *testing.T) {
	window := 100 * time.Millisecond
	limiter := NewMemoryLimiter(window, 3)

	now := time.Now()
	ip := "192.168.1.100"

	// 3 requests allowed
	if !limiter.Allow(ip, now) {
		t.Fatal("expected req 1 to be allowed")
	}
	if !limiter.Allow(ip, now.Add(10*time.Millisecond)) {
		t.Fatal("expected req 2 to be allowed")
	}
	if !limiter.Allow(ip, now.Add(20*time.Millisecond)) {
		t.Fatal("expected req 3 to be allowed")
	}

	// 4th request rejected
	if limiter.Allow(ip, now.Add(30*time.Millisecond)) {
		t.Fatal("expected req 4 to be rejected (quota exceeded)")
	}

	// Different IP allowed
	otherIP := "192.168.1.200"
	if !limiter.Allow(otherIP, now.Add(30*time.Millisecond)) {
		t.Fatal("expected other IP to be allowed")
	}

	// After window expiry, new request allowed
	if !limiter.Allow(ip, now.Add(window+10*time.Millisecond)) {
		t.Fatal("expected request after window expiry to be allowed")
	}
}

func TestRateLimiter_FallbackMode_NoRedis(t *testing.T) {
	// RateLimiter with nil client must fall back to memory limiter and enforce limits
	limiter := NewRateLimiter(nil, "test:limit:", 500*time.Millisecond, 2)
	ctx := context.Background()
	key := "user_1"

	allowed, fallback, err := limiter.AllowDetailed(ctx, key)
	if !allowed || !fallback || err != nil {
		t.Fatalf("expected allowed=true, fallback=true, err=nil, got allowed=%v, fallback=%v, err=%v", allowed, fallback, err)
	}

	allowed, fallback, err = limiter.AllowDetailed(ctx, key)
	if !allowed || !fallback || err != nil {
		t.Fatalf("expected allowed=true, fallback=true, err=nil, got allowed=%v, fallback=%v, err=%v", allowed, fallback, err)
	}

	// 3rd request must be rejected
	allowed, fallback, err = limiter.AllowDetailed(ctx, key)
	if allowed {
		t.Fatal("expected 3rd request to be rejected under fallback rate limit")
	}
	if !fallback {
		t.Fatal("expected fallback to be true")
	}
}

func TestRateLimiter_OutageFallback_EnforcesLimit(t *testing.T) {
	// Point to an invalid / unreachable Redis port
	client, err := NewClient("redis://127.0.0.1:59999/0")
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	defer client.Close()

	limiter := NewRateLimiter(client, "test:outage:", 1*time.Second, 2)
	ctx := context.Background()
	key := "client_xyz"

	// Redis will fail / timeout, so limiter MUST fall back to memory limiter and enforce max 2 requests
	allowed, fallback, err := limiter.AllowDetailed(ctx, key)
	if !allowed || !fallback || err == nil {
		t.Fatalf("expected allowed=true with fallback and redis error, got allowed=%v fallback=%v err=%v", allowed, fallback, err)
	}

	allowed, fallback, err = limiter.AllowDetailed(ctx, key)
	if !allowed || !fallback || err == nil {
		t.Fatalf("expected allowed=true with fallback and redis error, got allowed=%v fallback=%v err=%v", allowed, fallback, err)
	}

	// 3rd request: MUST be rejected by fallback! No bypass allowed!
	allowed, fallback, err = limiter.AllowDetailed(ctx, key)
	if allowed {
		t.Fatal("SECURITY VIOLATION: abuse limits were bypassed during Redis outage!")
	}
	if !fallback {
		t.Fatal("expected fallback=true")
	}
}

func TestRateLimiter_ConcurrentAccess(t *testing.T) {
	limiter := NewRateLimiter(nil, "test:conc:", 1*time.Second, 100)
	ctx := context.Background()

	var wg sync.WaitGroup
	var allowedCount int64
	var mu sync.Mutex

	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			if limiter.Allow(ctx, "shared_key") {
				mu.Lock()
				allowedCount++
				mu.Unlock()
			}
		}(i)
	}
	wg.Wait()

	if allowedCount != 50 {
		t.Fatalf("expected all 50 concurrent requests allowed under limit 100, got %d", allowedCount)
	}
}

func TestRateLimiter_LiveRedis(t *testing.T) {
	redisURL := os.Getenv("GO_TEST_REDIS_URL")
	if redisURL == "" {
		t.Skip("GO_TEST_REDIS_URL not set; skipping live Redis tests")
	}

	client, err := NewClient(redisURL)
	if err != nil {
		t.Fatalf("failed to create redis client: %v", err)
	}
	defer client.Close()

	ctx := context.Background()
	if err := client.Ping(ctx); err != nil {
		t.Fatalf("redis ping failed: %v", err)
	}

	limiter := NewRateLimiter(client, "test:live:limiter:", 1*time.Second, 3)
	key := "test_ip_42"

	// 3 requests allowed via Redis
	for i := 1; i <= 3; i++ {
		allowed, fallback, err := limiter.AllowDetailed(ctx, key)
		if err != nil {
			t.Fatalf("req %d failed with error: %v", i, err)
		}
		if fallback {
			t.Fatalf("req %d unexpectedly used fallback instead of live Redis", i)
		}
		if !allowed {
			t.Fatalf("req %d was rejected but should have been allowed", i)
		}
	}

	// 4th request rejected
	allowed, fallback, err := limiter.AllowDetailed(ctx, key)
	if err != nil {
		t.Fatalf("req 4 failed with error: %v", err)
	}
	if fallback {
		t.Fatal("unexpected fallback on rejection")
	}
	if allowed {
		t.Fatal("req 4 should have been rejected (quota 3 exceeded)")
	}
}
