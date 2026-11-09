package rediscache

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/dev-bramwel/Bliss-Atelier-Perfumes/internal/catalog"
	"github.com/redis/go-redis/v9"
)

const (
	DefaultCatalogKey = "bliss:catalog:products:v1"
	DefaultCatalogTTL = 5 * time.Minute
	CatalogOpTimeout  = 150 * time.Millisecond
)

const invalidateLua = `
local key = KEYS[1]
local verKey = KEYS[2]
redis.call('DEL', key)
redis.call('INCR', verKey)
return 1
`

const setVersionedLua = `
local key = KEYS[1]
local verKey = KEYS[2]
local expectedVer = ARGV[1]
local payload = ARGV[2]
local ttlSeconds = tonumber(ARGV[3])
local currentVer = redis.call('GET', verKey) or "0"

if currentVer == expectedVer then
    redis.call('SET', key, payload, 'EX', ttlSeconds)
    return 1
else
    return 0
end
`

// CatalogInvalidator defines the interface for purging the catalog cache.
type CatalogInvalidator interface {
	Invalidate(ctx context.Context) error
}

// CatalogCache provides cached public catalog queries with bounded TTL and explicit invalidation.
type CatalogCache struct {
	client       *Client
	key          string
	verKey       string
	ttl          time.Duration
	invalScript  *redis.Script
	setVerScript *redis.Script
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
		client:       client,
		key:          key,
		verKey:       key + ":version",
		ttl:          ttl,
		invalScript:  redis.NewScript(invalidateLua),
		setVerScript: redis.NewScript(setVersionedLua),
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
	if products == nil {
		products = []catalog.Product{}
	}
	return products, nil
}

// Version returns the current catalog cache generation version.
func (c *CatalogCache) Version(ctx context.Context) string {
	if c == nil || c.client == nil || c.client.rdb == nil {
		return "0"
	}
	opCtx, cancel := context.WithTimeout(ctx, CatalogOpTimeout)
	defer cancel()

	val, err := c.client.rdb.Get(opCtx, c.verKey).Result()
	if err != nil {
		return "0"
	}
	return val
}

// Set stores products in Redis with the configured bounded TTL.
func (c *CatalogCache) Set(ctx context.Context, products []catalog.Product, ttl ...time.Duration) error {
	return c.SetVersioned(ctx, products, "", ttl...)
}

// SetVersioned stores products in Redis only if the current generation matches expectedVersion.
// This prevents slow reads from repopulating the cache with stale pre-mutation snapshots.
func (c *CatalogCache) SetVersioned(ctx context.Context, products []catalog.Product, expectedVersion string, ttl ...time.Duration) error {
	if c == nil || c.client == nil || c.client.rdb == nil {
		return nil
	}
	if products == nil {
		products = []catalog.Product{}
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

	if expectedVersion != "" {
		ttlSeconds := int(useTTL.Seconds())
		if ttlSeconds <= 0 {
			ttlSeconds = 300
		}
		res, err := c.setVerScript.Run(opCtx, c.client.rdb, []string{c.key, c.verKey}, expectedVersion, string(data), strconv.Itoa(ttlSeconds)).Int()
		if err != nil {
			return err
		}
		if res == 0 {
			slog.Debug("catalog_cache_stale_write_dropped", "key", c.key, "expected_version", expectedVersion)
		}
		return nil
	}

	return c.client.rdb.Set(opCtx, c.key, data, useTTL).Err()
}

// Invalidate removes the cached catalog key and increments the generation version.
func (c *CatalogCache) Invalidate(ctx context.Context) error {
	if c == nil || c.client == nil || c.client.rdb == nil {
		return nil
	}
	opCtx, cancel := context.WithTimeout(ctx, CatalogOpTimeout)
	defer cancel()

	err := c.invalScript.Run(opCtx, c.client.rdb, []string{c.key, c.verKey}).Err()
	if err != nil && !errors.Is(err, redis.Nil) {
		slog.Warn("catalog_cache_invalidation_failed", "error", err.Error())
		return err
	}
	return nil
}
