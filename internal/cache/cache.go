package cache

import (
	"context"
	"encoding/json"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/sirupsen/logrus"
	"xdorb-backend/internal/config"
)

// Cache wraps Redis client with fallback in-memory cache
type Cache struct {
	redis *redis.Client
	cfg   *config.Config
}

// CachedValue represents a cached value with metadata
type CachedValue struct {
	Data      interface{} `json:"data"`
	Timestamp int64       `json:"timestamp"`
}

// NewCache creates a new cache instance
func NewCache(cfg *config.Config) *Cache {
	rdb := redis.NewClient(&redis.Options{
		Addr:     cfg.RedisURL,
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
	})

	// Test connection
	ctx := context.Background()
	if err := rdb.Ping(ctx).Err(); err != nil {
		logrus.Warn("Redis connection failed, using in-memory cache only:", err)
	} else {
		logrus.Info("Redis connection established successfully")
	}

	return &Cache{
		redis: rdb,
		cfg:   cfg,
	}
}

// Ping checks if Redis is available
func (c *Cache) Ping() error {
	ctx := context.Background()
	return c.redis.Ping(ctx).Err()
}

// Get retrieves a value from cache
func (c *Cache) Get(key string) (*CachedValue, error) {
	ctx := context.Background()

	val, err := c.redis.Get(ctx, key).Result()
	if err != nil {
		return nil, err
	}

	var cached CachedValue
	if err := json.Unmarshal([]byte(val), &cached); err != nil {
		return nil, err
	}

	return &cached, nil
}

// Set stores a value in cache with TTL
func (c *Cache) Set(key string, value interface{}, ttl time.Duration) error {
	ctx := context.Background()

	cached := CachedValue{
		Data:      value,
		Timestamp: time.Now().Unix(),
	}

	data, err := json.Marshal(cached)
	if err != nil {
		return err
	}

	return c.redis.Set(ctx, key, data, ttl).Err()
}

// Delete removes a key from cache
func (c *Cache) Delete(key string) error {
	ctx := context.Background()
	return c.redis.Del(ctx, key).Err()
}

// Exists checks if a key exists in cache
func (c *Cache) Exists(key string) bool {
	ctx := context.Background()
	count, err := c.redis.Exists(ctx, key).Result()
	return err == nil && count > 0
}

// FlushAll clears all cache data
func (c *Cache) FlushAll() error {
	ctx := context.Background()
	return c.redis.FlushAll(ctx).Err()
}

// Unmarshal unmarshals cached data into the provided interface
func (cv *CachedValue) Unmarshal(v interface{}) error {
	data, err := json.Marshal(cv.Data)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}
