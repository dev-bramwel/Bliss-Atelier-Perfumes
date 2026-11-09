package rediscache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/catalog"
	"github.com/redis/go-redis/v9"
)

const (
	DefaultCatalogKey = "bliss:catalog:products:v1"
	DefaultCatalogTTL = 5 * time.Minute
	CatalogOpTimeout  = 150 * time.Millisecond
)

// CatalogInvalidator defines the interface for purging the catalog cache.
type CatalogInvalidator interface {
	Invalidate(ctx context.Context) error
}

// CatalogCache provides cached public catalog queries with bounded TTL and explicit invalidation.
type CatalogCache struct {
	client *Client
	key    string
	ttl    time.Duration
}

// NewCatalogCache creates a CatalogCache with bounded TTL.
func NewCatalogCache(client *Client, key string, ttl time.Duration) *CatalogCache {
	if key == "" {
		key = DefaultCatalogKey
	}
	if ttl <= 0 {
		ttl = DefaultCatalogTTL
	}
	return &CatalogCache{
		client: client,
		key:    key,
		ttl:    ttl,
	}
}

// Get returns cached products if present.
// Returns (nil, nil) on cache miss (redis.Nil).
// Returns (nil, err) on Redis outage or timeout, allowing caller to fall back to PostgreSQL.
func (c *CatalogCache) Get(ctx context.Context) ([]catalog.Product, error) {
	if c == nil || c.client == nil || c.client.rdb == nil {
		return nil, errors.New("redis client unavailable")
	}
	opCtx, cancel := context.WithTimeout(ctx, CatalogOpTimeout)
	defer cancel()

	val, err := c.client.rdb.Get(opCtx, c.key).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil // Cache miss
		}
		return nil, err // Outage / network error / timeout
	}

	var products []catalog.Product
	if err := json.Unmarshal([]byte(val), &products); err != nil {
		slog.Warn("catalog_cache_unmarshal_failed", "error", err.Error())
		return nil, err
	}
	return products, nil
}

// Set stores products in Redis with the configured bounded TTL.
func (c *CatalogCache) Set(ctx context.Context, products []catalog.Product, ttl ...time.Duration) error {
	if c == nil || c.client == nil || c.client.rdb == nil {
		return nil
	}
	data, err := json.Marshal(products)
	if err != nil {
		return fmt.Errorf("marshal products failed: %w", err)
	}

	useTTL := c.ttl
	if len(ttl) > 0 && ttl[0] > 0 {
		useTTL = ttl[0]
	}

	opCtx, cancel := context.WithTimeout(ctx, CatalogOpTimeout)
	defer cancel()

	return c.client.rdb.Set(opCtx, c.key, data, useTTL).Err()
}

// Invalidate removes the cached catalog key.
func (c *CatalogCache) Invalidate(ctx context.Context) error {
	if c == nil || c.client == nil || c.client.rdb == nil {
		return nil
	}
	opCtx, cancel := context.WithTimeout(ctx, CatalogOpTimeout)
	defer cancel()

	err := c.client.rdb.Del(opCtx, c.key).Err()
	if err != nil && !errors.Is(err, redis.Nil) {
		slog.Warn("catalog_cache_invalidation_failed", "error", err.Error())
		return err
	}
	return nil
}
