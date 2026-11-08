package httpapi

import (
	"net/http"
	"testing"
	"time"
)

func TestIPLimiter(t *testing.T) {
	window := 100 * time.Millisecond
	limiter := newIPLimiter(window, 3)

	now := time.Now()
	ip := "192.0.2.1"

	// First 3 requests allowed
	for i := 0; i < 3; i++ {
		if !limiter.Allow(ip, now.Add(time.Duration(i)*time.Millisecond)) {
			t.Fatalf("expected request %d to be allowed", i+1)
		}
	}

	// 4th request in same window rejected
	if limiter.Allow(ip, now.Add(50*time.Millisecond)) {
		t.Fatalf("expected 4th request in window to be rejected")
	}

	// Different IP allowed
	if !limiter.Allow("192.0.2.2", now.Add(50*time.Millisecond)) {
		t.Fatalf("expected different IP to be allowed")
	}

	// After window expires, requests allowed again
	if !limiter.Allow(ip, now.Add(150*time.Millisecond)) {
		t.Fatalf("expected request after window to be allowed")
	}
}

func TestClientIP(t *testing.T) {
	req, _ := http.NewRequest("GET", "/", nil)
	req.RemoteAddr = "192.0.2.10:54321"

	if ip := clientIP(req); ip != "192.0.2.10" {
		t.Fatalf("expected 192.0.2.10, got %s", ip)
	}

	// Untrusted remote: X-Forwarded-For must be ignored to prevent spoofing
	req.Header.Set("X-Forwarded-For", "203.0.113.195, 70.41.3.18")
	if ip := clientIP(req); ip != "192.0.2.10" {
		t.Fatalf("expected 192.0.2.10 when remote is untrusted, got %s", ip)
	}

	// Trusted loopback remote: X-Forwarded-For is parsed safely
	req.RemoteAddr = "127.0.0.1:54321"
	if ip := clientIP(req); ip != "70.41.3.18" {
		t.Fatalf("expected rightmost untrusted hop 70.41.3.18 from XFF, got %s", ip)
	}
}
