package rediscache

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/catalog"
)

func TestCatalogCache_NilClient(t *testing.T) {
	cache := NewCatalogCache(nil, "", 5*time.Minute)
	ctx := context.Background()

	// Get returns error when client is nil, signalling caller to fall back to DB
	products, err := cache.Get(ctx)
	if err == nil {
		t.Fatal("expected error on Get with nil client")
	}
	if products != nil {
		t.Fatal("expected nil products on Get with nil client")
	}

	// Set and Invalidate are safe no-ops
	if err := cache.Set(ctx, []catalog.Product{{ID: "p1", Name: "Perfume 1"}}); err != nil {
		t.Fatalf("expected nil error on Set with nil client, got %v", err)
	}
	if err := cache.Invalidate(ctx); err != nil {
		t.Fatalf("expected nil error on Invalidate with nil client, got %v", err)
	}
}

func TestCatalogCache_OutageFallback(t *testing.T) {
	// Point to unreachable port
	client, err := NewClient("redis://127.0.0.1:59999/0")
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	defer client.Close()

	cache := NewCatalogCache(client, "test:catalog:outage", 5*time.Minute)
	ctx := context.Background()

	// Get fails with error so HTTP handler falls back to PostgreSQL
	products, err := cache.Get(ctx)
	if err == nil {
		t.Fatal("expected outage error on Get")
	}
	if products != nil {
		t.Fatal("expected nil products on outage")
	}

	// Invalidate fails safely with error logged, without panic
	_ = cache.Invalidate(ctx)
}

func TestCatalogCache_LiveRedis(t *testing.T) {
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
	cache := NewCatalogCache(client, "test:live:catalog:v1", 2*time.Second)

	// Invalidate initially to ensure clean state
	_ = cache.Invalidate(ctx)

	// 1. Initial read should be a cache miss
	products, err := cache.Get(ctx)
	if err != nil {
		t.Fatalf("unexpected error on cache miss: %v", err)
	}
	if products != nil {
		t.Fatalf("expected cache miss (nil products), got %v", products)
	}

	// 2. Set products in cache
	sampleProducts := []catalog.Product{
		{ID: "p1", Name: "Rose Gold", PriceKes: 2500, Available: true},
		{ID: "p2", Name: "Midnight Oud", PriceKes: 3500, Available: false},
	}
	if err := cache.Set(ctx, sampleProducts); err != nil {
		t.Fatalf("failed to set products in cache: %v", err)
	}

	// 3. Subsequent read should be a cache hit
	cached, err := cache.Get(ctx)
	if err != nil {
		t.Fatalf("unexpected error on cache hit: %v", err)
	}
	if len(cached) != 2 || cached[0].ID != "p1" || cached[1].PriceKes != 3500 {
		t.Fatalf("cached products mismatch: %+v", cached)
	}

	// 4. Invalidate should purge cache
	if err := cache.Invalidate(ctx); err != nil {
		t.Fatalf("failed to invalidate cache: %v", err)
	}

	// 5. Read after invalidation should be miss again
	afterInval, err := cache.Get(ctx)
	if err != nil {
		t.Fatalf("unexpected error after invalidation: %v", err)
	}
	if afterInval != nil {
		t.Fatalf("expected cache miss after invalidation, got %+v", afterInval)
	}
}
