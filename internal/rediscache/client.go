package rediscache

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// Client wraps a redis.Client with bounded operational timeouts.
type Client struct {
	rdb *redis.Client
}

// NewClient parses a Redis URL and creates a connection pool with bounded timeouts.
func NewClient(redisURL string) (*Client, error) {
	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("invalid redis url: %w", err)
	}
	opts.DialTimeout = 2 * time.Second
	opts.ReadTimeout = 200 * time.Millisecond
	opts.WriteTimeout = 200 * time.Millisecond
	opts.PoolSize = 10
	opts.MinIdleConns = 2

	rdb := redis.NewClient(opts)
	return &Client{rdb: rdb}, nil
}

// NewFromRDB creates a Client wrapping an existing *redis.Client.
func NewFromRDB(rdb *redis.Client) *Client {
	return &Client{rdb: rdb}
}

// Ping checks if the Redis server is reachable.
func (c *Client) Ping(ctx context.Context) error {
	if c == nil || c.rdb == nil {
		return fmt.Errorf("redis client is nil")
	}
	return c.rdb.Ping(ctx).Err()
}

// Close closes the underlying Redis connection pool.
func (c *Client) Close() error {
	if c == nil || c.rdb == nil {
		return nil
	}
	return c.rdb.Close()
}

// Underlying returns the raw *redis.Client for direct operations if needed.
func (c *Client) Underlying() *redis.Client {
	if c == nil {
		return nil
	}
	return c.rdb
}
