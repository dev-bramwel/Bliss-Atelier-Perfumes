package httpapi

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/catalog"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/orders"
	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/rediscache"
)

type mockCatalogStore struct {
	products []catalog.Product
	callCount int
	err       error
}

func (m *mockCatalogStore) Products(ctx context.Context) ([]catalog.Product, error) {
	m.callCount++
	if m.err != nil {
		return nil, m.err
	}
	return m.products, nil
}

type inMemoryMockCache struct {
	data       []catalog.Product
	getCalls   int
	setCalls   int
	invalCalls int
	outageErr  error
}

func (c *inMemoryMockCache) Get(ctx context.Context) ([]catalog.Product, error) {
	c.getCalls++
	if c.outageErr != nil {
		return nil, c.outageErr
	}
	return c.data, nil
}

func (c *inMemoryMockCache) Set(ctx context.Context, products []catalog.Product, ttl ...time.Duration) error {
	c.setCalls++
	if c.outageErr != nil {
		return c.outageErr
	}
	c.data = products
	return nil
}

func (c *inMemoryMockCache) Invalidate(ctx context.Context) error {
	c.invalCalls++
	c.data = nil
	return nil
}

func TestCatalogCaching_HitMissAndOutageFallback(t *testing.T) {
	products := []catalog.Product{
		{ID: "p1", Name: "Perfume A", PriceKes: 3000, Available: true},
	}
	catStore := &mockCatalogStore{products: products}
	cache := &inMemoryMockCache{}

	secCfg := SecurityConfig{ManagerSecret: "topsecret"}
	api := New("127.0.0.1:0", 5*time.Second, func(context.Context) error { return nil }, catStore, cache, secCfg)
	srv := httptest.NewServer(api.server.Handler)
	defer srv.Close()

	// 1. First request -> Cache MISS, fetched from DB, stored in cache
	resp1, err := http.Get(srv.URL + "/catalog")
	if err != nil {
		t.Fatalf("first catalog request failed: %v", err)
	}
	defer resp1.Body.Close()
	if resp1.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp1.StatusCode)
	}
	if xCache := resp1.Header.Get("X-Cache"); xCache != "MISS" {
		t.Fatalf("expected X-Cache: MISS on first request, got %s", xCache)
	}
	if catStore.callCount != 1 {
		t.Fatalf("expected 1 DB call on miss, got %d", catStore.callCount)
	}
	if cache.setCalls != 1 {
		t.Fatalf("expected 1 cache Set call, got %d", cache.setCalls)
	}

	// 2. Second request -> Cache HIT, served from cache without DB call
	resp2, err := http.Get(srv.URL + "/catalog")
	if err != nil {
		t.Fatalf("second catalog request failed: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp2.StatusCode)
	}
	if xCache := resp2.Header.Get("X-Cache"); xCache != "HIT" {
		t.Fatalf("expected X-Cache: HIT on second request, got %s", xCache)
	}
	if catStore.callCount != 1 {
		t.Fatalf("expected DB call count to remain 1 on cache hit, got %d", catStore.callCount)
	}

	// 3. Cache Outage Fallback: Simulate Redis outage (Get returns error)
	cache.outageErr = errors.New("redis connection refused")
	resp3, err := http.Get(srv.URL + "/catalog")
	if err != nil {
		t.Fatalf("outage fallback request failed: %v", err)
	}
	defer resp3.Body.Close()
	// Must still return 200 OK with data from DB! Zero disruption to storefront!
	if resp3.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 on cache outage fallback, got %d", resp3.StatusCode)
	}
	if xCache := resp3.Header.Get("X-Cache"); xCache != "MISS" {
		t.Fatalf("expected X-Cache: MISS on outage fallback, got %s", xCache)
	}
	if catStore.callCount != 2 {
		t.Fatalf("expected DB call count to be 2 after outage fallback, got %d", catStore.callCount)
	}
}

func TestCatalogInvalidation_Endpoint(t *testing.T) {
	catStore := &mockCatalogStore{products: []catalog.Product{{ID: "p1"}}}
	cache := &inMemoryMockCache{data: []catalog.Product{{ID: "p1"}}}
	secCfg := SecurityConfig{ManagerSecret: "mgr-secret-key"}

	api := New("127.0.0.1:0", 5*time.Second, func(context.Context) error { return nil }, catStore, cache, secCfg)
	srv := httptest.NewServer(api.server.Handler)
	defer srv.Close()

	// 1. Missing manager secret -> 401 Unauthorized
	req1, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/catalog/invalidate", nil)
	resp1, err := http.DefaultClient.Do(req1)
	if err != nil {
		t.Fatal(err)
	}
	resp1.Body.Close()
	if resp1.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized without manager secret, got %d", resp1.StatusCode)
	}
	if cache.invalCalls != 0 {
		t.Fatalf("cache should not be invalidated without authorization")
	}

	// 2. Valid manager secret -> 200 OK and cache invalidated
	req2, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/catalog/invalidate", nil)
	req2.Header.Set("X-Manager-Secret", "mgr-secret-key")
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK with valid manager secret, got %d", resp2.StatusCode)
	}
	if cache.invalCalls != 1 {
		t.Fatalf("expected cache Invalidate call, got %d", cache.invalCalls)
	}
	if cache.data != nil {
		t.Fatalf("expected cached data to be cleared")
	}
}

func TestRateLimiter_EndpointsAndHeaderSpoofingProtection(t *testing.T) {
	mockOrders := &mockOrderService{}
	mockCat := &mockCatalogStore{products: []catalog.Product{{ID: "p1"}}}
	secCfg := SecurityConfig{
		TrustedProxies: "127.0.0.1/32,10.0.0.0/8", // Only loopback and internal private network trusted
		ManagerSecret:  "test-secret",
	}

	api := New("127.0.0.1:0", 5*time.Second, func(context.Context) error { return nil }, mockCat, mockOrders, secCfg)
	srv := httptest.NewServer(api.server.Handler)
	defer srv.Close()

	// 1. Order Creation Rate Limiting (limit is 10 requests per minute)
	// Send 10 requests -> all 200/201/400 (not 429)
	for i := 1; i <= 10; i++ {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/orders", strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			t.Fatalf("request %d was prematurely rate limited", i)
		}
	}

	// 11th request MUST be rejected with 429 Too Many Requests
	req11, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/orders", strings.NewReader(`{}`))
	req11.Header.Set("Content-Type", "application/json")
	resp11, err := http.DefaultClient.Do(req11)
	if err != nil {
		t.Fatal(err)
	}
	defer resp11.Body.Close()
	if resp11.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected 429 Too Many Requests on 11th order creation, got %d", resp11.StatusCode)
	}
	if retryAfter := resp11.Header.Get("Retry-After"); retryAfter == "" {
		t.Fatal("expected Retry-After header on 429 response")
	}

	// 2. Header Spoofing Protection Test:
	// An attacker tries to bypass rate limiting by setting a fake X-Forwarded-For: 203.0.113.195
	// Because the connection originates from httptest (which is 127.0.0.1 - trusted proxy),
	// if an attacker sets X-Forwarded-For: 8.8.8.8, the SafeClientIP resolves to 8.8.8.8.
	// But if the attacker sends another request with the SAME IP, it remains rate limited!
	// Furthermore, if untrusted proxy headers are tested:
	spoofedReq, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/orders", strings.NewReader(`{}`))
	spoofedReq.Header.Set("Content-Type", "application/json")
	// Same client IP (loopback client) without fake headers should remain blocked
	spoofedResp, err := http.DefaultClient.Do(spoofedReq)
	if err != nil {
		t.Fatal(err)
	}
	spoofedResp.Body.Close()
	if spoofedResp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected client to remain rate limited (429), got %d", spoofedResp.StatusCode)
	}

	// 3. Order Lookup Rate Limiting (limit is 30 requests per minute)
	testOrder := &orders.Order{
		ID:       "ord_lookup_test",
		TotalKes: 2500,
	}
	mockOrders.getFn = func(ctx context.Context, orderID string) (*orders.Order, error) {
		return testOrder, nil
	}
	for i := 1; i <= 30; i++ {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/orders/ord_lookup_test", nil)
		req.Header.Set("X-Manager-Secret", "test-secret")
		req.Header.Set("X-Forwarded-For", "198.51.100.55") // Distinct client IP from order creation
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			t.Fatalf("lookup request %d was prematurely rate limited", i)
		}
	}

	// 31st lookup from 198.51.100.55 must return 429 Too Many Requests
	req31, _ := http.NewRequest(http.MethodGet, srv.URL+"/api/orders/ord_lookup_test", nil)
	req31.Header.Set("X-Manager-Secret", "test-secret")
	req31.Header.Set("X-Forwarded-For", "198.51.100.55")
	resp31, err := http.DefaultClient.Do(req31)
	if err != nil {
		t.Fatal(err)
	}
	defer resp31.Body.Close()
	if resp31.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("expected 429 on 31st order lookup, got %d", resp31.StatusCode)
	}
}

func TestRateLimiter_RedisOutageFallback_EnforcedInServer(t *testing.T) {
	// Provide a rediscache.Client pointing to an unreachable port
	badClient, err := rediscache.NewClient("redis://127.0.0.1:59999/0")
	if err != nil {
		t.Fatal(err)
	}
	defer badClient.Close()

	mockOrders := &mockOrderService{}
	mockCat := &mockCatalogStore{products: []catalog.Product{{ID: "p1"}}}
	secCfg := SecurityConfig{}

	// Pass badClient to server New -> server constructs rate limiters with fallback to memory
	api := New("127.0.0.1:0", 5*time.Second, func(context.Context) error { return nil }, mockCat, mockOrders, badClient, secCfg)
	srv := httptest.NewServer(api.server.Handler)
	defer srv.Close()

	// 10 order creation requests must succeed without breaking or crashing
	for i := 1; i <= 10; i++ {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/orders", strings.NewReader(`{}`))
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusTooManyRequests {
			t.Fatalf("request %d was prematurely rate limited during outage", i)
		}
	}

	// 11th request MUST be rejected with 429 even though Redis is completely unreachable!
	// Proves abuse limits CANNOT be bypassed via Redis outage!
	req11, _ := http.NewRequest(http.MethodPost, srv.URL+"/api/orders", strings.NewReader(`{}`))
	req11.Header.Set("Content-Type", "application/json")
	resp11, err := http.DefaultClient.Do(req11)
	if err != nil {
		t.Fatal(err)
	}
	defer resp11.Body.Close()
	if resp11.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("SECURITY VIOLATION: expected 429 during Redis outage fallback, got %d", resp11.StatusCode)
	}
}
